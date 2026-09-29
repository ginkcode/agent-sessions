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
}

export interface DeleteResultItem {
  ref: SessionRef;
  title: string;
  ok: boolean;
  error?: string;
  moved?: string[] | null;
  remaining?: string[] | null;
}

export interface DeleteResult {
  items: DeleteResultItem[];
  deleted: number;
  failed: number;
  freedBytes: number;
  forgotten: SessionRef[] | null;
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

export interface BundleHandoffRequest {
  bundleId: string;
  target: AgentID;
  budget?: number;
  includeReasoning?: boolean;
  redactSecrets?: boolean;
  cwd?: string;
}


