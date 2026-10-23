// Regression: the user's browse sequence main.go -> mousenav_other.go ->
// mousenav_linux.c -> reopen, with save and Refresh, through the real files and
// diagnostics stores and a fake backend that enforces the Go manager's document
// rules (documents.go: one open document per path, single-use identities,
// monotonic versions; live_owner.go: no owner for non-Go files).
import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule } from './helpers.mjs';
import { LiveDocumentBridge } from '../src/lib/liveDocuments.ts';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')],
]);
const { FilesStore } = await loadServerModule(new URL('../src/lib/stores/files.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__syncRecoveryFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__syncRecoveryFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(n => [`'../${n}'`, lib(n)]),
]);
const tick = async () => { for (let i = 0; i < 10; i++) await new Promise(r => setImmediate(r)); };
const context = { connectionId: 'c', attachmentId: 'a', generation: 'g' };
const MAIN = 'cmd/agent-sessions/main.go', OTHER = 'cmd/agent-sessions/mousenav_other.go', C = 'cmd/agent-sessions/mousenav_linux.c';
const disk = { [MAIN]: 'package main\n', [OTHER]: '//go:build !linux && !darwin\n\npackage main\n', [C]: '#include <stdio.h>\n' };
const content = (path, text = disk[path], revision = 'r1') => ({ path, text, revision, size: text.length, edit: { eligible: true, newline: 'lf' } });
const err = (code, message) => Object.assign(new Error(`${code}: ${message}`), { code });

/** Mirrors Manager.OpenDocument/ChangeDocument/SaveDocument/CloseDocument. */
function fakeBackend({ extensions = ['.go'] } = {}) {
  const open = new Map(), closed = new Set(), calls = [], listeners = new Map();
  let seq = 0;
  const provider = { providerId: 'gopls', availability: 'available', phase: 'idle', freshness: 'uncertain', truncated: false,
    coverage: { kind: 'openedDocuments', roots: [''], partial: true, externalChanges: 'unwatched' }, ...(extensions ? { documentExtensions: extensions } : {}) };
  const snapshot = () => ({ contractVersion: 1, context, sequence: seq++, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers: [provider], resultSets: [] });
  const api = {
    onEvent(n, fn) { listeners.set(n, fn); return () => listeners.delete(n); },
    async attachCodeDiagnostics() { return { project: { hostId: 'h', root: '/p' }, context, snapshot: snapshot() }; },
    async detachCodeDiagnostics() { open.clear(); return { context }; },
    async getCodeDiagnosticsSnapshot() { await tick(); return snapshot(); },
    async openCodeDiagnosticDocument(c, path, id, version, text) {
      calls.push(['open', path, id]);
      if (open.has(id) || closed.has(id)) throw err('invalidArgument', 'Document identity was already used');
      if ([...open.values()].some(d => d.path === path)) throw err('invalidArgument', 'Path already has an open document');
      if (!path.endsWith('.go')) throw err('unsupported', 'No unique language server owns this file');
      open.set(id, { path, version, text });
      return { context: c, documentId: id, version };
    },
    async changeCodeDiagnosticDocument(c, id, version, text) {
      calls.push(['change', id, version]);
      if (closed.has(id)) throw err('staleContext', 'Document lifecycle is closed');
      const d = open.get(id);
      if (!d) throw err('notFound', 'Document is not open');
      if (version < d.version) throw err('invalidArgument', 'Document version is not monotonic');
      if (version === d.version && text !== d.text) throw err('invalidArgument', 'Document version conflicts with different text');
      Object.assign(d, { version, text });
      return { context: c, documentId: id, version };
    },
    async saveCodeDiagnosticDocument(c, id, saved, revision) {
      calls.push(['save', id, saved]);
      const d = open.get(id);
      if (!d) throw err(closed.has(id) ? 'staleContext' : 'notFound', 'Document is not open');
      if (saved < 1 || saved > d.version || !revision) throw err('invalidArgument', 'Invalid saved version or revision');
      if (saved !== d.version) throw err('resyncRequired', 'Saved snapshot is older than the live document');
      return { context: c, documentId: id };
    },
    async closeCodeDiagnosticDocument(c, id) {
      calls.push(['close', id]);
      if (closed.has(id)) return { context: c, documentId: id };
      if (!open.delete(id)) throw err('notFound', 'Document is not open');
      closed.add(id);
      return { context: c, documentId: id };
    },
  };
  return { api, open, calls, listeners, snapshot };
}

async function harness(options) {
  const backend = fakeBackend(options);
  globalThis.__syncRecoveryFixture = {
    link: { dataHost: 'h', blockedReason: null },
    api: {
      listFiles: async () => ({ root: '/p', files: Object.keys(disk) }),
      readFile: async (_d, path) => content(path),
      saveFile: async (_d, r) => ({ status: 'saved', content: content(r.path, r.text, 'r2') }),
    },
  };
  const files = new FilesStore(), checks = new CodeDiagnosticsStore(backend.api, () => 0);
  let id = 0;
  const timers = new Map();
  const bridge = new LiveDocumentBridge({ api: backend.api, store: checks, source: () => ({ dir: files.dir, documents: files.displayedDocuments() }), newId: () => `d${++id}`, timers: { set(fn) { timers.set(fn, fn); return fn; }, clear(fn) { timers.delete(fn); } } });
  checks.setLiveBasis(path => bridge.basis(path));
  checks.setLiveRecovery(() => bridge.statuses, (path, docId) => bridge.reopen(path, docId));
  bridge.onChange(() => checks.liveChanged());
  files.observe(bridge);
  checks.onLifecycle(() => bridge.sync());
  await files.show('/p');
  await checks.attach('/p', { phase: 'connected', generation: 1, hostId: 'h', capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } });
  await tick();
  const flush = async () => { for (const fn of [...timers.keys()]) { timers.delete(fn); fn(); } await tick(); };
  /** Every displayed path has at most one backend document and it is the bridge's. */
  const consistent = () => {
    const held = [...backend.open.entries()].map(([docId, d]) => [d.path, docId]).sort();
    const tracked = bridge.tracked.filter(p => !bridge.failure(p)).map(p => [p, bridge.basis(p)?.documentId ?? bridge.statuses.find(s => s.path === p)?.documentId]).sort();
    assert.deepEqual(held, tracked, 'backend documents and bridge lifecycles diverged');
  };
  return { files, checks, bridge, backend, flush, consistent };
}

const failures = h => h.checks.liveDocuments.filter(d => d.failure).map(d => `${d.path}:${d.failure}`);

test('browse main.go -> mousenav_other.go -> mousenav_linux.c -> reopen never reports invalidArgument or unsupported', async () => {
  const h = await harness();
  h.files.openFile(MAIN); await tick();
  assert.equal(h.bridge.basis(MAIN).synced, true);
  h.files.openFile(OTHER); await tick(); // preview replaces main.go
  h.files.openFile(C); await tick(); // preview replaces mousenav_other.go
  assert.deepEqual(failures(h), []);
  // Unsupported files are not live, not failed; nothing was sent for them.
  assert.equal(h.bridge.failure(C), undefined);
  assert.equal(h.backend.calls.some(c => c[1] === C), false);
  h.files.openFile(MAIN, { keep: true }); await tick();
  h.files.openFile(OTHER, { keep: true }); await tick();
  assert.deepEqual(failures(h), []);
  assert.equal(h.bridge.basis(MAIN).synced, true);
  assert.equal(h.bridge.basis(OTHER).synced, true);
  h.consistent();
});

test('Refresh (resync) keeps open lifecycles instead of reopening paths the backend still holds', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  h.files.openFile(OTHER, { keep: true }); await tick();
  const before = [h.bridge.basis(MAIN).documentId, h.bridge.basis(OTHER).documentId];
  await h.checks.refresh(); await tick();
  assert.equal(h.checks.status, 'ready');
  assert.deepEqual(failures(h), []);
  assert.deepEqual([h.bridge.basis(MAIN).documentId, h.bridge.basis(OTHER).documentId], before, 'refresh must not replace lifecycles');
  assert.equal(h.backend.calls.filter(c => c[0] === 'open').length, 2);
  h.consistent();
});

test('typing and tab changes during a resync are delivered once it settles', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  h.files.openFile(OTHER, { keep: true }); await tick();
  h.files.startEditing(MAIN); h.files.updateDraft(MAIN, 'package main\n// typed\n');
  const refresh = h.checks.refresh();
  // Attachment is momentarily not live: the bridge must stay inert, not forget.
  h.files.updateDraft(MAIN, 'package main\n// typed more\n');
  h.files.closeTab(OTHER);
  h.files.openFile(C, { keep: true });
  await refresh; await tick(); await h.flush();
  assert.deepEqual(failures(h), []);
  assert.equal(h.backend.open.get(h.bridge.basis(MAIN).documentId).text, 'package main\n// typed more\n');
  assert.equal(h.bridge.basis(MAIN).synced, true);
  assert.equal(h.bridge.basis(OTHER), undefined);
  h.consistent();
});

test('save after a refresh sends didSave for the live lifecycle', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  await h.checks.refresh(); await tick();
  h.files.startEditing(MAIN); h.files.updateDraft(MAIN, 'package main\n\nfunc main() {}\n');
  assert.equal(await h.files.saveEdited(MAIN), true); await tick();
  const id = h.bridge.basis(MAIN).documentId;
  assert.deepEqual(h.backend.calls.filter(c => c[0] === 'save'), [['save', id, h.bridge.basis(MAIN).version]]);
  assert.deepEqual(failures(h), []);
  h.consistent();
});

test('a store error (status error) followed by recovery does not orphan backend documents', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  const id = h.bridge.basis(MAIN).documentId;
  h.backend.api.approveCodeDiagnostics = async () => { throw err('busy', 'Request already pending'); };
  // Any failed store action flips status to error (attachment not live), then a
  // refresh restores ready on the same attachment and generation.
  await assert.rejects(h.checks.approve('session'));
  assert.equal(h.checks.status, 'error');
  assert.equal(h.checks.liveAttachment, undefined);
  assert.equal(h.bridge.basis(MAIN), undefined, 'no live claim while not live');
  await h.checks.refresh(); await tick();
  assert.equal(h.checks.status, 'ready');
  assert.deepEqual(failures(h), []);
  assert.equal(h.bridge.basis(MAIN).documentId, id);
  h.consistent();
});

test('a genuine invalidArgument stays visible', async () => {
  const h = await harness();
  const change = h.backend.api.changeCodeDiagnosticDocument;
  h.backend.api.changeCodeDiagnosticDocument = async (...args) => { await change(...args); throw err('invalidArgument', 'Document version conflicts with different text'); };
  h.files.openFile(MAIN, { keep: true }); await tick();
  h.files.startEditing(MAIN); h.files.updateDraft(MAIN, 'x'); await h.flush();
  h.files.updateDraft(MAIN, 'xy'); await h.flush();
  h.files.updateDraft(MAIN, 'xyz'); await h.flush();
  assert.deepEqual(failures(h), [`${MAIN}:invalidArgument`]);
});

test('a declared-supported file that the backend rejects as unsupported is still reported', async () => {
  const h = await harness({ extensions: ['.go', '.c'] });
  h.files.openFile(C, { keep: true }); await tick();
  assert.deepEqual(failures(h), [`${C}:unsupported`]);
});

test('an older backend without document metadata keeps opening every displayed file', async () => {
  const h = await harness({ extensions: null });
  h.files.openFile(C, { keep: true }); await tick();
  assert.equal(h.backend.calls.some(c => c[0] === 'open' && c[1] === C), true);
});

test('closing tabs or the dialog during a resync still closes the backend lifecycles', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  h.files.openFile(OTHER, { keep: true }); await tick();
  const refresh = h.checks.refresh();
  h.files.closeTab(OTHER);
  h.files.close();
  await refresh; await tick();
  assert.equal(h.backend.open.size, 0, 'paused bridge leaked backend documents');
  await h.files.show('/p'); await tick();
  assert.deepEqual(failures(h), []);
  h.consistent();
});

test('a connection interruption never re-adopts the old attachment; reattach reopens cleanly', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  const opens = () => h.backend.calls.filter(c => c[0] === 'open').length;
  h.bridge.interrupted();
  h.bridge.sync(); await tick(); // same token still reported: must stay inert
  assert.equal(opens(), 1);
  assert.equal(h.bridge.basis(MAIN), undefined);
  h.backend.open.clear(); // detach released the old namespace
  await h.checks.attach('/p', { phase: 'connected', generation: 2, hostId: 'h', capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } }); await tick();
  assert.equal(opens(), 2);
  assert.deepEqual(failures(h), []);
  h.consistent();
});

test('a save in flight across a resync sends no didSave for an unknown window, then resumes typing', async () => {
  const h = await harness();
  h.files.openFile(MAIN, { keep: true }); await tick();
  h.files.startEditing(MAIN); h.files.updateDraft(MAIN, 'package main\n// v2\n');
  let release; globalThis.__syncRecoveryFixture.api.saveFile = (_d, r) => new Promise(res => { release = () => res({ status: 'saved', content: content(r.path, r.text, 'r2') }); });
  const saving = h.files.saveEdited(MAIN); await tick();
  const refresh = h.checks.refresh();
  release(); await saving; await refresh; await tick();
  h.files.updateDraft(MAIN, 'package main\n// v3\n'); await h.flush();
  assert.deepEqual(failures(h), []);
  assert.equal(h.backend.open.get(h.bridge.basis(MAIN).documentId).text, 'package main\n// v3\n');
  h.consistent();
});
