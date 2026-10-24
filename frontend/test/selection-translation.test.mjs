import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { captureSelection, holdSelection, requestSelectionTranslation, selectionMenuItems, sentenceContext, MAX_SELECTION_CONTEXT } from '../src/lib/selectionTranslation.ts';
import { createAPI } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { loadServerComponent } from './helpers.mjs';
import { render } from 'svelte/server';

test('selection popover renders an accessible loading card without printing controls', async () => {
  const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
  const { default: Popover } = await loadServerComponent(new URL('../src/lib/components/transcript/SelectionTranslation.svelte', import.meta.url), [
    ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
    ["import { api } from '../../api';", 'const api = {};'],
    ["import { toast } from '../../stores/toast.svelte';", 'const toast = {};'],
    ...['portable', 'manage', 'guidance', 'selectionTranslation'].map(name => [`'../../${name}'`, lib(name)]),
  ]);
  const html = render(Popover, { props: { selection: { text: 'bank', context: 'River bank.', rect: { left: 0, right: 20, top: 0, bottom: 10 } }, language: 'Vietnamese', onClose() {} } }).body;
  assert.match(html, /role="dialog" aria-label="Selection translation"/);
  assert.match(html, /role="status"[^>]*>Translating/);
  assert.match(html, /Close translation/);
  assert.doesNotMatch(html, /Print|Save as PDF/);
});

function text(value) { return { nodeType: 3, textContent: value }; }
function element(tagName, ...childNodes) {
  return {
    nodeType: 1, tagName, childNodes,
    contains(node) { return node === this || childNodes.some(c => c === node || c.contains?.(node)); },
    matches() { return this.hidden || tagName === 'BUTTON'; },
  };
}
function selection(startContainer, startOffset, endContainer, endOffset, value, touched = []) {
  return {
    isCollapsed: false, rangeCount: 1,
    getRangeAt() { return {
      startContainer, startOffset, endContainer, endOffset,
      toString: () => value,
      intersectsNode: n => touched.includes(n),
      getBoundingClientRect: () => ({ left: 10, right: 40, top: 20, bottom: 35 }),
      cloneRange() { return { clone: true, startContainer, startOffset, endContainer, endOffset }; },
    }; },
  };
}

test('sentence context uses actual offsets for repeated phrases and multiple sentences', () => {
  const source = 'The bank was closed. Walk to the bank by the river. Then stop.';
  const start = source.lastIndexOf('bank');
  assert.equal(sentenceContext(source, start, start + 4), 'Walk to the bank by the river.');
  assert.equal(sentenceContext(source, 4, start + 4), 'The bank was closed. Walk to the bank by the river.');
});

test('sentence fallback handles CJK and context stays bounded in UTF-8', () => {
  const saved = Intl.Segmenter;
  try {
    Intl.Segmenter = undefined;
    assert.equal(sentenceContext('最初。次の文！最後。', 4, 6), '次の文！');
    const long = '😀'.repeat(10000);
    const result = sentenceContext(long, 12000, 12002);
    assert.ok(new TextEncoder().encode(result).length <= MAX_SELECTION_CONTEXT);
    assert.ok(!result.includes('�'));
    assert.ok(result.length > 1000);
  } finally { Intl.Segmenter = saved; }
});

test('selection capture uses ordered endpoints across markup, paragraphs and code', () => {
  const saved = globalThis.getComputedStyle;
  globalThis.getComputedStyle = () => ({ display: 'block', visibility: 'visible' });
  try {
    const a = text('First sentence. The '), b = text('river bank'), c = text(' is nearby.'), d = text('Run pwd.');
    const root = element('DIV', element('P', a, element('EM', b), c), element('PRE', d));
    const range = selection(b, 6, b, 10, 'bank');
    const captured = captureSelection(root, range);
    assert.equal(captured.text, 'bank');
    assert.equal(captured.context, 'The river bank is nearby.');
    assert.deepEqual(captured.rect, { left: 10, right: 40, top: 20, bottom: 35 });
    // A copy, so the held range survives the selection being cleared.
    assert.deepEqual(captured.range, { clone: true, startContainer: b, startOffset: 6, endContainer: b, endOffset: 10 });
    // Anchor direction is intentionally irrelevant: the Range is ordered.
    range.anchorNode = c;
    range.focusNode = b;
    assert.equal(captureSelection(root, range).context, captured.context);
    assert.match(captureSelection(root, selection(b, 6, d, 7, 'bank is nearby.\nRun pwd')).context, /Run pwd/);
    const p = root.childNodes[0];
    assert.equal(captureSelection(root, selection(p, 1, p, 2, 'river bank')).context, 'The river bank is nearby.');
  } finally { globalThis.getComputedStyle = saved; }
});

test('invalid, cross-message, hidden and control selections stay native', () => {
  const saved = globalThis.getComputedStyle;
  globalThis.getComputedStyle = () => ({ display: 'block', visibility: 'visible' });
  try {
    const a = text('hello'), outside = text('header');
    const button = element('BUTTON', text('Copy'));
    const hidden = element('DIV', text('hidden')); hidden.hidden = true;
    const root = element('DIV', a, button, hidden);
    assert.equal(captureSelection(root, null), null);
    assert.equal(captureSelection(root, { isCollapsed: true }), null);
    assert.equal(captureSelection(root, selection(a, 0, outside, 2, 'hello header')), null);
    assert.equal(captureSelection(root, selection(a, 0, a, 4, 'hell', [button])), null);
    assert.equal(captureSelection(root, selection(a, 0, a, 4, 'hell', [hidden])), null);
    assert.equal(captureSelection(root, selection(a, 0, a, 2, '  ')), null);
  } finally { globalThis.getComputedStyle = saved; }
});

test('mock selection translation keeps context separate and leaves message translations alone', async () => {
  const mock = new MockBackendAPI();
  mock.translateDelayMs = 0;
  await assert.rejects(mock.translateSelection('bank', 'Walk along the river bank.'), /not set up/);
  mock.translateConfig = { baseURL: 'https://fixture.invalid/v1', apiKey: 'fixture', model: 'fixture', language: 'Vietnamese' };
  assert.equal(await mock.translateSelection('bank', 'Walk along the river bank.'), '[Vietnamese] bank');
  assert.deepEqual(mock.translatedSelections, [{ text: 'bank', context: 'Walk along the river bank.' }]);
  assert.deepEqual(mock.translated, []);
});

test('selection menu has Copy and Translate, without Print or message actions', () => {
  const calls = [];
  const configured = selectionMenuItems(true, 'Vietnamese', () => calls.push('copy'), () => calls.push('translate'), () => calls.push('setup'));
  assert.deepEqual(configured.map(i => i.label), ['Copy', 'Translate to Vietnamese']);
  configured.forEach(i => i.onSelect());
  assert.deepEqual(calls, ['copy', 'translate']);
  const setup = selectionMenuItems(false, 'Vietnamese', () => {}, () => {}, () => calls.push('setup'));
  assert.deepEqual(setup.map(i => [i.label, !!i.disabled]), [['Copy', false], ['Translate', true], ['Set up translation…', false]]);
  assert.ok(!setup.concat(configured).some(i => /Print|Save|original|Retranslate/.test(i.label)));
});

test('dismissed or superseded selection requests ignore late success and failure', async () => {
  const pending = [];
  const seen = [];
  const request = () => new Promise((resolve, reject) => pending.push({ resolve, reject }));
  const first = requestSelectionTranslation(request, t => seen.push(['done', t]), e => seen.push(['error', e]));
  const second = requestSelectionTranslation(request, t => seen.push(['done', t]), e => seen.push(['error', e]));
  await Promise.resolve();
  first();
  pending[0].resolve('old');
  pending[1].reject('current failure');
  await new Promise(r => setImmediate(r));
  assert.deepEqual(seen, [['error', 'current failure']]);
  second();
});

test('the Wails binding receives selection and context separately and errors normalize', async () => {
  const previous = globalThis.window;
  try {
    const binding = {};
    globalThis.window = { go: { app: { App: binding } } };
    const backend = createAPI();
    let args;
    binding.TranslateSelection = async (...a) => { args = a; return 'ngân hàng'; };
    assert.equal(await backend.translateSelection('bank', 'The bank was closed.'), 'ngân hàng');
    assert.deepEqual(args, ['bank', 'The bank was closed.']);
    binding.TranslateSelection = async () => { throw 'translation context is too long'; };
    await assert.rejects(backend.translateSelection('x', 'y'), /context is too long/);
  } finally { globalThis.window = previous; }
  assert.match(readFileSync(new URL('../src/lib/api.ts', import.meta.url), 'utf8'), /'translate',\n  'translateSelection',/);
});

function withSelectionGlobals(highlights, selection, run) {
  const saved = { CSS: globalThis.CSS, Highlight: globalThis.Highlight, window: globalThis.window };
  try {
    globalThis.CSS = highlights ? { highlights } : undefined;
    globalThis.Highlight = highlights ? class { constructor(...ranges) { this.ranges = ranges; } } : undefined;
    globalThis.window = { getSelection: () => selection };
    run();
  } finally { Object.assign(globalThis, saved); }
}
function fakeSelection(collapsed = true) {
  const calls = [];
  return {
    calls, rangeCount: collapsed ? 0 : 1, isCollapsed: collapsed,
    removeAllRanges() { calls.push('clear'); },
    addRange(r) { calls.push(r); },
  };
}

test('a held selection is painted while open and selected again on release', () => {
  const range = { startContainer: { isConnected: true }, endContainer: { isConnected: true } };
  const highlights = new Map();
  const selection = fakeSelection();
  withSelectionGlobals(highlights, selection, () => {
    const release = holdSelection(range);
    assert.deepEqual(highlights.get('held-selection').ranges, [range]);
    assert.deepEqual(selection.calls, []);
    release();
    assert.equal(highlights.has('held-selection'), false);
    assert.deepEqual(selection.calls, ['clear', range]);
  });
});

test('release leaves a newer selection, detached ranges and newer holds alone', () => {
  const live = { startContainer: { isConnected: true }, endContainer: { isConnected: true } };
  const highlights = new Map();
  const newer = fakeSelection(false);
  withSelectionGlobals(highlights, newer, () => {
    const first = holdSelection(live);
    const second = holdSelection(live);
    first();
    assert.ok(highlights.has('held-selection'), 'an older release keeps the newer highlight');
    second();
    assert.deepEqual(newer.calls, [], 'the user selected something else meanwhile');
  });
  const detached = { startContainer: { isConnected: false }, endContainer: { isConnected: true } };
  const empty = fakeSelection();
  withSelectionGlobals(undefined, empty, () => {
    holdSelection(detached)();
    assert.deepEqual(empty.calls, []);
    // Without the highlight API, release still restores the selection.
    holdSelection(live)();
    assert.deepEqual(empty.calls, ['clear', live]);
  });
});

test('the transcript holds the selection across its menu and popover', () => {
  const bubble = readFileSync(new URL('../src/lib/components/transcript/MessageBubble.svelte', import.meta.url), 'utf8');
  assert.match(bubble, /let held = \$derived\(menu\?\.selection\?\.range \?\? snippet\?\.range\);/);
  assert.match(bubble, /return range \? holdSelection\(range\) : undefined;/);
  const css = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8');
  assert.match(css, /::highlight\(held-selection\)\s*\{\s*background-color: Highlight;\s*\}/);
});
