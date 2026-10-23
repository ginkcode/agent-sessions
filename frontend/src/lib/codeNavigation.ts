import type { CodeDiagnosticsProvider, CodeNavigationOp, CodeNavigationResult, LiveDocumentBasis } from './codeDiagnostics';

/**
 * What asking about a symbol came to: an answer, a reason it cannot be asked
 * (`unavailable`, for the editor to show), a failed request, or `stale` when
 * the file or the attachment moved on while the server answered; a stale
 * answer is dropped without a word.
 */
export type CodeNavigationOutcome =
  | { kind: 'result'; result: CodeNavigationResult }
  | { kind: 'unavailable'; reason: string }
  | { kind: 'failed'; code: string; reason: string }
  | { kind: 'stale' };

/** What the diagnostics store knows about one file, for navigationBlocker. */
export interface NavigationView {
  status: 'detached' | 'attaching' | 'ready' | 'unsupported' | 'resyncing' | 'disconnected' | 'error';
  /** The host runs language servers for open files (liveCodeDiagnostics). */
  live: boolean;
  /** The host answers navigation (codeNavigation). */
  navigation: boolean;
  approved: boolean;
  approvalBlocker?: string;
  /** The opened-documents providers that declare this file. */
  servers: CodeDiagnosticsProvider[];
  /** The file's synchronization failed. */
  failure?: string;
  basis?: LiveDocumentBasis;
}

export const OP_LABEL: Record<CodeNavigationOp, string> = {
  definition: 'Go to definition',
  references: 'Find references',
  hover: 'Hover',
};

/**
 * Why navigation cannot be asked for a file now, or undefined when it can.
 * Each reason says what is missing and, where there is one, what to do.
 */
export function navigationBlocker(v: NavigationView): string | undefined {
  switch (v.status) {
    case 'unsupported':
      return 'Code navigation is not available for this host.';
    case 'disconnected':
      return 'The host is disconnected. Reconnect to use code navigation.';
    case 'error':
      return 'Code checks failed for this project. Open Code checks to see why.';
    case 'ready':
      break;
    default:
      return 'Code checks are still starting for this project.';
  }
  if (!v.live) return 'This host does not run language servers for open files, so code navigation is unavailable.';
  if (!v.navigation) return "This host's Agent Sessions server is older and does not offer code navigation.";
  if (!v.approved) return v.approvalBlocker ?? 'Approve code checks for this project in Code checks to use go to definition, references and hover.';
  const servers = v.servers.filter(p => p.availability === 'available');
  if (!servers.length) {
    const missing = v.servers.find(p => p.availability === 'missingTool' || p.missingTool);
    return missing ? `${missing.providerId} is not installed on this host; Code checks shows how to get it.` : 'No language server for this file was found on this host.';
  }
  if (v.failure) return `This file is not synced with the language server: ${v.failure}`;
  if (servers.every(p => p.phase === 'failed')) {
    const failure = servers.find(p => p.failure)?.failure;
    return `The language server is not running${failure ? `: ${failure.message}` : ''}. Open Code checks to retry.`;
  }
  if (!v.basis) return 'The language server is still opening this file.';
  return undefined;
}

/** How a failed request reads, by its stable code. */
export function navigationFailure(code: string, op: CodeNavigationOp, message = ''): string {
  switch (code) {
    case 'notRunning':
      return 'The language server is not running. Open Code checks to retry.';
    case 'unsupported':
      return `The language server does not offer ${OP_LABEL[op].toLowerCase()} for this file.`;
    case 'timeout':
      return 'The language server did not answer in 10 seconds. It may still be loading the project; try again.';
    case 'busy':
      return 'The language server is busy. Try again in a moment.';
    case 'resyncRequired':
      return 'The language server restarted and is opening this file again. Try again in a moment.';
    case 'disconnected':
      return 'The host is disconnected. Reconnect to use code navigation.';
    case 'approvalRequired':
    case 'approvalChanged':
      return 'Approve code checks for this project in Code checks to use go to definition, references and hover.';
    case 'canceled':
      return 'The request was canceled.';
    default:
      return `The language server could not answer${message ? `: ${message}` : '.'}`;
  }
}

/** How a failed read of an external file reads, by its stable code. */
export function externalReadFailure(code: string, message = ''): string {
  switch (code) {
    case 'notFound':
      if (/no longer exists/.test(message)) return 'The file no longer exists on the host.';
    // falls through: the grant ended or never was.
    case 'staleContext':
      return 'This file is shown only after code navigation points to it. Go to the definition again to show it.';
    case 'disconnected':
      return 'The host is disconnected. Reconnect to show this file.';
    case 'approvalRequired':
    case 'approvalChanged':
      return 'Approve code checks for this project in Code checks to show files outside it.';
    case 'unsupported':
      return message.replace(/^unsupported:\s*/, '') || 'Files outside the project cannot be shown for this host.';
    default:
      return `The file could not be read${message ? `: ${message.replace(/^\w+:\s*/, '')}` : '.'}`;
  }
}

/**
 * The full path of a file outside the project, with the home directory the
 * project is in shortened to ~. Another home is shown as it is.
 */
export function externalLabel(path: string, projectDir: string | null | undefined): string {
  const home = /^(\/Users\/[^/]+|\/home\/[^/]+|[A-Za-z]:\\Users\\[^\\]+)(?=[\\/]|$)/.exec(projectDir ?? '')?.[1];
  if (!home) return path;
  const windows = home.includes('\\');
  const same = windows ? path.toLowerCase().startsWith(home.toLowerCase()) : path.startsWith(home);
  const rest = path.slice(home.length);
  return same && (rest === '' || /^[\\/]/.test(rest)) ? `~${rest}` : path;
}
