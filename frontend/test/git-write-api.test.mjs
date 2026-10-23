import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI, GIT_WRITE_UNSUPPORTED } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

async function withApp(App, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  try { await fn(); } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
}
test('Git writes forward paths, requests and original host with normalized errors', async () => {
  const calls = [];
  const App = Object.fromEntries(['Stage', 'Unstage', 'Discard', 'Commit'].map(name =>
    [`Git${name}`, async (...args) => calls.push([name, ...args])]));
  const discard = { paths: ['file'], draftPaths: ['draft'] };
  const commit = { message: 'message\nbody', amend: true, stageAll: true };
  await withApp(App, async () => {
    const api = createAPI();
    await api.gitStage('/repo', ['literal[1]'], 'box');
    await api.gitUnstage('/repo', [], undefined);
    await api.gitDiscard('/repo', discard, 'box');
    await api.gitCommit('/repo', commit, 'box');
    assert.deepEqual(calls, [
      ['Stage', '/repo', ['literal[1]'], 'box'], ['Unstage', '/repo', [], ''],
      ['Discard', '/repo', discard, 'box'], ['Commit', '/repo', commit, 'box'],
    ]);
    App.GitCommit = async () => { throw 'hook failed'; };
    await assert.rejects(api.gitCommit('/repo', commit, 'box'), e => e instanceof Error && e.message === 'hook failed');
  });
});
test('older bindings reject Git writes clearly', async () => {
  await withApp({}, async () => {
    const api = createAPI();
    for (const call of [() => api.gitStage('/repo', []), () => api.gitUnstage('/repo', []),
      () => api.gitDiscard('/repo', { paths: [] }), () => api.gitCommit('/repo', { message: 'x', amend: false, stageAll: false })]) {
      await assert.rejects(call(), e => e.message === GIT_WRITE_UNSUPPORTED);
    }
  });
});
test('mock Git writes mutate staged states and refuse drafts, empty commits and wrong hosts', async () => {
  const api = new MockBackendAPI();
  const first = await api.gitStatus('/project');
  assert.ok(first.entries.length);
  const path = first.entries[0].path;
  await api.gitStage('/project', [path], undefined);
  assert.ok((await api.gitStatus('/project')).entries.find(e => e.path === path).index);
  await api.gitUnstage('/project', [path], undefined);
  assert.equal((await api.gitStatus('/project')).entries.find(e => e.path === path).index, '');
  await assert.rejects(api.gitDiscard('/project', { paths: [path], draftPaths: [path] }), /unsaved draft/);
  await assert.rejects(api.gitStage('/project', [], 'another-host'), /connection changed/);
  await assert.rejects(api.gitCommit('/project', { message: ' ', amend: false, stageAll: true }), /empty/);
  await api.gitCommit('/project', { message: 'commit', amend: false, stageAll: true });
  assert.deepEqual((await api.gitStatus('/project')).entries, []);
});
