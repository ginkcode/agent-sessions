import test, { mock } from 'node:test';
import assert from 'node:assert/strict';
import * as cs from '../src/lib/contentSearch.ts';
import { StaleReplyError } from '../src/lib/link.ts';
import { fixtureStore, loadServerComponent, loadServerModule, renderWithFixture } from './helpers.mjs';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const file = (path, matches, extra = {}) => ({ path, revision: `r-${path}`, matches, ...extra });
const hit = (line, start, end, preview) => ({ line, start, end, preview, previewStart: start, previewEnd: end });

// Pure helpers.

test('patterns: literal text is escaped, whole word and case apply, bad regex is reported', () => {
  assert.equal(cs.searchRegExp({ pattern: 'a.b(' }).test('xa.b(y'), true);
  assert.equal(cs.searchRegExp({ pattern: 'a.b' }).test('axb'), false);
  assert.equal(cs.searchRegExp({ pattern: 'Foo' }).test('foo'), true);
  assert.equal(cs.searchRegExp({ pattern: 'Foo', matchCase: true }).test('foo'), false);
  assert.equal(cs.searchRegExp({ pattern: 'cat', wholeWord: true }).test('concat'), false);
  assert.equal(cs.searchRegExp({ pattern: 'cat|dog', regex: true, wholeWord: true }).test('hotdog dog'), true);
  assert.throws(() => cs.searchRegExp({ pattern: '(', regex: true }), e => e instanceof cs.SearchPatternError && /invalid search pattern/.test(e.message));
});

test('globs: names match any component, slashed globs anchor at the root, braces and negated classes', () => {
  const q = (include, exclude) => path => cs.searchesPath({ pattern: 'x', include, exclude }, path);
  assert.equal(q('*.ts')('src/a.ts'), true);
  assert.equal(q('*.ts')('src/a.tsx'), false);
  assert.equal(q('src/**')('src/deep/a.go'), true);
  assert.equal(q('src/**')('lib/src/a.go'), false);
  assert.equal(q('src')('lib/src/a.go'), true, 'a bare name is any component');
  assert.equal(q('*.{ts,go}')('a.go'), true);
  assert.equal(q('', 'vendor/, *.min.js')('vendor/x.js'), false);
  assert.equal(q('', 'vendor/, *.min.js')('web/app.min.js'), false);
  assert.equal(q('', 'vendor/, *.min.js')('web/app.js'), true);
  assert.equal(q('[!a]*.md')('b.md'), true);
  assert.equal(q('[!a]*.md')('a.md'), false);
});

test('text search: UTF-16 columns, CRLF lines, empty matches skipped, long lines cut around the match', () => {
  const f = cs.searchText('a.txt', '😀 foo\r\nbar foo foo\n', cs.searchRegExp({ pattern: 'foo' }));
  assert.deepEqual(f.matches.map(m => [m.line, m.start, m.end]), [[1, 3, 6], [2, 4, 7], [2, 8, 11]]);
  assert.equal(cs.matchedText(f.matches[0]), 'foo');
  assert.equal(f.matches[0].preview, '😀 foo', 'the carriage return is not part of the line');
  assert.equal(cs.searchText('a', 'abc', cs.searchRegExp({ pattern: 'x*', regex: true })).matches.length, 0);
  const long = `${'x'.repeat(500)}needle${'y'.repeat(500)}`;
  const [m] = cs.searchText('a', `    ${long}`, cs.searchRegExp({ pattern: 'needle' })).matches;
  assert.ok(m.preview.length <= 240 && m.previewStart === 60, 'previews keep 60 characters before the match');
  assert.equal(m.preview.slice(m.previewStart, m.previewEnd), 'needle');
  assert.deepEqual(cs.matchRange(m), { start: { line: 0, character: 504 }, end: { line: 0, character: 510 } });
  const many = cs.searchText('a', 'x\n'.repeat(cs.MAX_FILE_MATCHES + 5), cs.searchRegExp({ pattern: 'x' }));
  assert.equal(many.matches.length, cs.MAX_FILE_MATCHES);
  assert.equal(many.truncated, true);
});

test('draft overlay replaces, adds and removes drafted files and recounts', () => {
  const host = { files: [file('a.go', [hit(1, 0, 3, 'foo')]), file('b.go', [hit(1, 0, 3, 'foo')])], matches: 2, searched: 9 };
  const q = { pattern: 'foo' };
  const { result } = cs.overlayDrafts(host, [
    { path: 'b.go', text: 'nothing here' },
    { path: 'c.go', text: 'foo foo' },
    { path: 'a.go', text: 'x\nfoo' },
  ], q);
  assert.deepEqual(result.files.map(f => [f.path, f.matches.length, !!f.draft]), [['a.go', 1, true], ['c.go', 2, true]]);
  assert.equal(result.matches, 3);
  assert.equal(result.files[0].revision, 'r-a.go');
  assert.equal(result.searched, 9);
  // Drafts outside the globs keep the host's answer; an unreadable pattern leaves them unsearched.
  assert.equal(cs.overlayDrafts(host, [{ path: 'b.go', text: '' }], { pattern: 'foo', include: '*.md' }).result, host);
  const bad = cs.overlayDrafts(host, [{ path: 'b.go', text: '' }], { pattern: '(?<=x', regex: true });
  assert.equal(bad.result, host);
  assert.deepEqual(bad.unsearched, ['b.go']);
});

test('summary, match order and stepping wrap through the visible matches', () => {
  const result = { files: [file('a', [hit(1, 0, 1, 'a'), hit(2, 0, 1, 'a')]), file('b', [hit(1, 0, 1, 'b')])], matches: 3, truncated: false };
  assert.equal(cs.searchSummary(result, 5000), '3 results in 2 files');
  assert.equal(cs.searchSummary({ files: [result.files[1]], matches: 1, truncated: true }, 5000), '1 result in 1 file (limited to 5,000)');
  const order = cs.matchOrder(result);
  assert.deepEqual(order, [{ path: 'a', index: 0 }, { path: 'a', index: 1 }, { path: 'b', index: 0 }]);
  assert.deepEqual(cs.matchOrder(result, new Set(['a'])), [{ path: 'b', index: 0 }]);
  assert.deepEqual(cs.stepMatch(order, null, 1), order[0]);
  assert.deepEqual(cs.stepMatch(order, null, -1), order[2]);
  assert.deepEqual(cs.stepMatch(order, order[2], 1), order[0]);
  assert.deepEqual(cs.stepMatch(order, { path: 'gone', index: 0 }, 1), order[0]);
  assert.equal(cs.stepMatch([], order[0], 1), null);
});

test('selection seeds and the search shortcut', () => {
  assert.equal(cs.searchSeed('foo'), 'foo');
  assert.equal(cs.searchSeed(''), undefined);
  assert.equal(cs.searchSeed('a\nb'), undefined);
  assert.equal(cs.searchSeed('x'.repeat(201)), undefined);
  const key = extra => ({ code: 'KeyF', metaKey: false, ctrlKey: false, altKey: false, shiftKey: true, ...extra });
  assert.equal(cs.isSearchShortcut(key({ metaKey: true }), true), true);
  assert.equal(cs.isSearchShortcut(key({ ctrlKey: true }), true), false);
  assert.equal(cs.isSearchShortcut(key({ ctrlKey: true }), false), true);
  assert.equal(cs.isSearchShortcut(key({ ctrlKey: true, shiftKey: false }), false), false);
});

// The store, against a scripted API and fixture file tabs.

const { ContentSearchStore, REPLACE_UNSUPPORTED, SEARCH_DELAY_MS } = await loadServerModule(new URL('../src/lib/stores/contentSearch.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__searchFixture')],
  ["'../manage'", lib('manage')],
  ["'../link'", lib('link')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__searchFixture')],
  ["import { files, tabHasUnsaved } from './files.svelte';", `${fixtureStore('files', '__searchFixture')} const tabHasUnsaved = t => !!t.edit?.dirty;`],
  ["'../contentSearch'", lib('contentSearch')],
  ["import { connectionStore } from './connection.svelte';", fixtureStore('connectionStore', '__searchFixture')],
  ["import { filePrompt } from './filePrompt.svelte';", fixtureStore('filePrompt', '__searchFixture')],
  ["import { toast } from './toast.svelte';", fixtureStore('toast', '__searchFixture')],
  ["'../fileEditing'", lib('fileEditing')],
  ["'../searchReplace'", lib('searchReplace')],
]);

function scripted() {
  const calls = [], cancels = [], replaces = [], asks = [], shown = [], drafts = [], reloads = [];
  const fixture = {
    link: { dataHost: undefined },
    connectionStore: { capabilities: { searchFiles: true, replaceFiles: true } },
    filePrompt: { answer: 'replace', async ask(...args) { asks.push(args); return this.answer; } },
    toast: { show(m) { shown.push(m); } },
    files: {
      dir: '/p', tabs: [],
      updateDraft(tab, text) { drafts.push([tab.path, text]); tab.edit.draft = text; return true; },
      async reloadReplaced(paths) { reloads.push(paths); },
    },
    api: {
      replaceReply: null,
      async replaceInFiles(...args) {
        replaces.push(args);
        if (this.replaceReply instanceof Error) throw this.replaceReply;
        return this.replaceReply ?? { files: args[1].files.map(f => ({ path: f.path, status: 'replaced', replaced: f.matches?.length ?? 1 })), replaced: 0 };
      },
      searchFiles(dir, query, id) {
        let resolve, reject;
        const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
        calls.push({ dir, query, id, resolve, reject });
        return promise;
      },
      async cancelSearch(id) { cancels.push(id); },
    },
  };
  globalThis.__searchFixture = fixture;
  return { fixture, calls, cancels, replaces, asks, shown, drafts, reloads, store: new ContentSearchStore() };
}
const settle = async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); };

test('store debounces typing, skips filter disclosure, and searches at once on open', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { calls, store } = scripted();
  store.set({ text: 'f' });
  store.set({ text: 'fo' });
  t.mock.timers.tick(SEARCH_DELAY_MS - 1);
  assert.equal(calls.length, 0);
  t.mock.timers.tick(1);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].query.pattern, 'fo');
  assert.equal(calls[0].dir, '/p');
  store.toggle('filtersOpen');
  t.mock.timers.tick(SEARCH_DELAY_MS);
  assert.equal(calls.length, 1, 'showing the filters is not a new search');
  store.toggle('matchCase');
  t.mock.timers.tick(SEARCH_DELAY_MS);
  assert.equal(calls.length, 2);
  assert.equal(calls[1].query.matchCase, true);
  store.open();
  t.mock.timers.tick(0);
  assert.equal(calls.length, 3, 'opening the pane searches again: files may have changed');
  assert.equal(store.mode, 'search');
});

test('store drops stale replies, ignores host-switch rejections, cancels on close and reports errors', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { calls, cancels, store } = scripted();
  store.set({ text: 'one' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  store.set({ text: 'two' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  assert.equal(calls[0].id, calls[1].id, 'one ID: the backend stops the search a new one replaces');
  calls[1].resolve({ files: [file('b', [hit(1, 0, 3, 'two')])], matches: 1 });
  await settle();
  calls[0].resolve({ files: [file('a', [hit(1, 0, 3, 'one')])], matches: 1 });
  await settle();
  assert.deepEqual(store.result.files.map(f => f.path), ['b']);
  assert.equal(store.searching, false);
  assert.equal(store.upToDate, true);

  store.set({ text: 'three' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  assert.equal(store.searching, true);
  store.dialogClosed();
  assert.deepEqual(cancels, [calls[2].id]);
  assert.equal(store.searching, false);
  calls[2].resolve({ files: [], matches: 0 });
  await settle();
  assert.deepEqual(store.result.files.map(f => f.path), ['b'], 'a search answered after closing is dropped');

  store.searchNow();
  t.mock.timers.tick(0);
  calls[3].reject(new StaleReplyError());
  await settle();
  assert.equal(store.error, null);
  store.set({ text: '(', regex: true });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  calls[4].reject('invalid search pattern: missing closing )');
  await settle();
  assert.equal(store.error, 'invalid search pattern: missing closing )');
  assert.equal(store.result, null);
  assert.equal(store.searching, false);
  store.set({ text: '' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  assert.equal(calls.length, 5, 'an empty field searches nothing');
  assert.equal(store.error, null);
});

test('store lays unsaved drafts over the host result', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { fixture, calls, store } = scripted();
  store.set({ text: 'foo' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  calls[0].resolve({ files: [file('a.go', [hit(1, 0, 3, 'foo')])], matches: 1 });
  await settle();
  fixture.files.tabs = [
    { path: 'a.go', edit: { dirty: true, draft: 'bar\nfoo foo' } },
    { path: 'b.go', edit: { dirty: false, draft: 'foo' } },
  ];
  assert.deepEqual(store.result.files.map(f => [f.path, f.matches.length, f.draft]), [['a.go', 2, true]]);
  assert.equal(store.result.matches, 2);
  assert.equal(store.hostResult.matches, 1, 'the host answer is kept as it came');
});

test('store moves through expanded matches, F4 through all, and keeps options per project', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const { fixture, calls, store } = scripted();
  store.set({ text: 'x', wholeWord: true });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  calls[0].resolve({ files: [file('a', [hit(1, 0, 1, 'x'), hit(2, 0, 1, 'x')]), file('b', [hit(1, 0, 1, 'x')])], matches: 3 });
  await settle();
  assert.deepEqual(store.move(1), { path: 'a', index: 0 });
  store.toggleCollapsed('a');
  assert.equal(store.active, null, 'folding the active match away clears it');
  assert.deepEqual(store.move(1), { path: 'b', index: 0 });
  assert.deepEqual(store.move(1), { path: 'b', index: 0 }, 'only expanded files are stepped by the arrows');
  assert.deepEqual(store.step(1), { path: 'a', index: 0 }, 'F4 goes through folded files too');
  assert.equal(store.match(store.active).line, 1);

  fixture.files.dir = '/q';
  store.setMode('search');
  assert.equal(store.options.text, '', 'another project starts empty');
  assert.equal(store.result, null);
  store.set({ text: 'other' });
  fixture.files.dir = '/p';
  store.setMode('search');
  assert.equal(store.options.text, 'x');
  assert.equal(store.options.wholeWord, true);
  fixture.link.dataHost = 'remote';
  store.setMode('search');
  assert.equal(store.options.text, '', 'the same path on another host is another project');
});

test('store opens Go to File in Search only when asked to', () => {
  const { store } = scripted();
  store.setMode('search');
  assert.equal(store.dialogOpened(), 'files', 'a plain open shows the file list');
  store.open('seed');
  assert.equal(store.options.text, 'seed');
  assert.equal(store.dialogOpened(), 'search');
  store.dialogClosed();
  assert.equal(store.dialogOpened(), 'files');
  store.open();
  store.reset();
  assert.equal(store.mode, 'files');
  assert.equal(store.options.text, '');
  assert.equal(store.dialogOpened(), 'files');
});

// Replacing.

async function searchedFoo(t, extra = {}) {
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const s = scripted();
  s.fixture.files.tabs = [{ path: 'b.go', edit: { dirty: true, draft: 'x foo\nfoo' } }];
  s.store.set({ text: 'foo', replaceOpen: true, replace: 'bar', ...extra });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  s.calls[0].resolve({
    files: [file('a.go', [hit(1, 0, 3, 'foo foo'), hit(1, 4, 7, 'foo foo')]), file('b.go', [hit(9, 0, 3, 'foo')]), file('c.go', [hit(2, 0, 3, 'foo')])],
    matches: 4,
  });
  await settle();
  return s;
}

test('replace all asks once, sends disk files with their matches, changes drafts in place and searches again', async (t) => {
  const { fixture, calls, replaces, asks, shown, drafts, reloads, store } = await searchedFoo(t);
  assert.equal(store.replaceBlocked(), null);
  fixture.api.replaceReply = { files: [{ path: 'a.go', status: 'replaced', replaced: 2, revision: 'r2' }, { path: 'c.go', status: 'conflict', observedRevision: 'r9' }], replaced: 2 };
  await store.replaceAll();
  assert.equal(asks.length, 1);
  assert.equal(asks[0][1], 'Replace 5 matches in 3 files with “bar”?', 'the draft is counted as the browser searched it');
  assert.equal(asks[0][2][0].danger, true);
  assert.equal(replaces.length, 1);
  const [dir, req, host] = replaces[0];
  assert.equal(dir, '/p');
  assert.equal(host, undefined);
  assert.equal(req.replacement, 'bar');
  assert.equal(req.query.pattern, 'foo');
  assert.deepEqual(req.files, [
    { path: 'a.go', expectedRevision: 'r-a.go', matches: [{ line: 1, start: 0 }, { line: 1, start: 4 }] },
    { path: 'c.go', expectedRevision: 'r-c.go', matches: [{ line: 2, start: 0 }] },
  ], 'the draft never goes to disk');
  assert.deepEqual(drafts, [['b.go', 'x bar\nbar']]);
  assert.deepEqual(reloads, [['a.go']], 'only written files are read again');
  assert.equal(shown.length, 1);
  assert.equal(shown[0].title, 'Replaced 4 matches in 2 files');
  assert.match(shown[0].body, /unsaved draft, not yet saved: b\.go/);
  assert.match(shown[0].body, /Changed on disk since the search, left as they are: c\.go/);
  assert.equal(shown[0].tone, 'error', 'a file left unchanged is not a quiet success');
  assert.equal(store.replacing, false);
  t.mock.timers.tick(0);
  assert.equal(calls.length, 2, 'the search runs again to show the files as they are');
});

test('replacing one match or one file does not ask; dismissed matches and files are left alone', async (t) => {
  const { replaces, asks, store } = await searchedFoo(t, { replace: '' });
  await store.replaceMatch({ path: 'a.go', index: 1 });
  assert.equal(asks.length, 0);
  assert.deepEqual(replaces[0][1].files, [{ path: 'a.go', expectedRevision: 'r-a.go', matches: [{ line: 1, start: 4 }] }]);
  assert.equal(replaces[0][1].replacement, '');

  assert.deepEqual(store.move(1), { path: 'a.go', index: 0 });
  store.dismiss('a.go', { path: 'a.go', index: 0 });
  assert.equal(store.match(store.active).start, 4, 'the keyboard moves to the match taking its place');
  assert.equal(store.result.matches, 4);
  await store.replaceFile('a.go');
  assert.deepEqual(replaces[1][1].files[0].matches, [{ line: 1, start: 4 }]);

  store.dismiss('c.go');
  await store.replaceAll();
  assert.equal(asks[0][1], 'Replace 3 matches in 2 files with nothing?');
  assert.deepEqual(replaces[2][1].files.map(f => f.path), ['a.go']);

  store.set({ text: 'fo' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
});

test('a new search text brings dismissed matches back; a cancelled prompt changes nothing', async (t) => {
  const { fixture, calls, replaces, shown, store } = await searchedFoo(t);
  store.dismiss('c.go');
  assert.deepEqual(store.result.files.map(f => f.path), ['a.go', 'b.go']);
  store.set({ text: 'fo' });
  t.mock.timers.tick(SEARCH_DELAY_MS);
  calls.at(-1).resolve({ files: [file('c.go', [hit(2, 0, 2, 'foo')])], matches: 1 });
  await settle();
  assert.deepEqual(store.result.files.map(f => f.path), ['b.go', 'c.go']);
  fixture.filePrompt.answer = null;
  await store.replaceAll();
  assert.equal(replaces.length, 0);
  assert.equal(shown.length, 0);
});

test('replace says why it cannot run', async (t) => {
  const { fixture, replaces, store } = await searchedFoo(t);
  fixture.files.tabs.push({ path: 'c.go', content: { edit: { eligible: false, reason: 'protected' } } });
  assert.equal(store.replaceBlocked('c.go'), 'Read-only: protected file');
  assert.equal(store.replaceBlocked('b.go'), null, 'a searched draft can be replaced');
  fixture.files.tabs[0].edit.saving = true;
  assert.equal(store.replaceBlocked('b.go'), 'Wait for the save to finish.');
  fixture.files.renaming = 'x';
  assert.equal(store.replaceBlocked(), 'Wait for the file change to finish.');
  fixture.files.renaming = undefined;
  fixture.link.blockedReason = 'Reconnecting…';
  assert.equal(store.replaceBlocked(), 'Reconnecting…');
  fixture.link.blockedReason = undefined;
  store.set({ text: 'food' });
  assert.equal(store.replaceBlocked(), 'Wait for the search to finish.');
  fixture.connectionStore.capabilities = { searchFiles: true };
  assert.equal(store.replaceBlocked(), REPLACE_UNSUPPORTED);
  await store.replaceAll();
  assert.equal(replaces.length, 0);
  t.mock.timers.tick(SEARCH_DELAY_MS);
});

test('a host error during replace says files may have changed and searches again', async (t) => {
  const { fixture, calls, shown, reloads, store } = await searchedFoo(t);
  fixture.files.tabs = [];
  fixture.api.replaceReply = new Error('disk full');
  await store.replaceFile('a.go');
  assert.equal(shown.length, 1);
  assert.equal(shown[0].title, 'Replace may not have finished');
  assert.match(shown[0].body, /disk full/);
  assert.equal(shown[0].tone, 'error');
  assert.equal(reloads.length, 0);
  t.mock.timers.tick(0);
  assert.equal(calls.length, 2);
});

test('a draft whose match moved is skipped, not guessed at', async (t) => {
  const { fixture, replaces, shown, drafts, store } = await searchedFoo(t);
  // The pane listed the draft's matches; the draft then changed under them.
  const listed = store.result.files.find(f => f.path === 'b.go');
  fixture.files.tabs[0].edit.draft = 'foo';
  store.dismissed = new Set();
  await store.replace([listed], false);
  assert.equal(replaces.length, 0);
  assert.deepEqual(drafts, []);
  assert.match(shown[0].body, /Skipped \(matches changed\): b\.go/);
});

// Rendering.

const base = new URL('../src/lib/components/common/go-to-file/', import.meta.url);
const pane = await loadServerComponent(new URL('SearchPane.svelte', base), [
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["import { connectionStore } from '../../../stores/connection.svelte';", fixtureStore('connectionStore')],
  ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
  ["import LoadingSpinner from '../LoadingSpinner.svelte';", 'const LoadingSpinner = () => {};'],
  ["'../../../searchReplace'", lib('searchReplace')],
]);

async function renderPane(setup, capabilities = { searchFiles: true, replaceFiles: true }) {
  const { fixture, store } = scripted();
  fixture.connectionStore.capabilities = capabilities;
  setup?.(store, fixture);
  try {
    return await renderWithFixture(pane, { contentSearch: store, connectionStore: { capabilities } }, { onOpen() {} });
  } finally {
    delete globalThis.__searchFixture;
  }
}

function answered(store, query, result) {
  store.options = { ...store.options, text: query.pattern, ...query };
  store.searched = { include: '', exclude: '', regex: false, matchCase: false, wholeWord: false, ...query };
  store.hostResult = result;
}

test('pane renders toggles, filters and grouped results with the match highlighted', async () => {
  const result = { files: [file('src/a.ts', [hit(3, 4, 7, 'let foo = 1;'), hit(9, 0, 3, 'foo()')]), file('b.md', [hit(1, 0, 3, 'foo')], { truncated: true })], matches: 3 };
  const html = await renderPane(store => {
    answered(store, { pattern: 'foo', matchCase: true }, result);
    store.options = { ...store.options, matchCase: true, include: '*.ts' };
    store.collapsed = new Set(['b.md']);
    store.active = { path: 'src/a.ts', index: 1 };
  });
  assert.match(html, /aria-label="Match case"[^>]*>Aa</);
  assert.match(html, /class="field-toggle[^"]* on" aria-pressed="true" aria-label="Match case"/);
  assert.match(html, /aria-pressed="false"[^>]*aria-label="Match whole word"/);
  assert.match(html, /aria-pressed="false"[^>]*aria-label="Use regular expression"/);
  assert.match(html, /aria-expanded="false"[^>]*aria-controls="search-filters"/);
  assert.match(html, /title="Filters apply"/, 'hidden filters still say they apply');
  assert.match(html, /3 results in 2 files/);
  assert.match(html, /<span class="file-name[^"]*">a\.ts<\/span>\s*(<!--\[0-->)?<span class="file-dir[^"]*">src<\/span>/);
  assert.match(html, /let <mark[^>]*>foo<\/mark> = 1;/);
  assert.match(html, /aria-selected="true" class="match-row[^"]* active" title="Line 9"/);
  assert.match(html, /aria-expanded="false"[^>]*title="b\.md · More matches in this file were not listed"/);
  assert.equal((html.match(/<mark[ >]/g) ?? []).length, 2, 'folded files list no matches');
  assert.match(html, />1\+</);
});

test('pane shows truncation, errors in place, drafts and an empty result', async () => {
  let html = await renderPane(store => answered(store, { pattern: 'x' }, { files: [file('a', [hit(1, 0, 1, 'x')])], matches: 1, truncated: true }));
  assert.match(html, /1 result in 1 file \(limited to 5,000\)/);
  html = await renderPane(store => { store.options = { ...store.options, text: '(', regex: true }; store.error = 'invalid search pattern: missing closing )'; });
  assert.match(html, /role="status"[^>]*>(<!--[^>]*-->)?<span class="error[^"]*">invalid search pattern: missing closing \)<\/span>/);
  assert.doesNotMatch(html, /class="file-row"/);
  html = await renderPane(store => answered(store, { pattern: 'x' }, { files: [], matches: 0 }));
  assert.match(html, /No results/);
  html = await renderPane((store, fixture) => {
    answered(store, { pattern: 'foo' }, { files: [], matches: 0 });
    fixture.files.tabs = [{ path: 'd.go', edit: { dirty: true, draft: 'foo' } }];
  });
  assert.match(html, /<span class="draft-tag[^"]*"[^>]*>draft<\/span>/);
  html = await renderPane((store, fixture) => {
    answered(store, { pattern: '(?<=x', regex: true }, { files: [], matches: 0 });
    fixture.files.tabs = [{ path: 'd.go', edit: { dirty: true, draft: 'foo' } }];
  });
  assert.match(html, /Unsaved draft shown as saved: the browser cannot read this pattern/);
});

test('pane explains an older host instead of a field that cannot work', async () => {
  const html = await renderPane(undefined, {});
  assert.match(html, /role="note"[^>]*>\s*This host's agent-sessions server cannot search file contents\. Update agent-sessions on the host, then reconnect\./);
  assert.doesNotMatch(html, /Search file contents"/);
});

test('pane previews each replacement, with regex groups expanded as the host will', async () => {
  const one = { files: [file('a.go', [hit(1, 0, 3, 'foo x')])], matches: 1 };
  let html = await renderPane(store => {
    answered(store, { pattern: 'foo' }, one);
    store.options = { ...store.options, replaceOpen: true, replace: 'bar' };
  });
  assert.match(html, /aria-expanded="true" aria-controls="search-replace" aria-label="Toggle replace"/);
  assert.match(html, /<del[^>]*>foo<\/del>(<!--[^>]*-->)?<ins[^>]*>bar<\/ins> x/);
  assert.match(html, /aria-label="Replace all" title="Replace All \((⌘⌥Enter|Ctrl\+Alt\+Enter)\)" aria-keyshortcuts="(Meta|Control)\+Alt\+Enter"/);
  assert.doesNotMatch(html, /aria-label="Replace all"[^>]*disabled/);
  assert.match(html, /aria-label="Replace all in a\.go" title="Replace all in file"/);
  assert.match(html, /aria-label="Replace line 1 of a\.go" title="Replace"/);
  assert.match(html, /aria-label="Dismiss line 1 of a\.go"/);
  html = await renderPane(store => {
    answered(store, { pattern: 'f(o)o', regex: true }, one);
    store.options = { ...store.options, replaceOpen: true, replace: 'b${1}r\\t' };
  });
  assert.match(html, /<ins[^>]*>bor\t<\/ins>/);
  html = await renderPane(store => {
    answered(store, { pattern: 'foo' }, one);
    store.options = { ...store.options, replace: 'bar' };
  });
  assert.match(html, /aria-expanded="false" aria-controls="search-replace"/);
  assert.match(html, /<mark[^>]*>foo<\/mark>/, 'a closed replace field previews nothing');
  assert.doesNotMatch(html, /aria-label="Replace with"|Replace line 1/);
});

test('pane disables replace with the reason where it cannot run', async () => {
  const one = { files: [file('a.go', [hit(1, 0, 3, 'foo')])], matches: 1 };
  const open = store => {
    answered(store, { pattern: 'foo' }, one);
    store.options = { ...store.options, replaceOpen: true, replace: 'bar' };
  };
  let html = await renderPane(open, { searchFiles: true });
  assert.match(html, /aria-label="Replace all" title="This host's agent-sessions server cannot replace in files\.[^"]*"[^>]*disabled/);
  assert.match(html, /role="note"[^>]*>\s*This host's agent-sessions server cannot replace in files\. Update agent-sessions on the host, then reconnect\./);
  html = await renderPane((store, fixture) => {
    open(store);
    fixture.files.tabs = [{ path: 'a.go', content: { edit: { eligible: false, reason: 'protected' } } }];
  });
  assert.match(html, /aria-label="Replace line 1 of a\.go" title="Read-only: protected file"[^>]*disabled/);
  assert.match(html, /aria-label="Replace all in a\.go" title="Read-only: protected file"[^>]*disabled/);
  assert.doesNotMatch(html, /aria-label="Replace all" [^>]*disabled/, 'other files can still be replaced');
});

const picker = await loadServerComponent(new URL('FilePicker.svelte', base), [
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
  ["import DiagnosticBadge from './DiagnosticBadge.svelte';", 'const DiagnosticBadge = () => {};'],
  ["'../../../files'", lib('files')],
  ["'../../../codeCheckSummary'", lib('codeCheckSummary')],
  ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
  ["import LoadingSpinner from '../LoadingSpinner.svelte';", 'const LoadingSpinner = () => {};'],
]);

test('picker switches between the file list and the Search pane', async () => {
  const render = mode => renderWithFixture(picker, {
    contentSearch: { mode },
    checks: { status: 'ready', providers: [], counts: { error: 0, warning: 0 }, pathCounts: { files: new Map(), folders: new Map() } },
    files: { expanded: new Set(), listing: { files: ['a.go'] } },
  }, { onPick() {}, onContext() {}, onOpenMatch() {} });
  let html = await render('files');
  assert.match(html, /role="tablist" aria-label="Left pane"/);
  assert.match(html, /class="mode-btn[^"]* active" aria-selected="true"[^>]*>Files</);
  assert.match(html, /aria-label="Find a file"/);
  assert.doesNotMatch(html, /data-search-pane/);
  html = await render('search');
  assert.match(html, /class="mode-btn[^"]* active" aria-selected="true" title="Search file contents \((⌘⇧F|Ctrl\+Shift\+F)\)"/);
  assert.match(html, /data-search-pane/);
  assert.doesNotMatch(html, /aria-label="Find a file"/);
  assert.doesNotMatch(html, /picker-footer/);
});

test.after(() => { mock.timers.reset(); delete globalThis.__searchFixture; });
