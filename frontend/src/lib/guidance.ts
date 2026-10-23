import type { AgentID, ConnectionPhase, LaunchInfo } from './types';
import { ALL_AGENTS } from './portable';
import { hostLabel, shellCommand } from './hosts';

export interface ToastMessage {
  title: string;
  body: string;
  /** A command to show on its own line, e.g. `ssh <host>`. */
  code?: string;
  tone?: 'info' | 'error';
}

export type CopiedKind = 'resume' | 'launch' | 'prompt' | 'doc';

function agentLabel(agent: AgentID | undefined): string {
  return ALL_AGENTS.find((a) => a.id === agent)?.label ?? 'the agent';
}

/**
 * What to do next after copying a command or prompt. Commands for a remote
 * session are built for that host, so the user pastes them into an
 * interactive shell there, where the PATH that finds the agent is loaded.
 * Local commands use the local shell's syntax: PowerShell on Windows.
 */
export function copiedGuidance(
  kind: CopiedKind,
  opts: { host?: string; agent?: AgentID; shell?: LaunchInfo['shell'] } = {},
): ToastMessage {
  const { host } = opts;
  const agent = agentLabel(opts.agent);
  const local = opts.shell === 'powershell' ? 'Paste it into PowerShell.' : 'Paste it into a terminal.';
  const where = host ? `Open a shell on ${hostLabel(host)}, then paste it there.` : local;
  const code = host ? shellCommand(host) : undefined;

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
        body: `Start ${agent} in the project directory${host ? ` on ${hostLabel(host)}` : ''} and paste it as your first message.`,
      };
    case 'doc':
      return {
        title: 'Document copied',
        body: 'The full handoff document is on your clipboard.',
      };
  }
}

/** What happens after Open in terminal started a resume, a handoff or a new session. */
export function openedGuidance(kind: 'resume' | 'launch' | 'new', agent?: AgentID): ToastMessage {
  if (kind === 'new') {
    return {
      title: 'Opened in a terminal',
      body: `A new ${agentLabel(agent)} session starts in the project directory in a new terminal window.`,
    };
  }
  if (kind === 'resume') {
    return {
      title: 'Opened in a terminal',
      body: 'The session resumes in its directory in a new terminal window.',
    };
  }
  return {
    title: 'Opened in a terminal',
    body: `${agentLabel(agent)} starts in the project directory in a new terminal window, restores the context and waits for your next request.`,
  };
}

export function openFailed(err: unknown): ToastMessage {
  return {
    title: "Couldn't open a terminal",
    body: err instanceof Error ? err.message : String(err),
    tone: 'error',
  };
}

/** Shown while files dropped on an SSH host's terminal are copied there. */
export function copyingDrop(host: string, count: number): ToastMessage {
  return {
    title: `Copying ${count === 1 ? 'the file' : `${count} files`} to ${host}`,
    body: 'Their paths are typed in the terminal once they are in a private folder on the host.',
  };
}

export function dropFailed(err: unknown): ToastMessage {
  return {
    title: "Couldn't add the dropped files",
    body: err instanceof Error ? err.message : String(err),
    tone: 'error',
  };
}

export function copyFailed(what: string, err: unknown): ToastMessage {
  return {
    title: `Couldn't copy ${what}`,
    body: err instanceof Error ? err.message : String(err),
    tone: 'error',
  };
}

/**
 * Part of the error the backend returns when the host chosen is open in
 * another window of the app (ErrHostInOtherWindow). A host is open in one
 * window at most; that window is brought to the front instead.
 */
export const HOST_IN_OTHER_WINDOW = 'is open in another window';

/**
 * The toast for a host open in another window, or null when err is any
 * other connect error.
 */
export function hostInOtherWindow(host: string, err: unknown): ToastMessage | null {
  const message = err instanceof Error ? err.message : String(err ?? '');
  const at = message.indexOf(HOST_IN_OTHER_WINDOW);
  if (at < 0) return null;
  const label = hostLabel(host);
  // The backend appends why the window could not be raised, if it could not.
  const why = message.slice(at + HOST_IN_OTHER_WINDOW.length).replace(/^:\s*/, '').trim();
  return {
    title: `${label} is open in another window`,
    body: why
      ? `Switch to that window to use ${label}. ${why[0].toUpperCase()}${why.slice(1)}.`
      : `That window was brought to the front. A host is open in one window at a time.`,
  };
}

/** The error the backend reports when a live session to the host dropped. */
export const CONNECTION_LOST = 'connection lost';

export interface ConnectionBannerInput {
  phase: ConnectionPhase;
  host?: string;
  error?: string;
  /** The current connect attempt reached the host before (same generation). */
  wasConnected?: boolean;
}

export interface ConnectionBannerMessage {
  /** `lost`: a live session dropped; `failed`: the host was never reached. */
  kind: 'lost' | 'failed' | 'disconnected';
  retrying: boolean;
  /** Summary text around the host name, which is rendered emphasized. */
  before: string;
  host: string;
  after: string;
  /** Full error text, kept apart from the summary so it can wrap. */
  detail?: string;
  detailLabel?: string;
}

/**
 * Normalizes a connection error for display: Windows line endings, trailing
 * spaces and blank edges go; the content and its line breaks stay.
 */
export function connectionErrorDetail(error: string | undefined): string | undefined {
  if (!error) return undefined;
  const text = error
    .replace(/\r\n?/g, '\n')
    .split('\n')
    .map((line) => line.trimEnd())
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
  return text || undefined;
}

/**
 * Describes a dropped or failed remote connection for the banner, or null
 * when there is nothing to report. A failed first connect is told apart from
 * a live session that was lost; the raw error is detail, not summary.
 */
export function connectionBanner(input: ConnectionBannerInput): ConnectionBannerMessage | null {
  const { phase } = input;
  if (phase !== 'reconnecting' && phase !== 'disconnected') return null;
  const retrying = phase === 'reconnecting';
  const host = input.host ? hostLabel(input.host) : 'the host';
  const error = connectionErrorDetail(input.error);
  const lostNow = error === CONNECTION_LOST;
  const detail = lostNow ? undefined : error;

  if (lostNow || input.wasConnected) {
    return {
      kind: 'lost',
      retrying,
      before: 'Connection to ',
      host,
      after: retrying ? ' lost. Automatically reconnecting…' : ' lost.',
      detail,
      detailLabel: detail ? 'Last reconnect attempt failed:' : undefined,
    };
  }
  if (detail) {
    return {
      kind: 'failed',
      retrying,
      before: 'Could not connect to ',
      host,
      after: retrying ? '. Retrying…' : '.',
      detail,
      detailLabel: 'Error:',
    };
  }
  return {
    kind: 'disconnected',
    retrying,
    before: retrying ? 'Reconnecting to ' : 'Disconnected from ',
    host,
    after: retrying ? '…' : '.',
  };
}
