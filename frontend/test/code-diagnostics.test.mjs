import test from 'node:test';
import assert from 'node:assert/strict';
import { loadServerModule } from './helpers.mjs';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { CODE_DIAGNOSTICS_EVENT as eventName, CODE_DIAGNOSTICS_LIMITS, groupCodeDiagnostics, inlineCodeDiagnostic } from '../src/lib/codeDiagnostics.ts';

const url = new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url);
const { CodeDiagnosticsStore } = await loadServerModule(url, [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", JSON.stringify(new URL('../src/lib/codeDiagnostics.ts', import.meta.url).href)],
  ["'../codeDiagnosticCounts'", JSON.stringify(new URL('../src/lib/codeDiagnosticCounts.ts', import.meta.url).href)],
]);
import { aggregateCodeDiagnostics } from '../src/lib/codeDiagnosticCounts.ts';
const clone = structuredClone;

test('exact equivalence counts file-level findings and component ancestors once', () => {
  const variants = [finding, { ...finding, range: undefined }, { ...finding, code: 'x' },
    { ...finding, message: finding.message + ' ' }, { ...finding, severity: 'warning' },
    { ...finding, range: { start: { line: 1, character: 0 }, end: { line: 1, character: 2 } } }];
  const sets = [{ providerId: 'a', diagnostics: variants.map(d => ({ ...d, path: 'src/a/nested/f.go' })) },
    { providerId: 'b', diagnostics: [{ ...variants[0], path: 'src/a/nested/f.go', source: 'other', freshness: 'stale' },
      { ...finding, path: 'src/ab/f.go' }, { ...finding, path: 'root.go' },
      { ...finding, path: 'src/a/info.go', severity: 'information' }, { ...finding, path: 'src/a/hint.go', severity: 'hint' },
      { ...finding, path: 'Src/a/f.go' }] }];
  const groups = groupCodeDiagnostics(sets);
  const counts = aggregateCodeDiagnostics(groups);
  assert.equal(groups[0].entries.length, 2);
  assert.equal(counts.files.get('src/a/nested/f.go').error, 5);
  assert.equal(counts.folders.get('src/a').error, 5);
  assert.equal(counts.folders.get('src/a/nested').warning, 1);
  assert.equal(counts.folders.get('src').error, 6);
  assert.equal(counts.total.error, 8);
  assert.equal(counts.total.warning, 1);
  assert.equal(counts.folders.get('src/a').stale, true);
  assert.equal(counts.folders.has('empty'), false);
  assert.equal(counts.files.has('src/a/info.go'), false);
});

test('shared attribution and counts survive provider clears, then empty replacement and detach clear maps', async () => {
  const h = harness(); const s = snapshot();
  s.resultSets.push({ ...clone(s.resultSets[0]), providerId: 'second', diagnostics: [{ ...clone(finding), source: 'second' }] });
  h.setSnapshot(s); await h.store.attach('/input', connection);
  assert.equal(h.store.counts.error, 1);
  assert.equal(h.store.findings[0].entries.length, 2);
  h.emit(update(snapshot(1, { resultSets: [{ ...s.resultSets[0], diagnostics: [] }, s.resultSets[1]] })));
  assert.equal(h.store.counts.error, 1);
  assert.equal(h.store.findings[0].entries[0].providerId, 'second');
  assert.equal(h.store.pathCounts.files.get('a.go').error, 1);
  h.emit(update(snapshot(2, { resultSets: [] })));
  assert.equal(h.store.pathCounts.files.size, 0);
  assert.equal(h.store.countsUnder(''), 0);
  h.emit(update(snapshot(3)));
  h.store.disconnect();
  assert.equal(h.store.pathCounts.files.get('a.go').stale, true);
  await h.store.detach();
  assert.equal(h.store.pathCounts.files.size, 0);
});
const deferred = () => { let resolve, reject; const promise = new Promise((a,b) => { resolve=a; reject=b; }); return { promise, resolve, reject }; };
const tick = () => new Promise(r => setImmediate(r));
const context = { connectionId: 'connection', attachmentId: 'attachment', generation: 'generation' };
const finding = { path: 'a.go', severity: 'error', message: '<plain> 🐈', source: 'go', freshness: 'current', basis: { kind: 'saved', revision: 'r1' }, range: { start: { line: 0, character: 0 }, end: { line: 0, character: 2 } } };
function snapshot(sequence = 0, changes = {}) { return { contractVersion: 1, context: clone(context), sequence, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers: [{ providerId: 'go', availability: 'available', phase: 'idle', runId: 'run1', coverage: { kind: 'savedProject', roots: [''], partial: false, externalChanges: 'unwatched' }, freshness: 'current', truncated: false }], resultSets: [{ providerId: 'go', origin: 'checker', scope: { kind: 'project' }, runId: 'run1', diagnostics: [clone(finding)] }], ...changes }; }
function update(s, partIndex = 0, partCount = 1, updateId = `u${s.sequence}`) { return { contractVersion: 1, context: s.context, sequence: s.sequence, updateId, partIndex, partCount, snapshot: s }; }
const connection = { phase: 'local', generation: 0, hostId: 'host1', capabilities: { codeDiagnostics: true } };
function harness() {
  const listeners = new Map(), old = [];
  let attached = snapshot();
  const backend = {
    onEvent(name, fn) { listeners.set(name, fn); old.push(fn); return () => listeners.delete(name); },
    async attachCodeDiagnostics() { return { project: { hostId: 'host1', root: '/canonical' }, context: attached.context, snapshot: attached }; },
    async detachCodeDiagnostics() { return { context }; },
    async getCodeDiagnosticsSnapshot() { return attached; },
    async runCodeDiagnostics() { return { context, runs: [{ providerId: 'go', runId: 'late' }] }; },
  };
  const store = new CodeDiagnosticsStore(backend, () => 0);
  return { store, backend, old, emit: e => listeners.get(eventName)?.(e), setSnapshot: s => attached = s };
}

test('subscribe-before-attach, atomic multipart reversed/duplicate fragments, empty replacement', async () => {
  const h = harness(), reply = deferred(); h.backend.attachCodeDiagnostics = () => reply.promise;
  const attach = h.store.attach('/input', connection);
  const s = snapshot(1); s.resultSets[0].diagnostics = [];
  h.emit(update(snapshot(1), 1, 2)); h.emit(update(snapshot(1), 1, 2)); h.emit(update(s, 0, 2));
  assert.equal(h.store.snapshot, undefined);
  reply.resolve({ project: { hostId: 'host1', root: '/canonical' }, context, snapshot: snapshot() });
  await attach;
  assert.equal(h.store.snapshot.sequence, 1); assert.equal(h.store.counts.error, 1);
  h.emit(update(snapshot(2, { resultSets: [{ ...s.resultSets[0], diagnostics: [] }] })));
  assert.equal(h.store.counts.error, 0);
  h.emit(update(snapshot(1))); assert.equal(h.store.snapshot.sequence, 2);
  await h.store.detach();
});

test('gap resnapshot coalesces; late snapshot cannot roll back newer event', async () => {
  const h = harness(); await h.store.attach('/input', connection);
  const reply = deferred(); let calls = 0;
  h.backend.getCodeDiagnosticsSnapshot = () => { calls++; return reply.promise; };
  h.emit(update(snapshot(3))); h.emit(update(snapshot(4)));
  assert.equal(calls, 1); assert.equal(h.store.status, 'resyncing'); assert.equal(h.store.inline('a.go', { revision: 'r1', dirty: false }).length, 0);
  h.emit(update(snapshot(1))); h.emit(update(snapshot(2)));
  reply.resolve(snapshot(1)); await tick();
  assert.equal(h.store.snapshot.sequence, 2);
  await h.store.detach();
});

test('conflicts, overflow, incomplete parts timeout and bounded recovery', async () => {
  const h = harness(); await h.store.attach('/input', connection);
  let calls = 0; h.backend.getCodeDiagnosticsSnapshot = async () => { calls++; return snapshot(); };
  h.emit(update(snapshot(1), 0, 2));
  const conflicting = snapshot(1); conflicting.resultSets[0].diagnostics[0].message = 'conflict';
  h.emit(update(conflicting, 0, 2)); await tick(); assert.equal(calls, 1);
  h.emit(update(snapshot(1), 0, 33)); await tick(); assert.equal(calls, 2);
  h.emit(update(snapshot(4))); await tick(); assert.equal(calls, 2); assert.equal(h.store.status, 'error');
  h.store.refresh(); await tick(); assert.equal(calls, 3);
  // Real timer callback without a five-second wall-clock wait.
  const original = globalThis.setTimeout; let timeout;
  globalThis.setTimeout = fn => { timeout = fn; return original(() => {}, 100000); };
  try { h.emit(update(snapshot(1), 0, 2)); } finally { globalThis.setTimeout = original; }
  timeout(); await tick(); assert.equal(calls, 4);
  assert.equal(h.store.snapshot.sequence, 0); assert.equal(h.store.counts.error, 1);
  await h.store.detach();
});

test('byte limit triggers stale recovery, truncation and independent unavailable providers survive', async () => {
  const h = harness(); const s = snapshot();
  s.providers[0].truncated = true; s.providers[0].coverage.partial = true;
  s.providers.push({ ...clone(s.providers[0]), providerId: 'svelte', availability: 'missingTool' });
  h.setSnapshot(s); await h.store.attach('/input', connection);
  assert.equal(h.store.partial, true); assert.equal(h.store.truncated, true); assert.equal(h.store.providers[1].availability, 'missingTool');
  let calls = 0; h.backend.getCodeDiagnosticsSnapshot = async () => { calls++; return s; };
  const big = snapshot(1); big.resultSets[0].diagnostics[0].message = '<'.repeat(CODE_DIAGNOSTICS_LIMITS.partBytes / 6);
  h.emit(update(big)); await tick(); assert.equal(calls, 1);
  await h.store.detach();
});

test('revoke generation race rejects old callbacks/replies and unsolicited generations', async () => {
  const h = harness(); await h.store.attach('/input', connection);
  const approval = deferred(), revoke = deferred();
  h.backend.approveCodeDiagnostics = () => approval.promise; h.backend.revokeCodeDiagnostics = () => revoke.promise;
  const a = h.store.approve('session', 'fp'), r = h.store.revoke();
  const revoked = snapshot(1, { context: { ...context, generation: 'new' }, approval: { state: 'revoked' }, resultSets: [] });
  h.emit(update(revoked)); approval.resolve(snapshot(2)); revoke.resolve(revoked); await Promise.all([a, r]);
  assert.equal(h.store.snapshot.context.generation, 'new'); assert.equal(h.store.counts.error, 0);
  h.emit(update(snapshot(3))); assert.equal(h.store.snapshot.sequence, 1);
  h.emit(update(snapshot(4, { context: { ...context, generation: 'alien' } }))); assert.equal(h.store.snapshot.sequence, 1);
  await h.store.detach();
});

test('host switch, epoch change, reconnect same root and old listener cannot revive results', async () => {
  const h = harness(), reply = deferred(); h.backend.attachCodeDiagnostics = () => reply.promise;
  const pending = h.store.attach('/same', connection); const oldCallback = h.old[0];
  h.store.disconnect(); reply.resolve({ project: { hostId: 'host1', root: '/same' }, context, snapshot: snapshot() }); await pending;
  oldCallback(update(snapshot(1))); assert.equal(h.store.snapshot, undefined); assert.equal(h.store.status, 'disconnected');
  let epoch = 0; const store = new CodeDiagnosticsStore(h.backend, () => epoch);
  const p = deferred(); h.backend.attachCodeDiagnostics = () => p.promise;
  const attached = store.attach('/same', connection); epoch++;
  p.resolve({ project: { hostId: 'host1', root: '/same' }, context, snapshot: snapshot() }); await attached;
  assert.equal(store.snapshot, undefined); await store.detach();
  h.backend.attachCodeDiagnostics = async () => ({ project: { hostId: 'host2', root: '/same' }, context: { ...context, connectionId: 'new' }, snapshot: snapshot(0, { context: { ...context, connectionId: 'new' } }) });
  await h.store.attach('/same', { ...connection, hostId: 'host2', generation: 1 });
  oldCallback(update(snapshot(9))); h.emit(update(snapshot(10))); assert.equal(h.store.snapshot.sequence, 0);
  await h.store.detach();
});

test('dedup preserves eligible basis, file-level counts, stale/cancel retention and run ack never owns results', async () => {
  const h = harness(), s = snapshot();
  s.resultSets.push({ ...clone(s.resultSets[0]), providerId: 'other', diagnostics: [{ ...clone(finding), source: 'other', freshness: 'stale' }, { ...clone(finding), range: undefined }] });
  h.setSnapshot(s); await h.store.attach('/input', connection);
  assert.equal(h.store.counts.error, 2); assert.equal(h.store.findings[0].entries.length, 2);
  assert.equal(h.store.inline('a.go', { revision: 'r1', dirty: true }).length, 0);
  assert.equal(h.store.inline('a.go', { revision: 'r1', dirty: false }).length, 1);
  assert.equal(h.store.countsUnder(''), 2); assert.equal(h.store.countsUnder('a'), 0);
  await h.store.run(); assert.equal(h.store.snapshot.sequence, 0);
  const canceled = snapshot(1); canceled.providers[0].phase = 'canceled'; canceled.resultSets[0].diagnostics[0].freshness = 'stale';
  h.emit(update(canceled)); assert.equal(h.store.counts.error, 1); assert.equal(h.store.inline('a.go', { revision: 'r1', dirty: false }).length, 0);
  assert.equal(inlineCodeDiagnostic({ ...finding, basis: { kind: 'saved' } }, { revision: 'r1', dirty: false }), false);
  assert.equal(groupCodeDiagnostics([{ ...s.resultSets[0], diagnostics: [{ ...finding, code: 'a' }, { ...finding, code: 'b' }] }]).length, 2);
  await h.store.detach();
});

test('old capability absent: no attach/process; real browser mock approval/run/revoke lifecycle', async () => {
  const h = harness(); h.backend.attachCodeDiagnostics = () => assert.fail('must not attach');
  await h.store.attach('/input', { ...connection, capabilities: {} }); assert.equal(h.store.status, 'unsupported');
  const backend = new MockBackendAPI(); const store = new CodeDiagnosticsStore(backend, () => 0);
  await store.attach('/synthetic', await backend.connectionState());
  assert.equal(store.snapshot.approval.state, 'required'); assert.equal(store.resultSets.length, 0);
  await assert.rejects(store.run(), /Approve/);
  await store.approve('session'); await store.run(); await tick(); assert.equal(store.counts.warning, 1);
  await store.revoke(); assert.equal(store.snapshot.approval.state, 'revoked'); assert.equal(store.resultSets.length, 0);
  await store.detach();
});

test('CD-01 shared Go multipart fixtures assemble once with retained explicit empty document set', async () => {
  const { readFile } = await import('node:fs/promises');
  const parts = JSON.parse(await readFile(new URL('../../internal/codediagnostics/testdata/contract/events.json', import.meta.url), 'utf8'));
  const h = harness();
  const initial = { ...clone(parts[0].snapshot), sequence: 1, resultSets: [] };
  h.setSnapshot(initial); await h.store.attach('/input', connection);
  h.emit(parts[1]); assert.equal(h.store.resultSets.length, 0);
  h.emit(parts[1]); h.emit(parts[0]);
  assert.equal(h.store.snapshot.sequence, 2);
  assert.equal(h.store.resultSets.length, 2);
  assert.equal(h.store.resultSets.find(s => s.origin === 'checker').diagnostics.length, 2);
  assert.equal(h.store.resultSets.find(s => s.origin === 'lsp').diagnostics.length, 0);
  await h.store.detach();
});

test('early update-count overflow and detach during recovery keep bounded ownership', async () => {
  const h = harness(), attachReply = deferred(), recovery = deferred(); let calls = 0;
  h.backend.attachCodeDiagnostics = () => attachReply.promise;
  h.backend.getCodeDiagnosticsSnapshot = () => { calls++; return recovery.promise; };
  const pending = h.store.attach('/input', connection);
  h.emit(update(snapshot(1))); h.emit(update(snapshot(2))); h.emit(update(snapshot(3)));
  attachReply.resolve({ project: { hostId: 'host1', root: '/canonical' }, context, snapshot: snapshot() });
  await pending; assert.equal(calls, 1); assert.equal(h.store.status, 'resyncing');
  await h.store.detach(); recovery.resolve(snapshot(8)); await tick();
  assert.equal(h.store.status, 'detached'); assert.equal(h.store.snapshot, undefined);
});

test('visible discovery is asynchronous, hide/replacement invalidates replies, unsupported remote never attaches', async () => {
  const h = harness(), discovery = deferred(); let attaches = 0, runs = 0;
  h.backend.connectionState = () => discovery.promise;
  const attach = h.backend.attachCodeDiagnostics;
  h.backend.attachCodeDiagnostics = (...args) => { attaches++; return attach(...args); };
  h.backend.runCodeDiagnostics = () => { runs++; assert.fail('opening must not execute'); };
  const showing = h.store.show('/first');
  assert.equal(h.store.status, 'attaching'); assert.equal(attaches, 0);
  await h.store.hide(); discovery.resolve(connection); await showing;
  assert.equal(attaches, 0); assert.equal(h.store.status, 'detached');
  h.backend.connectionState = async () => ({ ...connection, phase: 'connected', hostId: undefined });
  await h.store.show('/remote'); assert.equal(h.store.status, 'unsupported'); assert.equal(attaches, 0);
  h.backend.connectionState = async () => connection;
  await h.store.show('/local'); assert.equal(attaches, 1); assert.equal(runs, 0);
  await h.store.hide();
});

test('approval never runs, duplicate checks blocked through ack/event gap, cancel exact run, retry scoped, revoke immediate', async () => {
  const h = harness(); await h.store.attach('/input', connection);
  let calls = 0; const reply = deferred();
  h.backend.runCodeDiagnostics = (c, ids, host) => { calls++; assert.deepEqual(c, context); assert.equal(host, 'host1'); assert.equal(ids, undefined); return reply.promise; };
  const running = h.store.run(); assert.equal(h.store.canRun, false);
  await assert.rejects(h.store.run(), /active/); assert.equal(calls, 1);
  reply.resolve({ context, runs: [{ providerId: 'go', runId: 'new-run' }] }); await running;
  assert.equal(h.store.running, true); await assert.rejects(h.store.run(), /active/);
  h.backend.cancelCodeDiagnostics = async (c, runs, host) => { assert.deepEqual(c, context); assert.equal(host, 'host1'); assert.ok(runs.some(r => r.runId === 'new-run')); return { context }; };
  await h.store.cancel();
  const failed = snapshot(1); failed.providers[0] = { ...failed.providers[0], runId: 'new-run', phase: 'failed' }; h.emit(update(failed));
  assert.equal(h.store.canRun, true);
  h.backend.runCodeDiagnostics = async (c, ids) => { assert.deepEqual(ids, ['go']); return { context, runs: [] }; };
  await h.store.retry('go');
  const revocation = deferred(); h.backend.revokeCodeDiagnostics = () => revocation.promise;
  const revoke = h.store.revoke(); assert.equal(h.store.approved, false); assert.equal(h.store.canRun, false);
  await assert.rejects(h.store.run(), /Approve/);
  revocation.resolve(snapshot(2, { approval: { state: 'revoked' } })); await revoke;
  for (const mode of ['session', 'persistent']) {
    h.backend.approveCodeDiagnostics = async (c, actualMode, fp, host) => { assert.deepEqual(c, context); assert.equal(host, 'host1'); assert.equal(actualMode, mode); assert.equal(fp, 'proposal'); return snapshot(h.store.snapshot.sequence + 1); };
    await h.store.approve(mode, 'proposal');
  }
  assert.equal(calls, 1); await h.store.hide();
});

test('late approval on root switch cannot mutate current pending controls', async () => {
  const h = harness(); await h.store.attach('/old', connection);
  const reply = deferred(); h.backend.approveCodeDiagnostics = () => reply.promise;
  const approval = h.store.approve('session', 'fp');
  await h.store.attach('/new', connection);
  reply.resolve(snapshot(10)); await approval;
  assert.equal(h.store.snapshot.sequence, 0); assert.equal(h.store.pending, undefined);
  await h.store.hide();
});

test('explicit refresh and checker rerun await read-only reconciliation; disconnect cancels queued run', async () => {
  const h = harness(); await h.store.attach('/input', connection);
  const order = [];
  h.store.setReconciler(async () => { order.push('reconcile'); });
  h.backend.getCodeDiagnosticsSnapshot = async () => { order.push('snapshot'); return snapshot(); };
  h.backend.runCodeDiagnostics = async () => { order.push('run'); return { context, runs: [] }; };
  await h.store.refresh(); await tick();
  assert.deepEqual(order, ['reconcile', 'snapshot']);
  order.length = 0; await h.store.run();
  assert.deepEqual(order, ['reconcile', 'run']);
  assert.equal(h.store.resultSets[0].diagnostics[0].basis.kind, 'saved');
  const wait = deferred(); h.store.setReconciler(() => wait.promise);
  const pending = h.store.run(); h.store.disconnect(); wait.resolve();
  await assert.rejects(pending, e => e.code === 'staleContext');
  assert.deepEqual(order, ['reconcile', 'run']);
});
