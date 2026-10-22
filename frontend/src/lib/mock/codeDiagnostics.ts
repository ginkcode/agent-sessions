import type { CodeDiagnosticsAPI, CodeDiagnosticsContext, CodeDiagnosticsSnapshot, CodeDiagnosticsRun } from '../codeDiagnostics';
import { CODE_DIAGNOSTICS_EVENT, CodeDiagnosticsError, sameContext } from '../codeDiagnostics';

/** Synthetic, in-memory checker. No executable or editor/text dependencies. */
export class MockCodeDiagnostics implements CodeDiagnosticsAPI {
  private serial = 0;
  private snapshots = new Map<string, CodeDiagnosticsSnapshot>();
  private publishEvent: (name: string, event: unknown) => void;
  constructor(publishEvent: (name: string, event: unknown) => void) { this.publishEvent = publishEvent; }
  private get(c: CodeDiagnosticsContext, host: string) {
    const s = this.snapshots.get(c.attachmentId);
    if (host !== 'mock:local' || !s || !sameContext(c, s.context)) throw new CodeDiagnosticsError('staleContext', 'Mock attachment expired');
    return s;
  }
  private publish(s: CodeDiagnosticsSnapshot) {
    s.sequence++;
    this.publishEvent(CODE_DIAGNOSTICS_EVENT, structuredClone({ contractVersion: 1, context: s.context, sequence: s.sequence, updateId: `mock-update-${++this.serial}`, partIndex: 0, partCount: 1, snapshot: s }));
    return structuredClone(s);
  }
  async attachCodeDiagnostics(dir: string, version: 1, host: string) {
    if (host !== 'mock:local' || version !== 1) throw new CodeDiagnosticsError('unsupported', 'Mock diagnostics support Local only');
    const context = { connectionId: 'mock-connection', attachmentId: `mock-${++this.serial}`, generation: `generation-${this.serial}` };
    const snapshot: CodeDiagnosticsSnapshot = { contractVersion: 1, context, sequence: 0, approval: { state: 'required' }, providers: [{ providerId: 'mock-checker', availability: 'approvalRequired', phase: 'idle', coverage: { kind: 'savedProject', roots: [''], partial: true, externalChanges: 'unwatched' }, freshness: 'uncertain', truncated: false }], resultSets: [], proposal: { fingerprint: 'mock-fingerprint', tools: [{ providerId: 'mock-checker', executable: '/synthetic/checker', configPaths: [] }] } };
    this.snapshots.set(context.attachmentId, snapshot);
    return structuredClone({ project: { hostId: host, root: dir }, context, snapshot });
  }
  async detachCodeDiagnostics(c: CodeDiagnosticsContext, host: string) { this.get(c, host); this.snapshots.delete(c.attachmentId); return { context: c }; }
  async getCodeDiagnosticsSnapshot(c: CodeDiagnosticsContext, host: string) { return structuredClone(this.get(c, host)); }
  async approveCodeDiagnostics(c: CodeDiagnosticsContext, mode: 'session' | 'persistent', fingerprint: string, host: string) {
    const s = this.get(c, host);
    if (fingerprint !== s.proposal?.fingerprint) throw new CodeDiagnosticsError('approvalChanged', 'Mock proposal changed');
    s.approval = { state: 'approved', mode, fingerprint }; s.providers[0].availability = 'available';
    return this.publish(s);
  }
  async revokeCodeDiagnostics(c: CodeDiagnosticsContext, host: string) {
    const s = this.get(c, host); s.context = { ...c, generation: `generation-${++this.serial}` }; s.approval = { state: 'revoked' }; s.resultSets = [];
    s.providers[0] = { ...s.providers[0], phase: 'idle', availability: 'approvalRequired', runId: undefined };
    return this.publish(s);
  }
  async runCodeDiagnostics(c: CodeDiagnosticsContext, ids: string[] | undefined, host: string) {
    const s = this.get(c, host);
    if (s.approval.state !== 'approved') throw new CodeDiagnosticsError('approvalRequired', 'Approve mock checker first');
    if (ids && (ids.length !== 1 || ids[0] !== 'mock-checker')) throw new CodeDiagnosticsError('invalidArgument', 'Unknown mock provider');
    const run = { providerId: 'mock-checker', runId: `run-${++this.serial}` };
    s.providers[0] = { ...s.providers[0], runId: run.runId, phase: 'checking' }; this.publish(s);
    // Complete on a later microtask, with the same ownership/latest-run guards as a real checker.
    queueMicrotask(() => {
      if (this.snapshots.get(c.attachmentId) !== s || !sameContext(c, s.context) || s.providers[0].runId !== run.runId || s.providers[0].phase !== 'checking') return;
      s.providers[0].phase = 'idle';
      s.resultSets = [{ ...run, origin: 'checker', scope: { kind: 'project' }, diagnostics: [{ path: 'example.go', severity: 'warning', message: 'Synthetic mock finding', source: 'mock-checker', freshness: 'uncertain', basis: { kind: 'saved' } }] }];
      this.publish(s);
    });
    return { context: c, runs: [run] };
  }
  async cancelCodeDiagnostics(c: CodeDiagnosticsContext, runs: CodeDiagnosticsRun[], host: string) {
    const s = this.get(c, host);
    for (const r of runs) if (s.providers[0].runId === r.runId && r.providerId === 'mock-checker') {
      s.providers[0].phase = 'canceled'; s.providers[0].freshness = 'stale';
      s.resultSets = s.resultSets.map(set => ({ ...set, diagnostics: set.diagnostics.map(d => ({ ...d, freshness: 'stale' })) }));
    }
    this.publish(s); return { context: c };
  }
}
