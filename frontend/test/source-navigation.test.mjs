import test from 'node:test';
import assert from 'node:assert/strict';
import { SourceHistory, wordRange, caretPoint, sourcePosition, sourceRange, linkModifier, navigationKey, historyKey, locationText, hoverHtml } from '../src/lib/sourceNavigation.ts';
import { loadServerComponent } from './helpers.mjs';
import { render } from 'svelte/server';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const key = (extra = {}) => ({ key: '', code: '', metaKey: false, ctrlKey: false, altKey: false, shiftKey: false, target: null, ...extra });
const place = (path, line = 0) => ({ path, range: { start: { line, character: 2 }, end: { line, character: 2 } } });

test('identifier ranges use UTF-16 columns, accept word ends and reject numeric literals', () => {
  for (const column of [0, 2, 5]) assert.deepEqual(wordRange('hello + 42', column), { start: 0, end: 5 });
  assert.equal(wordRange('hello + 42', 7), undefined);
  assert.equal(wordRange('hello + 42', 9), undefined);
  assert.deepEqual(wordRange('😀 café_$2', 6), { start: 3, end: 10 });
  assert.equal(wordRange('', 0), undefined);
});

test('navigation modifiers and history shortcuts preserve text-field bindings', () => {
  assert.equal(linkModifier(key({ metaKey: true }), true), true);
  assert.equal(linkModifier(key({ ctrlKey: true }), false), true);
  assert.equal(linkModifier(key({ ctrlKey: true, altKey: true }), false), false);
  assert.equal(navigationKey(key({ key: 'F12' })), 'definition');
  assert.equal(navigationKey(key({ key: 'F12', shiftKey: true })), 'references');
  assert.equal(navigationKey(key({ key: 'F12', metaKey: true })), undefined);
  assert.equal(historyKey(key({ key: '[', metaKey: true }), true), 'back');
  assert.equal(historyKey(key({ key: '[', metaKey: true, target: { tagName: 'TEXTAREA' } }), true), null);
  assert.equal(historyKey(key({ code: 'Minus', ctrlKey: true, shiftKey: true, target: { isContentEditable: true } }), true), 'forward');
  assert.equal(historyKey(key({ key: 'ArrowLeft', altKey: true }), false), 'back');
});

test('history bounds, deduplicates, drops the forward branch and forgets removed places', () => {
  const h = new SourceHistory(2), a = place('a'), b = place('b'), c = place('c'), d = place('d');
  h.visit(a); h.visit(a); h.visit(b); h.visit(c);
  assert.deepEqual(h.goBack(d), c);
  assert.deepEqual(h.goBack(c), b);
  assert.equal(h.goBack(b), undefined);
  assert.deepEqual(h.goForward(b), c);
  h.visit(c);
  assert.equal(h.canGoForward, false);
  h.forget(p => p.path !== 'c');
  assert.deepEqual(h.goBack(d), b);
  h.clear();
  assert.equal(h.canGoBack, false);
  assert.equal(h.canGoForward, false);
});

test('hover escapes plaintext and renders markdown through the safe transcript renderer', () => {
  assert.equal(hoverHtml({ kind: 'plaintext', value: '<script>"&' }), '<pre class="hover-plain">&lt;script&gt;&quot;&amp;</pre>');
  const html = hoverHtml({ kind: 'markdown', value: '**symbol**\n\n```ts\nconst n = 1;\n```\n<script>alert(1)</script>' });
  assert.match(html, /<strong>symbol<\/strong>/);
  assert.match(html, /code-block/);
  assert.doesNotMatch(html, /<script>/);
  assert.equal(locationText({ ...place('/home/a/lib.go', 3), external: true }, () => '~/lib.go'), '~/lib.go:4:3');
});

test('Python documentation hover renders entities, escaped identifiers and admonitions', () => {
  const value = [
    '```python', 'class ItemCreate(*, name: str):', '    pass', '```', '---',
    '!!! abstract "Usage Documentation"',
    '&nbsp;&nbsp;&nbsp;&nbsp;[Models](https://docs.example/models)', '',
    'A base class for creating models.', '', 'Attributes:',
    String.raw`&nbsp;&nbsp;&nbsp;&nbsp;\_\_class\_vars\_\_: The class variables.`,
  ].join('\n');
  const html = hoverHtml({ kind: 'markdown', value });
  assert.match(html, /language-python/);
  assert.match(html, /<blockquote><p><strong>Usage Documentation<\/strong>/);
  assert.match(html, /href="https:\/\/docs.example\/models"/);
  assert.match(html, /<\/blockquote>\n<p>A base class/);
  assert.match(html, / {4}__class_vars__: The class variables/);
  assert.doesNotMatch(html, /&amp;nbsp;|\\_|!!! abstract/);
});

test('hover admonitions respect dedents, code literals, malformed headers and plaintext', () => {
  const value = 'Intro\n!!! note "Custom <title>"\n    First\n\n    Second\nAfter';
  const html = hoverHtml({ kind: 'markdown', value });
  assert.match(html, /<p>Intro<\/p>\n<blockquote>/);
  assert.match(html, /Custom &lt;title&gt;/);
  assert.match(html, /<p>First<\/p>\n<p>Second<\/p><\/blockquote>\n<p>After<\/p>/);
  for (const content of ['!!! broken "title', '!!! note "No body"\nAfter']) {
    assert.doesNotMatch(hoverHtml({ kind: 'markdown', value: content }), /<blockquote>/);
  }
  const literal = '```text\n!!! note "Literal"\n    &nbsp; \\_name\n```';
  const code = hoverHtml({ kind: 'markdown', value: literal });
  assert.doesNotMatch(code, /<blockquote>/);
  assert.match(code, /data-code="!!! note &quot;Literal&quot;\n    &amp;nbsp; \\_name"/);
  assert.match(hoverHtml({ kind: 'plaintext', value }), /!!! note &quot;Custom &lt;title&gt;&quot;/);
  assert.equal(hoverHtml({ kind: 'plaintext', value: '&nbsp; \\_name' }), '<pre class="hover-plain">&amp;nbsp; \\_name</pre>');
});

test('caret lookup supports Chromium and WebKit and missing points', () => {
  const node = {};
  assert.deepEqual(caretPoint({ caretPositionFromPoint: () => ({ offsetNode: node, offset: 3 }) }, 1, 2), { node, offset: 3 });
  assert.deepEqual(caretPoint({ caretRangeFromPoint: () => ({ startContainer: node, startOffset: 2 }) }, 1, 2), { node, offset: 2 });
  assert.equal(caretPoint({ caretPositionFromPoint: () => null }, 1, 2), undefined);
  assert.equal(caretPoint({}, 1, 2), undefined);
});

test('source coordinates count text rather than syntax spans and exclude non-source lines', () => {
  const nodes = [{ textContent: '😀 ' }, { textContent: 'name' }];
  let range;
  const el = { dataset: { sourceLine: '4' }, closest: () => el };
  const root = {
    contains: node => node === el,
    querySelector: selector => selector === '[data-source-line="4"]' ? el : null,
    ownerDocument: {
      createTreeWalker: () => { let i = 0; return { nextNode: () => nodes[i++] }; },
      createRange: () => (range = { setStart(...p) { this.start = p; }, setEnd(...p) { this.end = p; }, toString: () => '😀 na' }),
    },
  };
  assert.deepEqual(sourcePosition(root, { parentElement: el }, 2), { line: 3, character: 5 });
  assert.equal(sourcePosition(root, { parentElement: null }, 0), undefined);
  assert.equal(sourceRange(root, 0, 0, 1), undefined);
  sourceRange(root, 3, 3, 7);
  assert.deepEqual(range.start, [nodes[0], 3]);
  assert.deepEqual(range.end, [nodes[1], 4]);
  assert.equal(sourceRange(root, 3, 3, 8), undefined);
});

// Execute the actual result handler, not a parallel implementation. DOM paint
// and native focus remain manual checks.
const viewerSource = readFileSync(new URL('../src/lib/components/common/go-to-file/FileViewer.svelte', import.meta.url), 'utf8');
const askSource = viewerSource.slice(viewerSource.indexOf('  async function askSymbol('), viewerSource.indexOf('  export function navigateReference('));
const askJS = ts.transpileModule(askSource, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
function answers(navigate, blocker) {
  const tab = { path: 'main.ts' }, opened = [], notices = [];
  const context = {
    files: { selected: tab.path, activeTab: tab }, codeDiagnostics: { navigate }, symbolSequence: 0,
    symbolBlocker: blocker, symbolPicker: null, editing: false, text: '\n\n   symbol', wordRange,
    contentSearch: { showReferences(...args) { context.references = args; } },
    closeSymbolPopups() { context.symbolSequence++; },
    showSymbolNotice: (...args) => notices.push(args),
    openLocation: (...args) => opened.push(args), symbolStart: p => ({ start: p, end: p }),
    locationText, symbolLabel: p => p,
  };
  const ask = new Function('ctx', `with(ctx) { ${askJS}; return askSymbol; }`)(context);
  return { context, opened, notices, ask: op => ask(op, { line: 2, character: 3 }, { x: 10, y: 20 }) };
}
test('viewer opens a single definition, lists multiple results and explains empty or blocked answers', async () => {
  const location = place('target.ts');
  const h = answers(async () => ({ kind: 'result', result: { locations: [location] } }));
  await h.ask('definition');
  assert.equal(h.opened[0][0], location);
  assert.equal(h.opened[0][1].path, 'main.ts');
  await h.ask('references');
  assert.equal(h.context.references[0], 'symbol');
  assert.deepEqual(h.context.references[1], [location]);
  assert.equal(h.opened.length, 1);
  const empty = answers(async () => ({ kind: 'result', result: { locations: [] } }));
  await empty.ask('definition');
  assert.equal(empty.notices[0][0], 'No definition found');
  assert.equal(empty.notices[0][2], 2000);
  const blocked = answers(() => assert.fail('must not request'), 'Install the language server first.');
  await blocked.ask('definition');
  assert.equal(blocked.notices[0][0], 'Install the language server first.');
});
test('viewer drops answers superseded by another request or a popup close', async () => {
  const pending = [];
  const h = answers(() => new Promise(resolve => pending.push(resolve)));
  const first = h.ask('definition'), second = h.ask('definition');
  pending[1]({ kind: 'result', result: { locations: [place('new.ts')] } });
  await second;
  pending[0]({ kind: 'result', result: { locations: [place('old.ts')] } });
  await first;
  assert.deepEqual(h.opened.map(args => args[0].path), ['new.ts']);
});

const card = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/HoverCard.svelte', import.meta.url), [
  ["import { handleCopyCodeClick } from '../../../copycode';", 'const handleCopyCodeClick = () => {};'],
]);
test('hover card renders safe pre-rendered content, scroll limit and truncation notice', () => {
  const html = render(card.default, { props: { html: hoverHtml({ kind: 'plaintext', value: '<img onerror=attack>' }), truncated: true, x: 10, y: 20, maxHeight: 300, onenter() {}, onleave() {} } }).body;
  assert.match(html, /role="tooltip"/);
  assert.match(html, /max-height:\s*300px/);
  assert.match(html, /&lt;img onerror=attack&gt;/);
  assert.match(html, /The description was cut short/);
});


test('history reads the live editor/source selection rather than an old landing, and blocked steps preserve stacks', () => {
  const source = viewerSource.slice(viewerSource.indexOf('  function currentLocation()'), viewerSource.indexOf('  function openLocation('));
  const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const tab = { path: 'a.ts' };
  const ctx = { files: { activeTab: tab }, editing: true, editor: { caret: () => ({ line: 7, character: 9 }) },
    landed: place('a.ts', 1), codeScroll: undefined, sourcePositions: new WeakMap(), symbolStart: at => ({ start: at, end: at }) };
  const location = new Function('ctx', `with(ctx) { ${js}; return currentLocation; }`)(ctx);
  assert.deepEqual(location().range.start, { line: 7, character: 9 });
  ctx.editing = false;
  ctx.codeScroll = { ownerDocument: { getSelection: () => ({ anchorNode: {}, anchorOffset: 4 }) }, contains: () => true };
  ctx.sourcePosition = () => ({ line: 8, character: 4 });
  assert.deepEqual(location().range.start, { line: 8, character: 4 });
  ctx.codeScroll = undefined;
  assert.deepEqual(location(), ctx.landed, 'landing is only a fallback before source selection is available');

  const historySource = viewerSource.slice(viewerSource.indexOf("  export function goHistory("), viewerSource.indexOf('  /**\n   * Definition or references'));
  const historyJS = ts.transpileModule(historySource.replace('export ', ''), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const history = new SourceHistory(); history.visit(place('before.ts'));
  const opened = [];
  const context = { history, historyBlocked: true, currentLocation: location, historyVersion: 0, openLocation: to => opened.push(to) };
  const go = new Function('ctx', `with(ctx) { ${historyJS}; return goHistory; }`)(context);
  go('back');
  assert.equal(history.canGoBack, true, 'blocked back does not consume a place');
  assert.equal(history.canGoForward, false);
  assert.equal(opened.length, 0);
  context.historyBlocked = false;
  go('back');
  assert.equal(opened[0].path, 'before.ts');
  assert.equal(history.canGoForward, true);
});

test('viewer forwards the draft current guard and one-second sync bound, discarding changed draft answers', async () => {
  let resolve, args;
  const h = answers((...a) => { args = a; return new Promise(r => { resolve = r; }); });
  h.context.DRAFT_SYNC_WAIT = 1000;
  let current = true;
  const ask = new Function('ctx', `with(ctx) { ${askJS}; return askSymbol; }`)(h.context);
  const pending = ask('definition', { line: 2, character: 3 }, { x: 10, y: 20 }, () => current);
  assert.equal(args[3].wait, 1000);
  assert.equal(args[3].current(), true);
  current = false;
  resolve({ kind: 'result', result: { locations: [place('target.ts')] } });
  await pending;
  assert.equal(h.opened.length, 0);
});
