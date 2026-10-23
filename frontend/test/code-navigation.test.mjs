import test from 'node:test';
import assert from 'node:assert/strict';
import { loadServerModule } from './helpers.mjs';
import { createAPI } from '../src/lib/api.ts';
import { CODE_DIAGNOSTICS_EVENT, CodeDiagnosticsError, codeDiagnosticsError } from '../src/lib/codeDiagnostics.ts';
import { navigationBlocker, navigationFailure } from '../src/lib/codeNavigation.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')], ["'../codeNavigation'", lib('codeNavigation')],
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
    store, calls,
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
