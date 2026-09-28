import type {
  GroupMode,
  FilterOpts,
  SortOpts,
  GroupNode,
  SessionMeta,
  SessionRef,
  MessagesPage,
  BlobResponse,
  Diagnostics,
} from './types';
import { MockBackendAPI } from './mock/mockApi';

export interface BackendAPI {
  listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]>;
  listSessions(groupKey: string, filter?: FilterOpts, sort?: SortOpts): Promise<SessionMeta[]>;
  agentCounts(filter?: FilterOpts): Promise<Record<string, number>>;
  getSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  getMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  getBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  copyResumeCommand(ref: SessionRef): Promise<string>;
  revealSource(ref: SessionRef): Promise<void>;
  getDiagnostics(): Promise<Diagnostics>;
  openURL(url: string): Promise<void>;
  onEvent(name: string, callback: (...data: any[]) => void): () => void;
}

interface WailsAppBinding {
  ListGroups(mode: string, filter: FilterOpts): Promise<GroupNode[]>;
  ListSessions(groupKey: string, filter: FilterOpts, sort: SortOpts): Promise<SessionMeta[]>;
  AgentCounts(filter: FilterOpts): Promise<Record<string, number>>;
  GetSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  GetMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  GetBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  CopyResumeCommand(ref: SessionRef): Promise<string>;
  RevealSource(ref: SessionRef): Promise<void>;
  Diagnostics(): Promise<Diagnostics>;
  OpenURL(url: string): Promise<void>;
  Ping(name: string): Promise<string>;
}

function normalizeError(err: unknown): Error {
  if (err instanceof Error) return err;
  if (typeof err === 'string') return new Error(err);
  return new Error(String(err));
}

class WailsBackendAPI implements BackendAPI {
  private get binding(): WailsAppBinding {
    const b = (window as any).go?.app?.App;
    if (!b) {
      throw new Error('Wails desktop backend runtime is not available');
    }
    return b as WailsAppBinding;
  }

  async listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]> {
    try {
      return await this.binding.ListGroups(mode, filter || {});
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async listSessions(
    groupKey: string,
    filter?: FilterOpts,
    sort?: SortOpts
  ): Promise<SessionMeta[]> {
    try {
      return await this.binding.ListSessions(
        groupKey,
        filter || {},
        sort || { field: 'updated', desc: true }
      );
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async agentCounts(filter?: FilterOpts): Promise<Record<string, number>> {
    try {
      return (await this.binding.AgentCounts(filter || {})) || {};
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async getSessionMeta(ref: SessionRef): Promise<SessionMeta> {
    try {
      return await this.binding.GetSessionMeta(ref);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async getMessages(
    ref: SessionRef,
    offset: number,
    limit: number
  ): Promise<MessagesPage> {
    try {
      return await this.binding.GetMessages(ref, offset, limit);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async getBlob(ref: SessionRef, key: string): Promise<BlobResponse> {
    try {
      return await this.binding.GetBlob(ref, key);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async copyResumeCommand(ref: SessionRef): Promise<string> {
    try {
      return await this.binding.CopyResumeCommand(ref);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async revealSource(ref: SessionRef): Promise<void> {
    try {
      await this.binding.RevealSource(ref);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async getDiagnostics(): Promise<Diagnostics> {
    try {
      return await this.binding.Diagnostics();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openURL(url: string): Promise<void> {
    try {
      if (this.binding.OpenURL) {
        await this.binding.OpenURL(url);
        return;
      }
    } catch {}
    const runtime = (window as any).runtime;
    if (runtime?.BrowserOpenURL) {
      runtime.BrowserOpenURL(url);
      return;
    }
    if (typeof window !== 'undefined') {
      window.open(url, '_blank');
    }
  }

  onEvent(name: string, callback: (...data: any[]) => void): () => void {
    const runtime = (window as any).runtime;
    if (runtime?.EventsOn) {
      runtime.EventsOn(name, callback);
      return () => {
        runtime.EventsOff?.(name);
      };
    }
    return () => {};
  }
}

export function createAPI(): BackendAPI {
  // If running inside Wails desktop shell
  if (typeof window !== 'undefined' && (window as any).go?.app?.App) {
    return new WailsBackendAPI();
  }
  // Otherwise browser / mock environment
  return new MockBackendAPI();
}

export const api: BackendAPI = createAPI();
