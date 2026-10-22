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
]);
const clone = structuredClone;
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
