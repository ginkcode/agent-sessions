import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI, subscribeDiscoveryStatus } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

async function withBinding(binding, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App: binding } } };
  try { await fn(createAPI()); }
  finally { if (previous === undefined) delete globalThis.window; else globalThis.window = previous; }
}

test('discovery API preserves availability, status and route identity; offline errors remain errors', async () => {
  const reply = { available: true, host: 'box', generation: 3, status: {
    lifetime: 2, revision: 4, state: 'degraded', catalogUsable: true, complete: true,
    cacheMode: 'writer', configuredProviders: 2, detectedProviders: 1,
    failedProviders: 1, detectionComplete: true,
  } };
  const binding = { DiscoveryStatus: async () => reply };
  await withBinding(binding, async api => {
    assert.deepEqual(await api.discoveryStatus(), reply);
    binding.DiscoveryStatus = async () => ({ available: false, host: 'old', generation: 4 });
    assert.deepEqual(await api.discoveryStatus(), { available: false, host: 'old', generation: 4 });
    binding.DiscoveryStatus = async () => { throw 'disconnected'; };
    await assert.rejects(api.discoveryStatus(), /disconnected/);
  });
});

test('old App bindings and browser mock report unknown without fabricated completion', async () => {
  const unavailable = { available: false, host: '', generation: 0 };
  await withBinding({}, async api => assert.deepEqual(await api.discoveryStatus(), unavailable));
  assert.deepEqual(await new MockBackendAPI().discoveryStatus(), unavailable);
});

test('discovery subscription forwards route-scoped snapshots and can unsubscribe', () => {
  const mock = new MockBackendAPI();
  const seen = [];
  const off = subscribeDiscoveryStatus(event => seen.push(event), mock);
  const event = { host: 'box', generation: 7, lifetime: 1, revision: 3, state: 'reconciling' };
  mock.emit('discovery:status', event);
  mock.emit('discovery:status', null);
  mock.emit('discovery:status', 'invalid');
  off();
  mock.emit('discovery:status', event);
  assert.deepEqual(seen, [event]);
});
