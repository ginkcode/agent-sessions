import type { CodeDiagnosticsProvider } from './codeDiagnostics';
import type { LocationKind } from './problemNavigation';

/** Display names only; provider IDs stay the protocol identity. Unknown IDs show as-is (escaped by Svelte). */
const toolNames: Record<string, string> = {
  gopls: 'Go language server (gopls)',
  'go-syntax': 'Go syntax',
  'go-vet': 'Go vet',
  typescript: 'TypeScript/JavaScript language server (vtsls)',
  svelte: 'Svelte language server',
  'svelte-check': 'svelte-check',
};
const languages: Record<string, string> = { gopls: 'Go', 'go-syntax': 'Go', 'go-vet': 'Go', typescript: 'TypeScript/JavaScript', svelte: 'Svelte', 'svelte-check': 'Svelte' };

export function toolName(providerId: string): string {
  if (toolNames[providerId]) return toolNames[providerId];
  if (/^go-vet-\d+$/.test(providerId)) return 'Go vet';
  return providerId;
}

/** Hosts reached over SSH/WSL. Managed setup only supports this computer (backend-enforced too). */
export function remoteHost(hostId: string | undefined): boolean { return !!hostId && /^(ssh|wsl):/.test(hostId); }

/** How a provider runs, consistent with the watcher: live LSP, automatic saved-file watching, or Check Project. */
export function providerMode(p: Pick<CodeDiagnosticsProvider, 'providerId' | 'coverage'> & { availability?: CodeDiagnosticsProvider['availability'] }): string {
  const language = languages[p.providerId] ?? (/^go-vet-\d+$/.test(p.providerId) ? 'Go' : undefined);
  const files = language ? `${language} files` : 'files';
  if (p.coverage.kind === 'openedDocuments') return `LSP · live, opened ${files}`;
  if (p.coverage.externalChanges === 'watching') return `saved ${files} · automatic`;
  // The built-in syntax provider follows the approved project watcher; before approval nothing watches yet.
  if (p.providerId === 'go-syntax' && p.availability === 'approvalRequired') return `saved ${files} · automatic after approval`;
  return `saved ${files} · on demand`;
}

/** Languages of live (opened-documents) providers in the given availabilities, e.g. "Go and TypeScript/JavaScript". */
export function liveLanguages(providers: Pick<CodeDiagnosticsProvider, 'providerId' | 'coverage' | 'availability'>[], states: CodeDiagnosticsProvider['availability'][] = ['available']): string {
  const names = [...new Set(providers.filter(p => p.coverage.kind === 'openedDocuments' && states.includes(p.availability)).map(p => languages[p.providerId] ?? p.providerId))];
  return names.length > 1 ? `${names.slice(0, -1).join(', ')} and ${names.at(-1)}` : names[0] ?? '';
}

const availability: Record<CodeDiagnosticsProvider['availability'], string> = {
  approvalRequired: 'Approval required', available: 'Available', missingTool: 'Not installed', unsupported: 'Unsupported', disabled: 'Disabled',
};
/** One short status per provider. Phase only matters for an available provider. */
export function providerStatus(p: Pick<CodeDiagnosticsProvider, 'availability' | 'phase' | 'failure'>): { text: string; tone: 'ok' | 'busy' | 'bad' | 'muted' } {
  if (p.availability !== 'available') return { text: availability[p.availability] ?? p.availability, tone: p.availability === 'missingTool' ? 'bad' : 'muted' };
  if (p.failure || p.phase === 'failed') return { text: 'Failed', tone: 'bad' };
  if (p.phase === 'starting') return { text: 'Starting…', tone: 'busy' };
  if (p.phase === 'checking') return { text: 'Checking…', tone: 'busy' };
  if (p.phase === 'canceled') return { text: 'Canceled', tone: 'muted' };
  if (p.phase === 'disconnected') return { text: 'Disconnected', tone: 'muted' };
  return { text: 'Ready', tone: 'ok' };
}

export const severityLabels = { error: 'Error', warning: 'Warning', information: 'Information', hint: 'Hint' } as const;

/** 0-based protocol positions shown as 1-based editor positions. */
export function locationLabel(range: { start: { line: number; character: number } } | undefined, kind?: LocationKind): string {
  if (!range || kind === 'fileLevel') return 'File';
  return `Ln ${range.start.line + 1}, Col ${range.start.character + 1}`;
}

/** Stale and unverified mean the result may predate the displayed text; file-level and drafts do not. */
export function staleKind(kind: LocationKind | undefined): boolean {
  return kind === 'staleDocument' || kind === 'staleSaved' || kind === 'unverified';
}

/** Messages longer than this, or multi-line ones, start collapsed to their first line. */
export function longMessage(message: string): boolean { return message.length > 160 || /\r|\n/.test(message); }
export function messagePreview(message: string): string {
  const first = message.split(/\r\n|\n|\r/)[0];
  return first.length > 140 ? `${first.slice(0, 140)}…` : first;
}

const syncReasons: Record<string, string> = {
  invalidArgument: 'the server rejected the update',
  toolFailed: 'the language server failed',
  disconnected: 'the connection dropped',
  staleContext: 'code checks were restarted',
  canceled: 'the update was canceled',
  unsupported: 'not supported on this connection',
  limitExceeded: 'the file is too large for live checks',
  notFound: 'the live document was not found',
  busy: 'the server was busy',
  resyncRequired: 'the server lost track of the edits',
  unauthorized: 'not authorized',
  approvalRequired: 'project approval is required',
  approvalChanged: 'project approval changed',
};
/** Short human reason; the raw code stays visible beside it for support. */
export function syncFailureReason(code: string): string { return syncReasons[code] ?? 'synchronization failed'; }
