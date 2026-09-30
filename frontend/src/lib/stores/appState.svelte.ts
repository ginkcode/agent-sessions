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
import { affectsSessionList, findGroup, hasRef, RequestSequence } from '../catalog';
import { nextSelectionAfterDelete, refsEqual } from '../manage';
import { isStaleReply } from '../link';

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

  async selectGroup(key: string | null): Promise<void> {
    this.selectedGroupKey = key;
    await this.loadSessions();
  }

  async selectSession(ref: SessionRef | null): Promise<void> {
    const id = this.sessionMetaRequests.next();
    this.selectedSessionRef = ref;
    this.selectedSessionMeta = null;
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

    if (hasRef(changed, this.selectedSessionRef)) {
      await this.refreshSelectedMeta();
    }
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
    // Drop a header load still in flight for the previous backend.
    this.sessionMetaRequests.next();
    this.targetHost = host;
    this.collapsedKeys = loadCollapsedKeys(host);
    this.defaultCollapsed = new Set();
    this.selectedGroupKey = null;
    this.selectedSessionRef = null;
    this.selectedSessionMeta = null;
    this.sessions = [];
    this.groups = [];
    this.agentCounts = {};
    this.error = null;
    await this.loadGroups();
    await this.loadSessions();
  }
}

export const appState = new AppState();
