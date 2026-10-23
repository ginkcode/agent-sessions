import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { createAPI, GIT_CHANGES_UNSUPPORTED } from '../src/lib/api.ts';

async function withApp(App, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  try { await fn(); } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
}

test('Git status forwards project, preserves XY/origin/branch metadata and normalizes errors', async () => {
  const calls = [];
  const reply = { ok: true, branch: '', detached: true, head: 'abcdef1', hasCommits: true,
    upstream: 'origin/main', ahead: 1, behind: 2, operation: 'merge', truncated: false,
    entries: [{ path: 'new', originalPath: 'old', index: 'R', worktree: 'M', conflict: false }] };
  const App = { async GitStatus(...args) { calls.push(args); return reply; } };
  await withApp(App, async () => {
    const api = createAPI();
    assert.deepEqual(await api.gitStatus('/repo/sub'), reply);
    assert.deepEqual(calls, [['/repo/sub']]);
    App.GitStatus = () => Promise.reject('not a session directory');
    await assert.rejects(api.gitStatus('/unknown'), (e) => e instanceof Error && e.message === 'not a session directory');
  });
});
test('older app without Git status binding rejects clearly', async () => {
  await withApp({}, async () => {
    await assert.rejects(createAPI().gitStatus('/repo'), (e) => e.message === GIT_CHANGES_UNSUPPORTED);
  });
});
test('mock provides project-relative Git changes and advertises capability', async () => {
  const api = new MockBackendAPI();
  const result = await api.gitStatus('/project');
  assert.equal(result.ok, true);
  assert.equal(result.branch, 'main');
  assert.ok(Array.isArray(result.entries));
  for (const e of result.entries) {
    assert.ok(!e.path.startsWith('/'));
    assert.equal(typeof e.index, 'string');
    assert.equal(typeof e.worktree, 'string');
    assert.equal(typeof e.conflict, 'boolean');
  }
  assert.deepEqual(await api.gitStatus('/project'), result);
});
