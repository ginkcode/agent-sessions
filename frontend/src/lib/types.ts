export type AgentID = 'claude-code' | 'codex' | 'opencode' | string;

export interface SessionRef {
  agent: AgentID;
  id: string;
}

export interface SearchFilter {
  agents?: string[];
  dir?: string;
  limit?: number;
}

export type SearchHitKind = 'title' | 'text' | 'reasoning' | 'tool';

export interface SearchHit {
  ref: SessionRef;
  /** Global, zero-based transcript index; -1 identifies the session title. */
  messageIndex: number;
  /** Backend-escaped HTML containing no elements other than <mark>. */
  snippet: string;
  score: number;
  kind: SearchHitKind;
}

/**
 * Coalesced catalog update from the backend. groupsDirty with no refs is a
 * full refresh: the catalog was rebuilt and every view must reload.
 */
export interface CatalogChanged {
  changed: SessionRef[] | null;
  removed: SessionRef[] | null;
  groupsDirty: boolean;
}

export interface FTSProgress {
  done: number;
  pending: number;
  failed: number;
  running: boolean;
}

export interface MessageJump {
  id: number;
  ref: SessionRef;
  messageIndex: number;
  kind: SearchHitKind;
  query: string;
  /** Resolves once the selected session header metadata has settled. */
  ready?: Promise<unknown>;
}

export interface ManageSettings {
  enabled: boolean;
  allowPermanentDelete: boolean;
  /** Also remove deleted sessions from Claude's and Codex's prompt history. */
  prunePromptHistory?: boolean;
}

export interface DeletePreviewItem {
  ref: SessionRef;
  agent: AgentID;
  title: string;
  // Go nil slices and omitempty strings arrive as null / absent.
  paths: string[] | null;
  bytes: number;
  reversible: boolean;
  warning?: string;
  /** Reason the item cannot be deleted; absent when actionable. */
  blocked?: string;
  action: string;
}

export interface DeletePreview {
  items: DeletePreviewItem[];
  totalBytes: number;
  token: string;
  /**
   * Where reversible items go on the host performing the delete: "Recycle
   * Bin" on Windows, "Trash" elsewhere. Absent from older servers.
   */
  trashLabel?: string;
}

export interface DeleteResultItem {
  ref: SessionRef;
  title: string;
  ok: boolean;
  error?: string;
  moved?: string[] | null;
  remaining?: string[] | null;
  /** Paths whose removal could not be verified; neither moved nor remaining. */
  unknown?: string[] | null;
}

export interface DeleteResult {
  items: DeleteResultItem[];
  deleted: number;
  failed: number;
  freedBytes: number;
  forgotten: SessionRef[] | null;
  /** Cleanup that was skipped, such as prompt history. */
  notes?: string[] | null;
}

export type GroupMode = 'dir-agent' | 'agent-dir' | 'flat';

export interface FilterOpts {
  agent?: string;
  query?: string;
  liveOnly?: boolean;
  archived?: boolean;
  hasSubagents?: boolean;
  // Case-insensitive substring of the session's cwd or repo root.
  path?: string;
}

export interface SortOpts {
  field: string;
  desc: boolean;
}

export type GroupNodeKind = 'directory' | 'agent' | 'session';

export interface GroupNode {
  key: string;
  label: string;
  kind?: GroupNodeKind;
  secondary?: string;
  agent?: string;
  cwd?: string;
  cwdMissing?: boolean;
  sessionCount: number;
  children?: GroupNode[];
  sessions?: SessionRef[];
}

export interface MessageCounts {
  user: number;
  assistant: number;
  toolCalls: number;
  errors?: number;
}

export interface TokenUsage {
  input: number;
  output: number;
  reasoning?: number;
  cacheRead?: number;
  cacheWrite?: number;
}

export interface SessionMeta {
  ref: SessionRef;
  parentId?: string;
  parentToolCallId?: string;
  cwd: string;
  cwdMissing?: boolean;
  repoRoot?: string;
  title: string;
  firstPrompt?: string;
  model?: string;
  agentName?: string;
  agentVersion?: string;
  gitBranch?: string;
  createdAt: string;
  updatedAt: string;
  counts: MessageCounts;
  tokens: TokenUsage;
  costUsd?: number;
  live?: boolean;
  liveStatus?: string;
  archived?: boolean;
  sourcePath?: string;
}

export type PartKind =
  | 'text'
  | 'reasoning'
  | 'tool'
  | 'patch'
  | 'file'
  | 'files'
  | 'compaction'
  | 'notice'
  | 'meta';

export type ToolStatus = 'pending' | 'completed' | 'error' | 'unknown';

export interface FileRef {
  path?: string;
  name?: string;
  mime?: string;
  size?: number;
  ref?: string;
}

export interface ToolCall {
  id: string;
  name: string;
  input?: any; // tagged ts_type:"any" on backend
  output?: string;
  outputTruncated?: boolean;
  outputRef?: string;
  status: ToolStatus;
  child?: SessionRef;
}

export interface Part {
  kind: PartKind;
  text?: string;
  tool?: ToolCall;
  file?: FileRef;
  files?: string[];
}

export interface Message {
  id: string;
  role: 'user' | 'assistant' | 'system';
  time: string;
  model?: string;
  parts: Part[];
  isMeta?: boolean;
  isSidechain?: boolean;
  tokens?: TokenUsage;
}

export interface Transcript {
  meta: SessionMeta;
  messages: Message[];
}

export interface MessagesPage {
  messages: Message[];
  offset: number;
  limit: number;
  totalCount: number;
  hasMore: boolean;
}

export interface BlobResponse {
  data: string;
  mime: string;
  isBinary: boolean;
}

/** The files of a session directory, for the Go to File dialog. */
export interface FileListing {
  root: string;
  /** Slash-separated paths relative to root, sorted. */
  files: string[];
  /** Explicit directories, including empty ones; older servers may omit this. */
  directories?: string[];
  truncated?: boolean;
  /** The list came from git, so ignored files are left out. */
  git?: boolean;
  /** Git status of each changed file; deleted files are here, not in files. */
  status?: Record<string, GitStatus>;
  /** Files .gitignore leaves out (such as .env), for search only. */
  ignored?: string[];
}

/** A file's git status letter, as VS Code shows it. */
export type GitStatus = 'M' | 'A' | 'U' | 'D' | 'R' | 'C';

/**
 * Lines that differ from the last commit, in current line numbers. A
 * deletion has count 0 and start the line after which lines went (0: before
 * the first).
 */
export interface LineChange {
  kind: 'added' | 'modified' | 'deleted';
  start: number;
  count: number;
  /** The lines it replaced or removed, as at the last commit. */
  removed?: string[];
}

/** One file read for the Go to File dialog. */
export interface FileContent {
  path: string;
  size: number;
  text?: string;
  binary?: boolean;
  tooLarge?: boolean;
  /** The MIME type of a common image file; data holds it base64 encoded. */
  image?: string;
  data?: string;
  changes?: LineChange[];
  /**
   * Opaque revision of the exact bytes returned in text, for a later
   * revision-checked save. Absent without text, and from older servers.
   */
  revision?: string;
  /** Edit eligibility and text format; absent without text, and from older servers. */
  edit?: FileEditInfo;
}

/** Why a text file cannot be edited. Unknown future reasons may appear. */
export type FileEditReason =
  | 'too_large'
  | 'unsupported_newlines'
  | 'not_checked'
  | 'protected'
  | 'linked'
  | 'read_only'
  | 'unsupported'
  | (string & {});

/**
 * An editor snapshot to save. text has LF line breaks and no leading BOM;
 * the host restores the file's own BOM and line endings.
 */
export interface FileSaveRequest {
  path: string;
  text: string;
  /** The revision the text was edited from; a changed file conflicts. */
  expectedRevision: string;
}

/**
 * A saved file's new content, or a conflict: the file changed since it was
 * read and nothing was written.
 */
export type FileSaveResult =
  | { status: 'saved'; content: FileContent; observedRevision?: undefined }
  | { status: 'conflict'; content?: undefined; observedRevision?: string };

/**
 * Whether a text file may be edited, and the format a save must keep. Text
 * in FileContent is the file's bytes unchanged, BOM and CRs included.
 */
export interface FileEditInfo {
  /** Always sent; only an explicit true allows editing. */
  eligible: boolean;
  reason?: FileEditReason;
  /** The file's line endings; absent when they are mixed or bare CR. */
  newline?: 'lf' | 'crlf' | 'none';
  /** The file starts with a UTF-8 byte order mark. */
  bom?: boolean;
}

export interface Warning {
  file: string;
  line: number;
  msg: string;
}

export interface Diagnostics {
  warnings?: Warning[];
  errors?: string[];
}

export interface HandoffReport {
  estimatedTokens: number;
  budgetTokens: number;
  trimmed: boolean;
  droppedItems: string[];
  redactionCounts: {
    emails?: number;
    ipv4?: number;
    apiKeys?: number;
    sshKeys?: number;
    tokens?: number;
    passwords?: number;
  };
}

export interface HandoffRequest {
  ref: SessionRef;
  target: AgentID;
  budget?: number;
  includeReasoning?: boolean;
  redactSecrets?: boolean;
  cwd?: string;
}

export interface HandoffPreview {
  promptMarkdown: string;
  fullMarkdown: string;
  report: HandoffReport;
  contextFile: string;
  /** File the launch command tells the agent to read. */
  promptFile: string;
  command: string;
  promptBytes: number;
}

/** How sessions can be continued on this machine. */
export interface LaunchInfo {
  /** Open in terminal works for local sessions. */
  terminal: boolean;
  /** Settings chooses the terminal app (Linux, macOS); absent on older backends. */
  chooseTerminal?: boolean;
  /** Syntax of locally copied commands; remote commands are always POSIX. */
  shell: 'powershell' | 'posix';
  /** Open in terminal also works for a WSL distribution's sessions; absent on older backends. */
  wsl?: boolean;
  /** The built-in terminal panel runs here; absent on older backends. */
  panel?: boolean;
  /** Where Resume and "Continue in" open agents: the panel or an external terminal. */
  open?: TerminalOpenMode;
}

export type TerminalOpenMode = 'panel' | 'external';

/** A size in character cells. */
export interface TerminalSize {
  cols: number;
  rows: number;
}

/** A terminal running in the built-in panel, as the backend started it. */
export interface TerminalTab {
  id: string;
  label: string;
  /** Where it runs: "" for this computer, or the WSL or SSH host. */
  host: string;
  kind: 'agent' | 'shell';
  /**
   * The directory it starts in, where the backend knows it; empty is the
   * home directory, or unknown for an agent on a WSL or SSH host.
   */
  dir?: string;
}

export interface TerminalDataEvent {
  id: string;
  /** Output, base64-encoded. */
  data: string;
}

export interface TerminalExitEvent {
  id: string;
  code: number;
}

/** A terminal app installed on this computer. */
export interface TerminalOption {
  id: string;
  name: string;
}

/** The terminal this computer opens sessions in. Local, whichever host is selected. */
export interface TerminalSettings {
  /** False where the terminal is fixed (Windows PowerShell). */
  choose: boolean;
  /** Chosen terminal id; empty means Automatic. */
  selected: string;
  selectedName: string;
  /** The chosen terminal is no longer installed. */
  missing: boolean;
  /** What Automatic opens; null when no terminal is installed. */
  auto: TerminalOption | null;
  options: TerminalOption[];
  /** A note for this platform, such as macOS asking for permission. */
  hint?: string;
}

/**
 * Where messages are translated: an OpenAI-compatible endpoint called from
 * this computer, whichever host is selected. The API key is never returned.
 */
export interface TranslateSettings {
  baseURL: string;
  model: string;
  language: string;
  apiKeySet: boolean;
  /** Base URL, API key and model are all set. */
  configured: boolean;
}

/** Saves translation settings. An empty apiKey keeps the saved key. */
export interface TranslateSettingsRequest {
  baseURL: string;
  model: string;
  language: string;
  apiKey: string;
  clearApiKey: boolean;
}

/** An image to save on this computer: SVG text, or the image in base64. */
export interface SaveImageRequest {
  /** Suggested file name; the extension follows the format. */
  name: string;
  format: 'svg' | 'png' | 'jpeg' | 'gif' | 'webp';
  data: string;
}

/** Handoff files kept in the app data directory. */
export interface HandoffCacheInfo {
  dir: string;
  files: number;
  bytes: number;
}

export type ExportProfile = 'complete' | 'share-safe';

export interface RedactionCounts {
  pem?: number;
  token?: number;
  jwt?: number;
  assignment?: number;
  home?: number;
}

export interface FidelityReport {
  resolved: number;
  resolvedBytes: number;
  overflow?: string[];
  unavailable?: string[];
}

export interface ExportRequest {
  ref: SessionRef;
  profile: ExportProfile;
  budget?: number;
  includeReasoning?: boolean;
  redactSecrets?: boolean;
}

export interface ExportPreview {
  profile: ExportProfile;
  sessions: number;
  nativeFiles: number;
  nativeBytes: number;
  redaction: RedactionCounts;
  fidelity: FidelityReport;
  handoffTokens: number;
  warning?: string;
}

export interface BundleSessionSummary {
  ref: SessionRef;
  parentId?: string;
  nativeFiles: number;
  nativeBytes: number;
  title?: string;
}

export interface BundleSummary {
  bundleId: string;
  path: string;
  format: string;
  version: number;
  createdAt: string;
  appVersion?: string;
  profile: ExportProfile;
  source: {
    agent: AgentID;
    agentVersion?: string;
    id: string;
    cwd: string;
    repoRoot?: string;
    gitBranch?: string;
    title?: string;
  };
  sessionsCount: number;
  nativeFilesCount: number;
  nativeBytes: number;
  redactionRules?: Record<string, number>;
  redactionCounts: number;
  handoffTokens: number;
  verified: boolean;
  restoreAvailable: boolean;
  handoffAvailable: boolean;
  sessions: BundleSessionSummary[];
}

export interface BundleRestoreRequest {
  bundleId: string;
  /** Directory on the selected host the session is imported into. */
  cwd: string;
  /** Import under a new session id when the original id already exists. */
  copy?: boolean;
  /** The id the preview minted for a copy; pass it back when confirming. */
  copyId?: string;
}

export interface RestorePreviewItem {
  sourceRef: SessionRef;
  targetRef: SessionRef;
  targetCwd: string;
  paths: string[];
  bytes: number;
  blocked?: string;
  warning?: string;
  isCopy: boolean;
}

export interface RestorePreview {
  items: RestorePreviewItem[];
  totalBytes: number;
  token: string;
  copyId?: string;
}

export interface RestoreReport {
  items: { ref: SessionRef; title: string; ok: boolean; error?: string; created?: string[] }[];
  restored: number;
  failed: number;
  totalBytes: number;
  createdRefs: SessionRef[];
}

export interface BundleHandoffRequest {
  bundleId: string;
  target: AgentID;
  budget?: number;
  includeReasoning?: boolean;
  redactSecrets?: boolean;
  cwd?: string;
}

export type ConnectionPhase =
  | 'local'
  | 'connecting'
  | 'connected'
  | 'disconnected'
  | 'reconnecting';

/** The last commit that touched one line of a file (git blame). */
export interface BlameLine {
  /** 1-based line number the blame is for. */
  line: number;
  /** Full commit hash; all zeros when the line is uncommitted. */
  commit: string;
  author: string;
  /** Author time, Unix seconds. */
  authorTime: number;
  /** First line of the commit message; empty when uncommitted. */
  summary: string;
  /** The line has changes not yet committed. */
  uncommitted: boolean;
}

export interface HostCapabilities {
  /** Absent on older servers; editing requires an explicit true. */
  saveFile?: boolean;
  /** Absent on older servers; per-line blame requires an explicit true. */
  blame?: boolean;
  trash: boolean;
  manage: boolean;
  export: boolean;
  import: boolean;
  search: boolean;
}

export interface ConnectionState {
  phase: ConnectionPhase;
  host?: string;
  generation: number;
  error?: string;
  capabilities: HostCapabilities;
  appVersion?: string;
}

export interface HostEntry {
  name: string;
  hostName?: string;
  user?: string;
  port?: number;
}

/** A WSL distribution on this Windows computer; host is "wsl:<name>". */
export interface WSLEntry {
  name: string;
  host: string;
  default?: boolean;
}

export interface AskpassPrompt {
  id: string;
  prompt: string;
}



