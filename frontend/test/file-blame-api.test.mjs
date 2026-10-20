import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { createAPI, BLAME_UNSUPPORTED } from '../src/lib/api.ts';
import { guardEpoch, StaleReplyError } from '../src/lib/link.ts';

function withWailsApp(App, fn) {
  const hadWindow = 'window' in globalThis;
  const previous = globalThis.window;
  globalThis.window = { go: { app: { App } } };
  return Promise.resolve()
    .then(fn)
    .finally(() => {
      if (hadWindow) globalThis.window = previous;
      else delete globalThis.window;
    });
}

test('mock blames committed lines deterministically with full metadata', async () => {
  const api = new MockBackendAPI();
  const first = await api.blameFile('/project', 'main.go', 1);
  assert.equal(first.line, 1);
  assert.match(first.commit, /^[0-9a-f]{40}$/);
  assert.notEqual(first.commit, '0'.repeat(40));
  assert.equal(first.uncommitted, false);
  assert.ok(first.author.length > 0);
  assert.ok(first.summary.length > 0);
  assert.ok(Number.isInteger(first.authorTime) && first.authorTime > 0);
  assert.deepEqual(await api.blameFile('/project', 'main.go', 1), first, 'stable across calls');
  // Lines of one block share a commit; the next block differs.
  assert.equal((await api.blameFile('/project', 'main.go', 3)).commit, first.commit);
  assert.notEqual((await api.blameFile('/project', 'main.go', 4)).commit, first.commit);
});

test('mock reports uncommitted lines: changed hunks, untracked files and saved drafts', async () => {
  const api = new MockBackendAPI();
  // App.svelte's mock diff marks line 5 modified and 6-7 added.
  for (const line of [5, 6, 7]) {
    const b = await api.blameFile('/project', 'frontend/src/App.svelte', line);
    assert.equal(b.uncommitted, true, `line ${line}`);
    assert.equal(b.commit, '0'.repeat(40));
    assert.equal(b.summary, '');
  }
  assert.equal((await api.blameFile('/project', 'frontend/src/App.svelte', 1)).uncommitted, false);
  assert.equal((await api.blameFile('/project', 'frontend/src/lib/new.ts', 1)).uncommitted, true);

  const original = await api.readFile('/project', 'main.go');
  await api.saveFile('/project', { path: 'main.go', text: 'edited\n', expectedRevision: original.revision });
  assert.equal((await api.blameFile('/project', 'main.go', 1)).uncommitted, true);
});

test('mock rejects invalid lines and non-text files', async () => {
  const api = new MockBackendAPI();
  for (const line of [0, -1, 1.5, 999]) {
    await assert.rejects(api.blameFile('/project', 'main.go', line), `line ${line}`);
  }
  await assert.rejects(api.blameFile('/project', 'assets/logo.png', 1));
});

test('Wails blameFile forwards arguments and normalizes errors', async () => {
  const calls = [];
  const reply = { line: 2, commit: 'a'.repeat(40), author: 'A', authorTime: 1, summary: 's', uncommitted: false };
  const App = { async BlameFile(...args) { calls.push(args); return reply; } };
  await withWailsApp(App, async () => {
    const backend = createAPI();
    assert.ok(!(backend instanceof MockBackendAPI));
    assert.deepEqual(await backend.blameFile('/repo', 'a/b.go', 2), reply);
    assert.deepEqual(calls, [['/repo', 'a/b.go', 2]]);
    App.BlameFile = () => Promise.reject('fatal: no such path');
    await assert.rejects(
      backend.blameFile('/repo', 'a/b.go', 2),
      (err) => err instanceof Error && err.message === 'fatal: no such path',
    );
  });
});

test('Wails blameFile rejects clearly on an older backend without the binding', async () => {
  await withWailsApp({}, async () => {
    const backend = createAPI();
    await assert.rejects(
      backend.blameFile('/repo', 'a.go', 1),
      (err) => err instanceof Error && err.message === BLAME_UNSUPPORTED,
    );
  });
});

test('blame replies that land after a host switch are stale', async () => {
  let current = 0;
  let resolve;
  const backend = { blameFile: () => new Promise((r) => { resolve = r; }) };
  const api = guardEpoch(backend, () => current, new Set());
  const pending = api.blameFile('/repo', 'a.go', 1);
  current++;
  resolve({ line: 1 });
  await assert.rejects(pending, (err) => err instanceof StaleReplyError);
});

test('mock hosts report blame support', async () => {
  const api = new MockBackendAPI();
  await api.connect('dev-box');
  const state = await api.connectionState();
  assert.equal(state.capabilities.blame, true);
});
