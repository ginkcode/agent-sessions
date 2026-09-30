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
  ManageSettings,
  DeletePreview,
  DeleteResult,
  SearchFilter,
  SearchHit,
  FTSProgress,
  CatalogChanged,
  HandoffRequest,
  HandoffPreview,
  HandoffCacheInfo,
  ExportRequest,
  ExportPreview,
  BundleSummary,
  BundleHandoffRequest,
  ConnectionState,
  HostEntry,
  AskpassPrompt,
} from './types';
import { MockBackendAPI } from './mock/mockApi';
import { epoch, guardEpoch } from './link';

export interface BackendAPI {
  listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]>;
  listSessions(groupKey: string, filter?: FilterOpts, sort?: SortOpts): Promise<SessionMeta[]>;
  agentCounts(filter?: FilterOpts): Promise<Record<string, number>>;
  getSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  getMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  search(query: string, filter?: SearchFilter): Promise<SearchHit[]>;
  indexProgress(): Promise<FTSProgress>;
  getBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  copyResumeCommand(ref: SessionRef): Promise<string>;
  revealSource(ref: SessionRef): Promise<void>;
  getDiagnostics(): Promise<Diagnostics>;
  openURL(url: string): Promise<void>;
  appVersion(): Promise<string>;
  onEvent(name: string, callback: (...data: any[]) => void): () => void;
  getSettings(): Promise<ManageSettings>;
  setManageEnabled(enabled: boolean): Promise<ManageSettings>;
  setAllowPermanentDelete(allow: boolean): Promise<ManageSettings>;
  previewDelete(refs: SessionRef[]): Promise<DeletePreview>;
  deleteSessions(refs: SessionRef[], token: string): Promise<DeleteResult>;
  buildHandoff(req: HandoffRequest): Promise<HandoffPreview>;
  handoffCommand(req: HandoffRequest): Promise<string>;
  saveHandoff(req: HandoffRequest): Promise<string>;
  previewExport(req: ExportRequest): Promise<ExportPreview>;
  exportBundle(req: ExportRequest): Promise<string>;
  openBundle(): Promise<BundleSummary | null>;
  openBundlePath(path: string): Promise<BundleSummary>;
  buildBundleHandoff(req: BundleHandoffRequest): Promise<HandoffPreview>;
  bundleHandoffCommand(req: BundleHandoffRequest): Promise<string>;
  saveBundleHandoff(req: BundleHandoffRequest): Promise<string>;
  handoffCache(): Promise<HandoffCacheInfo>;
  clearHandoffCache(): Promise<HandoffCacheInfo>;
  scan(): Promise<void>;
  listHosts(): Promise<HostEntry[]>;
  connect(alias: string): Promise<void>;
  disconnect(): Promise<void>;
  connectionState(): Promise<ConnectionState>;
  askpassReply(id: string, answer: string): Promise<boolean>;
  setHostEnv(env: Record<string, string>): Promise<void>;
  getHostEnv(): Promise<Record<string, string>>;
}

interface WailsAppBinding {
  ListGroups(mode: string, filter: FilterOpts): Promise<GroupNode[]>;
  ListSessions(groupKey: string, filter: FilterOpts, sort: SortOpts): Promise<SessionMeta[]>;
  AgentCounts(filter: FilterOpts): Promise<Record<string, number>>;
  GetSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  GetMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  Search(query: string, filter: SearchFilter): Promise<SearchHit[]>;
  IndexProgress(): Promise<FTSProgress>;
  GetBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  CopyResumeCommand(ref: SessionRef): Promise<string>;
  RevealSource(ref: SessionRef): Promise<void>;
  Diagnostics(): Promise<Diagnostics>;
  OpenURL(url: string): Promise<void>;
  AppVersion(): Promise<string>;
  GetSettings(): Promise<ManageSettings>;
  SetManageEnabled(enabled: boolean): Promise<ManageSettings>;
  SetAllowPermanentDelete(allow: boolean): Promise<ManageSettings>;
  PreviewDelete(refs: SessionRef[]): Promise<DeletePreview>;
  DeleteSessions(refs: SessionRef[], token: string): Promise<DeleteResult>;
  BuildHandoff(req: HandoffRequest): Promise<HandoffPreview>;
  HandoffCommand(req: HandoffRequest): Promise<string>;
  SaveHandoff(req: HandoffRequest): Promise<string>;
  PreviewExport(req: ExportRequest): Promise<ExportPreview>;
  ExportBundle(req: ExportRequest): Promise<string>;
  OpenBundle(): Promise<BundleSummary>;
  OpenBundlePath(path: string): Promise<BundleSummary>;
  BuildBundleHandoff(req: BundleHandoffRequest): Promise<HandoffPreview>;
  BundleHandoffCommand(req: BundleHandoffRequest): Promise<string>;
  SaveBundleHandoff(req: BundleHandoffRequest): Promise<string>;
  HandoffCache(): Promise<HandoffCacheInfo>;
  ClearHandoffCache(): Promise<HandoffCacheInfo>;
  Scan(): Promise<void>;
  Ping(name: string): Promise<string>;
  ListHosts(): Promise<HostEntry[]>;
  Connect(alias: string): Promise<void>;
  Disconnect(): Promise<void>;
  ConnectionState(): Promise<ConnectionState>;
  AskpassReply(id: string, answer: string): Promise<boolean>;
  SetHostEnv(env: Record<string, string>): Promise<void>;
  GetHostEnv(): Promise<Record<string, string>>;
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

  async search(query: string, filter?: SearchFilter): Promise<SearchHit[]> {
    try {
      return (await this.binding.Search(query, filter || {})) || [];
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async indexProgress(): Promise<FTSProgress> {
    try {
      return await this.binding.IndexProgress();
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

  async getSettings(): Promise<ManageSettings> {
    try {
      return await this.binding.GetSettings();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async setManageEnabled(enabled: boolean): Promise<ManageSettings> {
    try {
      return await this.binding.SetManageEnabled(enabled);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async setAllowPermanentDelete(allow: boolean): Promise<ManageSettings> {
    try {
      return await this.binding.SetAllowPermanentDelete(allow);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async previewDelete(refs: SessionRef[]): Promise<DeletePreview> {
    try {
      return await this.binding.PreviewDelete(refs);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async deleteSessions(refs: SessionRef[], token: string): Promise<DeleteResult> {
    try {
      return await this.binding.DeleteSessions(refs, token);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async buildHandoff(req: HandoffRequest): Promise<HandoffPreview> {
    try {
      return await this.binding.BuildHandoff(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async handoffCommand(req: HandoffRequest): Promise<string> {
    try {
      return await this.binding.HandoffCommand(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async saveHandoff(req: HandoffRequest): Promise<string> {
    try {
      return await this.binding.SaveHandoff(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async previewExport(req: ExportRequest): Promise<ExportPreview> {
    try {
      return await this.binding.PreviewExport(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async exportBundle(req: ExportRequest): Promise<string> {
    try {
      return await this.binding.ExportBundle(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openBundle(): Promise<BundleSummary | null> {
    try {
      const summary = await this.binding.OpenBundle();
      if (!summary || !summary.bundleId) return null;
      return summary;
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openBundlePath(path: string): Promise<BundleSummary> {
    try {
      return await this.binding.OpenBundlePath(path);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async buildBundleHandoff(req: BundleHandoffRequest): Promise<HandoffPreview> {
    try {
      return await this.binding.BuildBundleHandoff(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async bundleHandoffCommand(req: BundleHandoffRequest): Promise<string> {
    try {
      return await this.binding.BundleHandoffCommand(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async saveBundleHandoff(req: BundleHandoffRequest): Promise<string> {
    try {
      return await this.binding.SaveBundleHandoff(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async handoffCache(): Promise<HandoffCacheInfo> {
    try {
      return await this.binding.HandoffCache();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async clearHandoffCache(): Promise<HandoffCacheInfo> {
    try {
      return await this.binding.ClearHandoffCache();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async scan(): Promise<void> {
    try {
      await this.binding.Scan();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async listHosts(): Promise<HostEntry[]> {
    try {
      return (await this.binding.ListHosts()) || [];
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async connect(alias: string): Promise<void> {
    try {
      await this.binding.Connect(alias);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async disconnect(): Promise<void> {
    try {
      await this.binding.Disconnect();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async connectionState(): Promise<ConnectionState> {
    try {
      return await this.binding.ConnectionState();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async askpassReply(id: string, answer: string): Promise<boolean> {
    try {
      return await this.binding.AskpassReply(id, answer);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async setHostEnv(env: Record<string, string>): Promise<void> {
    try {
      await this.binding.SetHostEnv(env);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async getHostEnv(): Promise<Record<string, string>> {
    try {
      return (await this.binding.GetHostEnv()) || {};
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async appVersion(): Promise<string> {
    try {
      return await this.binding.AppVersion();
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

  // EventsOn returns a per-listener canceller; EventsOff(name) would drop
  // every listener registered for the event.
  onEvent(name: string, callback: (...data: any[]) => void): () => void {
    const runtime = (window as any).runtime;
    if (runtime?.EventsOn) {
      const off = runtime.EventsOn(name, callback);
      return typeof off === 'function' ? off : () => {};
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

// Connection control and events are not tied to the loaded data.
const UNGUARDED = new Set<string>([
  'onEvent',
  'openURL',
  'appVersion',
  'listHosts',
  'connect',
  'disconnect',
  'connectionState',
  'askpassReply',
  'setHostEnv',
  'getHostEnv',
]);

// Replies that arrive after the stores reset for another backend reject
// with StaleReplyError instead of landing in the new host's views.
export const api: BackendAPI = guardEpoch(createAPI(), epoch, UNGUARDED);

export const CATALOG_CHANGED_EVENT = 'catalog:changed';
export const INDEX_PROGRESS_EVENT = 'index:progress';
export const CONNECTION_STATE_EVENT = 'connection:state';
export const ASKPASS_PROMPT_EVENT = 'askpass:prompt';

export function subscribeConnectionState(
  fn: (state: ConnectionState) => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(CONNECTION_STATE_EVENT, (state: ConnectionState) => {
    if (!state || typeof state !== 'object') return;
    fn(state);
  });
}

export function subscribeAskpassPrompt(
  fn: (prompt: AskpassPrompt) => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(ASKPASS_PROMPT_EVENT, (prompt: AskpassPrompt) => {
    if (!prompt || typeof prompt !== 'object') return;
    fn(prompt);
  });
}

export function subscribeCatalogChanged(
  fn: (event: CatalogChanged) => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(CATALOG_CHANGED_EVENT, (event: CatalogChanged) => {
    if (!event || typeof event !== 'object') return;
    fn(event);
  });
}

export function subscribeIndexProgress(
  fn: (progress: FTSProgress) => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(INDEX_PROGRESS_EVENT, (progress: FTSProgress) => {
    if (!progress || typeof progress !== 'object') return;
    fn(progress);
  });
}
