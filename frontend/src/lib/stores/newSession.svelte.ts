import { api } from '../api';
import type { AgentID, AgentStatus, TerminalOpenMode } from '../types';
import { isStaleReply } from '../link';
import { openFailed, openedGuidance } from '../guidance';
import { defaultNewSessionAgent, newSessionChoices, type NewSessionChoice } from '../newSession';
import { connectionStore } from './connection.svelte';
import { launcher } from './launcher.svelte';
import { link } from './link.svelte';
import { terminals } from './terminals.svelte';
import { toast } from './toast.svelte';

const LAST_AGENT_KEY = 'agent-sessions:new-session-agent';

/**
 * Starting fresh agent sessions in a project: which agents the shown host
 * has, the agent last started, and where new sessions open.
 */
export class NewSessionStore {
  /** Each host's report of its agents, by host ('' is this computer). */
  reports = $state<Record<string, AgentStatus[]>>({});
  /** The agent last started, which the shortcut starts again. */
  lastAgent = $state<AgentID>('claude-code');
  starting = $state(false);

  init(): void {
    try {
      const saved = localStorage.getItem(LAST_AGENT_KEY);
      if (saved) this.lastAgent = saved;
    } catch {
      // Storage disabled; keep Claude Code.
    }
  }

  /**
   * Where a new session opens: where agents open by default, else the
   * built-in panel when only it can, or null where none can start (as on
   * hosts whose server predates new sessions).
   */
  get where(): TerminalOpenMode | null {
    if (!connectionStore.capabilities.newSession) return null;
    if (launcher.canOpen) return launcher.openMode;
    return launcher.canOpenPanel ? 'panel' : null;
  }

  get available(): boolean {
    return this.where !== null;
  }

  /** The shown host's agents, as last reported. */
  get choices(): NewSessionChoice[] {
    return newSessionChoices(this.reports[link.dataHost ?? '']);
  }

  /** The agent the shortcut starts. */
  get defaultAgent(): AgentID {
    return defaultNewSessionAgent(this.choices, this.lastAgent);
  }

  /** Asks the shown host which agents it has; agents come and go. */
  async refresh(): Promise<void> {
    if (!this.available) return;
    const host = link.dataHost ?? '';
    try {
      const report = await api.newSessionAgents();
      this.reports = { ...this.reports, [host]: report };
    } catch {
      // Keep the last report: a launch still tells what is missing.
    }
  }

  /**
   * Starts a fresh session of agent in dir, where new sessions open, and
   * reports whether it started; failures are shown as a toast.
   */
  async start(agent: AgentID, dir: string): Promise<boolean> {
    const where = this.where;
    if (!where || !dir || this.starting) return false;
    this.starting = true;
    try {
      if (where === 'panel') {
        if (!(await terminals.runNewSession(agent, dir))) return false;
      } else {
        await api.openNewSessionInTerminal(agent, dir);
        toast.show(openedGuidance('new', agent));
      }
      this.remember(agent);
      return true;
    } catch (err) {
      if (!isStaleReply(err)) toast.show(openFailed(err));
      return false;
    } finally {
      this.starting = false;
    }
  }

  private remember(agent: AgentID): void {
    this.lastAgent = agent;
    try {
      localStorage.setItem(LAST_AGENT_KEY, agent);
    } catch {
      // Storage disabled; remembered for this run.
    }
  }
}

export const newSession = new NewSessionStore();
