export type AgentID = 'claude-code' | 'codex' | 'opencode' | string;

export interface SessionRef {
  agent: AgentID;
  id: string;
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
}

export interface SortOpts {
  field: string;
  desc: boolean;
}

export interface GroupNode {
  key: string;
  label: string;
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
