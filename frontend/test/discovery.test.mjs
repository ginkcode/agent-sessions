import test from 'node:test';
import assert from 'node:assert/strict';
import { loadServerModule, loadServerComponent, fixtureStore, renderWithFixture } from './helpers.mjs';
import { discoveryOutcome, emptyCatalogState } from '../src/lib/discovery.ts';
import { guardEpoch, bumpEpoch, epoch } from '../src/lib/link.ts';

const lib = (name) => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const status = (update = {}) => ({ lifetime: 1, revision: 1, state: 'reconciling', catalogUsable: true,
  complete: false, configuredProviders: 3, detectedProviders: 2, failedProviders: 0, detectionComplete: true, ...update });
const settled = (update = {}) => status({ revision: 2, state: 'settled', complete: true, ...update });
const local = { phase: 'local', generation: 0 };
const remote = { phase: 'connected', host: 'generated-host', generation: 4 };
const id = (route) => ({ host: route.host ?? '', generation: route.phase === 'local' ? 0 : route.generation });
function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}
// Drain promise continuations only, never wall-clock timers.
async function flush() { for (let i = 0; i < 12; i++) await Promise.resolve(); }
const { DiscoveryState } = await loadServerModule(new URL('../src/lib/stores/discovery.svelte.ts', import.meta.url), [
  ["import { api, subscribeConnectionState, subscribeDiscoveryStatus } from '../api';",
    `const api = new Proxy({}, { get: (_, key) => globalThis.__discoveryAPI[key] });
     const subscribeConnectionState = (fn) => { globalThis.__connectionEvent = fn; return () => {}; };
     const subscribeDiscoveryStatus = (fn) => { globalThis.__discoveryEvent = fn; return () => {}; };`],
  ["'../discovery'", lib('discovery')],
]);
async function setup(route = local) {
  const snapshot = deferred();
  let requests = 0;
  globalThis.__discoveryAPI = {
    connectionState: async () => route,
    discoveryStatus: () => {
      assert.equal(typeof globalThis.__discoveryEvent, 'function', 'subscribe before snapshot');
      requests++;
      return snapshot.promise;
    },
  };
  const store = new DiscoveryState();
  let completed = 0;
  store.init(() => completed++);
  await flush();
  return { store, snapshot, requests: () => requests, completed: () => completed,
    event: (s, r = route) => globalThis.__discoveryEvent({ ...s, ...id(r) }) };
}

test('delayed initial snapshot is discovering, then newer event beats older snapshot', async () => {
  const f = await setup();
  assert.equal(f.store.outcome, 'waiting');
  assert.equal(emptyCatalogState(f.store.outcome, null, false), 'discovering');
  f.event(settled());
  f.snapshot.resolve({ available: true, status: status(), ...id(local) });
  await flush();
  assert.equal(f.store.status.revision, 2);
  assert.equal(f.store.outcome, 'settled');
  assert.equal(f.completed(), 1);
  f.event(status({ lifetime: 0, revision: 99 }));
  f.event(settled());
  assert.equal(f.store.status.lifetime, 1);
  assert.equal(f.completed(), 1);
  f.store.destroy();
});

test('snapshot recovers a terminal event published before subscription', async () => {
  const f = await setup();
  f.snapshot.resolve({ available: true, status: settled({ lifetime: 2 }), ...id(local) });
  await flush();
  assert.equal(f.store.status.lifetime, 2);
  assert.equal(f.completed(), 1);
  f.event(status({ lifetime: 3 }));
  assert.equal(f.store.outcome, 'reconciling');
  f.event(settled({ lifetime: 3 }));
  assert.equal(f.completed(), 2);
  f.store.destroy();
});

test('host switch rejects previous replies and other host/generation events', async () => {
  const f = await setup();
  const next = deferred();
  globalThis.__discoveryAPI.discoveryStatus = () => next.promise;
  globalThis.__connectionEvent(remote);
  f.event(settled());
  f.event(settled(), { ...remote, generation: 3 });
  f.snapshot.resolve({ available: true, status: settled({ lifetime: 99 }), ...id(local) });
  await flush();
  assert.equal(f.store.status, null);
  next.resolve({ available: true, status: status(), ...id(remote) });
  await flush();
  assert.equal(f.store.status.lifetime, 1);
  assert.equal(f.store.outcome, 'reconciling');
  f.store.destroy();
});

test('reconnect drops held lifetime and pending snapshots even with same generation', async () => {
  const f = await setup(remote);
  f.event(settled({ lifetime: 8 }));
  globalThis.__connectionEvent({ ...remote, phase: 'reconnecting' });
  assert.equal(f.store.status, null);
  assert.equal(f.store.outcome, 'unavailable');
  f.event(settled({ lifetime: 9 }));
  assert.equal(f.store.status, null);
  const next = deferred();
  globalThis.__discoveryAPI.discoveryStatus = () => next.promise;
  globalThis.__connectionEvent(remote);
  assert.equal(f.store.outcome, 'waiting');
  f.snapshot.resolve({ available: true, status: settled({ lifetime: 8 }), ...id(remote) });
  next.resolve({ available: true, status: status(), ...id(remote) });
  await flush();
  assert.equal(f.store.status.lifetime, 1);
  assert.equal(f.store.outcome, 'reconciling');
  f.store.destroy();
});

test('unavailable old hosts and snapshot errors do not invent completion or keep spinning', async () => {
  for (const error of [false, true]) {
    const f = await setup(remote);
    if (error) f.snapshot.reject(new Error('generated failure'));
    else f.snapshot.resolve({ available: false, ...id(remote) });
    await flush();
    assert.equal(f.store.outcome, 'unavailable');
    assert.equal(f.store.status, null);
    assert.equal(f.completed(), 0);
    f.store.destroy();
  }
});

test('newer events survive unavailable or rejected snapshots; destroyed replies are dropped', async () => {
  for (const error of [false, true]) {
    const f = await setup();
    f.event(status());
    if (error) f.snapshot.reject(new Error('generated failure'));
    else f.snapshot.resolve({ available: false, ...id(local) });
    await flush();
    assert.equal(f.store.outcome, 'reconciling');
    f.store.destroy();
  }
  const f = await setup();
  f.store.destroy();
  f.snapshot.resolve({ available: true, status: settled(), ...id(local) });
  await flush();
  assert.equal(f.store.status, null);
  assert.equal(f.completed(), 0);
});

test('connection event wins over an older connection snapshot', async () => {
  const route = deferred();
  const f = await setup();
  f.store.destroy();
  globalThis.__discoveryAPI.connectionState = () => route.promise;
  f.store.init();
  globalThis.__connectionEvent(remote);
  route.resolve(local);
  await flush();
  f.event(status(), remote);
  assert.equal(f.store.status.lifetime, 1);
  f.store.destroy();
});

// Render both real panes with real presentation helpers and lightweight child
// components. EmptyState remains real, so titles/descriptions are asserted.
const emptyComponent = await loadServerComponent(new URL('../src/lib/components/common/EmptyState.svelte', import.meta.url), []);
globalThis.__discoveryEmptyComponent = emptyComponent.default;
const common = [
  ["import { appState } from '../../stores/appState.svelte';", fixtureStore('appState')],
  ["import { discovery } from '../../stores/discovery.svelte';", fixtureStore('discovery')],
  ["import { manage } from '../../stores/manage.svelte';", fixtureStore('manage')],
  ["'../../discovery'", lib('discovery')],
  ["'../../manage'", lib('manage')],
];
const tree = await loadServerComponent(new URL('../src/lib/components/sidebar/GroupTree.svelte', import.meta.url), [
  ...common,
  ["'../../tree'", lib('tree')],
  ["import GroupNodeItem from './GroupNodeItem.svelte';", "const GroupNodeItem = ($$renderer) => $$renderer.push('<li>generated-group</li>');"],
]);
const list = await loadServerComponent(new URL('../src/lib/components/sessionlist/SessionList.svelte', import.meta.url), [
  ...common,
  ["'svelte';", `${JSON.stringify(import.meta.resolve('svelte'))};`],
  ["import VirtualList from './VirtualList.svelte';", "const VirtualList = ($$renderer) => $$renderer.push('<div>generated-session</div>');"],
  ["import SessionListSkeleton from './SessionListSkeleton.svelte';", "const SessionListSkeleton = ($$renderer) => $$renderer.push('<div>loading-skeleton</div>');"],
  ["import SessionRow from './SessionRow.svelte';", 'const SessionRow = () => {};'],
  ["import EmptyState from '../common/EmptyState.svelte';", 'const EmptyState = globalThis.__discoveryEmptyComponent;'],
  ["import ErrorState from '../common/ErrorState.svelte';", 'const ErrorState = () => {};'],
  ["import Dropdown from '../common/Dropdown.svelte';", 'const Dropdown = () => {};'],
]);
async function panes(s, { unavailable = false, rows = false, filter = {}, age = 'any', reloading = false } = {}) {
  const fixture = {
    appState: { groups: rows ? [{ key: 'generated' }] : [], sessions: rows ? [{ ref: { agent: 'test', id: 'generated' } }] : [],
      filter, selectedSessionRef: null, selectedGroupKey: null, sort: { field: 'updated', desc: true },
      loadingGroups: false, loadingSessions: false, discoveryReloading: reloading },
    discovery: { status: s, outcome: discoveryOutcome(unavailable ? 'unavailable' : s ? 'available' : 'pending', s) },
    manage: { ageFilter: age, getSelectedRefs: () => [], settings: { enabled: false } },
  };
  return [await renderWithFixture(tree, fixture), await renderWithFixture(list, fixture)];
}

test('empty pending panes show discovery, populated cache stays visible with updating indicator', async () => {
  for (const s of [null, status()]) {
    for (const html of await panes(s)) {
      assert.match(html, /Discovering/);
      assert.doesNotMatch(html, /No Agents Detected|No matching sessions/);
    }
  }
  for (const s of [null, status()]) {
    const [t, l] = await panes(s, { rows: true });
    assert.match(t, /generated-group/);
    assert.match(l, /generated-session/);
    for (const html of [t, l]) assert.match(html, /Updating…/);
  }
  for (const html of await panes(settled(), { rows: true })) assert.doesNotMatch(html, /Updating…|Discovery/);
});

test('true empty distinguishes agents with no history from no detected agents', async () => {
  for (const html of await panes(settled())) {
    assert.match(html, /No sessions found/);
    assert.doesNotMatch(html, /No Agents Detected|No matching sessions/);
  }
  const [t, l] = await panes(settled({ detectedProviders: 0 }));
  assert.match(t, /No Agents Detected/);
  assert.match(l, /No supported agents were detected/);
  for (const html of await panes(settled({ detectedProviders: 0 }), { reloading: true })) {
    assert.match(html, /Discovering/);
    assert.doesNotMatch(html, /No Agents Detected/);
  }
});

test('degraded and failed outcomes are explicit, with or without retained rows', async () => {
  for (const state of ['degraded', 'failed']) {
    const s = settled({ state, failedProviders: 1, detectedProviders: 0 });
    for (const html of await panes(s)) {
      assert.match(html, state === 'failed' ? /discovery failed/ : /discovery incomplete/);
      assert.doesNotMatch(html, /No Agents Detected|No matching sessions/);
    }
    const [t, l] = await panes(s, { rows: true });
    assert.match(t, /generated-group/);
    assert.match(l, /generated-session/);
    for (const html of [t, l]) assert.match(html, state === 'failed' ? /Discovery failed/ : /Discovery incomplete/);
  }
});

test('unavailable status preserves legacy empties and ordinary browsing', async () => {
  const [t, l] = await panes(null, { unavailable: true });
  assert.match(t, /No Agents Detected/);
  assert.match(l, /No matching sessions/);
  for (const html of [t, l]) assert.doesNotMatch(html, /Discovering|Updating/);
});

test('usable filtered data keeps no matches distinct from discovery', async () => {
  for (const s of [status(), settled()]) {
    const [t, l] = await panes(s, { filter: { query: 'generated-no-match' } });
    assert.match(t, /No sessions match current filter/);
    assert.match(l, /No matching sessions/);
  }
  for (const html of await panes(status({ catalogUsable: false }), { filter: { query: 'x' } })) {
    assert.match(html, /Discovering/);
  }
});

const { AppState } = await loadServerModule(new URL('../src/lib/stores/appState.svelte.ts', import.meta.url), [
  ...['tree', 'catalog', 'manage', 'link', 'navHistory'].map((name) => [`'../${name}'`, lib(name)]),
  ["import { api, subscribeCatalogChanged } from '../api';",
    `const api = new Proxy({}, { get: (_, key) => globalThis.__catalogAPI[key] });
     const subscribeCatalogChanged = (fn) => { globalThis.__catalogEvent = fn; return () => {}; };`],
]);

test('terminal reload applies final rows and retains selection, history and group sequencing', async () => {
  const ref = { agent: 'test', id: 'generated-selected' };
  const store = new AppState();
  const groups = deferred();
  const calls = [];
  globalThis.__catalogAPI = {
    listGroups: () => { calls.push('groups'); return groups.promise; }, agentCounts: async () => ({}),
    listSessions: async (group) => { calls.push(['sessions', group]); return [{ ref }]; },
  };
  store.selectedGroupKey = 'old-group';
  store.selectedSessionRef = ref;
  store.selectedSessionMeta = { ref, title: 'retained header' };
  const pending = store.reloadAfterDiscovery();
  await flush();
  assert.equal(store.discoveryReloading, true);
  assert.deepEqual(calls, ['groups']);
  groups.resolve([{ key: 'new-group', children: [] }]);
  await pending;
  assert.equal(store.discoveryReloading, false);
  assert.deepEqual(calls, ['groups', ['sessions', '']]);
  assert.equal(store.sessions[0].ref, ref);
  assert.equal(store.selectedSessionRef, ref);
  assert.equal(store.selectedSessionMeta.title, 'retained header');
  assert.equal(store.selectedGroupKey, null);
});

test('late Local catalog replies cannot replace remote rows after a host reset', async () => {
  const pending = { listGroups: deferred(), agentCounts: deferred(), listSessions: deferred() };
  const snapshot = deferred();
  const remoteRef = { agent: 'test', id: 'generated-remote' };
  const remoteGroups = [{ key: 'remote-group', children: [] }];
  const remoteCounts = { test: 7 };
  const remoteRows = [{ ref: remoteRef, title: 'Remote session' }];
  let servingRemote = false;
  const calls = [];
  globalThis.__catalogAPI = guardEpoch({
    ...Object.fromEntries(Object.keys(pending).map((method) => [method, () => {
      calls.push([method, servingRemote]);
      if (!servingRemote) return pending[method].promise;
      return Promise.resolve({ listGroups: remoteGroups, agentCounts: remoteCounts, listSessions: remoteRows }[method]);
    }])),
    connectionState: () => snapshot.promise,
    getSessionMeta: async () => remoteRows[0],
  }, epoch, new Set(['connectionState']));
  const store = new AppState();
  try {
    const initialSnapshot = globalThis.__catalogAPI.connectionState();
    const localGroups = store.loadGroups();
    const localSessions = store.loadSessions();
    assert.deepEqual(calls, [['listGroups', false], ['agentCounts', false], ['listSessions', false]]);

    servingRemote = true;
    bumpEpoch();
    await store.reset('generated-host');
    const assertRemote = () => {
      assert.equal(store.targetHost, 'generated-host');
      assert.deepEqual(store.groups, remoteGroups);
      assert.deepEqual(store.agentCounts, remoteCounts);
      assert.deepEqual(store.sessions, remoteRows);
      assert.deepEqual(store.selectedSessionRef, remoteRef);
      assert.deepEqual(store.selectedSessionMeta, remoteRows[0]);
      assert.equal(store.error, null);
      assert.equal(store.loadingGroups, false);
      assert.equal(store.loadingSessions, false);
    };
    assertRemote();
    pending.listSessions.resolve([{ ref: { agent: 'test', id: 'generated-local' } }]);
    await localSessions;
    assertRemote();
    pending.listGroups.resolve([{ key: 'local-group', children: [] }]);
    pending.agentCounts.resolve({ test: 99 });
    await localGroups;
    assertRemote();

    // The connection snapshot intentionally bypasses epochs. ConnectionStore's
    // event/lifecycle admission, not this API proxy, must reject stale identity.
    snapshot.resolve(local);
    assert.deepEqual(await initialSnapshot, local);
  } finally {
    store.destroy();
    delete globalThis.__catalogAPI;
  }
});

test('catalog events and terminal refresh serialize and retain auto-selection', async () => {
  const ref = { agent: 'test', id: 'generated-auto' };
  const calls = [];
  globalThis.__catalogAPI = {
    listGroups: async () => { calls.push('groups'); return []; }, agentCounts: async () => ({}),
    listSessions: async () => { calls.push('sessions'); return [{ ref }]; },
    getSessionMeta: async () => ({ ref }),
  };
  const store = new AppState();
  await store.init();
  globalThis.__catalogEvent({ groupsDirty: true, changed: [], removed: [] });
  await store.reloadAfterDiscovery();
  assert.deepEqual(store.selectedSessionRef, ref);
  assert.ok(calls.filter((x) => x === 'sessions').length >= 2);
  store.destroy();
});
