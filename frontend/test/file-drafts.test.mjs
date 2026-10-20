import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule } from './helpers.mjs';
import { bumpEpoch, StaleReplyError } from '../src/lib/link.ts';
import { editorText, serializedEditSize } from '../src/lib/fileEditing.ts';

const url = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const lib = (rel) => JSON.stringify(new URL(rel, url).href);
const { FilesStore, tabHasUnsaved } = await loadServerModule(url, [
  ["import { api } from '../api';", fixtureStore('api', '__draftFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__draftFixture')],
  ["'../manage'", lib('../manage.ts')],
  ["'../link'", lib('../link.ts')],
  ["'../files'", lib('../files.ts')],
  ["'../fileEditing'", lib('../fileEditing.ts')],
]);
const content = (text = 'old\n', revision = 'r1', edit = {}) => ({
  path: 'a.txt', text, revision, size: new TextEncoder().encode(text).length,
  edit: { eligible: true, newline: 'lf', ...edit },
});
const tick = () => new Promise((r) => setImmediate(r));
function deferred() {
  let resolve, reject;
  const promise = new Promise((a, b) => { resolve = a; reject = b; });
  return { promise, resolve, reject };
}
async function setup(initial = content()) {
  const fixture = globalThis.__draftFixture = {
    link: { dataHost: undefined, blockedReason: null },
    api: {
      listFiles: async (dir) => ({ root: dir, files: ['a.txt', 'b.txt'] }),
      readFile: async () => initial,
      saveFile: async (_dir, req) => ({ status: 'saved', content: content(req.text, 'r2') }),
    },
  };
  const store = new FilesStore();
  await store.show('/p');
  store.openFile('a.txt');
  await tick();
  return { store, fixture, tab: store.activeTab };
}

test('editor text preserves intended newline/BOM and serialized limits include disk expansion', () => {
  assert.equal(editorText(content('\uFEFFone\r\n\uFEFFtwo\r\n', 'r', { bom: true, newline: 'crlf' })), 'one\n\uFEFFtwo\n');
  assert.equal(editorText(content('\uFEFFx', 'r', { bom: false })), '\uFEFFx');
  assert.equal(serializedEditSize('é\n', { eligible: true, bom: true, newline: 'crlf' }), 7);
  assert.equal(serializedEditSize('x'.repeat(499_997), { eligible: true, bom: true, newline: 'lf' }), 500_000);
  for (const text of ['x'.repeat(499_998), '\0', '\r', '\ud800', '\udc00']) {
    assert.throws(() => serializedEditSize(text, { eligible: true, bom: true, newline: 'lf' }));
  }
  assert.equal(serializedEditSize('😀', { eligible: true, newline: 'none' }), 4);
  assert.throws(() => serializedEditSize('\n'.repeat(250_001), { eligible: true, newline: 'crlf' }));
});

test('editing promotes preview, invalidates pending reads, retains clean history on refresh/reopen', async () => {
  const { store, fixture, tab } = await setup();
  const pending = deferred();
  let reads = 0;
  fixture.api.readFile = () => { reads++; return pending.promise; };
  await store.refresh();
  assert.equal(reads, 1);
  assert.equal(store.startEditing('a.txt'), true);
  const edit = tab.edit;
  assert.equal(tab.preview, false);
  pending.resolve(content('external', 'external'));
  await tick();
  assert.equal(tab.content.text, 'old\n');
  assert.equal(edit.draft, 'old\n');
  store.updateDraft(tab, 'new');
  assert.equal(edit.dirty, true);
  store.updateDraft(tab, 'old\n');
  assert.equal(edit.dirty, false);
  await store.refresh();
  store.close();
  await store.show('/p');
  await tick();
  assert.equal(reads, 1);
  assert.equal(store.activeTab.edit, edit);
  store.openFile('b.txt');
  assert.equal(store.tabs[0], tab);
});

test('save snapshots remain attached to hidden/cached tab and later typing stays dirty', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'submitted');
  const pending = deferred();
  let request;
  fixture.api.saveFile = (dir, req, host) => { request = { dir, host, ...req }; return pending.promise; };
  const saving = store.saveEdited(tab);
  assert.equal(tab.edit.saving, true);
  assert.equal(store.closeTab(tab.path), false);
  assert.equal(store.reset(), false);
  store.updateDraft(tab, 'later typing');
  store.close();
  await store.show('/other');
  assert.equal(store.hasUnsaved, true);
  assert.equal(store.draftTargets[0].dir, '/p');
  pending.resolve({ status: 'saved', content: content('submitted', 'r2') });
  assert.equal(await saving, true);
  assert.deepEqual(request, { dir: '/p', host: undefined, path: 'a.txt', text: 'submitted', expectedRevision: 'r1' });
  assert.equal(tab.edit.baseline, 'submitted');
  assert.equal(tab.edit.draft, 'later typing');
  assert.equal(tab.edit.dirty, true);
  assert.equal(tab.content.revision, 'r2');
  await store.show('/p');
  store.updateDraft(tab, 'submitted');
  assert.equal(store.hasUnsaved, false);
  assert.equal(store.closeTab(tab.path), true);
});

test('conflicts and failures retain drafts; reload errors and races never discard', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'draft');
  fixture.api.saveFile = async () => ({ status: 'conflict', observedRevision: 'disk' });
  assert.equal(await store.saveEdited(tab.path), false);
  assert.equal(tab.edit.conflict.observedRevision, 'disk');
  assert.equal(tab.edit.draft, 'draft');
  fixture.api.saveFile = async () => { throw new Error('disk full'); };
  assert.equal(await store.saveEdited(tab), false);
  assert.match(tab.edit.saveError, /disk full/);
  assert.equal(tab.edit.uncertain, null);
  const version = tab.edit.version;
  fixture.api.readFile = async () => { throw new Error('missing'); };
  assert.equal(await store.discardEdited(tab), false);
  assert.equal(tab.edit.version, version);
  assert.equal(tab.edit.draft, 'draft');
  const pending = deferred();
  fixture.api.readFile = () => pending.promise;
  const reload = store.discardEdited(tab);
  store.updateDraft(tab, 'changed');
  store.updateDraft(tab, 'draft'); // Even typing then undo must invalidate the reload.
  pending.resolve(content('disk', 'disk'));
  assert.equal(await reload, false);
  fixture.api.readFile = async () => content('disk', 'disk');
  assert.equal(await store.discardEdited(tab), true);
  assert.equal(tab.edit.draft, 'disk');
  assert.ok(tab.edit.version > version);
  assert.equal(store.hasUnsaved, false);
});

test('a save failing after writing began stays protected even when undone to the baseline', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'submitted');
  const pending = deferred();
  fixture.api.saveFile = () => pending.promise;
  const saving = store.saveEdited(tab);
  store.updateDraft(tab, 'old\n'); // undo back to the baseline while saving
  const reads = [];
  fixture.api.readFile = () => { const d = deferred(); reads.push(d); return d.promise; };
  pending.reject(new Error('the save failed after writing began; the file may have changed: sync target: EIO'));
  assert.equal(await saving, false);
  assert.equal(tab.edit.uncertain.text, 'submitted');
  assert.equal(tabHasUnsaved(tab), true);
  assert.equal(store.closeTab(tab.path), false);
  assert.equal(reads.length, 1, 'the outcome is reconciled by a read, never a replayed write');

  // A partial write is neither the old nor the submitted bytes: a conflict
  // that still protects the draft, the only intact copy.
  reads[0].resolve(content('subm', 'partial'));
  await tick();
  assert.equal(tab.edit.uncertain, null);
  assert.deepEqual(tab.edit.conflict, { observedRevision: 'partial' });
  assert.equal(tab.edit.draft, 'old\n');
  assert.equal(tabHasUnsaved(tab), true);
  assert.equal(store.closeTab(tab.path), false);

  // A plain refusal (nothing written) is not uncertain.
  fixture.api.saveFile = async () => { throw new Error('file is read-only'); };
  tab.edit.conflict = null;
  store.updateDraft(tab, 'again');
  assert.equal(await store.saveEdited(tab), false);
  assert.equal(tab.edit.uncertain, null);
});

test('host and epoch gates prevent writes to a different host and retain uncertain snapshots', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'submitted');
  let saves = 0;
  const pending = deferred();
  fixture.api.saveFile = () => { saves++; return pending.promise; };
  fixture.link.blockedReason = 'Offline';
  assert.equal(await store.saveEdited(tab), false);
  assert.equal(saves, 0);
  fixture.link.blockedReason = null;
  const saving = store.saveEdited(tab);
  store.updateDraft(tab, 'later');
  bumpEpoch();
  fixture.link.dataHost = 'other';
  pending.resolve({ status: 'saved', content: content('submitted', 'r2') });
  assert.equal(await saving, false);
  assert.equal(tab.edit.baseline, 'old\n');
  assert.deepEqual(tab.edit.uncertain, { text: 'submitted', expectedRevision: 'r1',
    format: { eligible: true, newline: 'lf' } });
  assert.equal(tab.edit.draft, 'later');
  assert.equal(tabHasUnsaved(tab), true);
  assert.equal(store.reset(), false);
  await store.show('/new-host');
  assert.equal(store.dir, '/p');
  assert.equal(await store.saveEdited(tab), false);
  assert.equal(saves, 1);
  fixture.link.dataHost = undefined;
  assert.equal(await store.discardEdited(tab), true);
  assert.equal(tab.edit.uncertain, null);
});

test('stale/disconnected errors preserve uncertain snapshot and block automatic retry', async () => {
  for (const err of [new StaleReplyError(), new Error('disconnected from remote host')]) {
    const { store, fixture, tab } = await setup();
    store.startEditing(tab.path);
    store.updateDraft(tab, 'draft');
    let saves = 0;
    fixture.api.saveFile = async () => { saves++; fixture.link.blockedReason = 'Offline'; throw err; };
    assert.equal(await store.saveEdited(tab), false);
    assert.equal(tab.edit.uncertain.text, 'draft');
    assert.equal(await store.saveEdited(tab), false);
    assert.equal(saves, 1);
  }
});

test('ineligible/old-server reads cannot edit; invalid snapshots never reach transport', async () => {
  for (const initial of [{ path: 'a.txt', text: '', size: 0 }, content('x', 'r', { eligible: false })]) {
    const { store } = await setup(initial);
    assert.equal(store.startEditing('a.txt'), false);
  }
  const { store, fixture, tab } = await setup(content('', 'r'));
  assert.equal(store.startEditing(tab.path), true);
  fixture.api.saveFile = async () => { assert.fail('invalid snapshot sent'); };
  store.updateDraft(tab, 'x'.repeat(500_001));
  assert.equal(await store.saveEdited(tab), false);
  assert.match(tab.edit.saveError, /too large/);
  assert.equal(store.closeTab(tab.path), false);
  store.close();
  assert.equal(store.reset(), false, 'hidden dirty directories also prevent reset');
});

async function uncertainSave(store, fixture, tab, text) {
  store.startEditing(tab.path);
  store.updateDraft(tab, text);
  fixture.api.saveFile = async () => {
    fixture.link.blockedReason = 'Offline';
    throw new StaleReplyError();
  };
  assert.equal(await store.saveEdited(tab), false);
  fixture.link.blockedReason = null;
}

test('reconnect acknowledges exact BOM/CRLF snapshot without replacing later typing or history', async () => {
  const { store, fixture, tab } = await setup(content('\uFEFFold\r\n', 'r1', { bom: true, newline: 'crlf' }));
  await uncertainSave(store, fixture, tab, 'submitted\n');
  const version = tab.edit.version;
  store.updateDraft(tab, 'later');
  fixture.api.readFile = async () => content('\uFEFFsubmitted\r\n', 'disk', { bom: true, newline: 'crlf' });
  fixture.api.saveFile = () => assert.fail('recovery must never replay a write');
  await store.connectionChanged();
  assert.equal(tab.edit.uncertain, null);
  assert.equal(tab.edit.baseline, 'submitted\n');
  assert.equal(tab.edit.revision, 'disk');
  assert.equal(tab.edit.draft, 'later');
  assert.equal(tab.edit.version, version);
  assert.equal(tab.edit.dirty, true);
});

test('normalized equality is not evidence of a save; unsupported/error reads stay uncertain', async () => {
  const { store, fixture, tab } = await setup(content('\uFEFFold\r\n', 'r1', { bom: true, newline: 'crlf' }));
  await uncertainSave(store, fixture, tab, 'submitted\n');
  for (const read of [
    async () => { throw new Error('offline'); },
    async () => ({ path: 'a.txt', text: 'submitted\n' }),
    async () => content('submitted\n', 'disk', { newline: undefined }),
    async () => content('submitted\n', 'read-only', { eligible: false }),
  ]) {
    fixture.api.readFile = read;
    await store.connectionChanged();
    assert.ok(tab.edit.uncertain);
    assert.equal(tab.edit.baseline, 'old\n');
  }
  fixture.api.readFile = async () => content('submitted\n', 'external');
  await store.connectionChanged();
  assert.equal(tab.edit.uncertain, null);
  assert.deepEqual(tab.edit.conflict, { observedRevision: 'external' });
  assert.equal(tab.edit.baseline, 'old\n');
  assert.equal(tab.edit.draft, 'submitted\n');
});

test('save reply becoming uncertain after reconnect callback still gets reconciled', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'submitted');
  const pending = deferred();
  fixture.api.saveFile = () => pending.promise;
  const saving = store.saveEdited(tab);
  bumpEpoch();
  let reads = 0;
  fixture.api.readFile = async () => { reads++; return content('submitted', 'disk'); };
  await store.connectionChanged();
  assert.equal(reads, 0, 'in-flight save is not reconciled prematurely');
  store.updateDraft(tab, 'later');
  pending.reject(new StaleReplyError());
  assert.equal(await saving, false);
  await tick();
  assert.equal(reads, 1);
  assert.equal(tab.edit.baseline, 'submitted');
  assert.equal(tab.edit.draft, 'later');
  assert.equal(tab.edit.uncertain, null);
});

test('same-host reconnect retains clean editors, hidden caches, selection and invalidates late reads', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  const edit = tab.edit;
  store.expanded = new Set(['folder']);
  store.close();
  await store.show('/other');
  store.openFile('b.txt');
  await tick();
  const viewer = store.activeTab;
  const late = deferred();
  fixture.api.readFile = () => late.promise;
  await store.refresh();
  let reads = 0;
  fixture.api.readFile = async () => { reads++; return content('fresh', 'fresh'); };
  await store.connectionChanged();
  assert.equal(reads, 1);
  assert.equal(store.activeTab, viewer);
  assert.equal(store.selected, 'b.txt');
  late.resolve(content('stale', 'stale'));
  await tick();
  assert.equal(viewer.content.text, 'fresh');
  await store.show('/p');
  assert.equal(store.activeTab, tab);
  assert.equal(tab.edit, edit);
  assert.deepEqual([...store.expanded], ['folder']);
  assert.equal(reads, 1);
});

test('reconnect settles hidden uncertain drafts without opening their directory', async () => {
  const { store, fixture, tab } = await setup();
  await uncertainSave(store, fixture, tab, 'submitted');
  const version = tab.edit.version;
  store.close();
  fixture.api.readFile = async (dir, path) => {
    assert.equal(dir, '/p');
    assert.equal(path, 'a.txt');
    return content('submitted', 'settled');
  };
  fixture.api.listFiles = () => assert.fail('closed dialog must not refresh listing');
  await store.connectionChanged();
  assert.equal(store.open, false);
  assert.equal(tab.edit.uncertain, null);
  assert.equal(tab.edit.baseline, 'submitted');
  assert.equal(tab.edit.version, version);
  assert.equal(store.hasUnsaved, false);
});

test('different-host reconnect refuses unresolved hidden drafts but resets clean caches', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'draft');
  store.close();
  fixture.link.dataHost = 'other';
  await store.connectionChanged('other');
  assert.equal(store.draftTargets[0].tab, tab);
  assert.equal(store.draftTargets[0].host, undefined);
  assert.equal(await store.saveEdited(tab), false);
  store.updateDraft(tab, tab.edit.baseline); // Coordinator's explicit discard.
  await store.connectionChanged('other');
  assert.equal(store.draftTargets.length, 0);
  await store.show('/p');
  assert.equal(store.tabs.length, 0);
});

test('confirmed overwrite stays revision checked and can conflict again; successful save refreshes list only', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'draft');
  let request;
  fixture.api.saveFile = async (_dir, req) => {
    request = req;
    return { status: 'conflict', observedRevision: 'newer' };
  };
  assert.equal(await store.saveEdited(tab, 'confirmed-disk'), false);
  assert.equal(request.expectedRevision, 'confirmed-disk');
  assert.deepEqual(tab.edit.conflict, { observedRevision: 'newer' });
  assert.equal(tab.edit.revision, 'r1');
  assert.equal(tab.edit.draft, 'draft');
  let lists = 0;
  fixture.api.listFiles = async () => { lists++; throw new Error('list failed'); };
  fixture.api.readFile = () => assert.fail('save must not reread tabs');
  fixture.api.saveFile = async () => ({ status: 'saved', content: content('draft', 'saved') });
  assert.equal(await store.saveEdited(tab, 'newer'), true);
  await tick();
  assert.equal(lists, 1);
  assert.equal(tab.edit.saveError, null);
  assert.equal(tab.edit.baseline, 'draft');
  assert.match(store.listError, /list failed/);
});
