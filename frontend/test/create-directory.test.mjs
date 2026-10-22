import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { fixtureStore, loadServerModule, loadServerComponent, renderWithFixture } from './helpers.mjs';
import { createAPI } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { buildFileTree } from '../src/lib/files.ts';
import { bumpEpoch } from '../src/lib/link.ts';

const url = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const { FilesStore } = await loadServerModule(url, [
  ["import { api } from '../api';", fixtureStore('api', '__mkdirFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__mkdirFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(n => [`'../${n}'`, JSON.stringify(new URL(`../${n}.ts`, url).href)]),
]);
async function setup() {
  const calls = [];
  const fixture = globalThis.__mkdirFixture = {
    link: { dataHost: undefined, blockedReason: null },
    api: {
      listFiles: async dir => ({ root: dir, files: [], directories: ['src/nested/new'] }),
      createDirectory: async (...args) => { calls.push(args); },
    },
  };
  const store = new FilesStore();
  await store.show('/repo');
  return { store, fixture, calls };
}

test('explicit empty directories are visible, nested, deduplicated and sorted with inferred folders', () => {
  const tree = buildFileTree(['src/a.txt', 'README.md'], ['empty/nested', 'src', 'empty', 'empty/nested']);
  assert.deepEqual(tree.map(n => n.name), ['empty', 'src', 'README.md']);
  assert.deepEqual(tree[0].children, [{ name: 'nested', path: 'empty/nested', children: [] }]);
  assert.equal(tree[1].children[0].path, 'src/a.txt');
  assert.deepEqual(buildFileTree([]), []);
});

test('creation forwards root and nested destinations, refreshes tree and expands new directory and parents', async () => {
  const { store, calls } = await setup();
  await store.createDirectory('', 'empty');
  await store.createDirectory('src/nested', 'new');
  assert.deepEqual(calls, [['/repo', '', 'empty', ''], ['/repo', 'src/nested', 'new', '']]);
  assert.deepEqual([...store.expanded], ['empty', 'src', 'src/nested', 'src/nested/new']);
  assert.ok(store.listing.directories.includes('src/nested/new'));
});

test('creation leaves open drafts and editor identity untouched and uses the captured remote host', async () => {
  const { store, fixture, calls } = await setup();
  fixture.api.readFile = async (_dir, path) => ({ path, text: 'old', revision: 'r1', edit: { eligible: true, newline: 'none' } });
  store.openFile('src/a.txt', { keep: true });
  await new Promise(r => setImmediate(r));
  store.startEditing('src/a.txt');
  store.updateDraft('src/a.txt', 'draft');
  const tab = store.activeTab, edit = tab.edit;
  await store.createDirectory('', 'empty');
  assert.equal(store.activeTab, tab);
  assert.equal(tab.edit, edit);
  assert.equal(edit.draft, 'draft');
  fixture.link.dataHost = 'remote';
  const remote = new FilesStore();
  await remote.show('/remote');
  await remote.createDirectory('', 'new');
  assert.deepEqual(calls.at(-1), ['/remote', '', 'new', 'remote']);
});

test('invalid names, traversal, blocked connections and pending operations never submit', async () => {
  const { store, fixture, calls } = await setup();
  for (const name of ['', '.', '..', 'a/b', 'a\\b', 'a\0b']) await assert.rejects(store.createDirectory('', name), /single folder/);
  for (const path of ['../src', '/src', 'src//a', 'src\\a']) await assert.rejects(store.createDirectory(path, 'x'), /project directory/);
  fixture.link.blockedReason = 'Disconnected';
  await assert.rejects(store.createDirectory('', 'x'), /Disconnected/);
  fixture.link.blockedReason = null;
  store.renaming = true;
  await assert.rejects(store.createDirectory('', 'x'), /already in progress/);
  assert.deepEqual(calls, []);
});

test('pending creation blocks duplicate submission and project close; stale completion never expands another tree', async () => {
  const { store, fixture } = await setup();
  let resolve;
  fixture.api.createDirectory = () => new Promise(r => { resolve = r; });
  const pending = store.createDirectory('', 'new');
  await assert.rejects(store.createDirectory('', 'other'), /already in progress/);
  store.close();
  assert.equal(store.dir, '/repo');
  bumpEpoch();
  resolve();
  await assert.rejects(pending, /outcome is unknown/);
  assert.equal(store.expanded.size, 0);
  assert.equal(store.creatingDirectory, false);
});

test('native CreateDirectory binding forwards host and normalizes errors', async () => {
  const previous = globalThis.window;
  const calls = [];
  globalThis.window = { go: { app: { App: { CreateDirectory: async (...args) => { calls.push(args); } } } } };
  try {
    await createAPI().createDirectory('/repo', '', 'new', 'remote');
    assert.deepEqual(calls, [['/repo', '', 'new', 'remote']]);
    globalThis.window.go.app.App.CreateDirectory = async () => { throw 'exists'; };
    await assert.rejects(createAPI().createDirectory('/repo', '', 'new', ''), /exists/);
  } finally { globalThis.window = previous; }
});

test('mock empty folders can be nested, renamed, deleted and reject collisions', async () => {
  const mock = new MockBackendAPI();
  await mock.createDirectory('/repo', '', 'empty', '');
  await mock.createDirectory('/repo', 'empty', 'child', '');
  await assert.rejects(mock.createDirectory('/repo', '', 'empty', ''), /already exists/);
  await mock.renameFile('/repo', 'empty', 'renamed', '');
  assert.deepEqual((await mock.listFiles('/repo')).directories, ['renamed', 'renamed/child']);
  await mock.deleteFile('/repo', 'renamed', '');
  assert.deepEqual((await mock.listFiles('/repo')).directories, []);
});

test('folder prompt is a styled accessible text-input modal, initially empty and cancellable', async () => {
  const promptURL = new URL('../src/lib/components/common/go-to-file/NewFolderPrompt.svelte', import.meta.url);
  const prompt = await loadServerComponent(promptURL, [
    ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
    ...['files', 'link', 'fileActions'].map(n => [`import { ${n} } from '../../../stores/${n}.svelte';`, fixtureStore(n)]),
  ]);
  const html = await renderWithFixture(prompt, { files: { dir: '/repo', contextVersion: '1' }, link: {}, fileActions: {} }, { path: '', onClose() {} });
  assert.match(html, /role="dialog"/);
  assert.match(html, /aria-modal="true"/);
  assert.match(html, /Create a folder in \/repo/);
  assert.match(html, /value=""/);
  assert.match(html, />Cancel<\/button>/);
  assert.match(html, /type="submit" disabled/);
});

test('root blank-space context is tree-only and New Folder is directory-only', async () => {
  const picker = await readFile(new URL('../src/lib/components/common/go-to-file/FilePicker.svelte', import.meta.url), 'utf8');
  assert.match(picker, /if \(!query\.trim\(\)\) onContext\(e, '', true\)/);
  const dialog = await readFile(new URL('../src/lib/components/common/GoToFileDialog.svelte', import.meta.url), 'utf8');
  assert.match(dialog, /if \(directory\) items\.push\(\{\s*label: 'New Folder…'/);
});
