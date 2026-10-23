// R14 frontend leg. HONESTY NOTE: this is a *recorded-sequence replay*, not a live
// cross-language pipe. fixtures/live-route.json was recorded by
//   R14_RECORD=<path> go test ./internal/rpc -run TestRouteRecordFrontendFixture
// which drives rpc.Client -> pipes -> rpc.Server -> real engine.Engine (injected live
// flag) -> LiveDocuments/SessionManager -> framed fake LSP servers, and stores every
// call result and every codeDiagnostics:updated event the Go client emitted. Here the
// real CodeDiagnosticsStore, FilesStore and LiveDocumentBridge consume those exact
// bytes through an api object whose calls are verified against the recorded requests.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fixtureStore, loadServerModule } from './helpers.mjs';
import { LiveDocumentBridge } from '../src/lib/liveDocuments.ts';
import { CodeDiagnosticsError } from '../src/lib/codeDiagnostics.ts';

const recording = JSON.parse(readFileSync(new URL('./fixtures/live-route.json', import.meta.url), 'utf8'));
const step = name => recording.steps.find(s => s.name === name);
const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')], ["'../codeNavigation'", lib('codeNavigation')],
]);
const { FilesStore } = await loadServerModule(new URL('../src/lib/stores/files.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__routeFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__routeFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(n => [`'../${n}'`, lib(n)]),
]);
const tick = async () => { for (let i = 0; i < 8; i++) await new Promise(r => setImmediate(r)); };
const TEXT = 'a😀\r\nb';
const content = text => ({ path: 'a.go', text, revision: 'r1', size: text.length, edit: { eligible: true, newline: 'crlf' } });

async function harness({ live = true } = {}) {
  const calls = [], listeners = new Map(), timers = new Map();
  const ids = ['da', 'db', 'dc'];
  globalThis.__routeFixture = {
    link: { dataHost: recording.host, blockedReason: null },
    api: { listFiles: async () => ({ root: recording.root, files: ['a.go'] }), readFile: async () => content(TEXT), saveFile: async () => { throw new Error('unused'); } },
  };
  const replay = (name, check) => { const s = step(name); if (s.error) throw new CodeDiagnosticsError(s.error, 'recorded'); return s.response; };
  const api = {
    onEvent(n, fn) { listeners.set(n, fn); return () => listeners.delete(n); },
    async attachCodeDiagnostics(dir, version, host) { calls.push(['attach']); assert.equal(host, recording.host); return step('attach').response; },
    async detachCodeDiagnostics(context) { return { context }; },
    async getCodeDiagnosticsSnapshot() { calls.push(['snapshot']); throw new CodeDiagnosticsError('unsupported', 'a sequence gap must not occur in the recorded route'); },
    async approveCodeDiagnostics(c, mode, fp) { calls.push(['approve']); assert.equal(fp, step('attach').response.snapshot.proposal.fingerprint); return replay('approve'); },
    async invalidateCodeDiagnosticsPath(c, path) { calls.push(['invalidate', path]); assert.equal(path, 'b.ts'); return replay('invalidate'); },
    async openCodeDiagnosticDocument(c, path, id, version, text, revision) {
      calls.push(['open', path, id, version, text]);
      assert.deepEqual([path, id, version, text, revision], ['a.go', 'da', 1, TEXT, 'r1']);
      return replay('open');
    },
    async changeCodeDiagnosticDocument(c, id, version, text) {
      calls.push(['change', id, version, text]);
      assert.deepEqual([id, version, text], ['da', 3, TEXT + '!']);
      return replay('change');
    },
    async saveCodeDiagnosticDocument(c, documentId) { return { context: c, documentId }; },
    async closeCodeDiagnosticDocument(c, documentId) { return { context: c, documentId }; },
  };
  const files = new FilesStore(), checks = new CodeDiagnosticsStore(api, () => 0);
  const bridge = new LiveDocumentBridge({ api, store: checks, source: () => ({ dir: files.dir, documents: files.displayedDocuments() }), newId: () => ids.shift(), timers: { set(fn) { timers.set(fn, fn); return fn; }, clear(fn) { timers.delete(fn); } } });
  checks.setLiveBasis(path => bridge.basis(path));
  checks.setLiveRecovery(() => bridge.statuses, (path, id) => bridge.reopen(path, id));
  bridge.onChange(() => checks.liveChanged());
  files.observe(bridge);
  const off = checks.onLifecycle(() => bridge.sync());
  const deliver = name => { for (const e of step(name).events) listeners.get('codeDiagnostics:updated')?.(structuredClone(e)); };
  await files.show(recording.root);
  await checks.attach(recording.root, { phase: 'connected', generation: 1, hostId: recording.host, capabilities: { codeDiagnostics: true, ...(live ? { liveCodeDiagnostics: true } : {}) } });
  return { files, checks, bridge, calls, timers, deliver, off, async flush() { for (const fn of [...timers.keys()]) { timers.delete(fn); fn(); } await tick(); } };
}

test('recorded Go route drives real stores: trust, providers, late/unversioned/empty, truncation, reconcile, failure', async () => {
  const h = await harness();
  h.deliver('attach');
  assert.equal(h.checks.status, 'ready');
  assert.equal(h.checks.snapshot.approval.state, 'required');
  // Trust denial: no live document call is possible before approval, and the
  // recorded host refusal is the typed approvalRequired error.
  h.files.openFile('a.go', { keep: true }); await tick();
  assert.equal(h.calls.filter(c => c[0] === 'open').length, 0);
  assert.equal(step('openDenied').error, 'approvalRequired');
  assert.equal(h.checks.liveBasis('a.go'), undefined);

  await h.checks.approve('session'); h.deliver('approve'); await tick();
  assert.equal(h.checks.approved, true);
  assert.deepEqual(h.calls.filter(c => c[0] === 'open'), [['open', 'a.go', 'da', 1, TEXT]]);
  h.deliver('open'); h.deliver('openSecond');
  const inline = () => h.checks.liveInline('a.go', h.bridge.basis('a.go'));
  assert.equal(inline().length, 0);

  // Two providers (gopls on a.go, typescript on b.ts) attributed separately.
  h.deliver('pushV1');
  assert.equal(h.checks.counts.error, 2);
  assert.deepEqual(h.checks.findings.flatMap(f => f.entries.map(e => e.providerId)).sort(), ['gopls', 'typescript']);
  assert.equal(inline().length, 1);
  assert.equal(inline()[0].diagnostic.message, '<script>&😀');
  assert.equal(h.checks.status, 'ready'); // contiguous: no gap/resync

  // Draft change: the v1 finding is no longer projected onto the v2 text.
  h.files.startEditing('a.go'); h.files.updateDraft('a.go', TEXT + '!');
  assert.equal(inline().length, 0);
  await h.flush();
  assert.equal(h.bridge.basis('a.go').version, 3);
  h.deliver('change');
  assert.equal(inline().length, 0); // late v1 push result still cannot attach to v2
  h.deliver('pushLateThenV2'); // late+foreign pushes were dropped by Go; v2 is current
  assert.equal(inline().length, 1);
  assert.equal(inline()[0].diagnostic.message, '<script>&😀 v2');

  h.deliver('pushUnversioned'); // unversioned stays uncertain and is never inline
  assert.equal(inline().length, 0);
  assert.equal(h.checks.findings.find(f => f.entries.some(e => e.providerId === 'gopls')).diagnostic.freshness, 'uncertain');

  h.deliver('emptyClear');
  assert.equal(h.checks.findings.some(f => f.entries.some(e => e.providerId === 'gopls')), false);
  assert.equal(h.checks.findings.some(f => f.entries.some(e => e.providerId === 'typescript')), true);

  h.deliver('pushTruncated'); // multi-part (10,11) update assembled
  const ts = h.checks.findings.find(f => f.entries.some(e => e.providerId === 'typescript'));
  assert.ok(ts.diagnostic.message.length <= 8192 && ts.diagnostic.message.length > 1000);
  assert.ok(h.checks.providers.find(p => p.providerId === 'typescript').notice);

  // Reconciliation (R10): the real store calls the route and commits the snapshot.
  await h.checks.invalidatePath(recording.root, 'b.ts'); h.deliver('invalidate');
  assert.deepEqual(h.calls.at(-1), ['invalidate', 'b.ts']);
  assert.equal(h.checks.findings.some(f => f.entries.some(e => e.providerId === 'typescript')), false);

  h.deliver('pushBeforeCrash');
  assert.equal(inline().length, 1);
  h.deliver('crash'); // failed provider: findings retained but stale, never inline
  assert.equal(h.checks.providers.find(p => p.providerId === 'gopls').phase, 'failed');
  assert.equal(inline().length, 0);
  assert.equal(h.checks.findings.find(f => f.entries.some(e => e.providerId === 'gopls')).diagnostic.freshness, 'stale');
  assert.equal(h.calls.filter(c => c[0] === 'snapshot').length, 0);
  h.off(); await h.checks.detach();
});

test('recorded route: old capability (no liveCodeDiagnostics) never sends a document', async () => {
  const h = await harness({ live: false });
  h.deliver('attach');
  await h.checks.approve('session'); h.deliver('approve');
  h.files.openFile('a.go', { keep: true }); await tick(); await h.flush();
  assert.equal(h.calls.filter(c => ['open', 'change'].includes(c[0])).length, 0);
  assert.equal(h.checks.liveBasis('a.go'), undefined);
  h.off(); await h.checks.detach();
});
