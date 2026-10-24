import type {
  CatalogChanged,
  GroupMode,
  FilterOpts,
  SortOpts,
  GroupNode,
  SessionMeta,
  SessionRef,
} from '../types';
import { api, subscribeCatalogChanged } from '../api';
import {
  defaultCollapsedKeys,
  pruneKeys,
  loadCollapsedKeys,
  saveCollapsedKeys,
} from '../tree';
import {
  affectsSessionList,
  findGroup,
  hasRef,
  isFullRefresh,
  RequestSequence,
} from '../catalog';
import { nextSelectionAfterDelete, refsEqual } from '../manage';
import { isDisconnectedError, isStaleReply } from '../link';
import { NavHistory, type NavEntry } from '../navHistory';
import type { StartupObservation } from '../startupObservation';

// How a selection change is recorded in the back/forward history: as a new
// place, as an arrow-key step, as an update of the current place, or not at
// all (while going back or forward).
export type NavRecord = 'push' | 'step' | 'replace' | 'none';

export class AppState {
  targetHost = $state<string | undefined>(undefined);
  groupMode = $state<GroupMode>('dir-agent');
  selectedGroupKey = $state<string | null>(null);
  selectedSessionRef = $state<SessionRef | null>(null);
  selectedSessionMeta = $state<SessionMeta | null>(null);

  groups = $state<GroupNode[]>([]);
  sessions = $state<SessionMeta[]>([]);
  // Catalog-wide per-agent totals, independent of the selected group.
  agentCounts = $state<Record<string, number>>({});
  // Manual toggles only; each key flips that node's default collapse state.
  collapsedKeys = $state<Set<string>>(new Set());
  // Large groups that start collapsed, recomputed whenever groups load.
  defaultCollapsed = $state<Set<string>>(new Set());

  filter = $state<FilterOpts>({});
  sort = $state<SortOpts>({ field: 'updated', desc: true });

  loadingGroups = $state(false);
  loadingSessions = $state(false);
  error = $state<string | null>(null);
  refreshing = $state(false);
  // Bumped when the selected session's content may have changed; the
  // transcript reloads its loaded pages in place.
  transcriptVersion = $state(0);

  // Back/forward through the places visited on this host.
  canGoBack = $state(false);
  canGoForward = $state(false);
  private history = new NavHistory();
  private navRequests = new RequestSequence();

  // Late responses from superseded requests are dropped, never applied.
  private groupsRequests = new RequestSequence();
  private sessionsRequests = new RequestSequence();
  private sessionMetaRequests = new RequestSequence();
  private unsubscribeCatalog: (() => void) | null = null;
  // Catalog events apply one at a time so each sees the tree its
  // predecessor loaded.
  private catalogQueue: Promise<void> = Promise.resolve();
  // Set by loadGroups when it cleared a stale group selection and reloaded
  // the full list itself, so callers skip their own list load and a
  // vanished group costs one fetch instead of two.
  private staleGroupReloaded = false;
  // A catalog reload queued by completed discovery has not applied yet.
  discoveryReloading = $state(false);
  private discoveryReloads = 0;
  private discoveryReset = 0;
  // Opt-in startup timing; null in production unless a caller supplies it.
  startupObservation: StartupObservation | null = null;

  async init(): Promise<void> {
    this.collapsedKeys = loadCollapsedKeys(this.targetHost);
    // Subscribe before the first load so a scan committing mid-load is
    // not missed; request ordering keeps the newer response.
    if (!this.unsubscribeCatalog) {
      this.unsubscribeCatalog = subscribeCatalogChanged((event) => {
        this.catalogQueue = this.catalogQueue
          .then(() => this.applyCatalogChange(event))
          .catch(() => {});
      });
    }
    await this.loadGroups();
    await this.loadSessions();
  }

  // Discovery reached a terminal state: reload once more, queued behind the
  // catalog events already received (a successful empty scan may emit none).
  // Uses reload, so groups-before-sessions stale-group reconciliation and the
  // current selection are kept. Until it applies, an empty catalog may predate
  // completion, so the panes stay "discovering" rather than definitively empty.
  // A host reset in between drops it; the reset loads the new host itself.
  reloadAfterDiscovery(): Promise<void> {
    const host = this.targetHost;
    const reset = this.discoveryReset;
    const id = ++this.discoveryReloads;
    this.discoveryReloading = true;
    const run = this.catalogQueue
      .then(async () => {
        if (this.targetHost === host && this.discoveryReset === reset) await this.reload(true);
      })
      .catch(() => {})
      .finally(() => {
        if (id === this.discoveryReloads) this.discoveryReloading = false;
      });
    this.catalogQueue = run;
    return run;
  }

  destroy(): void {
    this.unsubscribeCatalog?.();
    this.unsubscribeCatalog = null;
  }

  // User-triggered rescan. Desktop also reloads via catalog:changed;
  // reloading here too keeps the browser mock (no scanner) in sync.
  async refresh(): Promise<void> {
    if (this.refreshing) return;
    this.refreshing = true;
    try {
      await api.scan();
      await this.reload(true);
      // An explicit refresh reloads the open session even when the scan
      // reported nothing, e.g. its change event was already applied.
      await this.refreshSelectedSession();
    } catch (err: any) {
      if (isStaleReply(err)) return;
      this.error = err?.message || 'Failed to refresh sessions';
    } finally {
      this.refreshing = false;
    }
  }

  async setGroupMode(mode: GroupMode): Promise<void> {
    this.groupMode = mode;
    this.selectedGroupKey = null;
    // Group keys are mode-specific, so toggles from another mode are stale.
    this.collapsedKeys = new Set();
    saveCollapsedKeys(this.collapsedKeys, this.targetHost);
    await this.loadGroups();
    await this.loadSessions();
  }

  async selectGroup(key: string | null, record: NavRecord = 'replace'): Promise<void> {
    this.selectedGroupKey = key;
    this.record(record);
    await this.loadSessions();
  }

  async selectSession(ref: SessionRef | null, record: NavRecord = 'replace'): Promise<void> {
    const id = this.sessionMetaRequests.next();
    this.selectedSessionRef = ref;
    this.selectedSessionMeta = null;
    this.record(record);
    if (!ref) return;

    let meta: SessionMeta | null;
    try {
      meta = await api.getSessionMeta(ref);
    } catch {
      // Find in existing list if available
      meta = this.sessions.find(
        (s) => s.ref.agent === ref.agent && s.ref.id === ref.id
      ) || null;
    }
    if (this.sessionMetaRequests.isCurrent(id)) {
      this.selectedSessionMeta = meta;
    }
  }

  goBack(): Promise<void> {
    return this.travel(() => this.history.back());
  }

  goForward(): Promise<void> {
    return this.travel(() => this.history.forward());
  }

  // Drops deleted sessions, and their subagents, from the history.
  forgetHistory(refs: SessionRef[]): void {
    this.history.remove(refs);
    this.syncHistory();
  }

  private record(how: NavRecord): void {
    if (how === 'none') return;
    if (how !== 'replace') this.navRequests.next(); // A move supersedes going back.
    const entry: NavEntry = {
      ref: this.selectedSessionRef,
      groupKey: this.selectedGroupKey,
      groupMode: this.groupMode,
    };
    if (how === 'push') this.history.push(entry);
    else if (how === 'step') this.history.step(entry);
    else this.history.replace(entry);
    this.syncHistory();
  }

  private syncHistory(): void {
    this.canGoBack = this.history.canGoBack;
    this.canGoForward = this.history.canGoForward;
  }

  // Moves through the history, skipping places whose session is gone.
  private async travel(move: () => NavEntry | null): Promise<void> {
    for (;;) {
      const entry = move();
      this.syncHistory();
      if (!entry) return;
      const id = this.navRequests.next();
      const exists = await this.sessionExists(entry.ref);
      if (!this.navRequests.isCurrent(id)) return;
      if (exists) {
        await this.restore(entry);
        return;
      }
      if (entry.ref) this.forgetHistory([entry.ref]);
    }
  }

  // False only when the backend says the session is gone; a lost connection
  // keeps the place.
  private async sessionExists(ref: SessionRef | null): Promise<boolean> {
    if (!ref) return true;
    try {
      await api.getSessionMeta(ref);
      return true;
    } catch (err) {
      return isStaleReply(err) || isDisconnectedError(err);
    }
  }

  private async restore(entry: NavEntry): Promise<void> {
    // A group of another grouping mode, or one that vanished, shows all.
    const groupKey =
      entry.groupKey && entry.groupMode === this.groupMode && findGroup(this.groups, entry.groupKey)
        ? entry.groupKey
        : null;
    const select = this.selectSession(entry.ref, 'none');
    if (groupKey !== this.selectedGroupKey) {
      this.selectedGroupKey = groupKey;
      await this.loadSessions();
    }
    await select;
  }

  // Re-reads the selected session's header metadata in place, keeping the
  // current header (and the loaded transcript) mounted meanwhile.
  private async refreshSelectedMeta(): Promise<void> {
    const ref = this.selectedSessionRef;
    if (!ref) return;
    const id = this.sessionMetaRequests.next();
    try {
      const meta = await api.getSessionMeta(ref);
      if (this.sessionMetaRequests.isCurrent(id) && refsEqual(this.selectedSessionRef, ref)) {
        this.selectedSessionMeta = meta;
      }
    } catch {
      // Keep the previous header; a removal arrives as its own event.
    }
  }

  // Applies one coalesced backend update: reload the tree when it is dirty,
  // the list only when the update touches it, and the header in place.
  private async applyCatalogChange(event: CatalogChanged): Promise<void> {
    const changed = event.changed ?? [];
    const removed = event.removed ?? [];

    this.forgetHistory(removed);
    if (hasRef(removed, this.selectedSessionRef)) {
      void this.selectSession(
        nextSelectionAfterDelete(this.sessions, removed, this.selectedSessionRef)
      );
    }

    // A superseded tree load means another load is in flight; the tree in
    // hand may predate this event, so reload the list unconditionally.
    const treeCurrent = event.groupsDirty ? await this.loadGroups(true) : true;
    if (!treeCurrent) {
      await this.loadSessions(true);
    } else if (event.groupsDirty && this.staleGroupReloaded) {
      // The selected group vanished; loadGroups already fell back to the
      // full list.
    } else if (this.dropStaleGroup()) {
      // Every session in the selected group is gone; fall back to all.
      await this.loadSessions(true);
    } else if (
      affectsSessionList(event, this.sessions, this.groups, this.selectedGroupKey)
    ) {
      await this.loadSessions(true);
    }

    if (hasRef(changed, this.selectedSessionRef) || isFullRefresh(event)) {
      await this.refreshSelectedSession();
    }
  }

  private async refreshSelectedSession(): Promise<void> {
    if (!this.selectedSessionRef) return;
    this.transcriptVersion++;
    await this.refreshSelectedMeta();
  }

  toggleCollapsed(key: string): void {
    const next = new Set(this.collapsedKeys);
    if (next.has(key)) {
      next.delete(key);
    } else {
      next.add(key);
    }
    this.collapsedKeys = next;
    saveCollapsedKeys(next, this.targetHost);
  }

  async setFilter(update: Partial<FilterOpts>): Promise<void> {
    this.filter = { ...this.filter, ...update };
    await this.reload();
  }

  async setSort(sort: SortOpts): Promise<void> {
    this.sort = sort;
    await this.loadSessions();
  }

  // Reloads the tree, then the list unless the tree load already did:
  // loadGroups falls back to the full list when the selected group
  // vanished, so a dead group key never reaches the list request.
  async reload(background = false): Promise<void> {
    if (!(await this.loadGroups(background))) return;
    if (!this.staleGroupReloaded) await this.loadSessions(background);
  }

  // A background load keeps the populated tree mounted (no loading state,
  // no error over existing data). Resolves false when superseded.
  async loadGroups(background = false): Promise<boolean> {
    const id = this.groupsRequests.next();
    this.staleGroupReloaded = false;
    const quiet = background && this.groups.length > 0;
    if (!quiet) this.loadingGroups = true;
    try {
      // Groups and agent totals change on the same triggers (scan, filter).
      const [groups, counts] = await Promise.all([
        api.listGroups(this.groupMode, this.filter),
        api.agentCounts(this.filter),
      ]);
      if (!this.groupsRequests.isCurrent(id)) return false;
      this.groups = groups;
      this.agentCounts = counts;
      this.defaultCollapsed = defaultCollapsedKeys(groups);
      this.startupObservation?.data('groups_counts', groups.length > 0);
      // A narrowing filter hides nodes that still exist, so only prune
      // toggles for vanished nodes against the unfiltered tree. An empty
      // tree (e.g. before the first scan) is not evidence that nodes vanished.
      // The prune stays in memory until the next toggle saves it: a tree
      // loaded around a host switch can come from the other backend, and
      // saving then would wipe this host's toggles.
      const f = this.filter;
      if (groups.length > 0 && !f.agent && !f.query && !f.path && !f.liveOnly && !f.hasSubagents) {
        const pruned = pruneKeys(this.collapsedKeys, groups);
        if (pruned.size !== this.collapsedKeys.size) {
          this.collapsedKeys = pruned;
        }
      }
      this.error = null;
      // Only an authoritative, non-empty tree can prove a node vanished.
      // An empty one means "nothing loaded yet" or "the filter matched
      // nothing", and clearing the key would discard a selection that is
      // still valid.
      if (groups.length > 0 && this.dropStaleGroup()) {
        this.staleGroupReloaded = true;
        await this.loadSessions(background);
      }
      return true;
    } catch (err: any) {
      if (!this.groupsRequests.isCurrent(id)) return false;
      if (!quiet) this.error = err?.message || 'Failed to load groups';
      return true;
    } finally {
      if (this.groupsRequests.isCurrent(id)) this.loadingGroups = false;
    }
  }

  // Clears the selection when the freshly loaded tree no longer contains
  // it, so the next list load falls back to every session instead of asking
  // the backend for a group it just removed. A key that survived — a group
  // that still holds other sessions — is left alone.
  private dropStaleGroup(): boolean {
    if (this.selectedGroupKey && !findGroup(this.groups, this.selectedGroupKey)) {
      this.selectedGroupKey = null;
      return true;
    }
    return false;
  }

  // Background loads keep the list mounted so its scroll position survives.
  async loadSessions(background = false): Promise<void> {
    const id = this.sessionsRequests.next();
    const quiet = background && this.sessions.length > 0;
    if (!quiet) this.loadingSessions = true;
    try {
      const sessions = await api.listSessions(
        this.selectedGroupKey || '',
        this.filter,
        this.sort
      );
      if (!this.sessionsRequests.isCurrent(id)) return;
      this.sessions = sessions;
      this.error = null;
      this.startupObservation?.data('sessions', sessions.length > 0);

      // Auto-select first session if none is selected
      if (!this.selectedSessionRef && this.sessions.length > 0) {
        await this.selectSession(this.sessions[0].ref);
      }
    } catch (err: any) {
      if (!this.sessionsRequests.isCurrent(id)) return;
      if (!quiet) this.error = err?.message || 'Failed to load sessions';
    } finally {
      if (this.sessionsRequests.isCurrent(id)) this.loadingSessions = false;
    }
  }

  async reset(host?: string): Promise<void> {
    // A startup collector belongs to one hydration/host, not later navigation.
    this.startupObservation = null;
    this.discoveryReloads++;
    this.discoveryReset++;
    this.discoveryReloading = false;
    // Drop a header load still in flight for the previous backend.
    this.sessionMetaRequests.next();
    this.targetHost = host;
    this.collapsedKeys = loadCollapsedKeys(host);
    this.defaultCollapsed = new Set();
    this.selectedGroupKey = null;
    this.selectedSessionRef = null;
    this.selectedSessionMeta = null;
    this.navRequests.next();
    this.history.clear();
    this.syncHistory();
    this.sessions = [];
    this.groups = [];
    this.agentCounts = {};
    this.error = null;
    await this.loadGroups();
    await this.loadSessions();
  }
}

export const appState = new AppState();
