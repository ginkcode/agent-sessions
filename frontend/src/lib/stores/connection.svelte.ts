import {
  api,
  subscribeConnectionState,
  subscribeAskpassPrompt,
} from '../api';
import { targetKey } from '../tree';
import { hostLabel } from '../hosts';
import type {
  AskpassPrompt,
  ConnectionPhase,
  ConnectionState,
  HostCapabilities,
  HostEntry,
  WSLEntry,
} from '../types';
import { appState } from './appState.svelte';
import { manage } from './manage.svelte';
import { search } from './search.svelte';
import { handoff } from './handoff.svelte';
import { exporter } from './export.svelte';
import { importer } from './importer.svelte';
import { link } from './link.svelte';

// Local always has every capability; the backend reports none for it.
const LOCAL_CAPABILITIES: HostCapabilities = {
  trash: true,
  manage: true,
  export: true,
  import: true,
  search: true,
};

export class ConnectionStore {
  phase = $state<ConnectionPhase>('local');
  host = $state<string | undefined>(undefined);
  generation = $state<number>(0);
  error = $state<string | undefined>(undefined);
  capabilities = $state<HostCapabilities>({ ...LOCAL_CAPABILITIES });
  appVersion = $state<string | undefined>(undefined);

  hosts = $state<HostEntry[]>([]);
  loadingHosts = $state(false);
  hostsError = $state<string | null>(null);
  wslDistros = $state<WSLEntry[]>([]);

  pendingAskpass = $state<AskpassPrompt | null>(null);

  private unsubscribeState: (() => void) | null = null;
  private unsubscribeAskpass: (() => void) | null = null;
  private initialized = false;
  private resetCallbacks = new Set<(generation: number, host?: string) => void>();

  get isRemote(): boolean {
    return this.phase === 'connected' && Boolean(this.host);
  }

  get currentHost(): string {
    return this.host ? hostLabel(this.host) : 'Local';
  }

  get canTrash(): boolean {
    return this.capabilities.trash;
  }

  get canManage(): boolean {
    return this.capabilities.manage;
  }

  get canExport(): boolean {
    return this.capabilities.export;
  }

  get canImport(): boolean {
    return this.capabilities.import;
  }

  get canSearch(): boolean {
    return this.capabilities.search;
  }

  async init(): Promise<void> {
    if (this.initialized) return;
    this.initialized = true;

    this.unsubscribeState = subscribeConnectionState((state) => {
      this.applyState(state);
    });

    this.unsubscribeAskpass = subscribeAskpassPrompt((prompt) => {
      this.pendingAskpass = prompt;
    });

    try {
      const state = await api.connectionState();
      if (state) {
        this.applyState(state, false);
      }
    } catch {
      // Backend may not be available yet or in mock mode
    }

    await this.refreshHosts();
  }

  destroy(): void {
    this.unsubscribeState?.();
    this.unsubscribeState = null;
    this.unsubscribeAskpass?.();
    this.unsubscribeAskpass = null;
    this.initialized = false;
    this.resetCallbacks.clear();
  }

  onReset(callback: (generation: number, host?: string) => void): () => void {
    this.resetCallbacks.add(callback);
    return () => {
      this.resetCallbacks.delete(callback);
    };
  }

  async refreshHosts(): Promise<void> {
    this.loadingHosts = true;
    this.hostsError = null;
    // Neither list waits for or fails with the other.
    const wsl = this.refreshWSLDistros();
    try {
      this.hosts = (await api.listHosts()) || [];
    } catch (err: any) {
      this.hostsError = err?.message || 'Failed to list hosts';
    } finally {
      this.loadingHosts = false;
    }
    await wsl;
  }

  private async refreshWSLDistros(): Promise<void> {
    try {
      this.wslDistros = (await api.listWSLDistros()) || [];
    } catch {
      // The section is left out; SSH hosts still connect.
      this.wslDistros = [];
    }
  }

  async connect(alias: string, envOverrides?: Record<string, string>): Promise<void> {
    this.error = undefined;
    try {
      const env = envOverrides ?? getSavedHostEnv(alias);
      if (Object.keys(env).length > 0) {
        await api.setHostEnv(env);
      }
      await api.connect(alias);
    } catch (err: any) {
      this.error = err?.message || 'Failed to connect';
    }
  }

  async disconnect(): Promise<void> {
    this.error = undefined;
    try {
      await api.disconnect();
    } catch (err: any) {
      this.error = err?.message || 'Failed to disconnect';
    }
  }

  async askpassReply(id: string, answer: string): Promise<boolean> {
    try {
      const ok = await api.askpassReply(id, answer);
      if (this.pendingAskpass?.id === id) {
        this.pendingAskpass = null;
      }
      return ok;
    } catch {
      return false;
    }
  }

  cancelAskpass(id: string): void {
    void this.askpassReply(id, '');
  }

  applyState(state: ConnectionState, triggerResets = true): void {
    this.phase = state.phase;
    this.host = state.host;
    this.generation = state.generation;
    this.error = state.error;
    // Only a live session reports real capabilities. A dropped host keeps
    // its last ones so the stale view still describes that host.
    if (state.phase === 'local') {
      this.capabilities = { ...LOCAL_CAPABILITIES };
    } else if (state.phase === 'connected' && state.capabilities) {
      this.capabilities = state.capabilities;
    }
    this.appVersion = state.appVersion;

    if (!triggerResets) {
      link.seed(state.phase, state.host);
      return;
    }
    // Reload only when a host (re)connects or the app returns to Local; a
    // drop or a pending switch leaves the last data up as stale.
    if (link.update(state.phase, state.host)) {
      this.resetStores(link.dataHost);
    }
  }

  /** The loaded data is from a host that is not serving it right now. */
  get stale(): boolean {
    return link.stale;
  }

  private resetStores(host?: string): void {
    this.pendingAskpass = null;
    void appState.reset(host);
    manage.reset();
    search.reset();
    handoff.reset();
    exporter.reset();
    importer.reset();
    for (const cb of this.resetCallbacks) {
      try {
        cb(this.generation, host);
      } catch {
        // Ignore subscriber errors
      }
    }
  }
}

export const connectionStore = new ConnectionStore();

export function getSavedHostEnv(host?: string): Record<string, string> {
  if (typeof localStorage === 'undefined' || !host) return {};
  try {
    const raw = localStorage.getItem(`agent-sessions:host-env:${targetKey(host)}`);
    if (raw) return JSON.parse(raw);
  } catch {}
  return {};
}

export function saveHostEnv(host: string, env: Record<string, string>): void {
  if (typeof localStorage === 'undefined' || !host) return;
  try {
    localStorage.setItem(`agent-sessions:host-env:${targetKey(host)}`, JSON.stringify(env));
  } catch {}
}
