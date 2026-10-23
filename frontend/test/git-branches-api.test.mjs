import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI, GIT_CHANGES_UNSUPPORTED, GIT_WRITE_UNSUPPORTED } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

async function withApp(App, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  try { await fn(); } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
}
test('branch API forwards branch metadata and explicit switch intent with host/drafts', async () => {
  const calls = [];
  const reply = { branches: [{ name: 'main', current: true, remote: false, upstream: 'origin/main', commitTime: 1700000000 }], truncated: true };
  const App = { async GitBranches(...args) { calls.push(args); return reply; }, async GitSwitch(...args) { calls.push(args); } };
  await withApp(App, async () => {
    const api = createAPI();
    assert.deepEqual(await api.gitBranches('/repo/sub'), reply);
    const req = { name: 'origin/next', create: false, remote: true, draftPaths: ['draft'] };
    await api.gitSwitch('/repo', req, 'box');
    await api.gitSwitch('/repo', { name: 'main', create: false }, undefined);
    assert.deepEqual(calls, [['/repo/sub'], ['/repo', req, 'box'], ['/repo', { name: 'main', create: false }, '']]);
    App.GitSwitch = async () => { throw 'local changes would be overwritten'; };
    await assert.rejects(api.gitSwitch('/repo', req, 'box'), e => e instanceof Error && e.message === 'local changes would be overwritten');
  });
});
test('older branch bindings reject clearly', async () => {
  await withApp({}, async () => {
    const api = createAPI();
    await assert.rejects(api.gitBranches('/repo'), e => e.message === GIT_CHANGES_UNSUPPORTED);
    await assert.rejects(api.gitSwitch('/repo', { name: 'main', create: false }), e => e.message === GIT_WRITE_UNSUPPORTED);
  });
});
test('mock lists creates switches and tracks branches while refusing drafts and wrong hosts', async () => {
  const api = new MockBackendAPI();
  assert.ok((await api.gitBranches('/project')).branches.some(b => b.current && b.name === 'main'));
  await assert.rejects(api.gitSwitch('/project', { name: 'new', create: true, draftPaths: ['draft'] }), /unsaved drafts/);
  await assert.rejects(api.gitSwitch('/project', { name: '--detach', create: true }), /Invalid branch/);
  await assert.rejects(api.gitSwitch('/project', { name: 'new', create: true }, 'box'), /connection changed/);
  await api.gitSwitch('/project', { name: 'new', create: true });
  assert.equal((await api.gitStatus('/project')).branch, 'new');
  await api.gitSwitch('/project', { name: 'main', create: false });
  await api.gitSwitch('/project', { name: 'origin/remote-only', create: false, remote: true });
  const list = await api.gitBranches('/project');
  assert.ok(list.branches.some(b => b.current && b.name === 'remote-only' && b.upstream === 'origin/remote-only'));
  assert.ok(!list.branches.some(b => b.remote && b.name === 'origin/remote-only'));
});
