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
} from '../types.js';
import { mockSessions, mockMessages, mockBlobs, mockDiagnostics } from './fixtures.js';

export class MockBackendAPI {
  private sessions: SessionMeta[] = [...mockSessions];
  private messages: Record<string, typeof mockMessages[string]> = { ...mockMessages };

  async listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]> {
    const filtered = this.filterSessions(this.sessions, filter);
    if (mode === 'flat') {
      return filtered.map((s) => ({
        key: `flat:${s.ref.agent}:${s.ref.id}`,
        label: s.title || s.ref.id,
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
          label: agent === 'claude-code' ? 'Claude Code' : agent === 'codex' ? 'Codex' : 'OpenCode',
          agent,
          cwd,
          sessionCount: agentSessions.length,
          sessions: agentSessions.map((s) => s.ref),
        });
      }
      roots.push({
        key: `dir-agent:${cwd}`,
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
    let bin = 'claude';
    if (ref.agent === 'codex') bin = 'codex';
    else if (ref.agent === 'opencode') bin = 'opencode';
    return `cd '${meta.cwd}' && ${bin} --resume '${ref.id}'`;
  }

  async revealSource(ref: SessionRef): Promise<void> {
    console.info('[MockAPI] revealSource:', ref);
  }

  async getDiagnostics(): Promise<Diagnostics> {
    return mockDiagnostics;
  }

  async openURL(url: string): Promise<void> {
    if (typeof window !== 'undefined') {
      window.open(url, '_blank');
    }
  }

  onEvent(_name: string, _callback: (...data: any[]) => void): () => void {
    return () => {};
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
