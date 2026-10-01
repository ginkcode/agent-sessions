import type { AgentID } from './types';
import { ALL_AGENTS } from './portable';

export interface ToastMessage {
  title: string;
  body: string;
  /** A command to show on its own line, e.g. `ssh <host>`. */
  code?: string;
  tone?: 'info' | 'error';
}

export type CopiedKind = 'resume' | 'launch' | 'prompt';

function agentLabel(agent: AgentID | undefined): string {
  return ALL_AGENTS.find((a) => a.id === agent)?.label ?? 'the agent';
}

/**
 * What to do next after copying a command or prompt. Commands for a remote
 * session are built for that host, so the user pastes them into an
 * interactive shell there, where the PATH that finds the agent is loaded.
 */
export function copiedGuidance(
  kind: CopiedKind,
  opts: { host?: string; agent?: AgentID } = {},
): ToastMessage {
  const { host } = opts;
  const agent = agentLabel(opts.agent);
  const where = host ? `Open a shell on ${host}, then paste it there.` : 'Paste it into a terminal.';
  const code = host ? `ssh ${host}` : undefined;

  switch (kind) {
    case 'resume':
      return {
        title: 'Resume command copied',
        body: `${where} It opens the session in its directory so you can carry on.`,
        code,
      };
    case 'launch':
      return {
        title: 'Launch command copied',
        body: `${where} ${agent} starts in the project directory, restores the context and waits for your next request.`,
        code,
      };
    case 'prompt':
      return {
        title: 'Prompt copied',
        body: `Start ${agent} in the project directory${host ? ` on ${host}` : ''} and paste it as your first message.`,
      };
  }
}

export function copyFailed(what: string, err: unknown): ToastMessage {
  return {
    title: `Couldn't copy ${what}`,
    body: err instanceof Error ? err.message : String(err),
    tone: 'error',
  };
}
