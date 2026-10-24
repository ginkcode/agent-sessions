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

test('PF-03 listing baseline: same-open reuse, close/reopen refetch, refresh and root/host isolation', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'retained draft');
  store.expanded = new Set(['nested']);
  const requests = [];
  fixture.api.listFiles = (dir) => {
    const reply = deferred();
    requests.push({ dir, host: fixture.link.dataHost, reply });
    return reply.promise;
  };
  await store.show('/p');
  assert.equal(requests.length, 0, 'already-open populated listing is reused');
  store.close();
  assert.equal(store.listing, null, 'close drops the listing, not drafts');
  const reopening = store.show('/p');
  await tick();
  assert.equal(requests.length, 1);
  assert.deepEqual(store.listing.files, ['a.txt', 'b.txt']);
  assert.equal(store.loadingList, false);
  assert.equal(store.refreshing, true);
  assert.equal(store.listingStale, true);
  assert.equal(store.activeTab, tab);
  assert.equal(tab.edit.draft, 'retained draft');
  assert.deepEqual([...store.expanded], ['nested']);
  const listing = { root: '/p', files: ['a.txt'], directories: ['empty'], truncated: true };
  requests[0].reply.resolve(listing);
  await reopening;
  assert.deepEqual(store.listing, listing);
  const refreshing = store.refresh();
  assert.equal(requests.length, 2);
  assert.deepEqual(store.listing, listing, 'refresh retains the old visible snapshot');
  store.close();
  const other = store.show('/other');
  assert.equal(requests.length, 3);
  requests[1].reply.resolve({ root: '/p', files: ['stale'] });
  await refreshing;
  assert.equal(store.listing, null, 'closed-root reply cannot populate another root');
  requests[2].reply.resolve({ root: '/other', files: ['other'] });
  await other;
  assert.deepEqual(store.listing.files, ['other']);
  assert.equal(store.draftTargets[0].tab, tab);
  store.updateDraft(tab, tab.edit.baseline);
  store.close();
  fixture.link.dataHost = 'remote';
  const remote = store.show('/other');
  assert.equal(requests.length, 4);
  assert.equal(store.listing, null, 'same path on another host is not reused');
  requests[3].reply.resolve({ root: '/other', files: ['remote'] });
  await remote;
  assert.deepEqual(requests.map(({ dir, host }) => [dir, host]), [
    ['/p', undefined], ['/p', undefined], ['/other', undefined], ['/other', 'remote'],
  ]);
  assert.deepEqual(store.listing.files, ['remote']);
});

test('PF-11 cached listing is immediate while probes and reads remain unresolved; refresh failure retains hints', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'draft');
  const edit = tab.edit;
  store.close();
  const probe = deferred(), read = deferred(), list = deferred();
  let probes = 0, reads = 0, lists = 0;
  fixture.api.probeFiles = () => { probes++; return probe.promise; };
  fixture.api.readFile = () => { reads++; return read.promise; };
  fixture.api.listFiles = () => { lists++; return list.promise; };
  const reopening = store.show('/p');
  assert.deepEqual(store.listing.files, ['a.txt', 'b.txt'], 'visible synchronously before any resolution');
  assert.equal(probes, 1);
  assert.equal(reads, 0);
  assert.equal(lists, 1, 'listing does not wait for editor reconciliation');
  list.reject(new Error('refresh failed'));
  probe.resolve({ observations: [{ path: 'a.txt', state: 'present' }] });
  await tick();
  assert.equal(reads, 1);
  read.resolve(content('changed', 'r2'));
  await reopening;
  assert.equal(store.listError, 'refresh failed');
  assert.equal(store.listingStale, true);
  assert.equal(store.refreshing, false);
  assert.equal(tab.edit, edit);
  assert.equal(edit.draft, 'draft');
  fixture.api.listFiles = async () => ({ root: '/p', files: ['replacement'], truncated: true });
  await store.refresh();
  assert.deepEqual(store.listing.files, ['replacement']);
  assert.equal(store.listing.truncated, true);
  assert.equal(store.listingStale, false);
  assert.equal(store.listError, null);
});

test('PF-11 listing LRU and snapshot size bounds do not evict saved editors or drafts', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  store.updateDraft(tab, 'kept across listing eviction');
  const edit = tab.edit;
  for (const root of ['/2', '/3', '/4']) await store.show(root);
  // Touch /p, making /2 the least recently used of the four snapshots.
  await store.show('/p');
  await store.show('/5');
  store.close();
  let pending = deferred();
  fixture.api.listFiles = () => pending.promise;
  let showing = store.show('/2');
  assert.equal(store.listing, null);
  assert.equal(store.loadingList, true);
  pending.resolve({ root: '/2', files: ['fresh'] });
  await showing;
  store.close();
  pending = deferred();
  showing = store.show('/p/');
  assert.deepEqual(store.listing.files, ['a.txt', 'b.txt'], 'trailing separator shares listing key');
  pending.resolve({ root: '/p/', files: ['x'.repeat(500_000)] });
  await showing;
  assert.equal(store.listing.files[0].length, 500_000, 'oversized result still displays');
  store.close();
  pending = deferred();
  showing = store.show('/p');
  assert.equal(store.listing, null, 'oversized replacement removes the older snapshot');
  assert.equal(store.activeTab, tab);
  assert.equal(tab.edit, edit);
  assert.equal(edit.draft, 'kept across listing eviction');
  pending.resolve({ root: '/p', files: ['fresh'] });
  await showing;
});

test('PF-11 Windows separator normalization preserves root and host isolation', async () => {
  const { store, fixture } = await setup();
  fixture.api.listFiles = async dir => ({ root: dir, files: [dir] });
  await store.show('C:\\work\\project\\');
  store.close();
  let pending = deferred();
  fixture.api.listFiles = () => pending.promise;
  let showing = store.show('C:/work/project');
  assert.deepEqual(store.listing.files, ['C:\\work\\project\\']);
  pending.resolve({ root: 'C:/work/project', files: ['windows'] });
  await showing;
  store.close();
  pending = deferred();
  showing = store.show('C:/work/other');
  assert.equal(store.listing, null);
  pending.resolve({ root: 'C:/work/other', files: ['other'] });
  await showing;
  store.close();
  pending = deferred();
  showing = store.show('C:/work/project');
  fixture.link.dataHost = 'remote';
  await store.connectionChanged('remote');
  pending.resolve({ root: 'C:/work/project', files: ['late local'] });
  await showing;
  assert.equal(store.listing, null, 'old host completion cannot publish');
  pending = deferred();
  showing = store.show('C:/work/project');
  assert.equal(store.listing, null, 'old host snapshot cannot be restored');
  pending.resolve({ root: 'C:/work/project', files: ['remote'] });
  await showing;
});

test('PF-11 snapshots own immutable listing data and reject old epoch and same-root replies', async () => {
  const { store, fixture } = await setup();
  const result = { root: '/p', files: ['owned'], directories: ['empty'], ignored: ['.env'], status: { owned: 'M' } };
  fixture.api.listFiles = async () => result;
  await store.refresh();
  result.files.push('mutated'); result.directories.push('mutated'); result.ignored.push('mutated'); result.status.owned = 'D';
  assert.deepEqual(store.listing.files, ['owned']);
  assert.deepEqual(store.listing.directories, ['empty']);
  assert.deepEqual(store.listing.ignored, ['.env']);
  assert.equal(store.listing.status.owned, 'M');
  assert.throws(() => store.listing.files.push('oops'), TypeError);
  const old = deferred(), fresh = deferred();
  fixture.api.listFiles = () => old.promise;
  const refreshing = store.refresh();
  store.close();
  fixture.api.listFiles = () => fresh.promise;
  const showing = store.show('/p');
  fresh.resolve({ root: '/p', files: ['new'] });
  await showing;
  old.resolve({ root: '/p', files: ['late'] });
  await refreshing;
  assert.deepEqual(store.listing.files, ['new']);
  const stale = deferred();
  fixture.api.listFiles = () => stale.promise;
  const interrupted = store.refresh();
  bumpEpoch();
  stale.resolve({ root: '/p', files: ['old epoch'] });
  await interrupted;
  assert.deepEqual(store.listing.files, ['new']);
  store.close();
  const reopening = store.show('/p');
  assert.deepEqual(store.listing.files, ['new'], 'stale reply did not poison retained snapshot');
  await reopening;
});

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
  const refreshing = store.refresh();
  assert.equal(reads, 1);
  assert.equal(store.startEditing('a.txt'), true);
  const edit = tab.edit;
  assert.equal(tab.preview, false);
  pending.resolve(content('external', 'external'));
  await refreshing; await tick();
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
  assert.equal(reads, 3); // refresh and reopen now reconcile saved context, even in edit mode.
  assert.equal(store.activeTab.edit, edit);
  store.openFile('b.txt');
  assert.equal(store.tabs[0], tab);
});

test('stopEditing drops the draft for the saved content and only rereads when asked', async () => {
  const { store, fixture, tab } = await setup();
  let reads = 0;
  fixture.api.readFile = async () => { reads++; return content('disk\n', 'r9'); };
  store.startEditing(tab.path);
  store.updateDraft(tab, 'draft');
  assert.equal(store.stopEditing(tab), true);
  assert.equal(tab.edit, undefined);
  assert.equal(tab.content.text, 'old\n', 'the viewer shows the saved baseline');
  assert.equal(store.hasUnsaved, false);
  await tick();
  assert.equal(reads, 0);

  store.startEditing(tab.path);
  assert.equal(store.stopEditing(tab, true), true);
  await tick();
  assert.equal(reads, 1);
  assert.equal(tab.content.text, 'disk\n');

  // A save in flight cannot be dropped.
  store.startEditing(tab.path);
  store.updateDraft(tab, 'x');
  fixture.api.saveFile = () => new Promise(() => {});
  void store.saveEdited(tab);
  assert.equal(store.stopEditing(tab), false);
  assert.ok(tab.edit);
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
  fixture.api.readFile = async () => content('submitted', 'r2');
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
  const refreshing = store.refresh();
  let reads = 0;
  fixture.api.readFile = async () => { reads++; return content('fresh', 'fresh'); };
  await store.connectionChanged();
  assert.equal(reads, 1);
  assert.equal(store.activeTab, viewer);
  assert.equal(store.selected, 'b.txt');
  late.resolve(content('stale', 'stale'));
  await refreshing; await tick();
  assert.equal(viewer.content.text, 'fresh');
  await store.show('/p');
  assert.equal(store.activeTab, tab);
  assert.equal(tab.edit, edit);
  assert.deepEqual([...store.expanded], ['folder']);
  assert.equal(reads, 2); // reopening the retained editor reconciles saved context
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

test('a replace in files shows in clean editors without resetting history; typing meanwhile is kept as a conflict', async () => {
  const { store, fixture, tab } = await setup();
  store.startEditing(tab.path);
  const edit = tab.edit, version = edit.version;
  fixture.api.readFile = async () => content('new\n', 'r2');
  await store.reloadReplaced(['a.txt', 'gone.txt']);
  assert.equal(edit.draft, 'new\n');
  assert.equal(edit.baseline, 'new\n');
  assert.equal(edit.revision, 'r2');
  assert.equal(edit.version, version, 'the editor takes the change as an undoable edit');
  assert.equal(tabHasUnsaved(tab), false);

  const pending = deferred();
  fixture.api.readFile = () => pending.promise;
  const reloading = store.reloadReplaced(['a.txt']);
  store.updateDraft(tab, 'typed');
  pending.resolve(content('newer\n', 'r3'));
  await reloading;
  assert.equal(edit.draft, 'typed', 'typing during the read is never thrown away');
  assert.deepEqual(edit.conflict, { observedRevision: 'r3' });

  store.updateDraft(tab, 'new\n');
  edit.conflict = null;
  fixture.api.readFile = async () => { throw new Error('gone'); };
  await store.reloadReplaced(['a.txt']);
  assert.equal(tab.error, 'gone');
  assert.equal(edit.draft, 'new\n');
});
