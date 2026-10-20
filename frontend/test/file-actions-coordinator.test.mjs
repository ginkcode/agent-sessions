import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule } from './helpers.mjs';

// The real dirty predicate, so the coordinator is tested against the same
// rule the files store uses; its own api/link imports are never exercised.
const filesURL = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const filesLib = (rel) => JSON.stringify(new URL(rel, filesURL).href);
const { tabHasUnsaved } = await loadServerModule(filesURL, [
  ["import { api } from '../api';", fixtureStore('api', '__coordinatorUnused')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__coordinatorUnused')],
  ["'../manage'", filesLib('../manage.ts')],
  ["'../link'", filesLib('../link.ts')],
  ["'../files'", filesLib('../files.ts')],
  ["'../fileEditing'", filesLib('../fileEditing.ts')],
]);
globalThis.__coordinatorTabHasUnsaved = tabHasUnsaved;

const { FileActions } = await loadServerModule(new URL('../src/lib/stores/fileActions.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__actionsFixture')],
  ["import { files, tabHasUnsaved } from './files.svelte';",
    `${fixtureStore('files', '__actionsFixture')} const tabHasUnsaved = globalThis.__coordinatorTabHasUnsaved;`],
  ["import { filePrompt } from './filePrompt.svelte';", fixtureStore('filePrompt', '__actionsFixture')],
]);

const tick = () => new Promise((r) => setImmediate(r));
function deferred() {
  let resolve, reject;
  const promise = new Promise((a, b) => { resolve = a; reject = b; });
  return { promise, resolve, reject };
}

function tab(path, draft = 'draft', baseline = 'base') {
  return {
    path, preview: false, content: null, loading: false, error: null, token: 0,
    edit: { editing: true, baseline, revision: 'r1', draft, saving: false, saveError: null, conflict: null, uncertain: null, version: 0 },
  };
}

// Fixtures record every call; prompts stay pending until the test answers.
function setup(tabs) {
  const calls = { closeTab: [], saveEdited: [], discardEdited: [], completeFileClose: [] };
  const prompts = [];
  const fixture = globalThis.__actionsFixture = {
    calls,
    prompts,
    api: {
      completeFileClose: async (id, allow) => { calls.completeFileClose.push([id, allow]); },
    },
    files: {
      tabs,
      get draftTargets() { return this.tabs.filter(t => t.edit).map(t => ({ host: undefined, dir: '/p', tab: t })); },
      // The proxy is the receiver, so mutate the fixture explicitly.
      closeTab(path) { calls.closeTab.push(path); fixture.files.tabs = fixture.files.tabs.filter(t => t.path !== path); return true; },
      saveEdited: async (t, rev) => { calls.saveEdited.push([t.path, rev]); t.edit.baseline = t.edit.draft; return true; },
      discardEdited: async (t) => { calls.discardEdited.push(t.path); t.edit.draft = t.edit.baseline; return true; },
    },
    filePrompt: {
      ask(title, message, choices) {
        const d = deferred();
        prompts.push({ title, message, choices, answer: d.resolve });
        return d.promise;
      },
    },
  };
  return { actions: new FileActions(), fixture, calls, prompts };
}

async function prompt(prompts, n) {
  for (let i = 0; i < 20 && prompts.length < n; i++) await tick();
  assert.equal(prompts.length, n, `expected prompt #${n}`);
  return prompts[n - 1];
}

test('bulk close cancelled leaves every tab and draft in place', async () => {
  const a = tab('a.txt', 'A'), b = tab('b.txt', 'B');
  const { actions, fixture, calls, prompts } = setup([a, b]);
  const closing = actions.closeTabs(['a.txt', 'b.txt']);
  const p = await prompt(prompts, 1);
  assert.equal(actions.busy, true);
  assert.match(p.message, /\/p\/a\.txt\n\/p\/b\.txt/);
  p.answer(null);
  assert.equal(await closing, false);
  assert.deepEqual(calls.closeTab, []);
  assert.deepEqual(calls.saveEdited, []);
  assert.deepEqual(fixture.files.tabs, [a, b]);
  assert.equal(a.edit.draft, 'A');
  assert.equal(b.edit.draft, 'B');
  assert.equal(actions.busy, false);
});

test('save failure during close keeps drafts and closes nothing', async () => {
  const a = tab('a.txt', 'A'), b = tab('b.txt', 'B');
  const { actions, fixture, calls, prompts } = setup([a, b]);
  fixture.files.saveEdited = async (t, rev) => { calls.saveEdited.push([t.path, rev]); return false; };
  const closing = actions.closeTabs(['a.txt', 'b.txt']);
  (await prompt(prompts, 1)).answer('save');
  assert.equal(await closing, false);
  assert.deepEqual(calls.saveEdited, [['a.txt', undefined]], 'stops at the first failed save');
  assert.deepEqual(calls.closeTab, []);
  assert.equal(a.edit.draft, 'A');
  assert.equal(b.edit.draft, 'B');

  // A save that reports success while the tab is still dirty (typing during
  // the request) must not close it either.
  fixture.files.saveEdited = async (t, rev) => { calls.saveEdited.push([t.path, rev]); t.edit.baseline = 'other'; return true; };
  const again = actions.closeTabs(['a.txt']);
  (await prompt(prompts, 2)).answer('save');
  assert.equal(await again, false);
  assert.deepEqual(calls.closeTab, []);
  assert.equal(a.edit.draft, 'A');
  assert.equal(actions.busy, false);
});

test('discard closes only the targeted tabs and resets only their drafts', async () => {
  const a = tab('a.txt', 'A'), b = tab('b.txt', 'B'), c = tab('c.txt', 'C');
  a.edit.uncertain = { text: 'A', expectedRevision: 'r1' };
  const { actions, fixture, calls, prompts } = setup([a, b, c]);
  const closing = actions.closeTabs(['a.txt', 'b.txt']);
  const p = await prompt(prompts, 1);
  assert.doesNotMatch(p.message, /c\.txt/);
  p.answer('discard');
  assert.equal(await closing, true);
  assert.deepEqual(calls.closeTab, ['a.txt', 'b.txt']);
  assert.deepEqual(calls.discardEdited, [], 'discard authorizes disposal, not a disk reload');
  assert.deepEqual(fixture.files.tabs, [c]);
  for (const t of [a, b]) {
    assert.equal(t.edit.draft, 'base');
    assert.equal(t.edit.uncertain, null);
    assert.equal(t.edit.version, 1);
  }
  assert.equal(c.edit.draft, 'C');
  assert.equal(c.edit.version, 0);
  assert.equal(actions.busy, false);
});

test('changeHost stays busy through the async action and cancels routing on null', async () => {
  const a = tab('a.txt', 'A');
  const { actions, calls, prompts } = setup([a]);
  let routed = 0;
  const route = deferred();
  const switching = actions.changeHost(() => { routed++; return route.promise; });
  (await prompt(prompts, 1)).answer('discard');
  await tick();
  assert.equal(routed, 1);
  assert.equal(actions.busy, true, 'input stays frozen until routing has changed');
  assert.equal(await actions.closeTabs(['a.txt']), false);
  assert.equal(await actions.save(a), false);
  assert.equal(await actions.changeHost(async () => { routed++; }), false);
  assert.equal(prompts.length, 1);
  assert.deepEqual(calls.saveEdited, []);
  route.resolve();
  assert.equal(await switching, true);
  assert.equal(routed, 1);
  assert.equal(actions.busy, false);

  const b = tab('b.txt', 'B');
  const second = setup([b]);
  let called = false;
  const cancelled = second.actions.changeHost(async () => { called = true; });
  (await prompt(second.prompts, 1)).answer(null);
  assert.equal(await cancelled, false);
  assert.equal(called, false);
  assert.equal(b.edit.draft, 'B');
  assert.equal(second.actions.busy, false);
});

test('nativeClose: approval freezes for quit, cancel replies false, duplicates while busy reply false', async () => {
  const a = tab('a.txt', 'A');
  const { actions, calls, prompts } = setup([a]);

  // Duplicate/stale request while the first is still deciding.
  const first = actions.nativeClose(1);
  await prompt(prompts, 1);
  assert.equal(actions.busy, true);
  await actions.nativeClose(2);
  assert.deepEqual(calls.completeFileClose, [[2, false]]);
  assert.equal(actions.busy, true, 'a rejected duplicate does not release the freeze');
  assert.equal(prompts.length, 1);

  // Cancel.
  prompts[0].answer(null);
  await first;
  assert.deepEqual(calls.completeFileClose, [[2, false], [1, false]]);
  assert.equal(actions.quitting, false);
  assert.equal(actions.busy, false);
  assert.equal(a.edit.draft, 'A');

  // Approval.
  const approved = actions.nativeClose(3);
  (await prompt(prompts, 2)).answer('discard');
  await approved;
  assert.deepEqual(calls.completeFileClose.at(-1), [3, true]);
  assert.equal(actions.quitting, true);
  assert.equal(actions.busy, false);
  assert.equal(await actions.closeTabs(['a.txt']), false);
  assert.equal(await actions.done(a), false);
  assert.equal(await actions.discard(a), false);
  assert.equal(await actions.save(a), false);
  assert.equal(await actions.changeHost(async () => assert.fail('routed while quitting')), false);
  await actions.nativeClose(4);
  assert.deepEqual(calls.completeFileClose.at(-1), [4, false]);
  assert.equal(prompts.length, 2);
  assert.deepEqual(calls.closeTab, []);
  assert.deepEqual(calls.saveEdited, []);
});

test('nativeClose reply failure unfreezes and reports refusal', async () => {
  const { actions, fixture, calls } = setup([tab('a.txt', 'same', 'same')]);
  fixture.api.completeFileClose = async (id, allow) => {
    calls.completeFileClose.push([id, allow]);
    if (allow) throw new Error('gone');
  };
  await actions.nativeClose(5);
  assert.deepEqual(calls.completeFileClose, [[5, true], [5, false]]);
  assert.equal(actions.quitting, false);
  assert.equal(actions.busy, false);
});

test('conflict save needs two confirmations, passes the observed revision, and keeps a second conflict', async () => {
  const a = tab('a.txt', 'mine');
  const { actions, fixture, calls, prompts } = setup([a]);
  let conflicts = 0;
  fixture.files.saveEdited = async (t, rev) => {
    calls.saveEdited.push([t.path, rev]);
    t.edit.conflict = { observedRevision: `disk${++conflicts}` };
    return false;
  };
  const saving = actions.save(a);
  const first = await prompt(prompts, 1);
  assert.equal(actions.busy, true);
  assert.deepEqual(first.choices.map(c => c.value), ['reload', 'overwrite']);
  first.answer('overwrite');
  const second = await prompt(prompts, 2);
  assert.equal(calls.saveEdited.length, 1, 'no overwrite before the second confirmation');
  assert.deepEqual(second.choices.map(c => c.value), ['overwrite']);
  second.answer('overwrite');
  assert.equal(await saving, false);
  assert.deepEqual(calls.saveEdited, [['a.txt', undefined], ['a.txt', 'disk1']]);
  assert.deepEqual(a.edit.conflict, { observedRevision: 'disk2' }, 'second conflict stays for a fresh decision');
  assert.equal(a.edit.draft, 'mine');
  assert.equal(prompts.length, 2);
  assert.equal(actions.busy, false);

  // Cancelling the confirmation writes nothing.
  const retry = actions.save(a);
  (await prompt(prompts, 3)).answer('overwrite');
  (await prompt(prompts, 4)).answer(null);
  assert.equal(await retry, false);
  assert.deepEqual(calls.saveEdited.at(-1), ['a.txt', undefined]);
  assert.equal(calls.saveEdited.length, 3);
  assert.equal(a.edit.draft, 'mine');
});
