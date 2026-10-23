import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI, GIT_DIFF_UNSUPPORTED } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

async function withApp(App, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  try { await fn(); } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
}
test('Git diff API forwards explicit basis and preserves readonly deleted content', async () => {
  const calls = [];
  const reply = { content: { path: 'deleted', text: '', size: 0, changes: [{ kind: 'deleted', start: 0, count: 0, removed: ['old'] }] }, basis: 'staged', deleted: true, readOnly: true };
  const App = { async ReadGitDiff(...args) { calls.push(args); return reply; } };
  await withApp(App, async () => {
    const api = createAPI();
    assert.deepEqual(await api.readGitDiff('/repo', 'deleted', 'staged'), reply);
    assert.deepEqual(calls, [['/repo', 'deleted', 'staged']]);
    App.ReadGitDiff = async () => { throw 'invalid Git diff basis'; };
    await assert.rejects(api.readGitDiff('/repo', 'file', 'invalid'), e => e instanceof Error && e.message === 'invalid Git diff basis');
  });
});
test('older diff binding rejects instead of showing an incorrect basis', async () => {
  await withApp({}, async () => {
    await assert.rejects(createAPI().readGitDiff('/repo', 'file', 'index'), e => e.message === GIT_DIFF_UNSUPPORTED);
  });
});
test('mock staged diff cannot be saved and index reads keep working eligibility', async () => {
  const api = new MockBackendAPI();
  const staged = await api.readGitDiff('/project', 'main.go', 'staged');
  assert.equal(staged.basis, 'staged');
  assert.equal(staged.readOnly, true);
  assert.equal(staged.content.edit.eligible, false);
  assert.equal(staged.content.revision, undefined);
  assert.equal((await api.readGitDiff('/project', 'main.go', 'index')).readOnly, false);
  await assert.rejects(api.readGitDiff('/project', 'main.go', 'invalid'), /Invalid Git diff basis/);
});
