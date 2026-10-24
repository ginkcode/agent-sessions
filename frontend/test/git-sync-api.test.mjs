import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI, GIT_SYNC_UNSUPPORTED, GitSyncError } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

async function withApp(App, fn) {
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  try { await fn(); } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
}

test('Git sync API forwards op, sync ID and host and decodes coded failures', async () => {
  const calls = [];
  const reply = { op: 'pull', summary: 'Fast-forwarded main to origin/main (2 commits).', status: { ok: true, branch: 'main', upstream: 'origin/main', ahead: 0, behind: 0 } };
  const App = { async GitSync(...args) { calls.push(args); return reply; }, async CancelGitSync() {} };
  await withApp(App, async () => {
    const api = createAPI();
    assert.deepEqual(await api.gitSync('/repo/sub', 'pull', 'box'), reply);
    await api.gitSync('/repo', 'push', undefined);
    assert.equal(calls.length, 2);
    assert.deepEqual([calls[0][0], calls[0][1], calls[0][3]], ['/repo/sub', { op: 'pull' }, 'box']);
    assert.equal(calls[1][3], '');
    assert.match(calls[0][2], /^git-sync-/);
    assert.notEqual(calls[0][2], calls[1][2]);
    App.GitSync = async () => { throw 'nonFastForward: The branch and its upstream have diverged.\nfatal: Not possible to fast-forward, aborting.'; };
    await assert.rejects(api.gitSync('/repo', 'pull', 'box'), e => e instanceof GitSyncError && e.code === 'nonFastForward'
      && e.message.startsWith('The branch') && e.message.includes('Not possible to fast-forward'));
    App.GitSync = async () => { throw 'The connection changed to another host.'; };
    await assert.rejects(api.gitSync('/repo', 'pull', 'box'), e => e instanceof GitSyncError && e.code === 'failed' && e.message === 'The connection changed to another host.');
  });
});

test('aborting a Git sync cancels its backend run by ID and waits for it to stop', async () => {
  const cancels = [];
  let finish;
  const App = {
    GitSync: (_dir, _req, id) => new Promise((_, reject) => { finish = () => reject(`canceled: git push was canceled.`); App.id = id; }),
    async CancelGitSync(id) { cancels.push(id); finish(); },
  };
  await withApp(App, async () => {
    const controller = new AbortController();
    const pending = createAPI().gitSync('/repo', 'push', 'box', controller.signal);
    controller.abort();
    await assert.rejects(pending, e => e instanceof GitSyncError && e.code === 'canceled');
    assert.deepEqual(cancels, [App.id]);
    await assert.rejects(createAPI().gitSync('/repo', 'push', 'box', controller.signal), e => e.code === 'canceled');
    assert.equal(cancels.length, 1);
  });
});

test('older bindings reject Git sync without starting it', async () => {
  let started = 0;
  await withApp({}, async () => {
    await assert.rejects(createAPI().gitSync('/repo', 'fetch', ''), e => e instanceof GitSyncError && e.code === 'unsupported' && e.message === GIT_SYNC_UNSUPPORTED);
  });
  // A binding that cannot cancel must not run an operation the caller expects to abort.
  await withApp({ async GitSync() { started++; } }, async () => {
    await assert.rejects(createAPI().gitSync('/repo', 'fetch', '', new AbortController().signal), e => e.code === 'unsupported');
  });
  assert.equal(started, 0);
});

test('mock sync reports ahead/behind, refuses divergence, sets upstreams and cancels', async () => {
  const api = new MockBackendAPI();
  const key = JSON.stringify(['', '/project']);
  api.mockSyncState.set(key, { ahead: 0, behind: 2 });
  const fetched = await api.gitSync('/project', 'fetch', undefined);
  assert.equal(fetched.status.behind, 2);
  assert.match(fetched.summary, /0 ahead, 2 behind origin\/main/);
  await assert.rejects(api.gitSync('/project', 'push', undefined), e => e.code === 'nonFastForward');
  const pulled = await api.gitSync('/project', 'pull', undefined);
  assert.equal(pulled.status.behind, 0);
  assert.match(pulled.summary, /Fast-forwarded main to origin\/main \(2 commits\)/);
  api.mockSyncState.set(key, { ahead: 1, behind: 1 });
  await assert.rejects(api.gitSync('/project', 'pull', undefined), e => e.code === 'nonFastForward');
  api.mockSyncState.set(key, { ahead: 1, behind: 0 });
  await api.gitSwitch('/project', { name: 'topic', create: true });
  await assert.rejects(api.gitSync('/project', 'pull', undefined), e => e.code === 'noUpstream');
  const pushed = await api.gitSync('/project', 'push', undefined);
  assert.equal(pushed.status.upstream, 'origin/topic');
  assert.equal(pushed.status.ahead, 0);
  assert.match(pushed.summary, /set it as upstream/);
  await assert.rejects(api.gitSync('/project', 'fetch', 'box'), /connection changed/);
  api.gitSyncMs = 1000;
  const controller = new AbortController();
  const pending = api.gitSync('/project', 'fetch', undefined, controller.signal);
  controller.abort();
  await assert.rejects(pending, e => e.code === 'canceled');
});
