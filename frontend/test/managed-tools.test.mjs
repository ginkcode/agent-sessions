import test from 'node:test';
import assert from 'node:assert/strict';
import { loadServerModule, loadServerComponent, fixtureStore, renderWithFixture } from './helpers.mjs';
import { createAPI } from '../src/lib/api.ts';
const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')],
]);
const controls = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/CodeChecks.svelte', import.meta.url), [["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')]]);
const context = { connectionId: 'c', attachmentId: 'a', generation: 'g' };
const connection = { hostId: 'local:actual', phase: 'local', generation: 1, capabilities: { codeDiagnostics: true } };
const proposal = { hostId: connection.hostId, connectionId: 'consent-owner', tool: 'gopls', version: 'v0.22.0', source: 'pinned source', integrity: 'pinned integrity', destination: '/private/tools/gopls', token: 'once', expires: '2099-01-01T00:00:00Z' };
async function setup(managedTool) {
  const calls = [], listeners = {};
  const backend = {
    onEvent(name, fn) { listeners[name] = fn; return () => delete listeners[name]; },
    async attachCodeDiagnostics() { return { context, project: { hostId: connection.hostId, root: '/project' }, snapshot: { contractVersion: 1, sequence: 0, context, approval: { state: 'required' }, providers: [], resultSets: [] } }; },
    async detachCodeDiagnostics() {},
    ...(managedTool ? { async managedTool(p) { calls.push(p); return managedTool(p); } } : {}),
  };
  const store = new CodeDiagnosticsStore(backend, () => 0);
  await store.attach('/project', connection);
  await Promise.resolve();
  return { store, calls, listeners };
}
const state = (s, extra = {}) => ({ hostId: connection.hostId, state: s, ...extra });
const flush = async () => { for (let i = 0; i < 10; i++) await Promise.resolve(); };
test('real store: open/status only, review disclosure, Not now never installs; fresh Install exactly once', async () => {
  const { store, calls } = await setup(async p => p.action === 'propose' ? state('proposed', { proposal }) : p.action === 'install' ? state('installed') : state('missingTool'));
  assert.deepEqual(calls.map(p => p.action), ['status']);
  await store.managedAction('propose');
  const html = await renderWithFixture(controls, { checks: store });
  for (const text of ['local:actual', 'v0.22.0', 'pinned source', '/private/tools/gopls', 'Go >=1.27', 'network', 'separate', 'Install', 'Not now']) assert.ok(html.includes(text), text);
  await store.managedAction('dismiss');
  assert.equal(calls.filter(p => p.action === 'install').length, 0);
  await store.refreshManaged(); await store.managedAction('propose');
  await Promise.all([store.managedAction('install'), store.managedAction('install')]);
  assert.deepEqual(calls.filter(p => p.action === 'install'), [{ action: 'install', expectedHostId: connection.hostId, token: 'once' }]);
  await store.managedAction('install'); // no replay after completion
  assert.equal(calls.filter(p => p.action === 'install').length, 1);
  await store.hide();
});
test('old backend/no authenticated identity and wrong-host proposal fail closed, errors escaped', async () => {
  const old = await setup();
  assert.match(old.store.managedNotice, /older backend/);
  assert.doesNotMatch(await renderWithFixture(controls, { checks: old.store }), />Install<|>Review gopls installation</);
  await old.store.hide();
  const wrong = await setup(async () => state('proposed', { proposal: { ...proposal, hostId: 'remote:wrong' } }));
  assert.equal(wrong.store.managed, undefined);
  assert.match(wrong.store.managedNotice, /identity mismatch/);
  wrong.listeners['connection:state']({ ...connection, hostId: undefined });
  assert.equal(wrong.store.status, 'disconnected');
  const failed = await setup(async () => { throw new Error('<script>evil</script>'); });
  const html = await renderWithFixture(controls, { checks: failed.store });
  assert.doesNotMatch(html, /<script>/); assert.match(html, /&lt;script>/);
  await failed.store.hide();
});
test('fake timers: bounded polling and explicit cancel; hide stops timers without canceling installation', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let terminal = false;
  const { store, calls } = await setup(async p => p.action === 'cancel' ? (terminal = true, state('canceled', { jobId: 'job' })) : state(terminal ? 'canceled' : 'installing', { jobId: 'job' }));
  t.mock.timers.tick(2000); await flush();
  assert.equal(calls.length, 2);
  await store.managedAction('cancel');
  assert.deepEqual(calls.at(-1), { action: 'cancel', expectedHostId: connection.hostId, jobId: 'job' });
  t.mock.timers.tick(2000); await flush(); assert.equal(calls.length, 3);
  terminal = false; await store.refreshManaged();
  for (let i = 0; i < 301; i++) { t.mock.timers.tick(2000); await flush(); }
  assert.match(store.managedNotice, /polling paused/);
  const count = calls.length;
  t.mock.timers.tick(100000); await flush(); assert.equal(calls.length, count);
  await store.refreshManaged(); await store.hide();
  const hidden = calls.length;
  t.mock.timers.tick(100000); await flush(); assert.equal(calls.length, hidden);
  assert.equal(calls.filter(p => p.action === 'cancel').length, 1);
});
test('late status/installation replies cannot cross host, hide, disconnect or context replacement', async () => {
  for (const end of ['hide', 'disconnect', 'host', 'context']) {
    let resolve;
    const { store, listeners } = await setup(() => new Promise(r => { resolve = r; }));
    if (end === 'host') listeners['connection:state']({ ...connection, hostId: 'other' });
    else if (end === 'context') store.context = { ...context, generation: 'replacement' };
    else await store[end]();
    resolve(state('proposed', { proposal })); await flush();
    assert.equal(store.managed, undefined, end);
    await store.hide();
  }
});
test('lost Install acknowledgement is not replayed; failure requires a fresh proposal; late Install stays hidden', async () => {
  let rejectInstall, resolveInstall;
  const { store, calls } = await setup(async p => {
    if (p.action === 'propose') return state('proposed', { proposal });
    if (p.action === 'install') return new Promise((resolve, reject) => { resolveInstall = resolve; rejectInstall = reject; });
    return state('failed', { message: '<failed build>', jobId: 'job' });
  });
  await store.managedAction('propose');
  const pending = store.managedAction('install');
  rejectInstall(new Error('acknowledgement lost')); await pending;
  await store.managedAction('install');
  assert.equal(calls.filter(p => p.action === 'install').length, 1);
  await store.refreshManaged();
  const html = await renderWithFixture(controls, { checks: store });
  assert.match(html, /&lt;failed build>/); assert.match(html, /Review gopls installation/);
  await store.managedAction('propose');
  const late = store.managedAction('install');
  await store.hide(); resolveInstall(state('installing', { jobId: 'job' })); await late;
  assert.equal(store.managed, undefined);
  assert.equal(calls.filter(p => p.action === 'cancel').length, 0);
});
test('expired proposal and unsupported connection never install or discover setup', async () => {
  const { store, calls } = await setup(async p => p.action === 'propose' ? state('proposed', { proposal: { ...proposal, expires: '2000-01-01T00:00:00Z' } }) : state('missingTool'));
  await store.managedAction('propose'); await store.managedAction('install');
  assert.match(store.managedNotice, /expired/);
  assert.equal(calls.filter(p => p.action === 'install').length, 0);
  for (const c of [{ ...connection, hostId: undefined }, { ...connection, capabilities: {} }]) {
    const n = calls.length; await store.attach('/project', c);
    assert.equal(store.status, 'unsupported'); assert.equal(calls.length, n);
  }
  await store.hide();
});
test('Wails optional ManagedTool maps exact closed request, old App refuses', async () => {
  const previous = globalThis.window;
  try {
    const binding = {}; globalThis.window = { go: { app: { App: binding } } };
    const api = createAPI();
    await assert.rejects(api.managedTool({ action: 'status', expectedHostId: 'h' }), e => e.code === 'unsupported');
    let got;
    binding.ManagedTool = async p => { got = p; return state('unavailable'); };
    const p = { action: 'install', expectedHostId: 'h', token: 'once' };
    await api.managedTool(p); assert.deepEqual(got, p);
  } finally { globalThis.window = previous; }
});
