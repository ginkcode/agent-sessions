import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule } from './helpers.mjs';
import { createAPI } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { bumpEpoch } from '../src/lib/link.ts';
import { readFile } from 'node:fs/promises';

const url = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const { FilesStore, tabHasUnsaved } = await loadServerModule(url, [
  ["import { api } from '../api';", fixtureStore('api', '__deleteFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__deleteFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(n => [`'../${n}'`, JSON.stringify(new URL(`../${n}.ts`, url).href)]),
]);
globalThis.__deleteDirty = tabHasUnsaved;
const { FileActions } = await loadServerModule(new URL('../src/lib/stores/fileActions.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__deleteFixture')],
  ["import { files, tabHasUnsaved } from './files.svelte';", `${fixtureStore('files', '__deleteFixture')} const tabHasUnsaved = globalThis.__deleteDirty;`],
  ["import { filePrompt } from './filePrompt.svelte';", fixtureStore('filePrompt', '__deleteFixture')],
]);
const tick = () => new Promise(r => setImmediate(r));
function deferred() { let resolve, reject; const promise = new Promise((r, j) => { resolve = r; reject = j; }); return { promise, resolve, reject }; }
async function setup() {
  const calls = [];
  const fixture = globalThis.__deleteFixture = {
    link: { dataHost: undefined, blockedReason: null },
    api: {
      listFiles: async dir => ({ root: dir, files: ['src/a.txt', 'src/nested/b.txt', 'src-old/c.txt'] }),
      readFile: async (_dir, path) => ({ path, text: 'old', revision: 'r1', edit: { eligible: true, newline: 'none' } }),
      deleteFile: async (...args) => { calls.push(args); },
    },
  };
  const store = fixture.files = new FilesStore();
  await store.show('/repo');
  for (const path of ['src/a.txt', 'src/nested/b.txt', 'src-old/c.txt']) store.openFile(path, { keep: true });
  await tick();
  store.selectTab('src/a.txt');
  store.startEditing('src/a.txt');
  store.updateDraft('src/a.txt', 'draft');
  return { store, calls, fixture };
}

test('successful recursive delete disposes affected tabs/drafts only, fixes selection, expansion and saved state', async () => {
  const { store, calls } = await setup();
  store.setExpanded(new Set(['src', 'src/nested', 'src-old']));
  await store.delete('src');
  assert.deepEqual(calls, [['/repo', 'src', '']]);
  assert.deepEqual(store.tabs.map(t => t.path), ['src-old/c.txt']);
  assert.equal(store.selected, 'src-old/c.txt');
  assert.deepEqual([...store.expanded], ['src-old']);
  store.close();
  await store.show('/repo');
  assert.deepEqual(store.tabs.map(t => t.path), ['src-old/c.txt']);
  assert.equal(store.selected, 'src-old/c.txt');
});

test('partial recursive failure keeps draft and tab identities, refreshes only listing and warns', async () => {
  const { store, fixture } = await setup();
  const tabs = [...store.tabs], edit = tabs[0].edit;
  let lists = 0, reads = 0;
  fixture.api.listFiles = async () => { lists++; return { root: '/repo', files: ['src/a.txt'] }; };
  fixture.api.readFile = async () => { reads++; throw new Error('must not reload'); };
  fixture.api.deleteFile = async () => { throw new Error('permission denied'); };
  await assert.rejects(store.delete('src'), /permission denied.*Some contents may already have been removed/);
  assert.deepEqual(store.tabs, tabs);
  assert.equal(store.tabs[0].edit, edit);
  assert.equal(edit.draft, 'draft');
  assert.equal(store.selected, 'src/a.txt');
  assert.equal(lists, 1);
  assert.equal(reads, 0);
  assert.deepEqual(store.listing.files, ['src/a.txt']);
  assert.equal(store.deleteUncertain, null);
});

test('pending/uncertain saves block deletion; in-flight delete blocks saves, rename, close, reset and another delete', async () => {
  const { store, fixture, calls } = await setup();
  const edit = store.activeTab.edit;
  edit.saving = true;
  await assert.rejects(store.delete('src'), /pending saves/);
  edit.saving = false;
  edit.uncertain = {};
  await assert.rejects(store.delete('src'), /unknown save/);
  edit.uncertain = null;
  const pending = deferred();
  fixture.api.deleteFile = () => pending.promise;
  const deletion = store.delete('src');
  assert.equal(await store.saveEdited('src/a.txt'), false);
  await assert.rejects(store.rename('src', 'lib'), /already in progress/);
  await assert.rejects(store.delete('src'), /already in progress/);
  store.close();
  assert.equal(store.dir, '/repo');
  assert.equal(store.reset(), false);
  pending.resolve();
  await deletion;
  assert.equal(calls.length, 0);
});

test('stale successful deletion preserves drafts and blocks all subsequent writes to old paths', async () => {
  const { store, fixture } = await setup();
  const pending = deferred();
  fixture.api.deleteFile = () => pending.promise;
  const deletion = store.delete('src');
  bumpEpoch();
  pending.resolve();
  await assert.rejects(deletion, /outcome is unknown/);
  assert.equal(store.activeTab.edit.draft, 'draft');
  assert.equal(await store.saveEdited('src/a.txt'), false);
  assert.equal(await store.discardEdited('src/a.txt'), false);
  await assert.rejects(store.rename('src', 'lib'), /outcome is unknown/);
  await assert.rejects(store.delete('src'), /outcome is unknown/);
});

test('late reads cannot repopulate tabs removed by successful deletion', async () => {
  const { store, fixture } = await setup();
  const pending = deferred();
  fixture.api.readFile = () => pending.promise;
  const refresh = store.refresh();
  const removed = store.tabs.find(t => t.path === 'src/nested/b.txt');
  await store.delete('src');
  pending.resolve({ path: 'src/nested/b.txt', text: 'stale' });
  await refresh;
  await tick();
  assert.equal(store.tabs.includes(removed), false);
  assert.equal(removed.content.text, 'old');
});

test('invalid or root delete never reaches backend', async () => {
  const { store, calls } = await setup();
  for (const path of ['', '.', '..', '../outside', '/absolute', 'src/../other', 'src\\a', 'src//a']) {
    await assert.rejects(store.delete(path), /non-root/);
  }
  assert.deepEqual(calls, []);
});

test('confirmation warns of recursive permanent deletion and draft count; cancel retains everything', async () => {
  const { store, calls, fixture } = await setup();
  const decision = deferred();
  let prompt;
  fixture.filePrompt = { ask: (...args) => { prompt = args; return decision.promise; } };
  const actions = new FileActions();
  const deletion = actions.delete('src', true);
  assert.equal(actions.busy, true);
  assert.match(prompt[0], /directory/);
  assert.match(prompt[1], /recursively/);
  assert.match(prompt[1], /1 affected unsaved draft/);
  assert.match(prompt[1], /cannot be undone/);
  assert.equal(prompt[2][0].danger, true);
  assert.equal(await actions.closeTabs(['src/a.txt']), false);
  assert.equal(await actions.saveInPlace(store.activeTab), false);
  await assert.rejects(actions.rename('src', 'lib'), /in progress/);
  decision.resolve(null);
  assert.equal(await deletion, false);
  assert.equal(actions.busy, false);
  assert.equal(store.activeTab.edit.draft, 'draft');
  assert.deepEqual(calls, []);
});

test('confirmed deletion is routed through coordinator, and reconnect invalidates old confirmation', async () => {
  const { fixture, calls } = await setup();
  fixture.filePrompt = { ask: async () => 'delete' };
  assert.equal(await new FileActions().delete('src', true), true);
  assert.equal(calls.length, 1);
  const next = await setup();
  next.fixture.filePrompt = { ask: async () => { bumpEpoch(); return 'delete'; } };
  await assert.rejects(new FileActions().delete('src', true), /connection changed/);
  assert.deepEqual(next.calls, []);
});

test('Go to File context menu offers Delete only for non-root items through the coordinator', async () => {
  const source = await readFile(new URL('../src/lib/components/common/GoToFileDialog.svelte', import.meta.url), 'utf8');
  assert.match(source, /if \(path\) items\.push\(\{\s*label: 'Delete…'/);
  assert.match(source, /onSelect: \(\) => void deleteItem\(path, directory\)/);
  assert.match(source, /await fileActions\.delete\(path, directory\)/);
});

test('native DeleteFile forwards captured host and normalizes errors', async () => {
  const previous = globalThis.window;
  const calls = [];
  globalThis.window = { go: { app: { App: { DeleteFile: async (...args) => { calls.push(args); } } } } };
  try {
    await createAPI().deleteFile('/repo', 'src/a', 'remote');
    assert.deepEqual(calls, [['/repo', 'src/a', 'remote']]);
    globalThis.window.go.app.App.DeleteFile = async () => { throw 'permission denied'; };
    await assert.rejects(createAPI().deleteFile('/repo', 'a', ''), /permission denied/);
  } finally { globalThis.window = previous; }
});

test('mock deletion removes recursive listings and ignored descendants with prefix boundaries', async () => {
  const mock = new MockBackendAPI();
  await assert.rejects(mock.deleteFile('/repo', '', ''), /Invalid path/);
  await mock.deleteFile('/repo', 'frontend', '');
  const listing = await mock.listFiles('/repo');
  assert.ok(!listing.files.some(p => p.startsWith('frontend/')));
  assert.ok(!listing.ignored.some(p => p.startsWith('frontend/')));
  assert.ok(listing.files.includes('README.md'));
});
