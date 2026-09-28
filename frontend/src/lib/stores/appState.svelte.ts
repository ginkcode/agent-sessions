import type {
  GroupMode,
  FilterOpts,
  SortOpts,
  GroupNode,
  SessionMeta,
  SessionRef,
} from '../types';
import { api } from '../api';

export class AppState {
  groupMode = $state<GroupMode>('dir-agent');
  selectedGroupKey = $state<string | null>(null);
  selectedSessionRef = $state<SessionRef | null>(null);
  selectedSessionMeta = $state<SessionMeta | null>(null);

  groups = $state<GroupNode[]>([]);
  sessions = $state<SessionMeta[]>([]);
  collapsedKeys = $state<Set<string>>(new Set());

  filter = $state<FilterOpts>({});
  sort = $state<SortOpts>({ field: 'updated', desc: true });

  loadingGroups = $state(false);
  loadingSessions = $state(false);
  error = $state<string | null>(null);

  async init(): Promise<void> {
    await this.loadGroups();
    await this.loadSessions();
  }

  async setGroupMode(mode: GroupMode): Promise<void> {
    this.groupMode = mode;
    this.selectedGroupKey = null;
    await this.loadGroups();
    await this.loadSessions();
  }

  async selectGroup(key: string | null): Promise<void> {
    this.selectedGroupKey = key;
    await this.loadSessions();
  }

  async selectSession(ref: SessionRef | null): Promise<void> {
    this.selectedSessionRef = ref;
    if (!ref) {
      this.selectedSessionMeta = null;
      return;
    }
    try {
      this.selectedSessionMeta = await api.getSessionMeta(ref);
    } catch {
      // Find in existing list if available
      this.selectedSessionMeta =
        this.sessions.find(
          (s) => s.ref.agent === ref.agent && s.ref.id === ref.id
        ) || null;
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
      this.groups = await api.listGroups(this.groupMode, this.filter);
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

export const appState = new AppState();
