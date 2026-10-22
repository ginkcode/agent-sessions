/** Stage 1 contract v1. Scanner/provider health diagnostics are unrelated. */
export interface CodeDiagnosticsContext { connectionId: string; attachmentId: string; generation: string }
export interface CodeDiagnosticsProject { hostId: string; root: string }
export interface CodeDiagnosticPosition { line: number; character: number }
export interface CodeDiagnosticRange { start: CodeDiagnosticPosition; end: CodeDiagnosticPosition }
export type CodeDiagnosticFreshness = 'current' | 'stale' | 'uncertain';
export interface CodeDiagnostic {
  path: string;
  severity: 'error' | 'warning' | 'information' | 'hint';
  message: string;
  source: string;
  freshness: CodeDiagnosticFreshness;
  basis: { kind: 'saved'; revision?: string } | { kind: 'document'; documentId: string; version?: number };
  range?: CodeDiagnosticRange;
  code?: string;
  locationIssue?: 'invalidRange';
}
export interface CodeDiagnosticsResultSet {
  providerId: string;
  origin: 'checker' | 'lsp';
  scope: { kind: 'project' } | { kind: 'document'; path: string; documentId: string };
  diagnostics: CodeDiagnostic[];
  runId?: string;
}
export interface CodeDiagnosticsProvider {
  providerId: string;
  availability: 'approvalRequired' | 'available' | 'missingTool' | 'unsupported' | 'disabled';
  phase: 'idle' | 'starting' | 'checking' | 'failed' | 'canceled' | 'disconnected';
  coverage: { kind: 'savedProject' | 'openedDocuments'; roots: string[]; partial: boolean; externalChanges: 'reconciled' | 'unwatched' | 'watching' };
  freshness: CodeDiagnosticFreshness;
  truncated: boolean;
  runId?: string;
  notice?: string;
  failure?: { code: string; message: string };
}
export interface CodeDiagnosticsSnapshot {
  contractVersion: 1;
  context: CodeDiagnosticsContext;
  sequence: number;
  approval: { state: 'required' | 'approved' | 'revoked'; mode?: 'session' | 'persistent'; fingerprint?: string };
  providers: CodeDiagnosticsProvider[];
  resultSets: CodeDiagnosticsResultSet[];
  proposal?: { fingerprint: string; tools: { providerId: string; executable: string; configPaths: string[] }[] };
}
export interface CodeDiagnosticsAttachment {
  project: CodeDiagnosticsProject;
  context: CodeDiagnosticsContext;
  snapshot: CodeDiagnosticsSnapshot;
}
export interface CodeDiagnosticsUpdate {
  contractVersion: 1;
  context: CodeDiagnosticsContext;
  sequence: number;
  updateId: string;
  partIndex: number;
  partCount: number;
  snapshot: CodeDiagnosticsSnapshot;
}
export interface CodeDiagnosticsRun { providerId: string; runId: string }
export interface CodeDiagnosticsAck { context: CodeDiagnosticsContext }
export interface CodeDiagnosticsRuns extends CodeDiagnosticsAck { runs: CodeDiagnosticsRun[] }

/** All attached calls carry stable host identity, never a picker alias. */
export interface CodeDiagnosticsAPI {
  attachCodeDiagnostics(dir: string, contractVersion: 1, expectedHostId: string): Promise<CodeDiagnosticsAttachment>;
  detachCodeDiagnostics(context: CodeDiagnosticsContext, expectedHostId: string): Promise<CodeDiagnosticsAck>;
  getCodeDiagnosticsSnapshot(context: CodeDiagnosticsContext, expectedHostId: string): Promise<CodeDiagnosticsSnapshot>;
  approveCodeDiagnostics(context: CodeDiagnosticsContext, mode: 'session' | 'persistent', fingerprint: string, expectedHostId: string): Promise<CodeDiagnosticsSnapshot>;
  revokeCodeDiagnostics(context: CodeDiagnosticsContext, expectedHostId: string): Promise<CodeDiagnosticsSnapshot>;
  runCodeDiagnostics(context: CodeDiagnosticsContext, providerIds: string[] | undefined, expectedHostId: string): Promise<CodeDiagnosticsRuns>;
  cancelCodeDiagnostics(context: CodeDiagnosticsContext, runs: CodeDiagnosticsRun[], expectedHostId: string): Promise<CodeDiagnosticsAck>;
}

export const CODE_DIAGNOSTICS_EVENT = 'codeDiagnostics:updated';
export const CODE_DIAGNOSTICS_LIMITS = Object.freeze({ snapshotBytes: 8 * 1024 * 1024, partBytes: 1024 * 1024, bufferBytes: 16 * 1024 * 1024, findings: 10000, parts: 32, updates: 2, timeout: 5000, retries: 2 });

export class CodeDiagnosticsError extends Error {
  code: string;
  constructor(code: string, message: string) { super(message); this.code = code; }
}

/** Wails may serialize Go errors as strings; retain stable codes where provided. */
export function codeDiagnosticsError(error: unknown): CodeDiagnosticsError {
  if (error instanceof CodeDiagnosticsError) return error;
  const e = error as { code?: string; message?: string; data?: { code?: string } };
  const message = typeof error === 'string' ? error : e?.message ?? 'Code diagnostics request failed';
  return new CodeDiagnosticsError(e?.data?.code ?? e?.code ?? (/^(unsupported|disconnected|unauthorized|approvalRequired|approvalChanged|staleContext|invalidArgument|notFound|busy|limitExceeded|resyncRequired|toolFailed|canceled):/.exec(message)?.[1]) ?? 'toolFailed', message);
}

export function sameAttachment(a: CodeDiagnosticsContext, b: CodeDiagnosticsContext): boolean {
  return a.connectionId === b.connectionId && a.attachmentId === b.attachmentId;
}
export function sameContext(a: CodeDiagnosticsContext, b: CodeDiagnosticsContext): boolean {
  return sameAttachment(a, b) && a.generation === b.generation;
}
/** Account for Go encoding/json HTML escaping, not JS's smaller wire estimate. */
export function encodedBytes(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, c => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'))).length;
}
export function resultSetKey(set: CodeDiagnosticsResultSet): string {
  return JSON.stringify([set.providerId, set.origin, set.scope.kind, set.scope.kind === 'document' ? set.scope.path : '', set.scope.kind === 'document' ? set.scope.documentId : '', set.runId]);
}
export function diagnosticKey(d: CodeDiagnostic): string {
  return JSON.stringify([d.path, d.range ? [d.range.start.line, d.range.start.character, d.range.end.line, d.range.end.character] : null, d.severity, d.message, d.code]);
}
export interface AttributedCodeDiagnostic { diagnostic: CodeDiagnostic; entries: { providerId: string; diagnostic: CodeDiagnostic }[] }
export function groupCodeDiagnostics(sets: readonly CodeDiagnosticsResultSet[]): AttributedCodeDiagnostic[] {
  const groups = new Map<string, AttributedCodeDiagnostic>();
  for (const set of sets) for (const diagnostic of set.diagnostics) {
    const key = diagnosticKey(diagnostic);
    const group = groups.get(key) ?? { diagnostic, entries: [] };
    if (!groups.has(key)) groups.set(key, group);
    if (!group.entries.some(e => e.providerId === set.providerId && e.diagnostic.source === diagnostic.source)) group.entries.push({ providerId: set.providerId, diagnostic });
  }
  return [...groups.values()];
}
/** Eligibility only; renderer must additionally validate ranges against basis text. */
export function inlineCodeDiagnostic(d: CodeDiagnostic, saved: { dirty: boolean; revision?: string }): boolean {
  return !!d.range && !d.locationIssue && d.freshness === 'current' && d.basis.kind === 'saved' && !saved.dirty && !!saved.revision && d.basis.revision === saved.revision;
}
