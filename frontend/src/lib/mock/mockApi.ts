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
  Message,
  SearchFilter,
  SearchHit,
  SearchHitKind,
  FTSProgress,
  HandoffRequest,
  HandoffPreview,
  HandoffCacheInfo,
  HandoffReport,
  ExportRequest,
  ExportPreview,
  BundleSummary,
  BundleHandoffRequest,
  ConnectionState,
  HostCapabilities,
  HostEntry,
} from '../types.js';
import { mockSessions, mockMessages, mockBlobs, mockDiagnostics } from './fixtures.js';
import { refKey } from '../manage.js';

function searchNeedle(query: string): string {
  const words = query
    .replace(/\b(?:agent|dir):(?:"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\S+)/gi, ' ')
    .match(/"([^"]+)"|([^\s]+)/g);
  if (!words) return '';
  return words
    .map((word) => word.startsWith('"') ? word.slice(1, -1) : word)
    .join(' ')
    .trim()
    .toLocaleLowerCase();
}

function normalizeSearchDir(value?: string): string {
  const trimmed = value?.trim().replace(/\/+$/, '') || '';
  return trimmed === '' ? '' : trimmed;
}

function isInsideDir(cwd: string, dir: string): boolean {
  return cwd === dir || cwd.startsWith(`${dir}/`);
}

function escapeHTML(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export function highlightedSnippet(text: string, at: number, length: number): string {
  const start = Math.max(0, at - 48);
  const end = Math.min(text.length, at + length + 72);
  const prefix = start > 0 ? '…' : '';
  const suffix = end < text.length ? '…' : '';
  return `${prefix}${escapeHTML(text.slice(start, at))}<mark>${escapeHTML(text.slice(at, at + length))}</mark>${escapeHTML(text.slice(at + length, end))}${suffix}`;
}

function mockMessageSearchText(message: Message): { text: string; kind: SearchHitKind } {
  const text: string[] = [];
  const reasoning: string[] = [];
  const tools: string[] = [];
  for (const part of message.parts) {
    if (part.kind === 'text' && part.text) text.push(part.text);
    if (part.kind === 'reasoning' && part.text) reasoning.push(part.text);
    if (part.kind === 'tool' && part.tool) {
      let input = '';
      try {
        input = typeof part.tool.input === 'string'
          ? part.tool.input
          : JSON.stringify(part.tool.input ?? '');
      } catch {}
      tools.push(`${part.tool.name} ${input}`.trim());
    }
  }
  if (text.length) return { text: [...text, ...reasoning, ...tools].join('\n'), kind: 'text' };
  if (reasoning.length) return { text: [...reasoning, ...tools].join('\n'), kind: 'reasoning' };
  return { text: tools.join('\n'), kind: 'tool' };
}

export class MockBackendAPI {
  private sessions: SessionMeta[] = [...mockSessions];
  private messages: Record<string, typeof mockMessages[string]> = { ...mockMessages };
  private settings: ManageSettings = { enabled: false, allowPermanentDelete: false };
  private preview: { token: string; refs: string[] } | null = null;
  private hosts: HostEntry[] = [
    { name: 'dev-box', hostName: '192.168.1.50', user: 'dev', port: 22 },
    { name: 'prod-server', hostName: 'prod.example.com', user: 'admin', port: 22 },
  ];
  private connState: ConnectionState = {
    phase: 'local',
    generation: 0,
    capabilities: {
      trash: true,
      manage: true,
      export: true,
      import: true,
      search: true,
    },
  };
  private askpassReplies: Record<string, string> = {};

  async listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]> {
    const filtered = this.filterSessions(this.sessions, filter);
    if (mode === 'flat') {
      return filtered.map((s) => ({
        key: `flat:${s.ref.agent}:${s.ref.id}`,
        label: s.title || s.ref.id,
        kind: 'session',
        agent: s.ref.agent,
        cwd: s.cwd,
        cwdMissing: s.cwdMissing,
        sessionCount: 1,
        sessions: [s.ref],
      }));
    }

    if (mode === 'agent-dir') {
      const byAgent = new Map<string, SessionMeta[]>();
      for (const s of filtered) {
        const list = byAgent.get(s.ref.agent) || [];
        list.push(s);
        byAgent.set(s.ref.agent, list);
      }
      const roots: GroupNode[] = [];
      for (const [agent, agentSessions] of byAgent) {
        const dirMap = new Map<string, SessionMeta[]>();
        for (const s of agentSessions) {
          const list = dirMap.get(s.cwd) || [];
          list.push(s);
          dirMap.set(s.cwd, list);
        }
        const children: GroupNode[] = [];
        for (const [cwd, dirSessions] of dirMap) {
          children.push({
            key: `agent-dir:${agent}:${cwd}`,
            label: cwd.split('/').pop() || cwd,
            kind: 'directory',
            secondary: cwd,
            agent,
            cwd,
            cwdMissing: dirSessions.some((s) => s.cwdMissing),
            sessionCount: dirSessions.length,
            sessions: dirSessions.map((s) => s.ref),
          });
        }
        roots.push({
          key: `agent-dir:${agent}`,
          kind: 'agent',
          label: agent === 'claude-code' ? 'Claude Code' : agent === 'codex' ? 'Codex' : 'OpenCode',
          agent,
          sessionCount: agentSessions.length,
          children,
          sessions: agentSessions.map((s) => s.ref),
        });
      }
      return roots;
    }

    // Default: 'dir-agent'
    const byDir = new Map<string, SessionMeta[]>();
    for (const s of filtered) {
      const list = byDir.get(s.cwd) || [];
      list.push(s);
      byDir.set(s.cwd, list);
    }
    const roots: GroupNode[] = [];
    for (const [cwd, dirSessions] of byDir) {
      const agentMap = new Map<string, SessionMeta[]>();
      for (const s of dirSessions) {
        const list = agentMap.get(s.ref.agent) || [];
        list.push(s);
        agentMap.set(s.ref.agent, list);
      }
      const children: GroupNode[] = [];
      for (const [agent, agentSessions] of agentMap) {
        children.push({
          key: `dir-agent:${cwd}:${agent}`,
          kind: 'agent',
          label: agent === 'claude-code' ? 'Claude Code' : agent === 'codex' ? 'Codex' : 'OpenCode',
          agent,
          cwd,
          sessionCount: agentSessions.length,
          sessions: agentSessions.map((s) => s.ref),
        });
      }
      roots.push({
        key: `dir-agent:${cwd}`,
        kind: 'directory',
        label: cwd.split('/').pop() || cwd,
        secondary: cwd,
        cwd,
        cwdMissing: dirSessions.some((s) => s.cwdMissing),
        sessionCount: dirSessions.length,
        children,
        sessions: dirSessions.map((s) => s.ref),
      });
    }
    return roots;
  }

  async listSessions(
    groupKey: string,
    filter?: FilterOpts,
    sort?: SortOpts
  ): Promise<SessionMeta[]> {
    let result = this.filterSessions(this.sessions, filter);
    if (groupKey) {
      // Extract mode prefix from groupKey ('flat:', 'agent-dir:', 'dir-agent:')
      let mode: GroupMode = 'dir-agent';
      if (groupKey.startsWith('flat:')) {
        mode = 'flat';
      } else if (groupKey.startsWith('agent-dir:')) {
        mode = 'agent-dir';
      }
      const groups = await this.listGroups(mode, filter);
      const targetRefs = this.findSessionsInGroups(groups, groupKey);
      if (targetRefs) {
        const refKeys = new Set(targetRefs.map((r) => `${r.agent}:${r.id}`));
        result = result.filter((s) => refKeys.has(`${s.ref.agent}:${s.ref.id}`));
      }
    }

    if (sort) {
      result.sort((a, b) => {
        let diff = 0;
        if (sort.field === 'title') {
          diff = a.title.localeCompare(b.title);
        } else if (sort.field === 'created' || sort.field === 'createdAt') {
          diff = new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime();
        } else if (sort.field === 'messages' || sort.field === 'count') {
          const totalA = a.counts.user + a.counts.assistant;
          const totalB = b.counts.user + b.counts.assistant;
          diff = totalA - totalB;
        } else if (sort.field === 'tokens') {
          diff = a.tokens.input + a.tokens.output - (b.tokens.input + b.tokens.output);
        } else if (sort.field === 'cost' || sort.field === 'costUsd') {
          diff = (a.costUsd || 0) - (b.costUsd || 0);
        } else {
          // Default: updated
          diff = new Date(a.updatedAt).getTime() - new Date(b.updatedAt).getTime();
        }
        return sort.desc ? -diff : diff;
      });
    }
    return result;
  }

  async getSessionMeta(ref: SessionRef): Promise<SessionMeta> {
    const s = this.sessions.find(
      (m) => m.ref.agent === ref.agent && m.ref.id === ref.id
    );
    if (!s) {
      throw new Error(`unknown session: ${ref.agent}:${ref.id}`);
    }
    return s;
  }

  async getMessages(
    ref: SessionRef,
    offset: number,
    limit: number
  ): Promise<MessagesPage> {
    const list = this.messages[ref.id] || [
      {
        id: 'mock-1',
        role: 'user',
        time: new Date().toISOString(),
        parts: [{ kind: 'text', text: 'Sample question in mock session' }],
      },
      {
        id: 'mock-2',
        role: 'assistant',
        time: new Date().toISOString(),
        parts: [{ kind: 'text', text: 'Sample answer in mock session' }],
      },
    ];

    const totalCount = list.length;
    const start = Math.min(offset, totalCount);
    const end = Math.min(start + limit, totalCount);
    return {
      messages: list.slice(start, end),
      offset,
      limit,
      totalCount,
      hasMore: end < totalCount,
    };
  }

  async search(query: string, filter: SearchFilter = {}): Promise<SearchHit[]> {
    const needle = searchNeedle(query);
    if (!needle) return [];
    const agents = filter.agents?.length ? new Set(filter.agents) : null;
    const dir = normalizeSearchDir(filter.dir);
    const limit = Math.max(1, Math.min(100, filter.limit || 30));
    const hits: SearchHit[] = [];

    for (const session of this.sessions) {
      if (agents && !agents.has(session.ref.agent)) continue;
      if (dir && !isInsideDir(session.cwd, dir)) continue;

      const titleAt = session.title.toLocaleLowerCase().indexOf(needle);
      if (titleAt >= 0) {
        hits.push({
          ref: session.ref,
          messageIndex: -1,
          snippet: highlightedSnippet(session.title, titleAt, needle.length),
          score: 100 - titleAt,
          kind: 'title',
        });
      }

      const list = this.messages[session.ref.id] || [];
      list.forEach((message, messageIndex) => {
        const searchable = mockMessageSearchText(message);
        const at = searchable.text.toLocaleLowerCase().indexOf(needle);
        if (at < 0) return;
        hits.push({
          ref: session.ref,
          messageIndex,
          snippet: highlightedSnippet(searchable.text, at, needle.length),
          score: 50 - at / 100,
          kind: searchable.kind,
        });
      });
    }

    return hits
      .sort((a, b) => b.score - a.score || a.ref.id.localeCompare(b.ref.id) || a.messageIndex - b.messageIndex)
      .slice(0, limit);
  }

  async indexProgress(): Promise<FTSProgress> {
    return { done: this.sessions.length, pending: 0, failed: 0, running: false };
  }

  async getBlob(ref: SessionRef, key: string): Promise<BlobResponse> {
    const compositeKey = `${ref.id}:${key}`;
    if (mockBlobs[compositeKey]) {
      return mockBlobs[compositeKey];
    }
    return {
      data: `Mock blob content for ${ref.agent}:${ref.id} key ${key}`,
      mime: 'text/plain',
      isBinary: false,
    };
  }

  async copyResumeCommand(ref: SessionRef): Promise<string> {
    const meta = await this.getSessionMeta(ref);
    if (ref.agent === 'codex') {
      return `cd '${meta.cwd}' && codex resume '${ref.id}'`;
    }
    if (ref.agent === 'opencode') {
      return `cd '${meta.cwd}' && opencode --session '${ref.id}'`;
    }
    return `cd '${meta.cwd}' && claude --resume '${ref.id}'`;
  }

  async revealSource(ref: SessionRef): Promise<void> {
    console.info('[MockAPI] revealSource:', ref);
  }

  async getDiagnostics(): Promise<Diagnostics> {
    return mockDiagnostics;
  }

  async getSettings(): Promise<ManageSettings> {
    return { ...this.settings };
  }

  async setManageEnabled(enabled: boolean): Promise<ManageSettings> {
    this.settings = { ...this.settings, enabled };
    if (!enabled) {
      this.settings = { ...this.settings, allowPermanentDelete: false };
    }
    return { ...this.settings };
  }

  async setAllowPermanentDelete(allow: boolean): Promise<ManageSettings> {
    if (!this.settings.enabled) {
      throw new Error('Session management must be enabled first');
    }
    this.settings = { ...this.settings, allowPermanentDelete: allow };
    return { ...this.settings };
  }

  async previewDelete(refs: SessionRef[]): Promise<DeletePreview> {
    if (!this.settings.enabled) {
      throw new Error('Session management is disabled');
    }
    if (!refs.length) {
      throw new Error('no sessions selected');
    }
    const items = [];
    for (const ref of refs) {
      const s = this.sessions.find(
        (m) => m.ref.agent === ref.agent && m.ref.id === ref.id
      );
      if (!s) {
        throw new Error(`unknown session: ${ref.agent}:${ref.id}`);
      }
      const paths = s.sourcePath ? [s.sourcePath] : [];
      // Mock heuristics: archived or codex (archive-backed) previews as
      // permanent (irreversible) unless the path looks Trash-restorable;
      // live sessions are blocked.
      const reversible = s.ref.agent !== 'opencode' && !s.archived;
      items.push({
        ref: s.ref,
        agent: s.ref.agent,
        title: s.title,
        paths,
        bytes: paths.length * 4096 + s.tokens.output,
        reversible,
        warning:
          s.ref.agent === 'opencode'
            ? 'OpenCode will permanently delete both OpenCode 1.x and 2.x copies of this session and its child sessions. It will not go to Trash and cannot be restored.'
            : undefined,
        blocked: s.live ? 'session is live: session is currently active' : undefined,
        action: reversible ? 'trash' : 'delete',
      });
    }
    const token = `preview-${this.previewSeq++}`;
    // Like the backend, the token binds only the actionable items.
    this.preview = {
      token,
      refs: items.filter((i) => !i.blocked).map((i) => refKey(i.ref)),
    };
    return { items, totalBytes: items.reduce((n, i) => n + i.bytes, 0), token };
  }

  async deleteSessions(refs: SessionRef[], token: string): Promise<DeleteResult> {
    if (!this.settings.enabled) {
      throw new Error('Session management is disabled');
    }
    if (!this.preview || this.preview.token !== token) {
      throw new Error('delete preview token missing or expired');
    }
    const wanted = new Set(refs.map(refKey));
    if (
      wanted.size !== this.preview.refs.length ||
      refs.some((r) => !this.preview!.refs.includes(refKey(r)))
    ) {
      throw new Error('session set changed since preview');
    }
    this.preview = null;

    const items = [];
    let deleted = 0;
    let failed = 0;
    let freedBytes = 0;
    const forgotten: SessionRef[] = [];
    for (const ref of refs) {
      const idx = this.sessions.findIndex(
        (s) => s.ref.agent === ref.agent && s.ref.id === ref.id
      );
      const s = idx >= 0 ? this.sessions[idx] : null;
      if (!s) {
        items.push({
          ref,
          title: ref.id,
          ok: false,
          error: `unknown session: ${ref.agent}:${ref.id}`,
          moved: [],
          remaining: [],
        });
        failed++;
        continue;
      }
      if (s.live) {
        items.push({
          ref,
          title: s.title,
          ok: false,
          error: 'session is live',
          moved: [],
          remaining: s.sourcePath ? [s.sourcePath] : [],
        });
        failed++;
        continue;
      }
      const paths = s.sourcePath ? [s.sourcePath] : [];
      forgotten.push(ref);
      this.sessions.splice(idx, 1);
      delete this.messages[ref.id];
      items.push({
        ref,
        title: s.title,
        ok: true,
        error: '',
        moved: paths,
        remaining: [],
      });
      deleted++;
      freedBytes += paths.length * 4096 + s.tokens.output;
    }
    return { items, deleted, failed, freedBytes, forgotten };
  }

  private previewSeq = 1;

  async buildHandoff(req: HandoffRequest): Promise<HandoffPreview> {
    const meta = await this.getSessionMeta(req.ref);
    const cwd = req.cwd || meta.cwd;
    const target = req.target;
    const promptFile = `/tmp/handoffs/${req.ref.id}-handoff.md`;
    const cmd = mockLaunchCommand(target, cwd, promptFile);

    const report: HandoffReport = {
      estimatedTokens: 1250,
      budgetTokens: req.budget || 80000,
      trimmed: false,
      droppedItems: [],
      redactionCounts: {},
    };

    const prompt = `# Handoff to ${target}\n\nTask: ${meta.title}\nCWD: ${cwd}\n\nWait for the user to confirm or give the next request.`;
    return {
      promptMarkdown: prompt,
      fullMarkdown: prompt + '\n\n## Timeline\n(Full conversation history)',
      report,
      contextFile: `/tmp/handoffs/${req.ref.id}-full.md`,
      promptFile,
      command: cmd,
      promptBytes: prompt.length,
    };
  }

  async handoffCommand(req: HandoffRequest): Promise<string> {
    const preview = await this.buildHandoff(req);
    this.handoffFiles.add(preview.promptFile);
    return preview.command;
  }

  private handoffFiles = new Set<string>();

  async handoffCache(): Promise<HandoffCacheInfo> {
    // Each handoff writes a prompt file and a full-context file.
    const files = this.handoffFiles.size * 2;
    return { dir: '/tmp/handoffs', files, bytes: files * 2048 };
  }

  async clearHandoffCache(): Promise<HandoffCacheInfo> {
    this.handoffFiles.clear();
    return this.handoffCache();
  }

  async saveHandoff(req: HandoffRequest): Promise<string> {
    const meta = await this.getSessionMeta(req.ref);
    return `/mock/downloads/${meta.ref.id}-handoff.md`;
  }

  async previewExport(req: ExportRequest): Promise<ExportPreview> {
    const meta = await this.getSessionMeta(req.ref);
    const shareSafe = req.profile === 'share-safe';
    return {
      profile: req.profile,
      sessions: 1 + (meta.parentId ? 0 : 1),
      nativeFiles: shareSafe ? 0 : 2,
      nativeBytes: shareSafe ? 0 : 4096,
      redaction: shareSafe || req.redactSecrets ? { token: 1, home: 2 } : {},
      fidelity: { resolved: 1, resolvedBytes: 2048 },
      handoffTokens: 1250,
      warning: shareSafe
        ? undefined
        : 'A complete bundle contains the session\'s original records and can include secrets such as tokens, keys, and passwords.',
    };
  }

  async exportBundle(req: ExportRequest): Promise<string> {
    await this.previewExport(req);
    const meta = this.sessions.find((s) => s.ref.agent === req.ref.agent && s.ref.id === req.ref.id);
    const dir = meta?.cwd.split('/').filter(Boolean).pop();
    const shortId = req.ref.id.split(/[-/]/)[0].slice(0, 12);
    const name = [req.ref.agent, dir, shortId].filter(Boolean).join('_');
    return `/mock/downloads/${name}.agent-session.zip`;
  }

  private mockBundles = new Map<string, BundleSummary>();

  async openBundle(): Promise<BundleSummary | null> {
    return this.openBundlePath('/mock/downloads/sample.agent-session.zip');
  }

  async openBundlePath(path: string): Promise<BundleSummary> {
    const summary: BundleSummary = {
      bundleId: 'mock-bundle-1234',
      path,
      format: 'agent-sessions.bundle',
      version: 1,
      createdAt: new Date().toISOString(),
      appVersion: '0.6.0',
      profile: 'complete',
      source: {
        agent: 'claude-code',
        id: 'mock-imported-session-id',
        cwd: '/home/user/work/project',
        gitBranch: 'main',
        title: 'Imported Mock Session',
      },
      sessionsCount: 1,
      nativeFilesCount: 2,
      nativeBytes: 4096,
      redactionCounts: 0,
      handoffTokens: 1250,
      verified: true,
      restoreAvailable: true,
      handoffAvailable: true,
      sessions: [
        {
          ref: { agent: 'claude-code', id: 'mock-imported-session-id' },
          nativeFiles: 2,
          nativeBytes: 4096,
          title: 'Imported Mock Session',
        },
      ],
    };
    this.mockBundles.set(summary.bundleId, summary);
    return summary;
  }

  async buildBundleHandoff(req: BundleHandoffRequest): Promise<HandoffPreview> {
    const bundle = this.mockBundles.get(req.bundleId);
    const title = bundle?.source?.title || 'Imported Mock Session';
    const cwd = req.cwd || bundle?.source?.cwd || '/home/user/work/project';
    const target = req.target;
    const promptFile = `/tmp/handoffs/bundle-${req.bundleId}-handoff.md`;
    const cmd = mockLaunchCommand(target, cwd, promptFile);

    const report: HandoffReport = {
      estimatedTokens: 1250,
      budgetTokens: req.budget || 80000,
      trimmed: false,
      droppedItems: [],
      redactionCounts: {},
    };

    const prompt = `# Handoff to ${target}\n\nTask: ${title}\nCWD: ${cwd}\n\nWait for the user to confirm or give the next request.`;
    return {
      promptMarkdown: prompt,
      fullMarkdown: prompt + '\n\n## Timeline\n(Full conversation history)',
      report,
      contextFile: `/tmp/handoffs/bundle-${req.bundleId}-full.md`,
      promptFile,
      command: cmd,
      promptBytes: prompt.length,
    };
  }

  async bundleHandoffCommand(req: BundleHandoffRequest): Promise<string> {
    const preview = await this.buildBundleHandoff(req);
    this.handoffFiles.add(preview.promptFile);
    return preview.command;
  }

  async saveBundleHandoff(req: BundleHandoffRequest): Promise<string> {
    return `/mock/downloads/bundle-${req.bundleId}-handoff.md`;
  }

  async appVersion(): Promise<string> {
    return 'dev';
  }

  async openURL(url: string): Promise<void> {
    if (typeof window !== 'undefined') {
      window.open(url, '_blank');
    }
  }

  // Fixtures are static; the delay just makes the refreshing state visible.
  async scan(): Promise<void> {
    await new Promise((resolve) => setTimeout(resolve, 300));
  }

  async listHosts(): Promise<HostEntry[]> {
    return [...this.hosts];
  }

  setMockHosts(hosts: HostEntry[]): void {
    this.hosts = [...hosts];
  }

  async connectionState(): Promise<ConnectionState> {
    return { ...this.connState };
  }

  async connect(alias: string, succeed: boolean = true): Promise<void> {
    this.connState.generation++;
    const gen = this.connState.generation;
    this.connState = {
      phase: 'connecting',
      host: alias,
      generation: gen,
      capabilities: {
        trash: true,
        manage: true,
        export: true,
        import: true,
        search: true,
      },
    };
    this.emit('connection:state', { ...this.connState });

    if (!succeed) return;

    const caps: HostCapabilities = {
      trash: alias !== 'prod-server',
      manage: true,
      export: true,
      import: true,
      search: true,
    };
    this.connState = {
      phase: 'connected',
      host: alias,
      generation: gen,
      capabilities: caps,
      appVersion: 'v0.2.4',
    };
    this.emit('connection:state', { ...this.connState });
  }

  async disconnect(): Promise<void> {
    this.connState.generation++;
    this.connState = {
      phase: 'local',
      host: undefined,
      generation: this.connState.generation,
      capabilities: {
        trash: true,
        manage: true,
        export: true,
        import: true,
        search: true,
      },
    };
    this.emit('connection:state', { ...this.connState });
  }

  async askpassReply(id: string, answer: string): Promise<boolean> {
    this.askpassReplies[id] = answer;
    return true;
  }

  simulateAskpass(id: string, prompt: string): void {
    this.emit('askpass:prompt', { id, prompt });
  }

  setMockConnectionState(state: Partial<ConnectionState>): void {
    this.connState = { ...this.connState, ...state };
    this.emit('connection:state', { ...this.connState });
  }

  private hostEnv: Record<string, string> = {};

  async setHostEnv(env: Record<string, string>): Promise<void> {
    this.hostEnv = { ...env };
  }

  async getHostEnv(): Promise<Record<string, string>> {
    return { ...this.hostEnv };
  }

  private listeners = new Map<string, Set<(...data: any[]) => void>>();

  onEvent(name: string, callback: (...data: any[]) => void): () => void {
    let set = this.listeners.get(name);
    if (!set) {
      set = new Set();
      this.listeners.set(name, set);
    }
    // Wrap so registering one callback twice yields independent listeners.
    const listener = (...data: any[]) => callback(...data);
    set.add(listener);
    return () => {
      set.delete(listener);
    };
  }

  /** Delivers an event to every listener, mimicking the Wails runtime. */
  emit(name: string, ...data: any[]): void {
    for (const listener of [...(this.listeners.get(name) ?? [])]) {
      listener(...data);
    }
  }

  async agentCounts(filter?: FilterOpts): Promise<Record<string, number>> {
    const counts: Record<string, number> = {};
    for (const s of this.filterSessions(this.sessions, { ...filter, agent: undefined })) {
      counts[s.ref.agent] = (counts[s.ref.agent] || 0) + 1;
    }
    return counts;
  }

  private filterSessions(list: SessionMeta[], f?: FilterOpts): SessionMeta[] {
    if (!f) return list.filter((s) => !s.archived);
    return list.filter((s) => {
      if (f.agent && s.ref.agent !== f.agent) return false;
      if (f.liveOnly && !s.live) return false;
      if (!f.archived && s.archived) return false;
      if (f.query) {
        const q = f.query.toLowerCase();
        const match =
          s.title.toLowerCase().includes(q) ||
          (s.firstPrompt?.toLowerCase().includes(q) ?? false) ||
          s.cwd.toLowerCase().includes(q) ||
          (s.model?.toLowerCase().includes(q) ?? false);
        if (!match) return false;
      }
      const path = f.path?.trim().toLowerCase();
      if (path && !s.cwd.toLowerCase().includes(path)) return false;
      return true;
    });
  }

  private findSessionsInGroups(nodes: GroupNode[], key: string): SessionRef[] | null {
    for (const node of nodes) {
      if (node.key === key) {
        return node.sessions || null;
      }
      if (node.children) {
        const found = this.findSessionsInGroups(node.children, key);
        if (found) return found;
      }
    }
    return null;
  }
}

/** Mirrors handoff.BuildLaunchCommand: the prompt is a pointer to the file. */
function mockLaunchCommand(target: string, cwd: string, promptFile: string): string {
  const prompt = `'Read ${promptFile} completely to restore the context of an earlier session, then follow its instructions and wait for my next request.'`;
  const argv = target === 'opencode' ? `opencode --prompt ${prompt}` : `${target === 'codex' ? 'codex' : 'claude'} ${prompt}`;
  return `cd '${cwd}' && ${argv}`;
}
