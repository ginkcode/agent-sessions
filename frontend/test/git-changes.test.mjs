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
