import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

async function withBinding(binding, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App: binding } } };
  try { await fn(await createAPI()); }
  finally { if (previous === undefined) delete globalThis.window; else globalThis.window = previous; }
}

test('R09 native probe preserves explicit present/absent/unknown states without inferring rename', async () => {
  let observations = [{ path: 'a', state: 'present', revision: 'sha256:first' }];
  await withBinding({ ProbeFiles: async (dir, paths, host) => {
    assert.equal(dir, '/repo'); assert.deepEqual(paths, ['a']); assert.equal(host, 'box');
    return { root: '/repo', observations };
  } }, async api => {
    assert.equal((await api.probeFiles('/repo', ['a'], 'box')).observations[0].revision, 'sha256:first');
    observations = [{ path: 'a', state: 'absent' }];
    assert.deepEqual((await api.probeFiles('/repo', ['a'], 'box')).observations, observations);
    observations = [{ path: 'a', state: 'present', revision: 'sha256:recreated' }];
    assert.deepEqual((await api.probeFiles('/repo', ['a'], 'box')).observations, observations);
    observations = [{ path: 'a', state: 'unknown', reason: 'unavailable' }];
    assert.deepEqual((await api.probeFiles('/repo', ['a'], 'box')).observations, observations);
  });
});

test('R09 old bindings and synthetic mock reads never fabricate authoritative deletion', async () => {
  await withBinding({}, async api => {
    assert.deepEqual(await api.probeFiles('/repo', ['a'], ''), { root: '', observations: [{ path: 'a', state: 'unknown', reason: 'unsupported' }] });
  });
  assert.equal((await new MockBackendAPI().probeFiles('/repo', ['a'], '')).observations[0].state, 'unknown');
});
