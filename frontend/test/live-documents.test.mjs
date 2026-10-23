import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule } from './helpers.mjs';
import { LiveDocumentBridge } from '../src/lib/liveDocuments.ts';
import { liveDocumentEligibility } from '../src/lib/codeDiagnostics.ts';
import { MockCodeDiagnostics } from '../src/lib/mock/codeDiagnostics.ts';
import { readFile } from 'node:fs/promises';

const url = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const lib = (rel) => JSON.stringify(new URL(rel, url).href);
const { FilesStore } = await loadServerModule(url, [
  ["import { api } from '../api';", fixtureStore('api', '__liveFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__liveFixture')],
  ["'../manage'", lib('../manage.ts')],
  ["'../link'", lib('../link.ts')],
  ["'../files'", lib('../files.ts')],
  ["'../fileEditing'", lib('../fileEditing.ts')],
]);
const content = (path, text, revision = 'r1', edit = {}) => ({ path, text, revision, size: text.length, edit: { eligible: true, newline: 'lf', ...edit } });
const tick = async (n = 6) => { for (let i = 0; i < n; i++) await new Promise((r) => setImmediate(r)); };
const ctx = { connectionId: 'c', attachmentId: 'a', generation: 'g' };

function fakeTimers() {
  let now = 0, next = 1; const items = new Map();
  return {
    set: (fn, ms) => { const id = next++; items.set(id, { at: now + ms, fn }); return id; },
    clear: (id) => { items.delete(id); },
    async advance(ms) {
      const end = now + ms;
      for (;;) {
        const due = [...items].filter(([, t]) => t.at <= end).sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        items.delete(due[0]); now = due[1].at; due[1].fn(); await tick();
      }
      now = end; await tick();
    },
    get pending() { return items.size; },
  };
}

async function setup({ files: seed = { 'a.txt': content('a.txt', 'one\n'), 'b.txt': content('b.txt', 'bee\n') }, live = true, apiOverrides = {} } = {}) {
  const calls = [];
  const rec = (name, fn) => async (...args) => { calls.push([name, ...args.slice(1, -1).map((a, i) => a)]); return fn ? fn(...args) : { context: ctx, documentId: args[1] }; };
  const liveApi = {
    openCodeDiagnosticDocument: async (c, path, id, version, text, revision) => { calls.push(['open', path, version, text, revision]); return apiOverrides.open?.() ?? { context: c, documentId: id, version }; },
    changeCodeDiagnosticDocument: async (c, id, version, text) => { calls.push(['change', version, text]); await apiOverrides.change?.(); return { context: c, documentId: id, version }; },
    saveCodeDiagnosticDocument: async (c, id, version, revision) => { calls.push(['save', version, revision]); return { context: c, documentId: id }; },
    closeCodeDiagnosticDocument: async (c, id) => { calls.push(['close', id]); return { context: c, documentId: id }; },
  };
  const fixture = globalThis.__liveFixture = {
    link: { dataHost: 'h', blockedReason: null },
    api: {
      listFiles: async (dir) => ({ root: dir, files: Object.keys(seed) }),
      readFile: async (_d, path) => seed[path] ?? { ...seed['a.txt'], path },
      saveFile: async (_d, req) => ({ status: 'saved', content: content(req.path, req.text, 'r2') }),
      renameFile: async () => {}, deleteFile: async () => {},
    },
  };
  const store = new FilesStore();
  const att = live ? { context: ctx, host: 'h', directory: '/p', token: '1:g' } : undefined;
  const diag = { liveAttachment: att, set(a) { this.liveAttachment = a; bridge.sync(); } };
  const timers = fakeTimers();
  let n = 0;
  const bridge = new LiveDocumentBridge({ api: liveApi, store: diag, source: () => ({ dir: store.dir, documents: store.displayedDocuments() }), timers, newId: () => `doc${++n}` });
  store.observe(bridge);
  await store.show('/p');
  return { store, fixture, bridge, diag, timers, calls, seed };
}
const names = (calls) => calls.map((c) => c[0]);

test('inert without a live-capable attachment: no calls at all', async () => {
  const s = await setup({ live: false });
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'x'); await s.timers.advance(1000);
  await s.store.saveEdited('a.txt'); s.store.closeTab('a.txt'); await tick();
  assert.deepEqual(s.calls, []);
  assert.equal(s.timers.pending, 0);
  assert.equal(s.bridge.basis('a.txt'), undefined);
});

test('read-only open sends displayed text once; edit toggle and cached remount do not duplicate', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  assert.deepEqual(s.calls, [['open', 'a.txt', 1, 'one\n', 'r1']]);
  assert.deepEqual(s.bridge.basis('a.txt'), { documentId: 'doc1', version: 1, synced: true });
  s.store.startEditing('a.txt'); await tick();
  s.store.stopEditing(s.store.activeTab); await tick();
  s.store.openFile('a.txt'); s.store.selectTab('a.txt'); await tick();
  assert.deepEqual(names(s.calls), ['open']);
});

test('fast typing is debounced 300 ms into one monotonic change; old ranges invalid immediately', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt');
  s.store.updateDraft('a.txt', 'a'); assert.equal(s.bridge.basis('a.txt').synced, false);
  await s.timers.advance(200); s.store.updateDraft('a.txt', 'ab'); await s.timers.advance(299);
  assert.deepEqual(names(s.calls), ['open']);
  await s.timers.advance(1);
  assert.deepEqual(s.calls.slice(1), [['change', 2, 'ab']]);
  assert.deepEqual(s.bridge.basis('a.txt'), { documentId: 'doc1', version: 2, synced: true });
  s.store.updateDraft('a.txt', 'abc'); await s.timers.advance(300);
  assert.deepEqual(s.calls.slice(2), [['change', 3, 'abc']]);
  // Editor cache versions (edit.version) never leak into protocol versions.
  assert.notEqual(s.store.activeTab.edit.version, 3);
});

test('save flushes the exact written text before didSave; later typing waits and is not claimed saved', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'v2');
  const saved = s.store.saveEdited('a.txt');
  s.store.updateDraft('a.txt', 'v3'); // typed while saving
  await saved; await tick();
  assert.deepEqual(s.calls.slice(1), [['change', 2, 'v2'], ['save', 2, 'r2']]);
  assert.equal(s.store.activeTab.edit.draft, 'v3');
  await s.timers.advance(300);
  assert.deepEqual(s.calls.slice(3), [['change', 3, 'v3']]);
});

test('failed, conflicting and uncertain saves send no didSave', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'x');
  s.fixture.api.saveFile = async () => ({ status: 'conflict', observedRevision: 'r9' });
  assert.equal(await s.store.saveEdited('a.txt'), false); await tick();
  s.fixture.api.saveFile = async () => { throw new Error('boom'); };
  s.store.activeTab.edit.conflict = null;
  assert.equal(await s.store.saveEdited('a.txt'), false); await tick();
  s.fixture.link.dataHost = 'other';
  assert.ok(!names(s.calls).includes('save'));
  assert.deepEqual(s.calls.slice(1), [['change', 2, 'x']]);
  assert.equal(s.bridge.basis('a.txt').synced, true);
});

test('discard/reload publishes the committed text only after the store commits; failures send nothing', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'draft'); await s.timers.advance(300);
  s.fixture.api.readFile = async () => { throw new Error('offline'); };
  assert.equal(await s.store.discardEdited('a.txt'), false); await tick();
  assert.deepEqual(names(s.calls), ['open', 'change']);
  assert.equal(s.store.activeTab.edit.draft, 'draft');
  s.fixture.api.readFile = async () => content('a.txt', 'disk\n', 'r5');
  assert.equal(await s.store.discardEdited('a.txt'), true); await tick();
  assert.deepEqual(s.calls.at(-1), ['change', 3, 'disk\n']);
  assert.equal(s.timers.pending, 0);
});

test('preview replacement and guarded close close the old document; dirty close sends nothing', async () => {
  const s = await setup();
  s.store.openFile('a.txt'); await tick();
  s.store.openFile('b.txt'); await tick();
  assert.deepEqual(s.calls.map((c) => c[0] + ':' + (c[1] ?? '')), ['open:a.txt', 'close:doc1', 'open:b.txt']);
  s.store.startEditing('b.txt'); s.store.updateDraft('b.txt', 'dirty');
  assert.equal(s.store.closeTab('b.txt'), false); await tick();
  assert.ok(!names(s.calls).slice(3).includes('close'));
});

test('hidden tabs and dialog close: no duplicate sessions; cleared forgets and closes', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.close(); await tick();
  assert.deepEqual(names(s.calls), ['open', 'close']);
  await s.store.show('/p'); await tick();
  assert.deepEqual(names(s.calls), ['open', 'close', 'open']);
  assert.equal(s.calls[2][1], 'a.txt');
  assert.notEqual(s.bridge.basis('a.txt').documentId, 'doc1');
});

test('confirmed rename closes old URI and opens a fresh lifecycle; uncertain rename/delete stay uncertain', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  await s.store.rename('a.txt', 'c.txt'); await tick();
  assert.deepEqual(s.calls.map((c) => c[0] + ':' + (c[1] ?? '')), ['open:a.txt', 'close:doc1', 'open:c.txt']);
  assert.equal(s.bridge.basis('c.txt').documentId, 'doc2');
  s.fixture.api.deleteFile = async () => { throw Object.assign(new Error('x'), { stale: true }); };
  s.fixture.api.deleteFile = async () => { throw new (await import('../src/lib/link.ts')).StaleReplyError(); };
  await assert.rejects(s.store.delete('c.txt'));
  await tick();
  assert.equal(s.bridge.basis('c.txt'), undefined);
  assert.equal(names(s.calls).at(-1), 'close');
});

test('confirmed delete closes the document', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  await s.store.delete('a.txt'); await tick();
  assert.deepEqual(names(s.calls), ['open', 'close']);
  assert.equal(s.bridge.basis('a.txt'), undefined);
});

test('attachment replacement/revoke/host switch cancels timers and sends nothing to the old route', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'typed');
  assert.equal(s.timers.pending, 1);
  s.diag.set({ ...s.diag.liveAttachment, token: '2:g2' }); // replaced attachment
  assert.equal(s.timers.pending, 0);
  await s.timers.advance(1000);
  assert.deepEqual(names(s.calls).slice(0), ['open', 'open']);
  assert.equal(s.calls[1][2], 'typed'.length ? s.calls[1][2] : '');
  assert.equal(s.calls[1][3], 'typed'); // reopened from the files store's displayed draft
  s.store.updateDraft('a.txt', 'more');
  s.diag.set(undefined); // revoked / disconnected
  assert.equal(s.timers.pending, 0);
  await s.timers.advance(1000);
  assert.equal(s.calls.length, 2);
  s.store.updateDraft('a.txt', 'again'); await s.timers.advance(1000);
  assert.equal(s.calls.length, 2);
  assert.equal(s.store.activeTab.edit.draft, 'again'); // drafts untouched
  s.diag.set({ context: ctx, host: 'h', directory: '/p', token: '3:g3' }); await tick();
  assert.equal(s.calls.at(-1)[3], 'again');
});

test('connection change interrupts immediately without closing on the old route', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'x');
  s.diag.liveAttachment = s.diag.liveAttachment;
  await s.store.connectionChanged('h'); await tick();
  assert.equal(s.timers.pending, 0);
  await s.timers.advance(1000);
  assert.ok(!names(s.calls).includes('change'));
});

test('late attachment: documents already displayed are opened on attach', async () => {
  const s = await setup({ live: false });
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'draft');
  assert.deepEqual(s.calls, []);
  s.diag.set({ context: ctx, host: 'h', directory: '/p', token: '1:g' }); await tick();
  assert.deepEqual(s.calls, [['open', 'a.txt', 1, 'draft', 'r1']]);
});

test('Unicode and CRLF text is sent exactly; invalid text surfaces a failure without a call', async () => {
  const s = await setup({ files: { 'a.txt': content('a.txt', '\uFEFFa\r\n😀\r\n', 'r1', { bom: true, newline: 'crlf' }) } });
  s.store.openFile('a.txt', { keep: true }); await tick();
  assert.equal(s.calls[0][3], '\uFEFFa\r\n😀\r\n'); // exactly the displayed viewer text
  s.store.startEditing('a.txt'); await tick();
  assert.deepEqual(s.calls[1], ['change', 2, 'a\n😀\n']); // editor text: LF, no BOM
  s.store.updateDraft('a.txt', 'a\n\ud800'); await s.timers.advance(300);
  assert.equal(s.calls.length, 2);
  assert.equal(s.bridge.failure('a.txt'), 'invalidArgument');
  assert.equal(s.bridge.basis('a.txt'), undefined);
});

test('a failed change recovers with a fresh documentId, bounded', async () => {
  let fail = true;
  const s = await setup({ apiOverrides: { change: async () => { if (fail) { fail = false; throw { code: 'resyncRequired', message: 'x' }; } } } });
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'x'); await s.timers.advance(300);
  assert.deepEqual(names(s.calls), ['open', 'change', 'close', 'open']);
  assert.equal(s.calls[3][3], 'x');
  assert.equal(s.bridge.basis('a.txt').documentId, 'doc2');
});

test('result eligibility: unversioned/old/other-lifecycle/pending results never draw ranges', () => {
  const range = { start: { line: 0, character: 0 }, end: { line: 0, character: 1 } };
  const d = (basis, extra = {}) => ({ path: 'a', severity: 'error', message: 'm', source: 's', freshness: 'current', range, basis, ...extra });
  const doc = { documentId: 'x', version: 3, synced: true };
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'x', version: 3 }), doc), 'current');
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'x', version: 2 }), doc), 'hidden');
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'x', version: 4 }), doc), 'uncertain');
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'x' }), doc), 'uncertain');
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'y', version: 3 }), doc), 'hidden');
  assert.equal(liveDocumentEligibility(d({ kind: 'saved', revision: 'r' }), doc), 'hidden');
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'x', version: 3 }), { ...doc, synced: false }), 'uncertain');
  assert.equal(liveDocumentEligibility(d({ kind: 'document', documentId: 'x', version: 3 }, { freshness: 'uncertain' }), doc), 'uncertain');
});

test('mock bindings enforce lifecycle and stay unsupported unless enabled; no body logging in source', async () => {
  const mock = new MockCodeDiagnostics(() => {});
  const a = await mock.attachCodeDiagnostics('/p', 1, 'mock:local');
  await assert.rejects(mock.openCodeDiagnosticDocument(a.context, 'a', 'd', 1, 't', undefined, 'mock:local'), (e) => e.code === 'unsupported');
  for (const f of ['liveDocuments.ts', 'liveDocumentsWiring.ts']) {
    const src = await readFile(new URL(`../src/lib/${f}`, import.meta.url), 'utf8');
    assert.doesNotMatch(src, /console\.|localStorage|sessionStorage/);
  }
  const bridge = await readFile(new URL('../src/lib/liveDocuments.ts', import.meta.url), 'utf8');
  assert.doesNotMatch(bridge, /import\(|codemirror|fileEditor/i);
});

test('refresh compares saved revisions, preserving dirty/conflicted/uncertain drafts and undo versions', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'draft');
  const tab = s.store.activeTab, edit = tab.edit, version = edit.version;
  const uncertain = edit.uncertain = { text: 'attempt', expectedRevision: 'r1', format: tab.content.edit };
  s.seed['a.txt'] = content('a.txt', 'external', 'r9');
  await s.store.refresh(); await tick();
  assert.equal(tab.content.revision, 'r9');
  assert.equal(edit.draft, 'draft'); assert.equal(edit.baseline, 'one\n');
  assert.equal(edit.revision, 'r1'); assert.equal(edit.version, version);
  assert.equal(edit.uncertain, uncertain); assert.equal(edit.conflict.observedRevision, 'r9');
  assert.equal(s.calls.some(c => c[0] === 'save'), false);
  await s.timers.advance(300);
  assert.equal(s.calls.filter(c => c[0] === 'change').at(-1)[2], 'draft');
});

test('reopen rechecks saved context without replacing an editor or its undo version', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick();
  s.store.startEditing('a.txt');
  const tab = s.store.activeTab, version = tab.edit.version;
  s.store.close();
  s.seed['a.txt'] = content('a.txt', 'outside', 'r3');
  await s.store.show('/p'); await tick();
  assert.equal(s.store.activeTab.edit.draft, 'one\n');
  assert.equal(s.store.activeTab.edit.version, version);
  assert.equal(s.store.activeTab.content.revision, 'r3');
  assert.equal(s.store.activeTab.edit.conflict.observedRevision, 'r3');
});

test('typing during reconciliation retains the newest draft; late old-host read cannot commit', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick(); s.store.startEditing('a.txt');
  let resolve;
  s.fixture.api.readFile = () => new Promise(r => { resolve = r; });
  const pending = s.store.reconcileDisplayed();
  s.store.updateDraft('a.txt', 'newest');
  resolve(content('a.txt', 'disk', 'r4')); await pending;
  assert.equal(s.store.activeTab.edit.draft, 'newest');
  assert.equal(s.store.activeTab.content.revision, 'r4');
  const late = s.store.reconcileDisplayed();
  s.fixture.link.dataHost = 'other';
  resolve(content('a.txt', 'wrong host', 'r5')); await late;
  assert.equal(s.store.activeTab.content.revision, 'r4');
});

test('overlapping saved reads commit only the latest revision, and reload wins over a pending reconciliation', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick(); s.store.startEditing('a.txt');
  const replies = [];
  s.fixture.api.readFile = () => new Promise(r => replies.push(r));
  const first = s.store.reconcileDisplayed(), second = s.store.reconcileDisplayed();
  replies[1](content('a.txt', 'new disk', 'r3')); await second;
  replies[0](content('a.txt', 'old disk', 'r2')); await first;
  assert.equal(s.store.activeTab.content.revision, 'r3');
  const pending = s.store.reconcileDisplayed();
  const reload = s.store.discardEdited('a.txt');
  replies[3](content('a.txt', 'reload', 'r4')); await reload;
  replies[2](content('a.txt', 'stale', 'r3')); await pending;
  assert.equal(s.store.activeTab.edit.draft, 'reload');
  assert.equal(s.store.activeTab.content.revision, 'r4');
});

test('R10 absence retains dirty/uncertain text; observed same-content recreation starts fresh', async () => {
  const s = await setup();
  s.store.openFile('a.txt', { keep: true }); await tick(); s.store.startEditing('a.txt');
  s.store.updateDraft('a.txt', 'draft');
  const tab = s.store.activeTab, edit = tab.edit, version = edit.version;
  const uncertain = edit.uncertain = { text: 'attempt', expectedRevision: 'r1', format: tab.content.edit };
  let state = 'absent'; const clears = [];
  s.fixture.api.probeFiles = async () => ({ observations: [{ path: 'a.txt', state, revision: 'r1' }] });
  s.store.setPathInvalidator(async (dir, path) => clears.push([dir, path]));
  await s.store.reconcileDisplayed(); await tick();
  assert.equal(tab.diskAbsent, true); assert.equal(s.bridge.basis('a.txt'), undefined);
  s.store.updateDraft('a.txt', 'newest'); await s.timers.advance(400);
  assert.equal(s.calls.filter(c => c[0] === 'open').length, 1);
  assert.deepEqual(clears, [['/p', 'a.txt']]);
  state = 'unknown'; await s.store.reconcileDisplayed(); assert.equal(tab.diskAbsent, true);
  state = 'present'; await s.store.reconcileDisplayed(); await tick();
  assert.equal(s.bridge.basis('a.txt').documentId, 'doc2');
  assert.equal(edit.draft, 'newest'); assert.equal(edit.version, version);
  assert.equal(edit.uncertain, uncertain); assert.equal(edit.revision, 'r1');
  assert.equal(s.calls.some(c => c[0] === 'save'), false);
});

test('R10 overlapping probes, host changes and late read errors never guess deletion', async () => {
  const s = await setup(); s.store.openFile('a.txt', { keep: true }); await tick();
  const tab = s.store.activeTab, replies = [];
  s.fixture.api.probeFiles = () => new Promise(r => replies.push(r));
  const first = s.store.reconcileDisplayed(), second = s.store.reconcileDisplayed();
  replies[1]({ observations: [{ path: 'a.txt', state: 'present', revision: 'r1' }] }); await second;
  replies[0]({ observations: [{ path: 'a.txt', state: 'absent' }] }); await first;
  assert.notEqual(tab.diskAbsent, true);
  const late = s.store.reconcileDisplayed(); s.fixture.link.dataHost = 'other';
  replies[2]({ observations: [{ path: 'a.txt', state: 'absent' }] }); await late;
  assert.notEqual(tab.diskAbsent, true); assert.equal(tab.content.text, 'one\n');
  s.fixture.link.dataHost = 'h'; delete s.fixture.api.probeFiles;
  let reject; s.fixture.api.readFile = () => new Promise((_r, j) => { reject = j; });
  const old = s.store.reconcileDisplayed(); s.store.startEditing('a.txt'); s.store.updateDraft('a.txt', 'keep');
  reject(new Error('not found')); await old;
  assert.equal(tab.edit.draft, 'keep'); assert.equal(tab.content.text, 'one\n');
});

test('window focus re-reads open files only during live checks, one read at a time', async () => {
  const { refreshOnFocus } = await loadServerModule(new URL('../src/lib/liveDocumentsWiring.ts', import.meta.url), [
    ["import { api } from './api';", 'const api = {};'],
    ["import { LiveDocumentBridge } from './liveDocuments';", 'const LiveDocumentBridge = class {};'],
    ["import { files } from './stores/files.svelte';", fixtureStore('files', '__focusFixture')],
    ["import { codeDiagnostics } from './stores/codeDiagnostics.svelte';", fixtureStore('codeDiagnostics', '__focusFixture')],
  ]);
  let reads = 0, finish;
  const codeDiagnostics = { liveAttachment: undefined, pending: undefined };
  globalThis.__focusFixture = { codeDiagnostics, files: { reconcileDisplayed: () => { reads++; return new Promise(r => { finish = r; }); } } };
  refreshOnFocus();
  assert.equal(reads, 0, 'no live attachment');
  codeDiagnostics.liveAttachment = {}; codeDiagnostics.pending = 'approve';
  refreshOnFocus();
  assert.equal(reads, 0, 'request pending');
  codeDiagnostics.pending = undefined;
  refreshOnFocus(); refreshOnFocus();
  assert.equal(reads, 1, 'one read in flight');
  finish(); await new Promise(r => setTimeout(r));
  refreshOnFocus();
  assert.equal(reads, 2);
});
