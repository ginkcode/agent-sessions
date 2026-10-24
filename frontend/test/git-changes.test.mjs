import test from 'node:test';
import assert from 'node:assert/strict';
import { changeGroups, branchLabel, isChangesShortcut } from '../src/lib/gitChanges.ts';
import { fixtureStore, loadServerComponent, loadServerModule, renderWithFixture } from './helpers.mjs';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const status = entries => ({ ok: true, branch: 'main', head: 'abc1234', detached: false, hasCommits: true, upstream: 'origin/main', ahead: 1, behind: 0, entries, truncated: false });
const entry = (path, index = '', worktree = '', conflict = false) => ({ path, index, worktree, conflict });

test('change groups distinguish staged, unstaged, both and conflicts', () => {
  const a = entry('a', 'M', 'M'), b = entry('b', '', '?'), c = entry('c', 'U', 'U', true);
  assert.deepEqual(changeGroups(status([a, b, c])), { staged: [a], unstaged: [a, b], conflicts: [c] });
  assert.equal(branchLabel({ ...status([]), detached: true }), 'Detached at abc1234');
  assert.equal(isChangesShortcut({ code: 'KeyG', metaKey: true, shiftKey: true }, true), true);
});

const { GitChangesStore } = await loadServerModule(new URL('../src/lib/stores/gitChanges.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__gitFixture')],
  ["'../manage'", lib('manage')], ["'../gitChanges'", lib('gitChanges')],
  ["import { files, tabHasUnsaved } from './files.svelte';", `${fixtureStore('files', '__gitFixture')} const tabHasUnsaved = t => Boolean(t.edit?.dirty);`],
  ...['link', 'connectionStore', 'filePrompt', 'codeDiagnostics'].map(name => [
    `import { ${name} } from './${name === 'connectionStore' ? 'connection' : name}.svelte';`, fixtureStore(name, '__gitFixture'),
  ]),
]);
function fixture() {
  const calls = [];
  const f = {
    files: { dir: '/p', contextVersion: '1', tabs: [], async refresh() { calls.push('files'); }, async reloadReplaced(paths) { calls.push(['reload', paths]); } },
    link: { dataHost: undefined, blockedReason: null }, connectionStore: { capabilities: { gitChanges: true, gitWrite: true } },
    filePrompt: { async ask() { calls.push('confirm'); return 'discard'; } },
    codeDiagnostics: { async hide() {}, async show() {} },
    api: {
      async gitStatus() { calls.push('status'); return status([entry('a', '', 'M')]); },
      async gitStage() { calls.push('stage'); }, async gitUnstage() {}, async gitDiscard() { calls.push('discard'); },
      async gitCommit(_dir, request) { calls.push(['commit', request]); },
      async gitBranches() { return { branches: [], truncated: false }; }, async gitSwitch() { calls.push('switch'); },
    },
  };
  globalThis.__gitFixture = f;
  return { f, calls, store: new GitChangesStore() };
}

test('Git actions serialize, refresh after failure and keep the commit message', async () => {
  const { store, f, calls } = fixture();
  await store.refresh();
  let reject;
  f.api.gitStage = () => new Promise((_resolve, fail) => reject = fail);
  const pending = store.stage(['a']);
  assert.equal(store.busy, true);
  assert.equal(await store.unstage(['a']), false);
  reject(new Error('hook failed'));
  assert.equal(await pending, false);
  assert.match(store.error, /hook failed/);
  assert.deepEqual(calls.slice(-2), ['files', 'status']);
  store.message = 'message';
  f.api.gitCommit = async () => { throw new Error('commit hook failed'); };
  assert.equal(await store.commit(), false);
  assert.equal(store.message, 'message');
  delete globalThis.__gitFixture;
});

test('discard asks with names, refuses drafts and branch switching refuses drafts', async () => {
  const { store, f, calls } = fixture();
  await store.refresh();
  f.files.tabs = [{ path: 'a', edit: { dirty: true } }];
  assert.equal(await store.discard(['a']), false);
  assert.equal(await store.switchBranch('other'), false);
  assert.equal(calls.includes('confirm'), false);
  f.files.tabs = [];
  assert.equal(await store.discard(['a']), true);
  assert.ok(calls.indexOf('confirm') < calls.indexOf('discard'));
  assert.ok(calls.some(c => Array.isArray(c) && c[0] === 'reload'));
  delete globalThis.__gitFixture;
});

test('late status from another host is ignored', async () => {
  const { store, f } = fixture();
  let resolve;
  f.api.gitStatus = () => new Promise(r => resolve = r);
  const pending = store.refresh();
  f.link.dataHost = 'other';
  resolve(status([entry('stale')]));
  await pending;
  assert.equal(store.status, null);
  delete globalThis.__gitFixture;
});

const pane = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/ChangesPane.svelte', import.meta.url), [
  ["import { connectionStore } from '../../../stores/connection.svelte';", fixtureStore('connectionStore')],
  ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
]);
test('Changes pane renders separate sections, commit labels and unsupported host state', async () => {
  const { store } = fixture();
  store.status = status([entry('both.ts', 'M', 'M'), entry('conflict.ts', 'U', 'U', true)]);
  let html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: { gitChanges: true } } }, { onOpen() {} });
  assert.match(html, /Staged changes/); assert.match(html, /Merge conflicts/); assert.match(html, /Commit staged/);
  assert.equal((html.match(/class="path[^>]*>both.ts</g) ?? []).length, 2);
  store.status = status([entry('new.ts', '', '?')]);
  html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: { gitChanges: true } } }, { onOpen() {} });
  assert.match(html, /Commit all/);
  html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: {} } }, { onOpen() {} });
  assert.match(html, /Update agent-sessions on the host/);
  delete globalThis.__gitFixture;
});

test('Git sync cancels, keeps pull safe for drafts, publishes after asking and reloads tabs after pull', async () => {
  const { store, f, calls } = fixture();
  f.connectionStore.capabilities.gitSync = true;
  await store.refresh();
  let signal;
  f.api.gitSync = (_dir, op, _host, s) => { signal = s; calls.push(['sync', op]); return new Promise((_resolve, reject) => s.addEventListener('abort', () => reject(Object.assign(new Error('git fetch was canceled.'), { code: 'canceled' })))); };
  const pending = store.sync('fetch');
  assert.equal(store.syncOperation, 'fetch');
  assert.equal(store.syncBlocker, 'Wait for the current Git action to finish.');
  store.cancelSync();
  assert.equal(signal.aborted, true);
  assert.equal(await pending, false);
  assert.equal(store.error, null);
  assert.match(store.syncMessage, /Canceled fetch/);
  assert.equal(store.syncOperation, null);
  assert.equal(store.busy, false);

  f.files.tabs = [{ path: 'a', edit: { dirty: true } }];
  assert.equal(await store.sync('pull'), false);
  assert.match(store.error, /unsaved drafts before pulling/);
  f.files.tabs = [{ path: 'a' }, { path: '/elsewhere', external: true }];
  f.api.gitSync = async (_dir, op) => { calls.push(['sync', op]); return { op, summary: 'Fast-forwarded main.', status: status([]) }; };
  assert.equal(await store.sync('pull'), true);
  assert.equal(store.syncMessage, 'Fast-forwarded main.');
  assert.ok(calls.some(c => Array.isArray(c) && c[0] === 'reload' && c[1].length === 1 && c[1][0] === 'a'));

  calls.length = 0;
  store.status = { ...status([]), upstream: '' };
  f.filePrompt.ask = async () => { calls.push('confirm'); return undefined; };
  assert.equal(await store.sync('push'), false);
  assert.deepEqual(calls, ['confirm'], 'a declined publish never pushes');
  f.filePrompt.ask = async () => { calls.push('confirm'); return 'push'; };
  assert.equal(await store.sync('push'), true);
  assert.deepEqual(calls.slice(0, 3), ['confirm', 'confirm', ['sync', 'push']]);
  calls.length = 0;
  store.status = status([]);
  f.api.gitSync = async () => { throw Object.assign(new Error('The remote has commits this branch does not.'), { code: 'nonFastForward' }); };
  assert.equal(await store.sync('push'), false);
  assert.equal(calls.includes('confirm'), false, 'a push to the upstream does not ask');
  assert.match(store.error, /remote has commits/);
  f.connectionStore.capabilities.gitSync = false;
  assert.match(store.syncBlocker, /does not support Git sync/);
  delete globalThis.__gitFixture;
});

test('Changes pane shows sync actions by ahead/behind and a cancel while running', async () => {
  const { store } = fixture();
  const caps = { gitChanges: true, gitWrite: true, gitSync: true };
  store.status = { ...status([]), ahead: 2, behind: 3 };
  let html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: caps } }, { onOpen() {} });
  assert.match(html, />Fetch</); assert.match(html, /Pull ↓3/); assert.match(html, /Push ↑2/);
  store.status = { ...status([]), ahead: 0, behind: 0, upstream: '' };
  html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: caps } }, { onOpen() {} });
  assert.match(html, /Publish branch…/); assert.doesNotMatch(html, /Pull ↓/);
  store.syncOperation = 'pull';
  html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: caps } }, { onOpen() {} });
  assert.match(html, /Pulling…/); assert.match(html, />Cancel</); assert.doesNotMatch(html, />Fetch</);
  store.syncOperation = null;
  html = await renderWithFixture(pane, { gitChanges: store, connectionStore: { capabilities: { gitChanges: true } } }, { onOpen() {} });
  assert.match(html, /does not support Git sync/);
  delete globalThis.__gitFixture;
});

test('a sync reply after a host switch cannot overwrite or reload the new project', async () => {
  const { store, f, calls } = fixture();
  f.connectionStore.capabilities.gitSync = true;
  await store.refresh();
  let finish;
  f.api.gitSync = () => new Promise(resolve => finish = resolve);
  const pending = store.sync('pull');
  f.link.dataHost = 'other';
  await store.refresh();
  calls.length = 0;
  finish({ op: 'pull', summary: 'old project updated', status: { ...status([]), branch: 'wrong' } });
  assert.equal(await pending, true);
  assert.equal(store.status.branch, 'main');
  assert.equal(store.syncMessage, '');
  assert.deepEqual(calls, []);
  assert.equal(store.busy, false);
  delete globalThis.__gitFixture;
});
