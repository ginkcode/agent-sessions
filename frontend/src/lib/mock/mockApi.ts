import { MockCodeDiagnostics } from './codeDiagnostics';
import { searchLimit, searchRegExp, searchText, searchesPath } from '../contentSearch';
import { replaceInText } from '../searchReplace';
import { validatePortForward, type PortForwardRequest, type PortForwardState, type PortForwardRule } from '../portForward';
import type {
  ReplaceRequest,
  ReplaceResult,
  GroupMode,
  FilterOpts,
  SortOpts,
  GroupNode,
  SessionMeta,
  SessionRef,
  MessagesPage,
  BlobResponse,
  FileListing,
  FileContent,
  FileProbeResult,
  FileSaveRequest,
  FileSaveResult,
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
  LaunchInfo,
  TerminalSettings,
  TerminalOption,
  TranslateSettings,
  TranslateSettingsRequest,
  SaveImageRequest,
  HandoffReport,
  ExportRequest,
  ExportPreview,
  BundleSummary,
  BundleHandoffRequest,
  BundleRestoreRequest,
  RestorePreview,
  RestoreReport,
  ConnectionState,
  HostCapabilities,
  BlameLine,
  SearchQuery,
  SearchResult,
  HostEntry,
  WSLEntry,
  TerminalOpenMode,
  TerminalSize,
  TerminalTab,
  AgentID,
  AgentStatus,
} from '../types.js';
import { mockSessions, mockMessages, mockBlobs, mockDiagnostics } from './fixtures.js';
import { refKey } from '../manage.js';
import { wslDistro } from '../hosts.js';

async function mockFileRevision(text: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
  return 'sha256:' + Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, '0')).join('');
}

/** A small deterministic 32-bit hash (FNV-1a). */
function mockHash(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h;
}

/** A deterministic 40-hex fake commit hash for seed. */
function mockCommit(seed: string): string {
  let out = '';
  for (let i = 0; out.length < 40; i++) out += mockHash(`${seed}:${i}`).toString(16).padStart(8, '0');
  return out.slice(0, 40);
}

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

function toBase64(text: string): string {
  let binary = '';
  for (const byte of new TextEncoder().encode(text)) binary += String.fromCharCode(byte);
  return btoa(binary);
}

/** A terminal of the mock panel: it echoes input and runs nothing. */
interface MockTerminal {
  tab: TerminalTab;
  line: string;
  size: TerminalSize;
  unacked: number;
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

export class MockBackendAPI extends MockCodeDiagnostics {
  async managedTool(request: import('../managedTools').ManagedToolRequest): Promise<import('../managedTools').ManagedToolState> {
    const connection = await this.connectionState();
    if (!connection.hostId || request.expectedHostId !== connection.hostId) throw new Error('Actual host identity required');
    return { hostId: connection.hostId, state: 'unavailable', message: 'Mock only: no downloads, builds or installs are performed.' };
  }
  constructor() { super((name, event) => this.emit(name, event)); }
  private sessions: SessionMeta[] = [...mockSessions];
  private messages: Record<string, typeof mockMessages[string]> = { ...mockMessages };
  private settings: ManageSettings = { enabled: false, allowPermanentDelete: false, prunePromptHistory: false };
  private preview: { token: string; refs: string[] } | null = null;
  private hosts: HostEntry[] = [
    { name: 'dev-box', hostName: '192.168.1.50', user: 'dev', port: 22 },
    { name: 'prod-server', hostName: 'prod.example.com', user: 'admin', port: 22 },
  ];
  /** WSL distributions "registered" on this computer. */
  private wslDistros: WSLEntry[] = [
    { name: 'Ubuntu', host: 'wsl:Ubuntu', default: true },
    { name: 'Debian', host: 'wsl:Debian' },
  ];
  private connState: ConnectionState = {
    phase: 'local',
    hostId: 'mock:local',
    generation: 0,
    capabilities: {
      codeDiagnostics: true,
      liveCodeDiagnostics: false,
      trash: true,
      manage: true,
      export: true,
      import: true,
      search: true,
    },
  };
  private askpassReplies: Record<string, string> = {};
  /** Terminal support the mock reports; tests can enable it. */
  launch: LaunchInfo = { terminal: false, shell: 'posix', panel: true, open: 'panel' };
  /** Terminal apps "installed", and the one chosen ('' is Automatic). */
  terminals: TerminalOption[] = [];
  chosenTerminal = '';
  /** Commands "opened" in a terminal, newest last. */
  openedTerminals: string[] = [];
  /** Translation settings; the key stays here, as it stays in the backend. */
  translateConfig = { baseURL: '', model: '', language: 'Vietnamese', apiKey: '' };
  /** Images saved, newest last. */
  savedImages: SaveImageRequest[] = [];
  /** Texts sent to translate, newest last. */
  translated: string[] = [];
  translateDelayMs = 400;

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
            label: cwd,
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
        label: cwd,
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

    if (offset < -1 || limit <= 0) throw new Error(`Invalid page offset=${offset} limit=${limit}`);
    const totalCount = list.length;
    if (offset === -1) offset = Math.max(0, totalCount - limit);
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

  // Mirrors the backend: a new session starts only in a directory the
  // catalog knows, with an agent it has a provider for.
  private newSessionCommand(agent: AgentID, dir: string): string {
    const argv0 = ({ 'claude-code': 'claude', codex: 'codex', opencode: 'opencode' } as Record<string, string>)[agent];
    if (!argv0) throw new Error(`unknown provider: ${agent}`);
    if (!dir || !this.sessions.some((s) => s.cwd === dir)) throw new Error(`unknown directory: ${dir}`);
    if (this.missingAgents.has(agent)) throw new Error(`agent CLI not found: ${argv0} was not found on PATH or in its usual install locations. Install it, then try again`);
    return `cd '${dir}' && ${argv0}`;
  }

  async launchInfo(): Promise<LaunchInfo> {
    return { ...this.launch };
  }

  // Mirrors the backend: only an installed terminal can be chosen.
  async terminalSettings(): Promise<TerminalSettings> {
    if (!this.launch.chooseTerminal) {
      return { choose: false, selected: '', selectedName: '', missing: false, auto: null, options: [] };
    }
    const id = this.chosenTerminal;
    const found = this.terminals.find((t) => t.id === id);
    return {
      choose: true,
      selected: id,
      selectedName: id ? (found?.name ?? id) : '',
      missing: id !== '' && !found,
      auto: this.terminals[0] ? { ...this.terminals[0] } : null,
      options: this.terminals.map((t) => ({ ...t })),
    };
  }

  async setTerminal(id: string): Promise<TerminalSettings> {
    if (!this.launch.chooseTerminal) {
      throw new Error('opening a terminal is not supported on this platform yet; copy the command instead');
    }
    if (id && !this.terminals.some((t) => t.id === id)) {
      throw new Error(`terminal "${id}" is not installed on this computer`);
    }
    this.chosenTerminal = id;
    return this.terminalSettings();
  }

  async translateSettings(): Promise<TranslateSettings> {
    const c = this.translateConfig;
    return {
      baseURL: c.baseURL,
      model: c.model,
      language: c.language,
      apiKeySet: c.apiKey !== '',
      configured: c.baseURL !== '' && c.apiKey !== '' && c.model !== '',
    };
  }

  // Mirrors the backend: an empty key keeps the saved one.
  async setTranslateSettings(req: TranslateSettingsRequest): Promise<TranslateSettings> {
    const baseURL = req.baseURL.trim();
    if (baseURL && !/^https?:\/\/[^/]/.test(baseURL)) {
      throw new Error('base URL must be an http or https URL, such as https://api.openai.com/v1');
    }
    const c = this.translateConfig;
    this.translateConfig = {
      baseURL,
      model: req.model.trim(),
      language: req.language.trim() || 'Vietnamese',
      apiKey: req.clearApiKey ? '' : req.apiKey.trim() || c.apiKey,
    };
    return this.translateSettings();
  }

  async translate(text: string): Promise<string> {
    if (!(await this.translateSettings()).configured) {
      throw new Error('translation is not set up: add a base URL, API key and model in Settings');
    }
    this.translated.push(text);
    if (this.translateDelayMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, this.translateDelayMs));
    }
    return `[${this.translateConfig.language}] ${text}`;
  }

  async saveImage(req: SaveImageRequest): Promise<string> {
    this.savedImages.push(req);
    return `/home/mock/${req.name || 'diagram'}.${req.format}`;
  }

  async printWindow(): Promise<void> {
    if (typeof window !== 'undefined') window.print();
  }

  // Mirrors the backend: local sessions use the chosen terminal; WSL uses
  // the distro's console, while SSH sessions only offer Copy command.
  private async openTerminal(build: () => Promise<string>): Promise<void> {
    const distro = wslDistro(this.connState.host);
    if (this.connState.phase !== 'local' && distro) {
      if (!this.launch.wsl) throw new Error('WSL is not installed');
      if (this.connState.phase !== 'connected') throw new Error('remote disconnected');
      this.openedTerminals.push(await build());
      return;
    }
    if (!this.launch.terminal) {
      throw new Error('opening a terminal is not supported on this platform yet; copy the command instead');
    }
    if (this.connState.phase !== 'local') {
      throw new Error('open in terminal works for local and WSL sessions only; use Copy command and paste it into a shell on the host');
    }
    const terminal = await this.terminalSettings();
    if (terminal.missing) {
      throw new Error(`${terminal.selectedName} was not found. Choose another terminal in Settings, or use Copy command`);
    }
    this.openedTerminals.push(await build());
  }

  async openResumeInTerminal(ref: SessionRef): Promise<void> {
    await this.openTerminal(() => this.copyResumeCommand(ref));
  }

  /** Agents whose CLI the mock host reports missing. */
  missingAgents = new Set<AgentID>();

  async newSessionAgents(): Promise<AgentStatus[]> {
    if (this.connState.phase !== 'local' && this.connState.phase !== 'connected') throw new Error('remote disconnected');
    return [
      { agent: 'claude-code', name: 'Claude Code' },
      { agent: 'codex', name: 'Codex' },
      { agent: 'opencode', name: 'OpenCode' },
    ].map((a) => this.missingAgents.has(a.agent)
      ? { ...a, installed: false, hint: `${a.name} was not found on PATH or in its usual install locations. Install it, then try again` }
      : { ...a, installed: true });
  }

  async openNewSessionInTerminal(agent: AgentID, dir: string): Promise<void> {
    await this.openTerminal(async () => this.newSessionCommand(agent, dir));
  }

  async openHandoffInTerminal(req: HandoffRequest): Promise<void> {
    await this.openTerminal(() => this.handoffCommand(req));
  }

  async openBundleHandoffInTerminal(req: BundleHandoffRequest): Promise<void> {
    await this.openTerminal(() => this.bundleHandoffCommand(req));
  }

  /** The mock panel's running terminals by id. */
  panelTerminals = new Map<string, MockTerminal>();
  private nextTerminal = 1;

  // Mirrors the backend: agents start on the host the session belongs to,
  // SSH included; a shell needs no connection to the host's server.
  private startPanel(kind: TerminalTab['kind'], size: TerminalSize, banner: string, needsServer: boolean, dir = '', named: Pick<TerminalTab, 'label' | 'agent'> | null = null): TerminalTab {
    if (!this.launch.panel) throw new Error('the built-in terminal is not available');
    const host = this.connState.phase === 'local' ? '' : (this.connState.host ?? '');
    if (host && wslDistro(host) && !this.launch.wsl) throw new Error('WSL is not installed');
    if (host && needsServer && this.connState.phase !== 'connected') throw new Error('remote disconnected');
    const tab: TerminalTab = {
      id: `t${this.nextTerminal++}`,
      label: kind === 'agent' ? 'Agent' : 'Shell',
      host,
      kind,
      ...(dir && { dir }),
      ...named,
      startedAt: Date.now(),
    };
    this.panelTerminals.set(tab.id, { tab, line: '', size: { ...size }, unacked: 0 });
    // Output can arrive before the start reply, as it can from the backend.
    this.terminalOutput(tab.id, `\x1b[2m${banner}\x1b[0m\r\n${this.prompt(tab)}`);
    return { ...tab };
  }

  private prompt(tab: TerminalTab): string {
    return `\x1b[32m${tab.host || 'mock'}\x1b[0m $ `;
  }

  private terminalOutput(id: string, text: string): void {
    const t = this.panelTerminals.get(id);
    if (t) t.unacked += new TextEncoder().encode(text).length;
    this.emit('term:data', { id, data: toBase64(text) });
  }

  async panelResume(ref: SessionRef, size: TerminalSize): Promise<TerminalTab> {
    return this.startPanel('agent', size, `[mock] ${await this.copyResumeCommand(ref)}`, true);
  }

  async panelNewSession(agent: AgentID, dir: string, size: TerminalSize): Promise<TerminalTab> {
    const label = agent === 'claude-code' ? 'Claude Code' : agent === 'codex' ? 'Codex' : agent === 'opencode' ? 'OpenCode' : 'Agent';
    const banner = `[mock] ${this.newSessionCommand(agent, dir)}`;
    return this.startPanel('agent', size, banner, true, dir, { label, agent });
  }

  async panelHandoff(req: HandoffRequest, size: TerminalSize): Promise<TerminalTab> {
    return this.startPanel('agent', size, `[mock] ${await this.handoffCommand(req)}`, true);
  }

  async panelBundleHandoff(req: BundleHandoffRequest, size: TerminalSize): Promise<TerminalTab> {
    return this.startPanel('agent', size, `[mock] ${await this.bundleHandoffCommand(req)}`, true);
  }

  async panelShell(cwd: string, size: TerminalSize): Promise<TerminalTab> {
    return this.startPanel('shell', size, `[mock] shell in ${cwd || '~'}`, false, cwd);
  }

  // Echoes typed input; Enter starts a new prompt and "exit" ends the tab.
  async terminalWrite(id: string, data: string): Promise<void> {
    const t = this.panelTerminals.get(id);
    if (!t) throw new Error(`terminal not found: ${id}`);
    let out = '';
    for (const ch of data) {
      if (ch === '\r') {
        const line = t.line.trim();
        t.line = '';
        if (line === 'exit') {
          this.terminalOutput(id, `${out}\r\n`);
          this.endTerminal(id, 0);
          return;
        }
        out += `\r\n${line ? `mock: ${line}\r\n` : ''}${this.prompt(t.tab)}`;
      } else if (ch === '\x7f') {
        if (t.line) {
          t.line = t.line.slice(0, -1);
          out += '\b \b';
        }
      } else if (ch >= ' ') {
        t.line += ch;
        out += ch;
      }
    }
    if (out) this.terminalOutput(id, out);
  }

  async terminalResize(id: string, cols: number, rows: number): Promise<void> {
    const t = this.panelTerminals.get(id);
    if (!t) throw new Error(`terminal not found: ${id}`);
    t.size = { cols, rows };
  }

  async terminalAck(id: string, n: number): Promise<void> {
    const t = this.panelTerminals.get(id);
    if (t) t.unacked = Math.max(0, t.unacked - n);
  }

  async terminalClose(id: string): Promise<void> {
    if (this.panelTerminals.has(id)) this.endTerminal(id, 129);
  }

  /** Paths given to terminalDropFiles, newest last. */
  droppedFiles: string[][] = [];

  // Mirrors the backend: an SSH host gets copies in a private folder, and
  // paths are quoted as a POSIX shell needs.
  async terminalDropFiles(id: string, paths: string[]): Promise<string[]> {
    const t = this.panelTerminals.get(id);
    if (!t) throw new Error(`terminal not found: ${id}`);
    this.droppedFiles.push([...paths]);
    const copied = t.tab.host && !wslDistro(t.tab.host);
    return paths.map((p) => {
      const path = copied ? `/tmp/agent-sessions-drop.mock/${p.split(/[\\/]/).pop()}` : p;
      return /^[\w./:+,@%=-]+$/.test(path) ? path : `'${path.replaceAll("'", "'\\''")}'`;
    });
  }

  private endTerminal(id: string, code: number): void {
    this.panelTerminals.delete(id);
    this.emit('term:exit', { id, code });
  }

  async setTerminalOpen(mode: TerminalOpenMode): Promise<LaunchInfo> {
    if (mode !== 'panel' && mode !== 'external') throw new Error(`unknown terminal mode "${mode}"`);
    this.launch = { ...this.launch, open: mode };
    return this.launchInfo();
  }

  async revealFile(dir: string, path: string): Promise<void> {
    console.info('[MockAPI] revealFile:', dir, path);
  }

  async copyFile(dir: string, path: string): Promise<void> {
    console.info('[MockAPI] copyFile:', dir, path);
  }

  async addFile(dir: string, path: string, host: string): Promise<boolean> {
    console.info('[MockAPI] addFile:', dir, path, host);
    return false;
  }

  async downloadFile(dir: string, path: string): Promise<string> {
    console.info('[MockAPI] downloadFile:', dir, path);
    const name = path.split('/').pop();
    const directory = ['frontend', 'frontend/src', 'frontend/src/lib', 'internal', 'assets'].includes(path);
    return `/Downloads/${name}${directory ? '.zip' : ''}`;
  }

  async revealSource(ref: SessionRef): Promise<void> {
    console.info('[MockAPI] revealSource:', ref);
  }

  async resolveTerminalFile(dir: string, path: string): Promise<{ dir: string; path: string }> {
    if (!dir) throw new Error('The terminal working directory is unknown.');
    const root = dir.replace(/\\/g, '/').replace(/\/$/, '');
    let relative = path.replace(/\\/g, '/');
    if (relative.startsWith(root + '/')) relative = relative.slice(root.length + 1);
    relative = relative.replace(/^\.\//, '');
    const listing = await this.listFiles(root);
    if (![...listing.files, ...(listing.ignored ?? [])].includes(relative)) throw new Error('File not found in an authorized project.');
    return { dir: root, path: relative };
  }

  private renamedListings = new Map<string, FileListing>();

  async createDirectory(dir: string, path: string, name: string, host: string): Promise<void> {
    if (host !== (this.connState.host ?? '')) throw new Error('The selected host changed.');
    if (!name || name === '.' || name === '..' || /[\/\\\0]/.test(name)) throw new Error('Invalid folder name.');
    if ((path && path.split('/').some(p => !p || p === '.' || p === '..')) || /[\\\0]/.test(path)) throw new Error('Invalid path.');
    const listing = await this.listFiles(dir);
    const paths = [...listing.files, ...(listing.ignored ?? []), ...(listing.directories ?? [])];
    if (path && !paths.some(p => (p === path && listing.directories?.includes(p)) || p.startsWith(path + '/'))) throw new Error('Directory not found.');
    const destination = path ? `${path}/${name}` : name;
    if (paths.some(p => p === destination || p.startsWith(destination + '/'))) throw new Error('Destination already exists.');
    this.renamedListings.set(JSON.stringify([host, dir]), {
      ...listing, directories: [...(listing.directories ?? []), destination].sort(),
    });
  }

  async createFile(dir: string, path: string, name: string, host: string): Promise<void> {
    if (host !== (this.connState.host ?? '')) throw new Error('The selected host changed.');
    if (!name || name === '.' || name === '..' || /[\/\\\0]/.test(name)) throw new Error('Invalid file name.');
    if ((path && path.split('/').some(p => !p || p === '.' || p === '..')) || /[\\\0]/.test(path)) throw new Error('Invalid path.');
    const listing = await this.listFiles(dir);
    const paths = [...listing.files, ...(listing.ignored ?? []), ...(listing.directories ?? [])];
    if (path && !paths.some(p => (p === path && listing.directories?.includes(p)) || p.startsWith(path + '/'))) throw new Error('Directory not found.');
    const destination = path ? `${path}/${name}` : name;
    if (paths.some(p => p === destination || p.startsWith(destination + '/'))) throw new Error('Destination already exists.');
    this.fileDrafts.set(JSON.stringify([host, dir, destination]), { path: destination, size: 0, text: '', revision: await mockFileRevision(''), edit: { eligible: true, newline: 'none' } });
    this.renamedListings.set(JSON.stringify([host, dir]), { ...listing, files: [...listing.files, destination].sort() });
  }

  async renameFile(dir: string, path: string, name: string, host: string): Promise<void> {
    if (host !== (this.connState.host ?? '')) throw new Error('The selected host changed.');
    if (!path || !name || name === '.' || name === '..' || /[\/\\\\\0]/.test(name)) throw new Error('Invalid name.');
    const listing = await this.listFiles(dir);
    const paths = [...listing.files, ...(listing.ignored ?? [])];
    const matches = (p: string) => p === path || p.startsWith(path + '/');
    if (![...paths, ...(listing.directories ?? [])].some(matches)) throw new Error('File not found.');
    const destination = path.slice(0, path.lastIndexOf('/') + 1) + name;
    if (path === destination) return;
    if ([...paths, ...(listing.directories ?? [])].some(p => p === destination || p.startsWith(destination + '/'))) throw new Error('Destination already exists.');
    const remap = (p: string) => matches(p) ? destination + p.slice(path.length) : p;
    for (const old of paths.filter(matches)) {
      const content = await this.readFile(dir, old);
      this.fileDrafts.set(JSON.stringify([host, dir, remap(old)]), { ...content, path: remap(old) });
      this.fileDrafts.delete(JSON.stringify([host, dir, old]));
    }
    this.renamedListings.set(JSON.stringify([host, dir]), {
      ...listing, files: listing.files.map(remap), ignored: listing.ignored?.map(remap), directories: listing.directories?.map(remap),
      status: Object.fromEntries(Object.entries(listing.status ?? {}).map(([p, status]) => [remap(p), status])),
    });
  }

  async deleteFile(dir: string, path: string, host: string): Promise<void> {
    if (host !== (this.connState.host ?? '')) throw new Error('The selected host changed.');
    if (!path || path.split('/').some(p => !p || p === '.' || p === '..') || /[\\\\\0]/.test(path)) throw new Error('Invalid path.');
    const listing = await this.listFiles(dir);
    const matches = (p: string) => p === path || p.startsWith(path + '/');
    const paths = [...listing.files, ...(listing.ignored ?? [])];
    if (![...paths, ...(listing.directories ?? [])].some(matches)) throw new Error('File not found.');
    for (const p of paths.filter(matches)) this.fileDrafts.delete(JSON.stringify([host, dir, p]));
    this.renamedListings.set(JSON.stringify([host, dir]), {
      ...listing, files: listing.files.filter(p => !matches(p)), ignored: listing.ignored?.filter(p => !matches(p)), directories: listing.directories?.filter(p => !matches(p)),
      status: Object.fromEntries(Object.entries(listing.status ?? {}).filter(([p]) => !matches(p))),
    });
  }

  async listFiles(dir: string): Promise<FileListing> {
    const renamed = this.renamedListings.get(JSON.stringify([this.connState.host ?? '', dir]));
    if (renamed) return structuredClone(renamed);
    return {
      root: dir,
      git: true,
      files: [
        '.gitignore', 'README.md', 'Makefile', 'docs/formats.md', 'go.mod', 'main.go', 'internal/app/app.go', 'internal/app/app_test.go',
        'frontend/package.json', 'frontend/src/App.svelte', 'frontend/src/lib/api.ts', 'assets/logo.png',
        'frontend/src/lib/new.ts',
      ],
      ignored: ['.env', '.env.local', 'frontend/.env.development'],
      status: {
        'frontend/src/App.svelte': 'M',
        'frontend/src/lib/new.ts': 'U',
        'internal/app/old.go': 'D',
        'go.mod': 'M',
      },
    };
  }

  async enableFileEditing(): Promise<boolean> { return true; }
  async completeFileClose(_id: number, _allow: boolean): Promise<void> {}

  // Demo reads synthesize files and cannot establish authoritative absence.
  async probeFiles(_dir: string, paths: string[], _host: string): Promise<FileProbeResult> {
    return { root: '', observations: paths.map(path => ({ path, state: 'unknown', reason: 'unsupported' })) };
  }

  private fileDrafts = new Map<string, FileContent>();

  async readFile(dir: string, path: string): Promise<FileContent> {
    const key = JSON.stringify([this.connState.host ?? '', dir, path]);
    const saved = this.fileDrafts.get(key);
    if (saved) return structuredClone(saved);
    const content = await this.readMockFile(dir, path);
    if (!content.binary && !content.tooLarge) {
      content.revision = await mockFileRevision(content.text ?? '');
      content.edit = { eligible: true, newline: 'lf' };
    }
    return content;
  }

  async saveFile(dir: string, req: FileSaveRequest, _host?: string): Promise<FileSaveResult> {
    const current = await this.readFile(dir, req.path);
    if (!current.edit?.eligible) throw new Error('This file cannot be edited.');
    if (current.revision !== req.expectedRevision) {
      return { status: 'conflict', observedRevision: current.revision };
    }
    if (/[\r\0]/.test(req.text)) throw new Error('Unsupported edit text.');
    const bytes = new TextEncoder().encode(req.text);
    if (bytes.length > 500_000) throw new Error('Edit text is too large.');
    const content: FileContent = {
      path: req.path, size: bytes.length, text: req.text,
      revision: await mockFileRevision(req.text),
      edit: { eligible: true, newline: req.text.includes('\n') ? 'lf' : 'none' },
    };
    this.fileDrafts.set(JSON.stringify([this.connState.host ?? '', dir, req.path]), content);
    return { status: 'saved', content: structuredClone(content) };
  }

  private searchRuns = new Map<string, { canceled: boolean }>();

  async searchFiles(dir: string, query: SearchQuery, searchId: string): Promise<SearchResult> {
    const out: SearchResult = { files: [], matches: 0, searched: 0 };
    if (!query.pattern) return out;
    const prev = this.searchRuns.get(searchId);
    if (prev) prev.canceled = true;
    const run = { canceled: false };
    this.searchRuns.set(searchId, run);
    try {
      const re = searchRegExp(query);
      const limit = searchLimit(query);
      const paths = (await this.listFiles(dir)).files.filter(p => searchesPath(query, p)).sort();
      for (const path of paths) {
        // A real search yields between files; so does this one, to be cancelable.
        await new Promise(r => setTimeout(r, 0));
        if (run.canceled) throw new Error('context canceled');
        // Saved edits are the mock's disk.
        const content = await this.readMockFile(dir, path);
        const saved = this.fileDrafts.get(JSON.stringify([this.connState.host ?? '', dir, path]));
        const text = saved?.text ?? content.text;
        if (content.binary || content.tooLarge || text === undefined) continue;
        out.searched++;
        const file = searchText(path, text, re, saved?.revision ?? await mockFileRevision(text));
        if (file.matches.length === 0) continue;
        if (out.matches + file.matches.length > limit) {
          file.matches = file.matches.slice(0, limit - out.matches);
          file.truncated = true;
        }
        out.files.push(file);
        out.matches += file.matches.length;
        if (out.matches >= limit) { out.truncated = true; break; }
      }
      return out;
    } finally {
      if (this.searchRuns.get(searchId) === run) this.searchRuns.delete(searchId);
    }
  }

  // Saved edits are the mock's disk, as for saveFile.
  async replaceInFiles(dir: string, req: ReplaceRequest, _host: string | undefined): Promise<ReplaceResult> {
    if (!req.query.pattern) throw new Error('invalid search pattern: empty pattern');
    if (/[\r\0]/.test(req.replacement)) throw new Error('invalid replacement: unsupported text');
    const re = searchRegExp(req.query);
    const out: ReplaceResult = { files: [], replaced: 0 };
    const seen = new Set<string>();
    for (const f of req.files) {
      if (seen.has(f.path)) { out.files.push({ path: f.path, status: 'skipped', reason: 'duplicate' }); continue; }
      seen.add(f.path);
      try {
        const current = await this.readFile(dir, f.path);
        if (current.revision !== f.expectedRevision) {
          out.files.push({ path: f.path, status: 'conflict', observedRevision: current.revision });
          continue;
        }
        if (!current.edit?.eligible || current.text === undefined) {
          out.files.push({ path: f.path, status: 'skipped', reason: current.edit?.reason ?? 'unsupported_text' });
          continue;
        }
        const done = replaceInText(current.text, re, req.replacement, !!req.query.regex, f.matches);
        if (!done.ok) { out.files.push({ path: f.path, status: 'skipped', reason: 'stale_match' }); continue; }
        const content: FileContent = {
          ...current, text: done.text, size: new TextEncoder().encode(done.text).length,
          revision: await mockFileRevision(done.text),
        };
        this.fileDrafts.set(JSON.stringify([this.connState.host ?? '', dir, f.path]), content);
        out.files.push({ path: f.path, status: 'replaced', replaced: done.replaced, revision: content.revision });
        out.replaced += done.replaced;
      } catch (e) {
        out.files.push({ path: f.path, status: 'failed', message: e instanceof Error ? e.message : String(e) });
      }
    }
    return out;
  }

  async cancelSearch(searchId: string): Promise<void> {
    const run = this.searchRuns.get(searchId);
    if (run) run.canceled = true;
  }

  async blameFile(dir: string, path: string, line: number): Promise<BlameLine> {
    if (!Number.isInteger(line) || line < 1) throw new Error(`Invalid line number ${line}.`);
    const content = await this.readFile(dir, path);
    if (content.binary || content.tooLarge || content.text === undefined) {
      throw new Error('Blame is not available for this file.');
    }
    const text = content.text;
    const lines = text === '' ? 0 : text.replace(/\n$/, '').split('\n').length;
    if (line > lines) throw new Error(`Line ${line} is past the end of ${path}.`);
    const listing = await this.listFiles(dir);
    const drafted = this.fileDrafts.has(JSON.stringify([this.connState.host ?? '', dir, path]));
    const changed = (content.changes ?? []).some(
      (c) => c.kind !== 'deleted' && line >= c.start && line < c.start + c.count
    );
    if (drafted || listing.status?.[path] === 'U' || changed) {
      return {
        line, commit: '0'.repeat(40), author: 'Not Committed Yet',
        authorTime: Math.floor(Date.now() / 1000), summary: '', uncommitted: true,
      };
    }
    // Lines fall into blocks of three, each from its own fake commit.
    const block = Math.floor((line - 1) / 3);
    const pick = mockHash(`${path}#${block}`);
    const authors = ['Ada Lovelace', 'Grace Hopper', 'Linus Torvalds'];
    const summaries = ['Add initial implementation', 'Fix edge case in parsing', 'Refactor for readability'];
    return {
      line,
      commit: mockCommit(`${path}#${block}`),
      author: authors[pick % authors.length],
      authorTime: 1_760_000_000 - block * 86_400 - (pick % 3600),
      summary: summaries[pick % summaries.length],
      uncommitted: false,
    };
  }

  private async readMockFile(dir: string, path: string): Promise<FileContent> {
    if (path.endsWith('.png')) {
      // A 2x2 checker.
      const data = 'iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGP4z8DwHwyBDAYGBgYAPmgF+zAx4yAAAAAASUVORK5CYII=';
      return { path, size: 75, binary: true, image: 'image/png', data };
    }
    if (path.endsWith('.svelte')) {
      const text = `<script lang="ts">\n  import { onMount } from 'svelte';\n  /* The count shown. */\n  let count = $state(0);\n  onMount(() => console.log('ready', 42, 'a long line that goes on and on to show how soft wrapping looks when the text is much wider than the viewer pane'));\n  /* a block\n     comment */\n</script>\n\n<button class="btn" onclick={() => count++}>Clicked {count}</button>\n\n<style>\n  .btn {\n    color: #3b82f6;\n    padding: 4px 8px;\n  }\n</style>\n`;
      return {
        path,
        size: text.length,
        text,
        changes: [
          { kind: 'modified', start: 5, count: 1, removed: ["  onMount(() => console.log('ready'));"] },
          { kind: 'added', start: 6, count: 2 },
          { kind: 'deleted', start: 12, count: 0, removed: ['  /* buttons */', '  button { margin: 0; }'] },
        ],
      };
    }
    if (/\.(md|markdown|mdx)$/i.test(path)) {
      const text = '# Project overview\n\nMarkdown preview with **formatted text**, lists and diagrams.\n\n- Browse project files\n- Preview Markdown\n\n[Open package.json](frontend/package.json)\n\n```mermaid\nflowchart LR\n  Files --> Preview\n  Preview --> Presentation\n```\n';
      return { path, size: text.length, text };
    }
    const text = path.endsWith('.go')
      ? `package main\n\nimport "fmt"\n\n// main prints a greeting from ${dir}.\nfunc main() {\n\tfmt.Println("hello")\n}\n`
      : `// ${path}\nexport const answer = 42;\n`;
    return { path, size: text.length, text };
  }

  private forwardRules = new Map<string, PortForwardRule[]>();
  private nextForward = 0;

  async terminalFonts(): Promise<string[]> {
    return ['Consolas', 'DejaVu Sans Mono', 'FiraCode Nerd Font Mono', 'Menlo', 'SF Mono'];
  }

  async listPortForwards(): Promise<PortForwardState> {
    const host = this.connState.host ?? '';
    const rules = this.forwardRules.get(host) ?? [];
    return { host, rules: rules.map(r => ({ ...r, status: this.connState.phase === 'connected' ? r.status : 'disconnected' })) };
  }

  async savePortForward(rule: PortForwardRequest): Promise<PortForwardState> {
    const error = validatePortForward(rule);
    if (error) throw new Error(error);
    const { host } = await this.listPortForwards();
    if (!host || wslDistro(host)) throw new Error('Select an SSH host.');
    const rules = this.forwardRules.get(host) ?? [];
    if (rules.some(r => r.id !== rule.id && r.localPort === rule.localPort)) throw new Error('Local port is already configured.');
    const next: PortForwardRule = { ...rule, id: rule.id || `forward-${++this.nextForward}`, status: 'stopped' };
    this.forwardRules.set(host, [...rules.filter(r => r.id !== next.id), next]);
    return this.listPortForwards();
  }

  async startPortForward(id: string): Promise<PortForwardState> {
    const rules = this.forwardRules.get(this.connState.host ?? '') ?? [];
    const rule = rules.find(r => r.id === id);
    if (rule) rule.status = 'active';
    return this.listPortForwards();
  }

  async stopPortForward(id: string): Promise<PortForwardState> {
    const rules = this.forwardRules.get(this.connState.host ?? '') ?? [];
    const rule = rules.find(r => r.id === id);
    if (rule) rule.status = 'stopped';
    return this.listPortForwards();
  }

  async removePortForward(id: string): Promise<PortForwardState> {
    const host = this.connState.host ?? '';
    this.forwardRules.set(host, (this.forwardRules.get(host) ?? []).filter(r => r.id !== id));
    return this.listPortForwards();
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

  async setPrunePromptHistory(prune: boolean): Promise<ManageSettings> {
    this.settings = { ...this.settings, prunePromptHistory: prune };
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

  /** Directory the mock folder dialog "picks"; '' acts as cancel. */
  pickedDirectory = '/home/haith/Workspaces/imported';

  async pickImportDirectory(): Promise<string> {
    return this.pickedDirectory;
  }

  private restoreToken = '';

  async previewBundleRestore(req: BundleRestoreRequest): Promise<RestorePreview> {
    const bundle = this.mockBundles.get(req.bundleId);
    if (!bundle) throw new Error(`bundle "${req.bundleId}" not found or expired; please reopen the bundle`);
    if (!req.cwd?.startsWith('/')) throw new Error('the import directory must be an absolute path');
    const sourceRef = { agent: bundle.source.agent, id: bundle.source.id };
    const taken = this.sessions.some((s) => s.ref.agent === sourceRef.agent && s.ref.id === sourceRef.id);
    const copyId = req.copy ? req.copyId || `${bundle.source.id}-copy-${Date.now().toString(36)}` : undefined;
    const targetRef = { agent: sourceRef.agent, id: copyId || sourceRef.id };
    this.restoreToken = `restore-${Date.now()}`;
    return {
      items: [
        {
          sourceRef,
          targetRef,
          targetCwd: req.cwd,
          paths: [`/home/haith/.claude/projects/${req.cwd.replace(/[^A-Za-z0-9]/g, '-')}/${targetRef.id}.jsonl`],
          bytes: bundle.nativeBytes,
          blocked: taken && !req.copy ? `session ${sourceRef.id} already exists; import it as a copy to keep both` : undefined,
          isCopy: Boolean(copyId),
        },
      ],
      totalBytes: bundle.nativeBytes,
      token: this.restoreToken,
      copyId,
    };
  }

  async restoreBundle(req: BundleRestoreRequest, token: string): Promise<RestoreReport> {
    if (!token || token !== this.restoreToken) throw new Error('manage: preview is stale; preview again');
    const preview = await this.previewBundleRestore(req);
    const item = preview.items[0];
    if (item.blocked) throw new Error(`manage: item is blocked: ${item.blocked}`);
    const bundle = this.mockBundles.get(req.bundleId)!;
    const now = new Date().toISOString();
    this.sessions.unshift({
      ref: item.targetRef,
      title: bundle.source.title || 'Imported session',
      firstPrompt: bundle.source.title || '',
      cwd: req.cwd,
      gitBranch: bundle.source.gitBranch,
      createdAt: bundle.createdAt,
      updatedAt: now,
      counts: { user: 1, assistant: 1, toolCalls: 0 },
      tokens: { input: 0, output: 0 },
      live: false,
      sourcePath: item.paths[0],
    });
    this.restoreToken = '';
    return {
      items: [{ ref: item.targetRef, title: bundle.source.title || '', ok: true, created: item.paths }],
      restored: 1,
      failed: 0,
      totalBytes: item.bytes,
      createdRefs: [item.targetRef],
    };
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

  async listWSLDistros(): Promise<WSLEntry[]> {
    return this.wslDistros.map((d) => ({ ...d }));
  }

  setMockWSLDistros(distros: WSLEntry[]): void {
    this.wslDistros = distros.map((d) => ({ ...d }));
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
      saveFile: true,
      blame: true,
      searchFiles: true,
      replaceFiles: true,
      newSession: true,
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
      hostId: 'mock:local',
      host: undefined,
      generation: this.connState.generation,
      capabilities: {
        codeDiagnostics: true,
        liveCodeDiagnostics: false,
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

  // A browser does not tell a page where dropped files are, so they are
  // given made-up paths with their names.
  onFileDrop(callback: (x: number, y: number, paths: string[]) => void): () => void {
    if (typeof window === 'undefined') return () => {};
    const files = (e: DragEvent) => e.dataTransfer?.types.includes('Files') ?? false;
    const over = (e: DragEvent) => {
      if (files(e)) e.preventDefault();
    };
    const drop = (e: DragEvent) => {
      if (!files(e)) return;
      e.preventDefault();
      const el = document.elementFromPoint(e.clientX, e.clientY);
      if (!el || getComputedStyle(el).getPropertyValue('--wails-drop-target').trim() !== 'drop') return;
      callback(e.clientX, e.clientY, [...(e.dataTransfer?.files ?? [])].map((f) => `/mock/drops/${f.name}`));
    };
    window.addEventListener('dragover', over);
    window.addEventListener('drop', drop);
    return () => {
      window.removeEventListener('dragover', over);
      window.removeEventListener('drop', drop);
    };
  }

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
