import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule } from './helpers.mjs';
import { createAPI } from '../src/lib/api.ts';
import { LiveDocumentBridge } from '../src/lib/liveDocuments.ts';
import { CODE_DIAGNOSTICS_EVENT, CodeDiagnosticsError, codeDiagnosticsError } from '../src/lib/codeDiagnostics.ts';
import { externalLabel, externalReadFailure, navigationBlocker, navigationFailure } from '../src/lib/codeNavigation.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')], ["'../codeNavigation'", lib('codeNavigation')],
]);
const filesUrl = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const { FilesStore } = await loadServerModule(filesUrl, [
  ["import { api } from '../api';", fixtureStore('api', '__navFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__navFixture')],
  ["'../manage'", lib('manage')], ["'../link'", lib('link')], ["'../files'", lib('files')], ["'../fileEditing'", lib('fileEditing')],
]);

const context = { connectionId: 'connection', attachmentId: 'attachment', generation: 'generation' };
const gopls = { providerId: 'gopls', availability: 'available', phase: 'idle', coverage: { kind: 'openedDocuments', roots: [''], partial: true, externalChanges: 'unwatched' }, documentExtensions: ['.go'], freshness: 'uncertain', truncated: false };
const at = { line: 2, character: 5 };

test('the Wails binding gets the exact arguments; an old binding is unsupported', async () => {
  const previous = globalThis.window;
  try {
    const binding = {};
    globalThis.window = { go: { app: { App: binding } } };
    const api = createAPI();
    await assert.rejects(api.navigateCode(context, 'd1', 3, 'definition', at, 'host'), e => e.code === 'unsupported');
    let args;
    binding.NavigateCode = async (...a) => { args = a; return { op: 'definition', documentId: 'd1', version: 3, locations: [] }; };
    await api.navigateCode(context, 'd1', 3, 'definition', at, 'host');
    assert.deepEqual(args, [context, 'd1', 3, 'definition', at, 'host']);
    for (const code of ['notRunning', 'staleVersion', 'timeout']) {
      binding.NavigateCode = async () => { throw `${code}: something`; };
      await assert.rejects(api.navigateCode(context, 'd1', 3, 'hover', at, 'host'), e => e.code === code);
    }
  } finally { globalThis.window = previous; }
});

test('each reason navigation is unavailable says what is missing', () => {
  const ready = { status: 'ready', live: true, navigation: true, approved: true, servers: [gopls], basis: { documentId: 'd1', version: 1, synced: true } };
  assert.equal(navigationBlocker(ready), undefined);
  const cases = [
    [{ status: 'unsupported' }, /not available for this host/],
    [{ status: 'disconnected' }, /Reconnect/],
    [{ status: 'attaching' }, /still starting/],
    [{ live: false }, /does not run language servers/],
    [{ navigation: false }, /older and does not offer code navigation/],
    [{ approved: false }, /Approve code checks/],
    [{ approved: false, approvalBlocker: 'Nothing to approve: no supported checker was found on this host (gopls: not installed).' }, /Nothing to approve/],
    [{ servers: [] }, /No language server for this file/],
    [{ servers: [{ ...gopls, availability: 'missingTool' }] }, /gopls is not installed on this host; Code checks shows how to get it/],
    [{ failure: 'Document must be reopened' }, /not synced with the language server: Document must be reopened/],
    [{ servers: [{ ...gopls, phase: 'failed', failure: { code: 'toolFailed', message: 'gopls exited' } }] }, /not running: gopls exited\. Open Code checks to retry/],
    [{ basis: undefined }, /still opening this file/],
  ];
  for (const [change, reason] of cases) assert.match(navigationBlocker({ ...ready, ...change }), reason, JSON.stringify(change));
  // One of two servers running is enough.
  assert.equal(navigationBlocker({ ...ready, servers: [{ ...gopls, providerId: 'ruff', phase: 'failed' }, gopls] }), undefined);
});

test('failed requests read by their code, never as a raw error', () => {
  assert.match(navigationFailure('timeout', 'definition'), /did not answer in 10 seconds/);
  assert.match(navigationFailure('unsupported', 'references'), /does not offer find references/);
  assert.match(navigationFailure('notRunning', 'hover'), /not running/);
  assert.match(navigationFailure('toolFailed', 'hover', 'boom'), /could not answer: boom/);
});

function harness({ capabilities = { codeDiagnostics: true, liveCodeDiagnostics: true, codeNavigation: true }, providers = [gopls] } = {}) {
  const listeners = new Map(), calls = [];
  const snapshot = { contractVersion: 1, context, sequence: 0, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers, resultSets: [] };
  let answer = (c, id, version, op) => ({ op, documentId: id, version, locations: [{ path: 'b.go', range: { start: at, end: at } }] });
  const backend = {
    onEvent(name, fn) { listeners.set(name, fn); return () => listeners.delete(name); },
    async attachCodeDiagnostics() { return { project: { hostId: 'host1', root: '/p' }, context, snapshot }; },
    async detachCodeDiagnostics() { return { context }; },
    async navigateCode(...args) { calls.push(args); return answer(...args); },
  };
  const store = new CodeDiagnosticsStore(backend, () => 0);
  let basis = { documentId: 'd1', version: 4, synced: true };
  store.setLiveBasis(path => (path === 'a.go' ? basis : undefined));
  return {
    store, calls, backend,
    setBasis: b => { basis = b; store.liveChanged(); },
    answer: fn => { answer = fn; },
    attach: () => store.attach('/p', { phase: 'local', generation: 0, hostId: 'host1', capabilities }),
  };
}

test('the store asks about the synced version and returns the answer', async () => {
  const h = harness();
  await h.attach();
  assert.equal(h.store.navigationEnabled, true);
  assert.equal(h.store.navigationBlocker('a.go'), undefined);
  const out = await h.store.navigate('a.go', 'definition', at);
  assert.equal(out.kind, 'result');
  assert.deepEqual(out.result.locations.map(l => l.path), ['b.go']);
  assert.deepEqual(h.calls, [[context, 'd1', 4, 'definition', at, 'host1']]);
});

test('the store waits for typing to reach the server, then asks about that version', async () => {
  const h = harness();
  await h.attach();
  h.setBasis({ documentId: 'd1', version: 4, synced: false });
  const asking = h.store.navigate('a.go', 'hover', at);
  await new Promise(r => setTimeout(r, 120));
  assert.equal(h.calls.length, 0, 'not asked about a version the server does not have');
  h.setBasis({ documentId: 'd1', version: 6, synced: true });
  const out = await asking;
  assert.equal(out.kind, 'result');
  assert.equal(h.calls[0][2], 6);
});

test('an answer for a document that moved on is dropped; failures carry a reason', async () => {
  const h = harness();
  await h.attach();
  h.answer((c, id, version, op) => { h.setBasis({ documentId: 'd1', version: version + 1, synced: false }); return { op, documentId: id, version, locations: [] }; });
  assert.deepEqual(await h.store.navigate('a.go', 'references', at), { kind: 'stale' });

  h.setBasis({ documentId: 'd1', version: 9, synced: true });
  h.answer(() => { throw new CodeDiagnosticsError('staleVersion', 'changed'); });
  assert.deepEqual(await h.store.navigate('a.go', 'definition', at), { kind: 'stale' });
  h.answer(() => { throw 'timeout: The language server did not answer in time'; });
  const out = await h.store.navigate('a.go', 'definition', at);
  assert.equal(out.kind, 'failed');
  assert.equal(out.code, 'timeout');
  assert.match(out.reason, /10 seconds/);
  h.answer((c, id, version) => ({ op: 'hover', documentId: id, version: version + 1, locations: [] }));
  assert.deepEqual(await h.store.navigate('a.go', 'hover', at), { kind: 'stale' }, 'an answer for another version');

  // Detached while the server answered.
  let release;
  h.answer((c, id, version, op) => new Promise(r => { release = () => r({ op, documentId: id, version, locations: [] }); }));
  const asking = h.store.navigate('a.go', 'definition', at);
  await new Promise(r => setImmediate(r));
  await h.store.detach();
  release();
  assert.deepEqual(await asking, { kind: 'stale' });
});

test('the store says why instead of asking', async () => {
  const old = harness({ capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } });
  await old.attach();
  assert.equal(old.store.navigationEnabled, false);
  const out = await old.store.navigate('a.go', 'definition', at);
  assert.equal(out.kind, 'unavailable');
  assert.match(out.reason, /older and does not offer code navigation/);
  assert.equal(old.calls.length, 0);

  const other = harness();
  await other.attach();
  assert.match(other.store.navigationBlocker('a.py'), /No language server for this file/);
  assert.equal(other.store.navigationBlocker('a.go'), undefined, 'a.go can be asked');
});

test('the mock answers from its open documents', async () => {
  const events = [];
  const mock = new MockBackendAPI();
  mock.onEvent(CODE_DIAGNOSTICS_EVENT, e => events.push(e));
  mock.liveDocuments = true;
  const { context: c, snapshot } = await mock.attachCodeDiagnostics('/p', 1, 'mock:local');
  await mock.approveCodeDiagnostics(c, 'session', snapshot.proposal.fingerprint, 'mock:local');
  await mock.openCodeDiagnosticDocument(c, 'a.ts', 'd1', 1, 'function helper() {}\nhelper();\n', undefined, 'mock:local');
  await mock.openCodeDiagnosticDocument(c, 'b.ts', 'd2', 1, 'helper();\n', undefined, 'mock:local');
  const def = await mock.navigateCode(c, 'd2', 1, 'definition', { line: 0, character: 2 }, 'mock:local');
  assert.deepEqual(def.locations, [{ path: 'a.ts', range: { start: { line: 0, character: 9 }, end: { line: 0, character: 15 } } }]);
  const refs = await mock.navigateCode(c, 'd2', 1, 'references', { line: 0, character: 2 }, 'mock:local');
  assert.equal(refs.locations.length, 3);
  const hover = await mock.navigateCode(c, 'd1', 1, 'hover', { line: 1, character: 0 }, 'mock:local');
  assert.match(hover.hover.value, /function helper\(\) \{\}/);
  await assert.rejects(mock.navigateCode(c, 'd1', 2, 'hover', { line: 0, character: 0 }, 'mock:local'), e => codeDiagnosticsError(e).code === 'staleVersion');
});

test('external paths read from the home they share with the project', () => {
  assert.equal(externalLabel('/Users/me/go/pkg/mod/x.go', '/Users/me/p'), '~/go/pkg/mod/x.go');
  assert.equal(externalLabel('/Users/meg/x.go', '/Users/me/p'), '/Users/meg/x.go', 'another home is not shortened');
  assert.equal(externalLabel('/usr/local/go/src/fmt/print.go', '/home/me/p'), '/usr/local/go/src/fmt/print.go');
  assert.equal(externalLabel('c:\\users\\Me\\go\\x.go', 'C:\\Users\\me\\p'), '~\\go\\x.go');
  assert.equal(externalLabel('/Users/me/x.go', null), '/Users/me/x.go');
});

test('an external file that cannot be read says what to do', () => {
  assert.match(externalReadFailure('notFound', 'notFound: This file was not opened by code navigation'), /Go to the definition again/);
  assert.match(externalReadFailure('notFound', 'The file no longer exists'), /no longer exists on the host/);
  assert.match(externalReadFailure('staleContext'), /Go to the definition again/);
  assert.match(externalReadFailure('disconnected'), /Reconnect/);
  assert.match(externalReadFailure('approvalRequired'), /Approve code checks/);
  assert.equal(externalReadFailure('unsupported', 'unsupported: This file holds credentials or agent data and is not shown'), 'This file holds credentials or agent data and is not shown');
  assert.equal(externalReadFailure('toolFailed', 'toolFailed: permission denied'), 'The file could not be read: permission denied');
});

test('the Wails binding reads a navigation file; an old binding is unsupported', async () => {
  const previous = globalThis.window;
  try {
    const binding = {};
    globalThis.window = { go: { app: { App: binding } } };
    const api = createAPI();
    await assert.rejects(api.readNavigationFile(context, '/x.go', 'host'), e => e.code === 'unsupported');
    let args;
    binding.ReadNavigationFile = async (...a) => { args = a; return { path: '/x.go', size: 1, text: 'x' }; };
    assert.equal((await api.readNavigationFile(context, '/x.go', 'host')).text, 'x');
    assert.deepEqual(args, [context, '/x.go', 'host']);
    binding.ReadNavigationFile = async () => { throw 'notFound: This file was not opened by code navigation'; };
    await assert.rejects(api.readNavigationFile(context, '/x.go', 'host'), e => e.code === 'notFound');
  } finally { globalThis.window = previous; }
});

test('the store reads only for its attachment and hands back view-only content', async () => {
  const h = harness();
  const reads = [];
  let reply = (c, path) => ({ path, size: 1, text: 'x', revision: 'r', edit: { eligible: true }, changes: [{ kind: 'added' }] });
  h.backend.readNavigationFile = async (...args) => { reads.push(args); return reply(...args); };
  await assert.rejects(h.store.readNavigationFile('/dep/x.go'), e => e.code === 'staleContext' && /Go to the definition again/.test(e.message));
  await h.attach();
  const out = await h.store.readNavigationFile('/dep/x.go');
  assert.deepEqual(reads, [[context, '/dep/x.go', 'host1']]);
  assert.equal(out.text, 'x');
  assert.equal(out.edit, undefined);
  assert.equal(out.changes, undefined);

  reply = () => { throw 'notFound: This file was not opened by code navigation'; };
  await assert.rejects(h.store.readNavigationFile('/dep/x.go'), e => e.code === 'notFound' && /Go to the definition again/.test(e.message));
  reply = () => ({ path: '/dep/other.go', size: 1, text: 'x' });
  await assert.rejects(h.store.readNavigationFile('/dep/x.go'), e => e.code === 'staleContext', 'an answer for another path');

  let release;
  reply = (c, path) => new Promise(r => { release = () => r({ path, size: 1, text: 'x' }); });
  const reading = h.store.readNavigationFile('/dep/x.go');
  await new Promise(r => setImmediate(r));
  await h.store.detach();
  release();
  await assert.rejects(reading, e => e.code === 'staleContext', 'detached while reading');

  const old = harness({ capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } });
  old.backend.readNavigationFile = async () => { throw new Error('not asked'); };
  await old.attach();
  await assert.rejects(old.store.readNavigationFile('/dep/x.go'), e => e.code === 'unsupported' && /cannot be shown for this host/.test(e.message));
});

test('the mock grants only the external files its answers name', async () => {
  const mock = new MockBackendAPI();
  mock.liveDocuments = true;
  const { context: c, snapshot } = await mock.attachCodeDiagnostics('/p', 1, 'mock:local');
  await mock.approveCodeDiagnostics(c, 'session', snapshot.proposal.fingerprint, 'mock:local');
  await mock.openCodeDiagnosticDocument(c, 'main.go', 'd1', 1, 'fmt.Println()\n', undefined, 'mock:local');
  const external = '/usr/local/go/src/fmt/print.go';
  await assert.rejects(mock.readNavigationFile(c, external, 'mock:local'), e => codeDiagnosticsError(e).code === 'notFound');
  const def = await mock.navigateCode(c, 'd1', 1, 'definition', { line: 0, character: 6 }, 'mock:local');
  assert.deepEqual(def.locations.map(l => [l.path, l.external]), [[external, true]]);
  const file = await mock.readNavigationFile(c, external, 'mock:local');
  assert.match(file.text, /func Println/);
  assert.equal(file.edit, undefined);
  await mock.detachCodeDiagnostics(c, 'mock:local');
  await assert.rejects(mock.readNavigationFile(c, external, 'mock:local'));
});

function filesHarness() {
  const observed = [], reads = [];
  globalThis.__navFixture = {
    link: { dataHost: 'h', blockedReason: null },
    api: {
      listFiles: async dir => ({ root: dir, files: ['a.go'] }),
      readFile: async (_d, path) => ({ path, size: 1, text: 'a', revision: 'r', edit: { eligible: true, newline: 'none' } }),
    },
  };
  const store = new FilesStore();
  const record = name => (...args) => observed.push([name, ...args.slice(1, 2)]);
  store.observe({ opened: record('opened'), replaced: record('replaced'), changed: record('changed'), beforeSave() {}, saved() {}, saveFailed() {}, removed: record('removed'), renamed() {}, diskState: record('diskState') });
  let answer = async path => ({ path, size: 3, text: 'ext', revision: 'x' });
  store.setExternalReader(path => { reads.push(path); return answer(path); });
  return { store, observed, reads, answer: fn => { answer = fn; } };
}
const settle = async () => { for (let i = 0; i < 6; i++) await new Promise(r => setImmediate(r)); };

test('an external tab is read through navigation and never becomes a live or editable document', async () => {
  const h = filesHarness();
  await h.store.show('/p');
  const ext = '/usr/local/go/src/fmt/print.go';
  h.store.openExternal(ext);
  await settle();
  assert.deepEqual(h.reads, [ext]);
  const tab = h.store.tabs.find(t => t.path === ext);
  assert.equal(tab.external, true);
  assert.equal(tab.preview, true);
  assert.equal(h.store.content.text, 'ext');
  assert.deepEqual(h.store.displayedDocuments().map(d => d.path), [], 'not offered to the language server');
  assert.equal(h.store.startEditing(ext), false);
  await h.store.reconcileDisplayed();
  h.store.closeTab(ext);
  assert.deepEqual(h.observed, [], 'the live bridge never hears of it');

  // A project tab stays a project tab; the same path is not opened twice.
  h.store.openFile('a.go', { keep: true });
  await settle();
  h.store.openExternal('a.go');
  assert.equal(h.store.tabs.find(t => t.path === 'a.go').external, undefined);
  assert.ok(h.observed.some(o => o[0] === 'opened' && o[1] === 'a.go'), 'project files still reach the bridge');
  assert.ok(!h.observed.some(o => o[1] === ext));

  h.answer(async () => { throw new CodeDiagnosticsError('notFound', externalReadFailure('notFound')); });
  h.store.openExternal('/dep/gone.go', { keep: true });
  await settle();
  assert.match(h.store.fileError, /Go to the definition again/);
});


test('draft asks use the exact synchronized version; changed drafts cancel before sending', async () => {
  const h = harness();
  await h.attach();
  let text = 'name', shown = text;
  const current = () => text === shown;
  h.setBasis({ documentId: 'd1', version: 4, synced: false });
  const asking = h.store.navigate('a.go', 'definition', at, { current, wait: 1000 });
  assert.equal(h.calls.length, 0);
  h.setBasis({ documentId: 'd1', version: 5, synced: true });
  assert.equal((await asking).kind, 'result');
  assert.equal(h.calls[0][2], 5);
  assert.deepEqual(h.calls[0][4], at, 'position belongs to the exact unchanged draft');

  h.calls.length = 0;
  h.setBasis({ documentId: 'd1', version: 5, synced: false });
  const canceled = h.store.navigate('a.go', 'references', at, { current, wait: 1000 });
  shown = 'inserted name';
  h.setBasis({ documentId: 'd1', version: 6, synced: true });
  assert.deepEqual(await canceled, { kind: 'stale' });
  assert.equal(h.calls.length, 0, 'must not ask the old position in a newer draft');
});

test('typing while the answer is in flight makes it stale even before a new version is assigned', async () => {
  const h = harness();
  await h.attach();
  h.answer((c, id, version, op) => {
    h.setBasis({ documentId: id, version, synced: false });
    return { op, documentId: id, version, locations: [] };
  });
  assert.deepEqual(await h.store.navigate('a.go', 'definition', at), { kind: 'stale' });
  h.setBasis({ documentId: 'd1', version: 4, synced: true });
  let current = true;
  h.answer((c, id, version, op) => {
    current = false;
    return { op, documentId: id, version, locations: [] };
  });
  assert.deepEqual(await h.store.navigate('a.go', 'hover', at, { current: () => current }), { kind: 'stale' });
});

test('a pending draft times out after one second without asking the stale version', async t => {
  const h = harness();
  await h.attach();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  h.setBasis({ documentId: 'd1', version: 4, synced: false });
  let settled = false;
  const asking = h.store.navigate('a.go', 'hover', at, { current: () => true, wait: 1000 }).then(out => { settled = true; return out; });
  for (let n = 0; n < 19; n++) { t.mock.timers.tick(50); await Promise.resolve(); }
  assert.equal(settled, false);
  t.mock.timers.tick(50);
  const out = await asking;
  assert.equal(out.kind, 'unavailable');
  assert.match(out.reason, /not caught up/);
  assert.equal(h.calls.length, 0);
});


test('real live bridge: editor positions wait for the exact draft to be acknowledged, not the saved text', async () => {
  const h = harness();
  await h.attach();
  let draft = 'package main\nfunc helper() {}\n', sent, release;
  const timers = new Map();
  const bridge = new LiveDocumentBridge({
    store: h.store, source: () => ({ dir: '/p', documents: [{ path: 'a.go', text: draft }] }), newId: () => 'draft-document',
    timers: { set(fn) { timers.set(fn, fn); return fn; }, clear(fn) { timers.delete(fn); } },
    api: {
      async openCodeDiagnosticDocument(c, path, documentId, version, text) { sent = { text, version }; return { context: c, documentId, version }; },
      changeCodeDiagnosticDocument(c, documentId, version, text) {
        return new Promise(r => { release = () => { sent = { text, version }; r({ context: c, documentId, version }); }; });
      },
      async closeCodeDiagnosticDocument(c, documentId) { return { context: c, documentId }; },
    },
  });
  h.store.setLiveBasis(path => bridge.basis(path));
  bridge.onChange(() => h.store.liveChanged());
  bridge.sync();
  await new Promise(r => setImmediate(r));
  assert.equal(bridge.basis('a.go').synced, true);
  const newDraft = 'package main\n// inserted 😀\nfunc helper() {}\n';
  draft = newDraft;
  bridge.changed('/p', 'a.go', draft);
  const position = { line: 2, character: 6 };
  h.answer((c, id, version, op, at) => {
    assert.equal(sent.text, newDraft, 'server sees exactly the text the position came from');
    assert.equal(sent.version, version);
    assert.deepEqual(at, position);
    return { op, documentId: id, version, locations: [] };
  });
  const asking = h.store.navigate('a.go', 'definition', position, { current: () => draft === newDraft, wait: 1000 });
  assert.equal(h.calls.length, 0, 'debounced draft does not ask saved text');
  for (const fn of [...timers.keys()]) { timers.delete(fn); fn(); }
  await new Promise(r => setImmediate(r));
  assert.equal(bridge.basis('a.go').synced, false);
  assert.equal(h.calls.length, 0, 'sent but unacknowledged draft is not synced');
  release();
  assert.equal((await asking).kind, 'result');
  assert.equal(h.calls[0][1], 'draft-document');
  assert.equal(h.calls[0][2], 2);
  await h.store.detach();
  bridge.sync();
});
