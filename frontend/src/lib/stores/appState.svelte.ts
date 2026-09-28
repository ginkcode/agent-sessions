import type {
  GroupMode,
  FilterOpts,
  SortOpts,
  GroupNode,
  SessionMeta,
  SessionRef,
} from '../types';
import { api } from '../api';
import { defaultCollapsedKeys, pruneKeys } from '../tree';

const COLLAPSED_STORAGE_KEY = 'agent-sessions:tree-collapsed';

export class AppState {
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

  async init(): Promise<void> {
    this.collapsedKeys = loadCollapsedKeys();
    // Backend re-scans on startup; when a later scan finishes it refreshes
    // groups/sessions so new or updated transcripts appear without restart.
    api.onEvent('scan:ready', () => {
      void this.loadGroups();
      void this.loadSessions();
    });
    await this.loadGroups();
    await this.loadSessions();
  }

  // User-triggered rescan. Desktop also reloads via scan:ready; reloading
  // here too keeps the browser mock (no event emitter) in sync.
  async refresh(): Promise<void> {
    if (this.refreshing) return;
    this.refreshing = true;
    try {
      await api.scan();
      await this.loadGroups();
      await this.loadSessions();
    } catch (err: any) {
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
    saveCollapsedKeys(this.collapsedKeys);
    await this.loadGroups();
    await this.loadSessions();
  }

  async selectGroup(key: string | null): Promise<void> {
    this.selectedGroupKey = key;
    await this.loadSessions();
  }

  async selectSession(ref: SessionRef | null): Promise<void> {
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
    if (this.selectedSessionRef?.agent === ref.agent && this.selectedSessionRef?.id === ref.id) {
      this.selectedSessionMeta = meta;
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
    saveCollapsedKeys(next);
  }

  async setFilter(update: Partial<FilterOpts>): Promise<void> {
    this.filter = { ...this.filter, ...update };
    await this.loadGroups();
    await this.loadSessions();
  }

  async setSort(sort: SortOpts): Promise<void> {
    this.sort = sort;
    await this.loadSessions();
  }

  async loadGroups(): Promise<void> {
    this.loadingGroups = true;
    try {
      // Groups and agent totals change on the same triggers (scan, filter).
      const [groups, counts] = await Promise.all([
        api.listGroups(this.groupMode, this.filter),
        api.agentCounts(this.filter),
      ]);
      this.groups = groups;
      this.agentCounts = counts;
      this.defaultCollapsed = defaultCollapsedKeys(groups);
      // A narrowing filter hides nodes that still exist, so only prune
      // toggles for vanished nodes against the unfiltered tree. An empty
      // tree (e.g. before the first scan) is not evidence that nodes vanished.
      const f = this.filter;
      if (groups.length > 0 && !f.agent && !f.query && !f.path && !f.liveOnly && !f.hasSubagents) {
        const pruned = pruneKeys(this.collapsedKeys, groups);
        if (pruned.size !== this.collapsedKeys.size) {
          this.collapsedKeys = pruned;
          saveCollapsedKeys(pruned);
        }
      }
      this.error = null;
    } catch (err: any) {
      this.error = err?.message || 'Failed to load groups';
    } finally {
      this.loadingGroups = false;
    }
  }

  async loadSessions(): Promise<void> {
    this.loadingSessions = true;
    try {
      this.sessions = await api.listSessions(
        this.selectedGroupKey || '',
        this.filter,
        this.sort
      );
      this.error = null;

      // Auto-select first session if none is selected
      if (!this.selectedSessionRef && this.sessions.length > 0) {
        await this.selectSession(this.sessions[0].ref);
      }
    } catch (err: any) {
      this.error = err?.message || 'Failed to load sessions';
    } finally {
      this.loadingSessions = false;
    }
  }
}

function loadCollapsedKeys(): Set<string> {
  if (typeof localStorage === 'undefined') return new Set();
  try {
    const parsed = JSON.parse(localStorage.getItem(COLLAPSED_STORAGE_KEY) || '[]');
    if (Array.isArray(parsed)) {
      return new Set(parsed.filter((k): k is string => typeof k === 'string'));
    }
  } catch {
    // Ignore JSON parse errors and start from defaults
  }
  return new Set();
}

function saveCollapsedKeys(keys: Set<string>): void {
  if (typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(COLLAPSED_STORAGE_KEY, JSON.stringify([...keys]));
  } catch {
    // Storage quota or disabled
  }
}

export const appState = new AppState();
