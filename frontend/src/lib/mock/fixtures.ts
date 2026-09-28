import type { SessionMeta, Message, GroupNode, BlobResponse, Diagnostics } from '../types';

export const mockSessions: SessionMeta[] = [
  {
    ref: { agent: 'claude-code', id: 'session-claude-1' },
    title: 'Implement scan engine and catalog',
    firstPrompt: 'Implement scan engine and catalog for agent sessions',
    cwd: '/home/haith/Workspaces/ginkcode/tools/agent-sessions',
    repoRoot: '/home/haith/Workspaces/ginkcode/tools/agent-sessions',
    model: 'claude-sonnet-5',
    agentVersion: '0.2.14',
    gitBranch: 'main',
    createdAt: '2026-09-28T09:15:00Z',
    updatedAt: '2026-09-28T10:45:00Z',
    counts: { user: 4, assistant: 4, toolCalls: 8 },
    tokens: { input: 12400, output: 4300, cacheRead: 54000 },
    costUsd: 0.18,
    live: true,
    sourcePath: '/home/haith/.claude/projects/-home-haith-Workspaces-ginkcode-tools-agent-sessions/session-claude-1.jsonl',
  },
  {
    ref: { agent: 'claude-code', id: 'session-claude-2' },
    title: 'Fix SQLite WAL mode concurrent read lock',
    firstPrompt: 'Investigate why modernc.org/sqlite fails with database is locked in read-only mode',
    cwd: '/home/haith/Workspaces/ginkcode/tools/agent-sessions',
    repoRoot: '/home/haith/Workspaces/ginkcode/tools/agent-sessions',
    model: 'claude-opus-5-5',
    agentVersion: '0.2.14',
    gitBranch: 'main',
    createdAt: '2026-09-27T14:20:00Z',
    updatedAt: '2026-09-27T15:10:00Z',
    counts: { user: 2, assistant: 2, toolCalls: 3 },
    tokens: { input: 8200, output: 2100, cacheRead: 32000 },
    costUsd: 0.09,
    live: false,
    sourcePath: '/home/haith/.claude/projects/-home-haith-Workspaces-ginkcode-tools-agent-sessions/session-claude-2.jsonl',
  },
  {
    ref: { agent: 'codex', id: '01a0e61e-e703-75a2-bc1d-6349e32f6dd4' },
    title: 'Codex rollout parser test',
    firstPrompt: 'Hi, can you inspect the rollout format?',
    cwd: '/home/haith/Workspaces/ginkcode/tools/agent-sessions',
    repoRoot: '/home/haith/Workspaces/ginkcode/tools/agent-sessions',
    model: 'gpt-4o',
    agentVersion: 'codex-cli-0.8.2',
    gitBranch: 'main',
    createdAt: '2026-09-28T08:00:00Z',
    updatedAt: '2026-09-28T08:05:00Z',
    counts: { user: 1, assistant: 1, toolCalls: 0 },
    tokens: { input: 120, output: 45 },
    costUsd: 0.002,
    live: false,
    sourcePath: '/home/haith/.codex/sessions/2026/09/28/rollout-01a0e61e.jsonl',
  },
  {
    ref: { agent: 'opencode', id: 'opencode-sess-101' },
    title: 'Refactor HTTP handler routing and middleware',
    firstPrompt: 'Refactor HTTP handlers to use chi router and inject request logger',
    cwd: '/home/haith/Workspaces/backend-api',
    repoRoot: '/home/haith/Workspaces/backend-api',
    model: 'claude-3-7-sonnet',
    gitBranch: 'feature/routing',
    createdAt: '2026-09-26T11:00:00Z',
    updatedAt: '2026-09-26T13:30:00Z',
    counts: { user: 6, assistant: 6, toolCalls: 12 },
    tokens: { input: 24500, output: 6800 },
    costUsd: 0.32,
    live: false,
    sourcePath: '/home/haith/.local/share/opencode/opencode.db',
  },
  {
    ref: { agent: 'opencode', id: 'opencode-sess-archived' },
    title: 'Legacy documentation migration',
    firstPrompt: 'Migrate markdown docs to VitePress',
    cwd: '/home/haith/Workspaces/docs-old',
    cwdMissing: true,
    model: 'gpt-4-turbo',
    createdAt: '2026-08-10T10:00:00Z',
    updatedAt: '2026-08-10T11:00:00Z',
    counts: { user: 2, assistant: 2, toolCalls: 4 },
    tokens: { input: 5000, output: 1200 },
    costUsd: 0.04,
    archived: true,
    sourcePath: '/home/haith/.local/share/opencode/opencode.db',
  },
];

export const mockMessages: Record<string, Message[]> = {
  'session-claude-1': [
    {
      id: 'msg-1',
      role: 'user',
      time: '2026-09-28T09:15:00Z',
      parts: [
        {
          kind: 'text',
          text: 'Implement the scan engine and catalog for agent sessions.',
        },
      ],
    },
    {
      id: 'msg-2',
      role: 'assistant',
      time: '2026-09-28T09:16:10Z',
      model: 'claude-sonnet-5',
      tokens: { input: 1200, output: 450 },
      parts: [
        {
          kind: 'text',
          text: "I'll analyze the provider interface and design the concurrent `Runner` and in-memory `Catalog`.",
        },
        {
          kind: 'tool',
          tool: {
            id: 'call-1',
            name: 'Bash',
            input: { command: 'git status' },
            output: 'On branch main\nnothing to commit, working tree clean',
            status: 'completed',
          },
        },
        {
          kind: 'text',
          text: 'The working directory is clean. Let us create `internal/scan/catalog.go` and `internal/scan/scan.go`.',
        },
      ],
    },
    {
      id: 'msg-3',
      role: 'user',
      time: '2026-09-28T09:20:00Z',
      parts: [
        {
          kind: 'text',
          text: 'Make sure the catalog is thread-safe and supports incremental updates.',
        },
      ],
    },
    {
      id: 'msg-4',
      role: 'assistant',
      time: '2026-09-28T09:21:30Z',
      model: 'claude-sonnet-5',
      tokens: { input: 2400, output: 850 },
      parts: [
        {
          kind: 'text',
          text: 'Understood. We will use a `sync.RWMutex` protecting session maps keyed by `SessionRef.Key()`, and preserve sorting caches.',
        },
      ],
    },
  ],
};

export const mockBlobs: Record<string, BlobResponse> = {
  'session-claude-1:call-1': {
    data: 'On branch main\nnothing to commit, working tree clean',
    mime: 'text/plain',
    isBinary: false,
  },
};

export const mockDiagnostics: Diagnostics = {
  warnings: [],
  errors: [],
};
