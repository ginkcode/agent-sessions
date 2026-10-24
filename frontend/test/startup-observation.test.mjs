import test from 'node:test';
import assert from 'node:assert/strict';
import { StartupObservation } from '../src/lib/startupObservation.ts';
import { loadServerModule } from './helpers.mjs';

test('frontend timing is local, content-free, first-only and detached', () => {
  let time = 100;
  const o = new StartupObservation(() => time);
  time = 120; o.data('groups_counts', false);
  time = 130; o.data('sessions', false);
  assert.deepEqual(o.snapshot(), [
    { phase: 'groups_counts', elapsedMs: 20 }, { phase: 'sessions', elapsedMs: 30 },
  ]);
  time = 150; o.data('sessions', true);
  time = 160; o.data('sessions', true);
  assert.deepEqual(o.snapshot().at(-1), { phase: 'usable_data', elapsedMs: 50 });
  o.snapshot()[0].phase = 'modified';
  assert.equal(o.snapshot()[0].phase, 'groups_counts');
});

const replacements = ['tree', 'catalog', 'manage', 'link', 'navHistory'].map((name) => [
  `'../${name}'`, JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href),
]);
replacements.push(["import { api, subscribeCatalogChanged } from '../api';", 'const api = new Proxy({}, { get: (_, key) => globalThis.__startupAPI[key] }); const subscribeCatalogChanged = () => () => {};']);

test('real store observes applied data only, before optional session selection', async () => {
  let groupsResolve;
  globalThis.__startupAPI = {
    listGroups: () => new Promise((resolve) => { groupsResolve = resolve; }),
    agentCounts: async () => ({}),
    listSessions: async () => [],
  };
  const { AppState } = await loadServerModule(new URL('../src/lib/stores/appState.svelte.ts', import.meta.url), replacements);
  const store = new AppState();
  assert.equal(store.startupObservation, null);
  const o = new StartupObservation(); store.startupObservation = o;
  const pending = store.loadGroups();
  assert.deepEqual(o.snapshot(), []);
  groupsResolve([]); await pending;
  await store.loadSessions();
  assert.deepEqual(o.snapshot().map((s) => s.phase), ['groups_counts', 'sessions']);
  globalThis.__startupAPI.listSessions = async () => [{ ref: { agent: 'claude', id: 'generated' } }];
  store.selectedSessionRef = { agent: 'claude', id: 'generated' };
  await store.loadSessions();
  assert.deepEqual(o.snapshot().map((s) => s.phase), ['groups_counts', 'sessions', 'usable_data']);
  store.destroy();
});

test('superseded store replies do not produce startup marks', async () => {
  let resolveOld;
  globalThis.__startupAPI = {
    listGroups: async () => [], agentCounts: async () => ({}),
    listSessions: () => new Promise((resolve) => { resolveOld = resolve; }),
  };
  const { AppState } = await loadServerModule(new URL('../src/lib/stores/appState.svelte.ts', import.meta.url), replacements);
  const store = new AppState();
  const o = new StartupObservation(); store.startupObservation = o;
  const pending = store.loadSessions();
  // Supersede the pending response without applying any other session reply.
  store.sessionsRequests.next();
  resolveOld([{ ref: { agent: 'claude', id: 'generated' } }]);
  await pending;
  assert.deepEqual(o.snapshot(), []);
  store.destroy();
});
