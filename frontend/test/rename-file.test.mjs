import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule, loadServerComponent, renderWithFixture } from './helpers.mjs';
import { createAPI } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { bumpEpoch } from '../src/lib/link.ts';

const url = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const { FilesStore } = await loadServerModule(url, [
  ["import { api } from '../api';", fixtureStore('api', '__renameFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__renameFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(n => [`'../${n}'`, JSON.stringify(new URL(`../${n}.ts`, url).href)]),
]);
const tick = () => new Promise(r => setImmediate(r));
function deferred() { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; }
async function setup() {
  const calls = [];
  globalThis.__renameFixture = {
    link: { dataHost: undefined, blockedReason: null },
    api: {
      listFiles: async dir => ({ root: dir, files: ['src/a.txt', 'src-old/b.txt'] }),
      readFile: async (_dir, path) => ({ path, text: 'old', revision: 'r1', edit: { eligible: true, newline: 'none' } }),
      renameFile: async (...args) => { calls.push(args); },
    },
  };
  const store = new FilesStore();
  await store.show('/repo');
  store.openFile('src/a.txt', { keep: true });
  await tick();
  return { store, calls, fixture: globalThis.__renameFixture };
}

test('directory rename preserves editor identity, dirty draft/history, selection and expanded prefix boundaries', async () => {
  const { store, calls } = await setup();
  store.startEditing('src/a.txt');
  store.updateDraft('src/a.txt', 'unsaved');
  const tab = store.activeTab, edit = tab.edit;
  store.setExpanded(new Set(['src', 'src/nested', 'src-old']));
  await store.rename('src', 'lib');
  assert.deepEqual(calls, [['/repo', 'src', 'lib', '']]);
  assert.equal(store.activeTab, tab);
  assert.equal(tab.edit, edit);
  assert.equal(edit.draft, 'unsaved');
  assert.equal(edit.baseline, 'old');
  assert.equal(store.selected, 'lib/a.txt');
  assert.equal(tab.content.path, 'lib/a.txt');
  assert.deepEqual([...store.expanded], ['lib', 'lib/nested', 'src-old']);
  store.close();
  await store.show('/repo');
  assert.equal(store.activeTab, tab);
  assert.equal(store.selected, 'lib/a.txt');
});

test('pending and uncertain saves block rename; rename blocks saves and double submission', async () => {
  const { store, fixture, calls } = await setup();
  store.startEditing('src/a.txt');
  const edit = store.activeTab.edit;
  edit.saving = true;
  await assert.rejects(store.rename('src', 'lib'), /pending saves/);
  edit.saving = false;
  edit.uncertain = {};
  await assert.rejects(store.rename('src', 'lib'), /unknown save/);
  edit.uncertain = null;
  const pending = deferred();
  fixture.api.renameFile = () => pending.promise;
  const rename = store.rename('src', 'lib');
  assert.equal(await store.saveEdited('src/a.txt'), false);
  await assert.rejects(store.rename('src', 'other'), /already in progress/);
  pending.resolve();
  await rename;
  assert.equal(calls.length, 0);
});

test('failed rename keeps paths and drafts; stale success never remaps and blocks old-path writes', async () => {
  const { store, fixture } = await setup();
  store.startEditing('src/a.txt');
  store.updateDraft('src/a.txt', 'draft');
  fixture.api.renameFile = async () => { throw new Error('Destination exists'); };
  await assert.rejects(store.rename('src', 'lib'), /Destination exists/);
  assert.equal(store.selected, 'src/a.txt');
  assert.equal(store.renameUncertain, null);
  const pending = deferred();
  fixture.api.renameFile = () => pending.promise;
  const rename = store.rename('src', 'lib');
  bumpEpoch();
  pending.resolve();
  await assert.rejects(rename, /outcome is unknown/);
  assert.equal(store.selected, 'src/a.txt');
  assert.equal(store.activeTab.edit.draft, 'draft');
  assert.equal(await store.saveEdited('src/a.txt'), false);
  await assert.rejects(store.rename('src', 'lib'), /outcome is unknown/);
});

test('late old-path reads cannot replace renamed content', async () => {
  const { store, fixture } = await setup();
  const old = deferred();
  fixture.api.readFile = () => old.promise;
  const refresh = store.refresh();
  fixture.api.readFile = async (_dir, path) => ({ path, text: 'new' });
  await store.rename('src/a.txt', 'b.txt');
  old.resolve({ path: 'src/a.txt', text: 'stale' });
  await refresh;
  await tick();
  assert.equal(store.activeTab.path, 'src/b.txt');
  assert.equal(store.content.text, 'new');
});

test('rename prompt is a styled text-input modal with prefilled basename and explicit cancellation', async () => {
  const promptURL = new URL('../src/lib/components/common/go-to-file/RenamePrompt.svelte', import.meta.url);
  const prompt = await loadServerComponent(promptURL, [
    ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
    ...['files', 'link', 'fileActions'].map(n => [`import { ${n} } from '../../../stores/${n}.svelte';`, fixtureStore(n)]),
    ["'../../../link'", JSON.stringify(new URL('../../../link.ts', promptURL).href)],
  ]);
  const html = await renderWithFixture(prompt, { files: { dir: '/repo' }, link: {}, fileActions: {} }, { path: 'src/a.txt', onClose() {} });
  assert.match(html, /role="dialog"/);
  assert.match(html, /aria-modal="true"/);
  assert.match(html, /value="a.txt"/);
  assert.match(html, />Cancel<\/button>/);
  assert.match(html, /type="submit" disabled/);
});

test('root and invalid names never reach backend', async () => {
  const { store, calls } = await setup();
  for (const [path, name] of [['', 'x'], ['src', '..'], ['src', 'a/b'], ['src', 'a\\b'], ['src', '']]) {
    await assert.rejects(store.rename(path, name), /single file/);
  }
  assert.deepEqual(calls, []);
});

test('native RenameFile binding forwards captured host and normalizes errors', async () => {
  const previous = globalThis.window;
  const calls = [];
  globalThis.window = { go: { app: { App: { RenameFile: async (...args) => { calls.push(args); } } } } };
  try {
    await createAPI().renameFile('/repo', 'src/a', 'b', 'remote');
    assert.deepEqual(calls, [['/repo', 'src/a', 'b', 'remote']]);
    globalThis.window.go.app.App.RenameFile = async () => { throw 'exists'; };
    await assert.rejects(createAPI().renameFile('/repo', 'a', 'b', ''), /exists/);
  } finally { globalThis.window = previous; }
});

test('mock rename updates directory listing and retains source content, refusing collisions', async () => {
  const mock = new MockBackendAPI();
  const original = await mock.readFile('/repo', 'frontend/src/lib/api.ts');
  await mock.renameFile('/repo', 'frontend', 'web', '');
  const listing = await mock.listFiles('/repo');
  assert.ok(listing.files.includes('web/src/lib/api.ts'));
  assert.ok(!listing.files.includes('frontend/src/lib/api.ts'));
  assert.equal((await mock.readFile('/repo', 'web/src/lib/api.ts')).text, original.text);
  await assert.rejects(mock.renameFile('/repo', 'web', 'assets', ''), /already exists/);
});
