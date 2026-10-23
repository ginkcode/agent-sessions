import type { CodeDiagnosticsAPI, CodeDiagnosticsContext, CodeDiagnosticsRun, CodeDiagnosticsAttachment, CodeDiagnosticsSnapshot, CodeDiagnosticsAck, CodeDiagnosticsRuns, CodeDiagnosticsDocumentReceipt } from './codeDiagnostics';
import { CodeDiagnosticsError, codeDiagnosticsError } from './codeDiagnostics';
import type { PortForwardRequest, PortForwardState } from './portForward';
import type { ManagedToolRequest, ManagedToolState } from './managedTools';
import type { TerminalFileTarget } from './terminalFileOpen';
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
  FileListing,
  FileContent,
  FileProbeResult,
  BlameLine,
  SearchQuery,
  SearchResult,
  FileSaveRequest,
  FileSaveResult,
  ReplaceRequest,
  ReplaceResult,
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
  LaunchInfo,
  TerminalSettings,
  TranslateSettings,
  TranslateSettingsRequest,
  SaveImageRequest,
  ExportRequest,
  ExportPreview,
  BundleSummary,
  BundleHandoffRequest,
  BundleRestoreRequest,
  RestorePreview,
  RestoreReport,
  ConnectionState,
  HostEntry,
  WSLEntry,
  AskpassPrompt,
  TerminalOpenMode,
  TerminalSize,
  TerminalTab,
  TerminalDataEvent,
  TerminalExitEvent,
  AgentID,
} from './types';
import { MockBackendAPI } from './mock/mockApi';
import { epoch, guardEpoch } from './link';

export interface BackendAPI extends CodeDiagnosticsAPI {
  /** Optional on older App bindings. Remote provisioning currently fails closed. */
  managedTool?(request: ManagedToolRequest): Promise<ManagedToolState>;
  listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]>;
  listSessions(groupKey: string, filter?: FilterOpts, sort?: SortOpts): Promise<SessionMeta[]>;
  agentCounts(filter?: FilterOpts): Promise<Record<string, number>>;
  getSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  /** Offset -1 requests the latest page; the response contains its actual offset. */
  getMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  search(query: string, filter?: SearchFilter): Promise<SearchHit[]>;
  indexProgress(): Promise<FTSProgress>;
  getBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  copyResumeCommand(ref: SessionRef): Promise<string>;
  launchInfo(): Promise<LaunchInfo>;
  terminalSettings(): Promise<TerminalSettings>;
  setTerminal(id: string): Promise<TerminalSettings>;
  translateSettings(): Promise<TranslateSettings>;
  setTranslateSettings(req: TranslateSettingsRequest): Promise<TranslateSettings>;
  translate(text: string): Promise<string>;
  saveImage(req: SaveImageRequest): Promise<string>;
  /** Opens the print dialog; print styles pick what is printed. */
  printWindow(): Promise<void>;
  /**
   * Opens another window of the app, with its own connection. Absent where
   * the app has one window only (the browser mock).
   */
  newWindow?(): Promise<void>;
  /**
   * Brings the app's next window to the front, or the previous one when
   * forward is false. Absent where the app has one window only.
   */
  focusNextWindow?(forward: boolean): Promise<void>;
  openResumeInTerminal(ref: SessionRef): Promise<void>;
  /** A fresh session of agent in dir, a project of the selected host. */
  openNewSessionInTerminal(agent: AgentID, dir: string): Promise<void>;
  openHandoffInTerminal(req: HandoffRequest): Promise<void>;
  openBundleHandoffInTerminal(req: BundleHandoffRequest): Promise<void>;
  /** Starts a tab in the built-in terminal panel; size is its first size. */
  panelResume(ref: SessionRef, size: TerminalSize): Promise<TerminalTab>;
  /** A fresh session of agent in dir; the tab records both. */
  panelNewSession(agent: AgentID, dir: string, size: TerminalSize): Promise<TerminalTab>;
  panelHandoff(req: HandoffRequest, size: TerminalSize): Promise<TerminalTab>;
  panelBundleHandoff(req: BundleHandoffRequest, size: TerminalSize): Promise<TerminalTab>;
  /** A plain shell on the selected host, in cwd when it exists there. */
  panelShell(cwd: string, size: TerminalSize): Promise<TerminalTab>;
  terminalWrite(id: string, data: string): Promise<void>;
  terminalResize(id: string, cols: number, rows: number): Promise<void>;
  /** Acknowledges n bytes of output the panel has written. */
  terminalAck(id: string, n: number): Promise<void>;
  terminalClose(id: string): Promise<void>;
  /**
   * Readies files dropped on a tab and returns their paths as typed at its
   * prompt; an SSH tab's host gets copies of them.
   */
  terminalDropFiles(id: string, paths: string[]): Promise<string[]>;
  setTerminalOpen(mode: TerminalOpenMode): Promise<LaunchInfo>;
  revealSource(ref: SessionRef): Promise<void>;
  /** Lists the files of a session directory on the selected host. */
  resolveTerminalFile(dir: string, path: string): Promise<TerminalFileTarget>;
  listFiles(dir: string): Promise<FileListing>;
  /** Reads one file, path relative to dir, on the selected host. */
  probeFiles(dir: string, paths: string[], host: string): Promise<FileProbeResult>;
  readFile(dir: string, path: string): Promise<FileContent>;
  /**
   * Blames one 1-based line of a file, path relative to dir, on the selected
   * host. Rejects when the backend has no blame support.
   */
  blameFile(dir: string, path: string, line: number): Promise<BlameLine>;
  /**
   * Searches the text of the files of dir on the selected host. searchId
   * names the search for cancelSearch; a new search under the same ID
   * cancels the one before it. Rejects when the backend cannot search.
   */
  searchFiles(dir: string, query: SearchQuery, searchId: string): Promise<SearchResult>;
  /** Stops the search started under searchId; its call rejects. */
  cancelSearch(searchId: string): Promise<void>;
  /**
   * Saves an edited file on the selected host if it still has
   * req.expectedRevision; otherwise reports a conflict and writes nothing.
   */
  /** host: where the file was read (undefined for Local); refused, unwritten, if calls now go elsewhere. */
  saveFile(dir: string, req: FileSaveRequest, host: string | undefined): Promise<FileSaveResult>;
  /**
   * Replaces search matches in files on the selected host, each only if it
   * still has the revision it was searched at. host is where the search ran
   * (undefined for Local); refused, nothing written, if calls now go
   * elsewhere. Rejects when the backend cannot replace.
   */
  replaceInFiles(dir: string, req: ReplaceRequest, host: string | undefined): Promise<ReplaceResult>;
  /** Arm native close coordination before mounting any editable document. */
  enableFileEditing(): Promise<boolean>;
  completeFileClose(id: number, allow: boolean): Promise<void>;
  revealFile(dir: string, path: string): Promise<void>;
  copyFile(dir: string, path: string): Promise<void>;
  downloadFile(dir: string, path: string): Promise<string>;
  addFile(dir: string, path: string, host: string): Promise<boolean>;
  createDirectory(dir: string, path: string, name: string, host: string): Promise<void>;
  createFile(dir: string, path: string, name: string, host: string): Promise<void>;
  renameFile(dir: string, path: string, name: string, host: string): Promise<void>;
  deleteFile(dir: string, path: string, host: string): Promise<void>;
  terminalFonts(): Promise<string[]>;
  listPortForwards(): Promise<PortForwardState>;
  savePortForward(rule: PortForwardRequest): Promise<PortForwardState>;
  startPortForward(id: string): Promise<PortForwardState>;
  stopPortForward(id: string): Promise<PortForwardState>;
  removePortForward(id: string): Promise<PortForwardState>;
  getDiagnostics(): Promise<Diagnostics>;
  openURL(url: string): Promise<void>;
  appVersion(): Promise<string>;
  onEvent(name: string, callback: (...data: any[]) => void): () => void;
  /**
   * Calls back with the paths of files dropped on an element styled
   * `--wails-drop-target: drop`. One listener at a time.
   */
  onFileDrop(callback: (x: number, y: number, paths: string[]) => void): () => void;
  getSettings(): Promise<ManageSettings>;
  setManageEnabled(enabled: boolean): Promise<ManageSettings>;
  setAllowPermanentDelete(allow: boolean): Promise<ManageSettings>;
  setPrunePromptHistory(prune: boolean): Promise<ManageSettings>;
  previewDelete(refs: SessionRef[]): Promise<DeletePreview>;
  deleteSessions(refs: SessionRef[], token: string): Promise<DeleteResult>;
  buildHandoff(req: HandoffRequest): Promise<HandoffPreview>;
  handoffCommand(req: HandoffRequest): Promise<string>;
  saveHandoff(req: HandoffRequest): Promise<string>;
  previewExport(req: ExportRequest): Promise<ExportPreview>;
  exportBundle(req: ExportRequest): Promise<string>;
  openBundle(): Promise<BundleSummary | null>;
  openBundlePath(path: string): Promise<BundleSummary>;
  /** Native folder dialog for the import directory; "" when cancelled. Local host only. */
  pickImportDirectory(): Promise<string>;
  previewBundleRestore(req: BundleRestoreRequest): Promise<RestorePreview>;
  restoreBundle(req: BundleRestoreRequest, token: string): Promise<RestoreReport>;
  buildBundleHandoff(req: BundleHandoffRequest): Promise<HandoffPreview>;
  bundleHandoffCommand(req: BundleHandoffRequest): Promise<string>;
  saveBundleHandoff(req: BundleHandoffRequest): Promise<string>;
  handoffCache(): Promise<HandoffCacheInfo>;
  clearHandoffCache(): Promise<HandoffCacheInfo>;
  scan(): Promise<void>;
  listHosts(): Promise<HostEntry[]>;
  listWSLDistros(): Promise<WSLEntry[]>;
  connect(alias: string): Promise<void>;
  disconnect(): Promise<void>;
  connectionState(): Promise<ConnectionState>;
  askpassReply(id: string, answer: string): Promise<boolean>;
  setHostEnv(env: Record<string, string>): Promise<void>;
  getHostEnv(): Promise<Record<string, string>>;
}

interface WailsAppBinding {
  ManagedTool?: (request: ManagedToolRequest) => Promise<ManagedToolState>;
  AttachCodeDiagnostics?: CodeDiagnosticsAPI['attachCodeDiagnostics'];
  DetachCodeDiagnostics?: CodeDiagnosticsAPI['detachCodeDiagnostics'];
  GetCodeDiagnosticsSnapshot?: CodeDiagnosticsAPI['getCodeDiagnosticsSnapshot'];
  ApproveCodeDiagnostics?: CodeDiagnosticsAPI['approveCodeDiagnostics'];
  RevokeCodeDiagnostics?: CodeDiagnosticsAPI['revokeCodeDiagnostics'];
  RunCodeDiagnostics?: CodeDiagnosticsAPI['runCodeDiagnostics'];
  InvalidateCodeDiagnosticsPath?: NonNullable<CodeDiagnosticsAPI['invalidateCodeDiagnosticsPath']>;
  CancelCodeDiagnostics?: CodeDiagnosticsAPI['cancelCodeDiagnostics'];
  OpenCodeDiagnosticDocument?: CodeDiagnosticsAPI['openCodeDiagnosticDocument'];
  ChangeCodeDiagnosticDocument?: CodeDiagnosticsAPI['changeCodeDiagnosticDocument'];
  SaveCodeDiagnosticDocument?: CodeDiagnosticsAPI['saveCodeDiagnosticDocument'];
  CloseCodeDiagnosticDocument?: CodeDiagnosticsAPI['closeCodeDiagnosticDocument'];
  ListGroups(mode: string, filter: FilterOpts): Promise<GroupNode[]>;
  ListSessions(groupKey: string, filter: FilterOpts, sort: SortOpts): Promise<SessionMeta[]>;
  AgentCounts(filter: FilterOpts): Promise<Record<string, number>>;
  GetSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  GetMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  Search(query: string, filter: SearchFilter): Promise<SearchHit[]>;
  IndexProgress(): Promise<FTSProgress>;
  GetBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  CopyResumeCommand(ref: SessionRef): Promise<string>;
  LaunchInfo(): Promise<LaunchInfo>;
  TerminalSettings(): Promise<TerminalSettings>;
  SetTerminal(id: string): Promise<TerminalSettings>;
  TranslateSettings(): Promise<TranslateSettings>;
  SetTranslateSettings(req: TranslateSettingsRequest): Promise<TranslateSettings>;
  Translate(text: string): Promise<string>;
  SaveImage(req: SaveImageRequest): Promise<string>;
  PrintWindow(): Promise<void>;
  NewWindow(): Promise<void>;
  FocusNextWindow?(forward: boolean): Promise<void>;
  OpenResumeInTerminal(ref: SessionRef): Promise<void>;
  OpenNewSessionInTerminal(agent: AgentID, dir: string): Promise<void>;
  OpenHandoffInTerminal(req: HandoffRequest): Promise<void>;
  OpenBundleHandoffInTerminal(req: BundleHandoffRequest): Promise<void>;
  PanelResume(ref: SessionRef, size: TerminalSize): Promise<TerminalTab>;
  PanelNewSession(agent: AgentID, dir: string, size: TerminalSize): Promise<TerminalTab>;
  PanelHandoff(req: HandoffRequest, size: TerminalSize): Promise<TerminalTab>;
  PanelBundleHandoff(req: BundleHandoffRequest, size: TerminalSize): Promise<TerminalTab>;
  PanelShell(req: { cwd: string }, size: TerminalSize): Promise<TerminalTab>;
  TerminalWrite(id: string, data: string): Promise<void>;
  TerminalResize(id: string, cols: number, rows: number): Promise<void>;
  TerminalAck(id: string, n: number): Promise<void>;
  TerminalClose(id: string): Promise<void>;
  TerminalDropFiles(id: string, paths: string[]): Promise<string[]>;
  SetTerminalOpen(mode: string): Promise<LaunchInfo>;
  RevealSource(ref: SessionRef): Promise<void>;
  ResolveTerminalFile(dir: string, path: string): Promise<TerminalFileTarget>;
  ListFiles(dir: string): Promise<FileListing>;
  ProbeFiles?(dir: string, paths: string[], host: string): Promise<FileProbeResult>;
  ReadFile(dir: string, path: string): Promise<FileContent>;
  BlameFile?(dir: string, path: string, line: number): Promise<BlameLine>;
  SearchFiles?(dir: string, query: SearchQuery, searchId: string): Promise<SearchResult>;
  CancelSearch?(searchId: string): Promise<void>;
  SaveFile(dir: string, req: FileSaveRequest, host: string): Promise<FileSaveResult>;
  ReplaceInFiles?(dir: string, req: ReplaceRequest, host: string): Promise<ReplaceResult>;
  EnableFileEditing(): Promise<boolean>;
  CompleteFileClose(id: number, allow: boolean): Promise<void>;
  RevealFile(dir: string, path: string): Promise<void>;
  CopyFile(dir: string, path: string): Promise<void>;
  DownloadFile(dir: string, path: string): Promise<string>;
  AddFile(dir: string, path: string, host: string): Promise<boolean>;
  CreateDirectory(dir: string, path: string, name: string, host: string): Promise<void>;
  CreateFile(dir: string, path: string, name: string, host: string): Promise<void>;
  RenameFile(dir: string, path: string, name: string, host: string): Promise<void>;
  DeleteFile(dir: string, path: string, host: string): Promise<void>;
  TerminalFonts(): Promise<string[]>;
  ListPortForwards(): Promise<PortForwardState>;
  SavePortForward(rule: PortForwardRequest): Promise<PortForwardState>;
  StartPortForward(id: string): Promise<PortForwardState>;
  StopPortForward(id: string): Promise<PortForwardState>;
  RemovePortForward(id: string): Promise<PortForwardState>;
  Diagnostics(): Promise<Diagnostics>;
  OpenURL(url: string): Promise<void>;
  AppVersion(): Promise<string>;
  GetSettings(): Promise<ManageSettings>;
  SetManageEnabled(enabled: boolean): Promise<ManageSettings>;
  SetAllowPermanentDelete(allow: boolean): Promise<ManageSettings>;
  SetPrunePromptHistory(prune: boolean): Promise<ManageSettings>;
  PreviewDelete(refs: SessionRef[]): Promise<DeletePreview>;
  DeleteSessions(refs: SessionRef[], token: string): Promise<DeleteResult>;
  BuildHandoff(req: HandoffRequest): Promise<HandoffPreview>;
  HandoffCommand(req: HandoffRequest): Promise<string>;
  SaveHandoff(req: HandoffRequest): Promise<string>;
  PreviewExport(req: ExportRequest): Promise<ExportPreview>;
  ExportBundle(req: ExportRequest): Promise<string>;
  OpenBundle(): Promise<BundleSummary>;
  OpenBundlePath(path: string): Promise<BundleSummary>;
  PickImportDirectory(): Promise<string>;
  PreviewBundleRestore(req: BundleRestoreRequest): Promise<RestorePreview>;
  RestoreBundle(req: BundleRestoreRequest, token: string): Promise<RestoreReport>;
  BuildBundleHandoff(req: BundleHandoffRequest): Promise<HandoffPreview>;
  BundleHandoffCommand(req: BundleHandoffRequest): Promise<string>;
  SaveBundleHandoff(req: BundleHandoffRequest): Promise<string>;
  HandoffCache(): Promise<HandoffCacheInfo>;
  ClearHandoffCache(): Promise<HandoffCacheInfo>;
  Scan(): Promise<void>;
  Ping(name: string): Promise<string>;
  ListHosts(): Promise<HostEntry[]>;
  ListWSLDistros?(): Promise<WSLEntry[]>;
  Connect(alias: string): Promise<void>;
  Disconnect(): Promise<void>;
  ConnectionState(): Promise<ConnectionState>;
  AskpassReply(id: string, answer: string): Promise<boolean>;
  SetHostEnv(env: Record<string, string>): Promise<void>;
  GetHostEnv(): Promise<Record<string, string>>;
}

export const BLAME_UNSUPPORTED = 'Line blame is not supported by this version of the app backend.';
export const SEARCH_FILES_UNSUPPORTED = 'Searching file contents is not supported by this version of the app backend.';
export const REPLACE_FILES_UNSUPPORTED = 'Replacing in files is not supported by this version of the app backend.';

function normalizeError(err: unknown): Error {
  if (err instanceof Error) return err;
  if (typeof err === 'string') return new Error(err);
  return new Error(String(err));
}

class WailsBackendAPI implements BackendAPI {
  private async diagnosticsCall<T>(call: (() => Promise<T>) | undefined): Promise<T> {
    if (!call) throw new CodeDiagnosticsError('unsupported', 'Code diagnostics are unavailable on this backend');
    try { return await call(); } catch (e) { throw codeDiagnosticsError(e); }
  }
  managedTool(request: ManagedToolRequest): Promise<ManagedToolState> {
    const b = this.binding;
    if (!b.ManagedTool) return Promise.reject(new CodeDiagnosticsError('unsupported', 'Managed tool setup is unavailable on this older backend'));
    return this.diagnosticsCall(() => b.ManagedTool!(request));
  }
  attachCodeDiagnostics(dir: string, version: 1, host: string): Promise<CodeDiagnosticsAttachment> {
    const b = this.binding;
    return this.diagnosticsCall(b.AttachCodeDiagnostics && (() => b.AttachCodeDiagnostics!(dir, version, host)));
  }
  detachCodeDiagnostics(c: CodeDiagnosticsContext, host: string): Promise<CodeDiagnosticsAck> {
    const b = this.binding;
    return this.diagnosticsCall(b.DetachCodeDiagnostics && (() => b.DetachCodeDiagnostics!(c, host)));
  }
  getCodeDiagnosticsSnapshot(c: CodeDiagnosticsContext, host: string): Promise<CodeDiagnosticsSnapshot> {
    const b = this.binding;
    return this.diagnosticsCall(b.GetCodeDiagnosticsSnapshot && (() => b.GetCodeDiagnosticsSnapshot!(c, host)));
  }
  approveCodeDiagnostics(c: CodeDiagnosticsContext, mode: 'session' | 'persistent', fingerprint: string, host: string): Promise<CodeDiagnosticsSnapshot> {
    const b = this.binding;
    return this.diagnosticsCall(b.ApproveCodeDiagnostics && (() => b.ApproveCodeDiagnostics!(c, mode, fingerprint, host)));
  }
  revokeCodeDiagnostics(c: CodeDiagnosticsContext, host: string): Promise<CodeDiagnosticsSnapshot> {
    const b = this.binding;
    return this.diagnosticsCall(b.RevokeCodeDiagnostics && (() => b.RevokeCodeDiagnostics!(c, host)));
  }
  runCodeDiagnostics(c: CodeDiagnosticsContext, ids: string[] | undefined, host: string): Promise<CodeDiagnosticsRuns> {
    const b = this.binding;
    return this.diagnosticsCall(b.RunCodeDiagnostics && (() => b.RunCodeDiagnostics!(c, ids, host)));
  }
  invalidateCodeDiagnosticsPath(c: CodeDiagnosticsContext, path: string, host: string): Promise<CodeDiagnosticsSnapshot> {
    const b = this.binding;
    return this.diagnosticsCall(b.InvalidateCodeDiagnosticsPath && (() => b.InvalidateCodeDiagnosticsPath!(c, path, host)));
  }
  cancelCodeDiagnostics(c: CodeDiagnosticsContext, runs: CodeDiagnosticsRun[], host: string): Promise<CodeDiagnosticsAck> {
    const b = this.binding;
    return this.diagnosticsCall(b.CancelCodeDiagnostics && (() => b.CancelCodeDiagnostics!(c, runs, host)));
  }
  // Generated Wails bindings are not relied on: an absent method is `unsupported`.
  openCodeDiagnosticDocument(c: CodeDiagnosticsContext, path: string, documentId: string, version: 1, text: string, revision: string | undefined, host: string): Promise<CodeDiagnosticsDocumentReceipt> {
    const b = this.binding;
    return this.diagnosticsCall(b.OpenCodeDiagnosticDocument && (() => b.OpenCodeDiagnosticDocument!(c, path, documentId, version, text, revision, host)));
  }
  changeCodeDiagnosticDocument(c: CodeDiagnosticsContext, documentId: string, version: number, text: string, host: string): Promise<CodeDiagnosticsDocumentReceipt> {
    const b = this.binding;
    return this.diagnosticsCall(b.ChangeCodeDiagnosticDocument && (() => b.ChangeCodeDiagnosticDocument!(c, documentId, version, text, host)));
  }
  saveCodeDiagnosticDocument(c: CodeDiagnosticsContext, documentId: string, savedVersion: number, revision: string, host: string): Promise<CodeDiagnosticsDocumentReceipt> {
    const b = this.binding;
    return this.diagnosticsCall(b.SaveCodeDiagnosticDocument && (() => b.SaveCodeDiagnosticDocument!(c, documentId, savedVersion, revision, host)));
  }
  closeCodeDiagnosticDocument(c: CodeDiagnosticsContext, documentId: string, host: string): Promise<CodeDiagnosticsDocumentReceipt> {
    const b = this.binding;
    return this.diagnosticsCall(b.CloseCodeDiagnosticDocument && (() => b.CloseCodeDiagnosticDocument!(c, documentId, host)));
  }
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

  async launchInfo(): Promise<LaunchInfo> {
    try {
      return await this.binding.LaunchInfo();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalSettings(): Promise<TerminalSettings> {
    try {
      return await this.binding.TerminalSettings();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async setTerminal(id: string): Promise<TerminalSettings> {
    try {
      return await this.binding.SetTerminal(id);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async translateSettings(): Promise<TranslateSettings> {
    try {
      return await this.binding.TranslateSettings();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async setTranslateSettings(req: TranslateSettingsRequest): Promise<TranslateSettings> {
    try {
      return await this.binding.SetTranslateSettings(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async translate(text: string): Promise<string> {
    try {
      return await this.binding.Translate(text);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async saveImage(req: SaveImageRequest): Promise<string> {
    try {
      return await this.binding.SaveImage(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async printWindow(): Promise<void> {
    try {
      return await this.binding.PrintWindow();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async newWindow(): Promise<void> {
    try {
      return await this.binding.NewWindow();
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async focusNextWindow(forward: boolean): Promise<void> {
    try {
      // Older backends lack the binding: nothing to switch to.
      if (!this.binding.FocusNextWindow) return;
      return await this.binding.FocusNextWindow(forward);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openResumeInTerminal(ref: SessionRef): Promise<void> {
    try {
      return await this.binding.OpenResumeInTerminal(ref);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openNewSessionInTerminal(agent: AgentID, dir: string): Promise<void> {
    try {
      return await this.binding.OpenNewSessionInTerminal(agent, dir);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openHandoffInTerminal(req: HandoffRequest): Promise<void> {
    try {
      return await this.binding.OpenHandoffInTerminal(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async openBundleHandoffInTerminal(req: BundleHandoffRequest): Promise<void> {
    try {
      return await this.binding.OpenBundleHandoffInTerminal(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async panelResume(ref: SessionRef, size: TerminalSize): Promise<TerminalTab> {
    try {
      return await this.binding.PanelResume(ref, size);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async panelNewSession(agent: AgentID, dir: string, size: TerminalSize): Promise<TerminalTab> {
    try {
      return await this.binding.PanelNewSession(agent, dir, size);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async panelHandoff(req: HandoffRequest, size: TerminalSize): Promise<TerminalTab> {
    try {
      return await this.binding.PanelHandoff(req, size);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async panelBundleHandoff(req: BundleHandoffRequest, size: TerminalSize): Promise<TerminalTab> {
    try {
      return await this.binding.PanelBundleHandoff(req, size);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async panelShell(cwd: string, size: TerminalSize): Promise<TerminalTab> {
    try {
      return await this.binding.PanelShell({ cwd }, size);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalWrite(id: string, data: string): Promise<void> {
    try {
      await this.binding.TerminalWrite(id, data);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalResize(id: string, cols: number, rows: number): Promise<void> {
    try {
      await this.binding.TerminalResize(id, cols, rows);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalAck(id: string, n: number): Promise<void> {
    try {
      await this.binding.TerminalAck(id, n);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalClose(id: string): Promise<void> {
    try {
      await this.binding.TerminalClose(id);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalDropFiles(id: string, paths: string[]): Promise<string[]> {
    try {
      return await this.binding.TerminalDropFiles(id, paths);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async setTerminalOpen(mode: TerminalOpenMode): Promise<LaunchInfo> {
    try {
      return await this.binding.SetTerminalOpen(mode);
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

  async resolveTerminalFile(dir: string, path: string): Promise<TerminalFileTarget> {
    try { return await this.binding.ResolveTerminalFile(dir, path); } catch (e) { throw normalizeError(e); }
  }

  async listFiles(dir: string): Promise<FileListing> {
    try {
      return await this.binding.ListFiles(dir);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async probeFiles(dir: string, paths: string[], host: string): Promise<FileProbeResult> {
    if (!this.binding.ProbeFiles) return { root: '', observations: paths.map(path => ({ path, state: 'unknown', reason: 'unsupported' })) };
    try { return await this.binding.ProbeFiles(dir, paths, host); }
    catch (e) { throw normalizeError(e); }
  }

  async readFile(dir: string, path: string): Promise<FileContent> {
    try {
      return await this.binding.ReadFile(dir, path);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async blameFile(dir: string, path: string, line: number): Promise<BlameLine> {
    // Older backends lack the binding.
    if (!this.binding.BlameFile) throw new Error(BLAME_UNSUPPORTED);
    try {
      return await this.binding.BlameFile(dir, path, line);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async searchFiles(dir: string, query: SearchQuery, searchId: string): Promise<SearchResult> {
    if (!this.binding.SearchFiles) throw new Error(SEARCH_FILES_UNSUPPORTED);
    try {
      return await this.binding.SearchFiles(dir, query, searchId);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async cancelSearch(searchId: string): Promise<void> {
    if (!this.binding.CancelSearch) return;
    try { await this.binding.CancelSearch(searchId); } catch (e) { throw normalizeError(e); }
  }

  async saveFile(dir: string, req: FileSaveRequest, host: string | undefined): Promise<FileSaveResult> {
    try {
      return await this.binding.SaveFile(dir, req, host ?? '');
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async replaceInFiles(dir: string, req: ReplaceRequest, host: string | undefined): Promise<ReplaceResult> {
    if (!this.binding.ReplaceInFiles) throw new Error(REPLACE_FILES_UNSUPPORTED);
    try {
      return await this.binding.ReplaceInFiles(dir, req, host ?? '');
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async enableFileEditing(): Promise<boolean> {
    try { return await this.binding.EnableFileEditing(); } catch (e) { throw normalizeError(e); }
  }

  async completeFileClose(id: number, allow: boolean): Promise<void> {
    try { await this.binding.CompleteFileClose(id, allow); } catch (e) { throw normalizeError(e); }
  }

  async revealFile(dir: string, path: string): Promise<void> {
    try {
      await this.binding.RevealFile(dir, path);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async copyFile(dir: string, path: string): Promise<void> {
    try {
      await this.binding.CopyFile(dir, path);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async addFile(dir: string, path: string, host: string): Promise<boolean> {
    try {
      return await this.binding.AddFile(dir, path, host);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async createDirectory(dir: string, path: string, name: string, host: string): Promise<void> {
    try { await this.binding.CreateDirectory(dir, path, name, host); } catch (e) { throw normalizeError(e); }
  }

  async createFile(dir: string, path: string, name: string, host: string): Promise<void> {
    try { await this.binding.CreateFile(dir, path, name, host); } catch (e) { throw normalizeError(e); }
  }

  async renameFile(dir: string, path: string, name: string, host: string): Promise<void> {
    try { await this.binding.RenameFile(dir, path, name, host); } catch (e) { throw normalizeError(e); }
  }

  async deleteFile(dir: string, path: string, host: string): Promise<void> {
    try { await this.binding.DeleteFile(dir, path, host); } catch (e) { throw normalizeError(e); }
  }

  async downloadFile(dir: string, path: string): Promise<string> {
    try {
      return await this.binding.DownloadFile(dir, path);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async terminalFonts(): Promise<string[]> {
    try { return (await this.binding.TerminalFonts()) ?? []; } catch (e) { throw normalizeError(e); }
  }

  async listPortForwards(): Promise<PortForwardState> {
    try { return await this.binding.ListPortForwards(); } catch (e) { throw normalizeError(e); }
  }

  async savePortForward(rule: PortForwardRequest): Promise<PortForwardState> {
    try { return await this.binding.SavePortForward(rule); } catch (e) { throw normalizeError(e); }
  }

  async startPortForward(id: string): Promise<PortForwardState> {
    try { return await this.binding.StartPortForward(id); } catch (e) { throw normalizeError(e); }
  }

  async stopPortForward(id: string): Promise<PortForwardState> {
    try { return await this.binding.StopPortForward(id); } catch (e) { throw normalizeError(e); }
  }

  async removePortForward(id: string): Promise<PortForwardState> {
    try { return await this.binding.RemovePortForward(id); } catch (e) { throw normalizeError(e); }
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

  async setPrunePromptHistory(prune: boolean): Promise<ManageSettings> {
    try {
      return await this.binding.SetPrunePromptHistory(prune);
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

  async pickImportDirectory(): Promise<string> {
    try {
      return (await this.binding.PickImportDirectory()) || '';
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async previewBundleRestore(req: BundleRestoreRequest): Promise<RestorePreview> {
    try {
      return await this.binding.PreviewBundleRestore(req);
    } catch (e) {
      throw normalizeError(e);
    }
  }

  async restoreBundle(req: BundleRestoreRequest, token: string): Promise<RestoreReport> {
    try {
      return await this.binding.RestoreBundle(req, token);
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

  async listWSLDistros(): Promise<WSLEntry[]> {
    try {
      // Older backends lack the binding: no distributions.
      if (!this.binding.ListWSLDistros) return [];
      return (await this.binding.ListWSLDistros()) || [];
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

  // The runtime keeps one drop listener and ignores later ones until
  // OnFileDropOff.
  onFileDrop(callback: (x: number, y: number, paths: string[]) => void): () => void {
    const runtime = (window as any).runtime;
    if (!runtime?.OnFileDrop) return () => {};
    runtime.OnFileDrop(callback, true);
    return () => runtime.OnFileDropOff?.();
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
  'enableFileEditing',
  'completeFileClose',
  // Canceling must reach the backend even after a host switch, to stop a
  // search the switch made stale.
  'cancelSearch',
  'onFileDrop',
  'openURL',
  'appVersion',
  'launchInfo',
  'terminalSettings',
  'terminalFonts',
  'setTerminal',
  'setTerminalOpen',
  // Panel terminals outlive a host switch: their input, output and shells
  // are not tied to the loaded data. Agent launches are.
  'panelShell',
  'terminalWrite',
  'terminalResize',
  'terminalAck',
  'terminalClose',
  'terminalDropFiles',
  // Translation runs on this computer whichever host is selected.
  'translateSettings',
  'setTranslateSettings',
  'translate',
  // Images are saved, and pages printed, on this computer too.
  'saveImage',
  'printWindow',
  // A new window is another app on this computer.
  'newWindow',
  'focusNextWindow',
  'listPortForwards',
  'savePortForward',
  'startPortForward',
  'stopPortForward',
  'removePortForward',
  'listHosts',
  'listWSLDistros',
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
// The desktop app on Linux and macOS sends the mouse's side buttons as this
// event, "back" or "forward": its web view does not report them to the page
// as buttons 3 and 4.
export const MOUSE_NAVIGATE_EVENT = 'nav:mouse';
export const TERMINAL_DATA_EVENT = 'term:data';
export const TERMINAL_EXIT_EVENT = 'term:exit';

export function subscribeTerminalData(
  fn: (event: TerminalDataEvent) => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(TERMINAL_DATA_EVENT, (event: TerminalDataEvent) => {
    if (!event || typeof event.id !== 'string' || typeof event.data !== 'string') return;
    fn(event);
  });
}

export function subscribeTerminalExit(
  fn: (event: TerminalExitEvent) => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(TERMINAL_EXIT_EVENT, (event: TerminalExitEvent) => {
    if (!event || typeof event.id !== 'string') return;
    fn(event);
  });
}

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

export function subscribeMouseNavigate(
  fn: (direction: 'back' | 'forward') => void,
  backend: BackendAPI = api
): () => void {
  return backend.onEvent(MOUSE_NAVIGATE_EVENT, (direction: unknown) => {
    if (direction === 'back' || direction === 'forward') fn(direction);
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
