import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { compile } from 'svelte/compiler';
import ts from 'typescript';
import { EditorState } from '@codemirror/state';
import { forEachDiagnostic, diagnosticCount } from '@codemirror/lint';
import { EditorView } from '@codemirror/view';
import { undo, redo, undoDepth, indentMore, insertNewlineAndIndent } from '@codemirror/commands';
import { language, indentUnit } from '@codemirror/language';
import { searchPanelOpen } from '@codemirror/search';
import { javascriptLanguage, jsxLanguage, tsxLanguage, typescriptLanguage } from '@codemirror/lang-javascript';
import { jsonLanguage } from '@codemirror/lang-json';
import { yamlLanguage } from '@codemirror/lang-yaml';
import { loadServerComponent, loadServerModule } from './helpers.mjs';
import { projectSourceDiagnostics } from '../src/lib/sourceDiagnostics.ts';

const SRC = new URL('../src/', import.meta.url);
const EDITOR_URL = new URL('lib/fileEditor.ts', SRC);
const COMPONENT_URL = new URL('lib/components/common/go-to-file/FileEditor.svelte', SRC);

// ---------------------------------------------------------------------------
// Runtime harness: the real fileEditor.ts with only EditorView swapped for a
// DOM-free stand-in. Every extension, facet, compartment and command is the
// real CodeMirror one, so state, history, search and language behave for real.
// ---------------------------------------------------------------------------

const views = [];

// Node's EventTarget does not match the capture flag on removal, so track
// listeners by (type, capture) explicitly as the DOM does.
class FakeDom {
  listeners = [];
  addEventListener(type, fn, capture) { this.listeners.push({ type, fn, capture: Boolean(capture) }); }
  removeEventListener(type, fn, capture) {
    this.listeners = this.listeners.filter((l) => !(l.type === type && l.fn === fn && l.capture === Boolean(capture)));
  }
  dispatchEvent(event) {
    for (const l of this.listeners.filter((l) => l.type === event.type && l.capture)) l.fn(event);
  }
}

class FakeEditorView {
  static scrollIntoView = EditorView.scrollIntoView;
  static theme = EditorView.theme;
  static decorations = EditorView.decorations;
  static lineWrapping = EditorView.lineWrapping;
  static editable = EditorView.editable;
  static updateListener = EditorView.updateListener;
  static contentAttributes = EditorView.contentAttributes;

  constructor({ parent, state }) {
    this.parent = parent;
    this.state = state;
    this.dom = new FakeDom();
    // CodeMirror commands call `dispatch` detached from the view.
    this.dispatch = this.dispatch.bind(this);
    this.scrollDOM = Object.assign(new FakeDom(), { scrollTop: 0, scrollLeft: 0 });
    this.documentPadding = { top: 8, bottom: 8 };
    this.root = { activeElement: null };
    this.destroyed = false;
    this.focused = 0;
    this.measures = [];
    this.dispatches = 0;
    this.lateDispatches = 0;
    views.push(this);
  }

  dispatch(...specs) {
    if (this.destroyed) this.lateDispatches++;
    this.dispatches++;
    const tr = this.state.update(...specs);
    this.state = tr.state;
    for (const effect of tr.effects) {
      const probe = EditorView.scrollIntoView(0);
      if (effect.is(probe.type)) this.scrollTarget = effect.value.range.head;
    }
    for (const listener of this.state.facet(EditorView.updateListener)) {
      listener({ view: this, state: this.state, docChanged: tr.docChanged, transactions: [tr] });
    }
  }

  setState(state) { this.state = state; }
  requestMeasure(measure) { this.measures.push(measure); }
  flushMeasures() {
    for (const m of this.measures.splice(0)) m.write(m.read(this), this);
    this.flushScrollTarget();
  }
  flushScrollTarget() {
    if (this.scrollTarget !== undefined) {
      this.scrollDOM.scrollTop = this.lineBlockAt(this.scrollTarget).top + this.documentPadding.top;
      this.scrollTarget = undefined;
    }
  }
  lineBlockAt(from) { return { from, top: (this.state.doc.lineAt(from).number - 1) * 20 }; }
  lineBlockAtHeight(height) {
    this.flushMeasures();
    const number = Math.min(this.state.doc.lines, Math.floor(height / 20) + 1);
    return this.lineBlockAt(this.state.doc.line(number).from);
  }
  plugin() { return null; }
  focus() { this.focused++; }
  destroy() { this.destroyed = true; }
}

globalThis.__fileEditorTestView = FakeEditorView;

async function loadEditorModule() {
  const viewImport = "import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view';";
  let source = readFileSync(EDITOR_URL, 'utf8');
  assert.ok(source.includes(viewImport), 'fileEditor.ts no longer has the expected @codemirror/view import');
  source = source.replace(viewImport, [
    "import { EditorView as RealEditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view';",
    'void RealEditorView;',
    'const EditorView = globalThis.__fileEditorTestView;',
  ].join('\n'));
  // data: modules cannot resolve bare specifiers; pin every CodeMirror package
  // (including the lazily imported languages) to the same files the test uses.
  source = source.replace(/'(@(?:codemirror|lezer)\/[a-z-]+)'/g, (_, name) => JSON.stringify(import.meta.resolve(name)));
  source = source.replace("'./fullscreen'", JSON.stringify(new URL('fullscreen.ts', EDITOR_URL).href));
  source = source.replace("'./sourceDiagnostics'", JSON.stringify(new URL('sourceDiagnostics.ts', EDITOR_URL).href));
  source = source.replace("'./problemNavigation'",  JSON.stringify(new URL('problemNavigation.ts', EDITOR_URL).href));
  source = source.replace("'./files'", JSON.stringify(new URL('files.ts', EDITOR_URL).href));
  source = source.replace("'./codeHighlight'", JSON.stringify(new URL('codeHighlight.ts', EDITOR_URL).href));
  source = source.replace("'@replit/codemirror-lang-svelte'", JSON.stringify(import.meta.resolve('@replit/codemirror-lang-svelte')));
  const js = ts.transpileModule(source, {
    fileName: fileURLToPath(EDITOR_URL),
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText;
  return import(`data:text/javascript,${encodeURIComponent(js)}`);
}

const { createFileEditor } = await loadEditorModule();

const isMac = /Mac|iPhone|iPad/.test(globalThis.navigator?.platform ?? '');

function lintInput(id, ranges = [{ start: { line: 0, character: 1 }, end: { line: 0, character: 3 } }]) {
  return { tab: id, path: 'x.txt', context: 'host/root/generation', current: true, dirty: false, revision: 'r1',
    findings: ranges.map(range => {
      const diagnostic = { path: 'x.txt', range, severity: 'information', source: '<checker>', code: 'C1', message: '<img onerror=evil>', freshness: 'current', basis: { kind: 'saved', revision: 'r1' } };
      return { diagnostic, entries: [{ providerId: 'checker', diagnostic }] };
    }) };
}

test('Stage 1 real store eligible set agrees with editor lint and source across empty, stale and uncertain draft updates', async () => {
  const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
  const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
    ["import { api } from '../api';", 'const api = {};'], ["import { epoch } from '../link';", 'const epoch = () => 0;'],
    ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')],
  ]);
  const id = {}, input = lintInput(id), context = { connectionId: 'c', attachmentId: 'a', generation: 'g' };
  const d = input.findings[0].diagnostic;
  let emit;
  const snapshot = { contractVersion: 1, context, sequence: 0, approval: { state: 'approved' }, providers: [], resultSets: ['a', 'b'].map(providerId => ({ providerId, origin: 'checker', scope: { kind: 'project' }, diagnostics: [{ ...d, source: providerId }] })) };
  const store = new CodeDiagnosticsStore({
    onEvent(name, fn) { if (name === 'codeDiagnostics:updated') emit = fn; return () => {}; },
    async attachCodeDiagnostics() { return { project: { hostId: 'host', root: '/p' }, context, snapshot }; },
    async detachCodeDiagnostics() { return { context }; },
  }, () => 0);
  await store.attach('/p', { phase: 'local', hostId: 'host', generation: 0, capabilities: { codeDiagnostics: true } });
  const mounted = mount({ id, path: 'x.txt', text: 'a😀z\r\n' });
  const before = mounted.view.state.doc.toString();
  const depth = undoDepth(mounted.view.state);
  for (const [sequence, mode] of ['findings', 'dirty', 'uncertain', 'stale', 'empty'].entries()) {
    const next = structuredClone(snapshot); next.sequence = sequence + 1;
    if (mode === 'stale') next.resultSets.forEach(s => s.diagnostics.forEach(d => d.freshness = 'stale'));
    if (mode === 'empty') next.resultSets.forEach(s => s.diagnostics = []);
    emit({ contractVersion: 1, context, sequence: next.sequence, updateId: `u${sequence}`, partIndex: 0, partCount: 1, snapshot: next });
    // FileViewer collapses dirty/uncertain/conflict to the same overlay guard.
    const dirty = mode === 'dirty' || mode === 'uncertain';
    mounted.editor.update({ ...mounted.options, diagnostics: { ...input, dirty, findings: store.findings } });
    const expected = mode === 'findings' ? 1 : 0;
    assert.equal(diagnosticCount(mounted.view.state), expected);
    assert.equal(store.inline('x.txt', { dirty, revision: 'r1' }).length, expected);
    assert.equal(projectSourceDiagnostics('a😀z\r\n', store.findings, { dirty, revision: 'r1' }).segments.length, expected);
    assert.equal(store.findings.length, mode === 'empty' ? 0 : 1);
    assert.equal(mounted.view.state.doc.toString(), before);
    assert.equal(undoDepth(mounted.view.state), depth);
    assert.deepEqual(mounted.calls.changes, []);
  }
  mounted.editor.destroy(); await store.detach();
});

test('lint uses shared verified ranges against LF state, plain attribution and exclusive UTF16 endpoints', () => {
  const id = {}, diagnostics = lintInput(id, [
    { start: { line: 0, character: 1 }, end: { line: 1, character: 1 } },
    { start: { line: 2, character: 0 }, end: { line: 2, character: 0 } },
    { start: { line: 0, character: 2 }, end: { line: 0, character: 3 } },
    undefined,
  ]);
  const { view, editor, options, calls } = mount({ id, text: '\t😀e\u0301\r\nβ\r\n', path: 'x.txt', diagnostics });
  const actual = [];
  forEachDiagnostic(view.state, (d, from, to) => actual.push([from, to, d.severity, d.source, d.message]));
  // EditorState normalizes CRLF; columns remain UTF16, never visual tab widths.
  assert.deepEqual(actual, [[1, 7, 'info', '<checker> C1', '<img onerror=evil>'], [8, 8, 'info', '<checker> C1', '<img onerror=evil>']]);
  assert.deepEqual(calls.changes, []);
  editor.destroy();
});

test('lint rejects stale, uncertain, document and invalid ranges but uses verified provider fallback', () => {
  const id = {}, diagnostics = lintInput(id);
  const base = diagnostics.findings[0];
  for (const change of [{ freshness: 'stale' }, { freshness: 'uncertain' }, { basis: { kind: 'document', documentId: 'd' } }, { locationIssue: 'invalidRange' }, { range: { start: { line: -1, character: 0 }, end: { line: 0, character: 9 } } }]) {
    const diagnostic = { ...base.diagnostic, ...change };
    const findings = [{ diagnostic, entries: [{ providerId: 'checker', diagnostic }] }];
    const mounted = mount({ id, text: 'a😀z', path: 'x.txt', diagnostics: { ...diagnostics, findings } });
    assert.equal(diagnosticCount(mounted.view.state), 0);
    mounted.editor.destroy();
  }
  const diagnostic = { ...base.diagnostic, freshness: 'stale' };
  const mounted = mount({ id, text: 'a😀z', path: 'x.txt', diagnostics: { ...diagnostics, findings: [{ diagnostic, entries: [{ providerId: 'stale', diagnostic }, ...base.entries] }] } });
  assert.equal(diagnosticCount(mounted.view.state), 1);
  mounted.editor.update({ ...mounted.options, diagnostics: undefined });
  assert.equal(diagnosticCount(mounted.view.state), 0, 'revocation clears an already visible marker');
  mounted.editor.destroy();
});

test('live markers use confirmed displayed identity and clear on typing without resetting cached history', () => {
  const id = {}, diagnostics = lintInput(id);
  const diagnostic = { ...diagnostics.findings[0].diagnostic, basis: { kind: 'document', documentId: 'doc', version: 2 } };
  diagnostics.findings = [{ diagnostic, entries: [{ providerId: 'gopls', diagnostic }] }];
  diagnostics.dirty = true;
  diagnostics.live = { documentId: 'doc', version: 2, synced: true };
  const mounted = mount({ id, text: 'a😀z', path: 'x.txt', diagnostics });
  assert.equal(diagnosticCount(mounted.view.state), 1);
  mounted.view.dispatch({ changes: { from: 4, insert: '!' } });
  assert.equal(diagnosticCount(mounted.view.state), 0);
  const depth = undoDepth(mounted.view.state);
  for (const live of [undefined, { documentId: 'other', version: 2, synced: true }, { documentId: 'doc', version: 1, synced: true }, { documentId: 'doc', version: 2, synced: false }]) {
    mounted.editor.update({ ...mounted.options, text: 'a😀z!', diagnostics: { ...diagnostics, live } });
    assert.equal(diagnosticCount(mounted.view.state), 0);
    assert.equal(undoDepth(mounted.view.state), depth);
  }
  mounted.editor.destroy();
  const remount = mount({ ...mounted.options, text: 'a😀z!', diagnostics: { ...diagnostics, live: undefined } });
  assert.equal(diagnosticCount(remount.view.state), 0);
  assert.equal(undoDepth(remount.view.state), depth);
  remount.editor.destroy();
});

test('lint clears in edit/undo transactions and diagnostic updates preserve history, selection, scroll and owners', () => {
  const id = {}, diagnostics = lintInput(id);
  const first = mount({ id, text: 'a😀z', path: 'x.txt', diagnostics });
  const { editor, view, options, calls } = first;
  assert.equal(diagnosticCount(view.state), 1);
  type(view, '!');
  assert.equal(diagnosticCount(view.state), 0, 'no mapped saved marker even before reactive props');
  undo(view);
  assert.equal(diagnosticCount(view.state), 0, 'undo is not disk freshness proof');
  const depth = undoDepth(view.state), selection = view.state.selection;
  view.scrollDOM.scrollTop = 80;
  editor.update({ ...options, diagnostics: { ...diagnostics, dirty: true } });
  assert.equal(diagnosticCount(view.state), 0);
  editor.update({ ...options, diagnostics });
  assert.equal(diagnosticCount(view.state), 1, 'verified clean revision can reapply');
  assert.equal(undoDepth(view.state), depth);
  assert.equal(view.state.selection, selection);
  assert.equal(view.scrollDOM.scrollTop, 80);
  assert.equal(calls.changes.length, 2);
  assert.equal(calls.saves, 0);
  editor.destroy();
  for (const change of [{ current: false }, { findings: [] }, { revision: 'r2' }, { tab: {} }, { path: 'other' }, { context: '' }, { dirty: true }]) {
    const remount = mount({ ...options, diagnostics: { ...diagnostics, ...change } });
    assert.equal(diagnosticCount(remount.view.state), 0, JSON.stringify(change));
    remount.editor.destroy();
  }
  const remount = mount(options);
  assert.equal(diagnosticCount(remount.view.state), 1);
  remount.editor.destroy();
  const count = remount.view.dispatches;
  remount.editor.update({ ...options, diagnostics: { ...diagnostics, findings: [] } });
  assert.equal(remount.view.dispatches, count);
});

function key(k, mods = {}) {
  const { mod = false, alt = false, shift = false, prevented = false } = mods;
  const event = {
    key: k,
    metaKey: isMac && mod,
    ctrlKey: !isMac && mod,
    altKey: alt,
    shiftKey: shift,
    defaultPrevented: prevented,
    prevented: 0,
    stopped: 0,
    preventDefault() { this.prevented++; this.defaultPrevented = true; },
    stopPropagation() { this.stopped++; },
  };
  return event;
}

function mount(overrides = {}) {
  const calls = { changes: [], saves: 0 };
  const options = {
    id: {},
    text: 'const a = 1;\n',
    path: 'src/a.ts',
    version: 1,
    wrap: false,
    disabled: false,
    onchange: (text) => calls.changes.push(text),
    onsave: () => { calls.saves++; },
    ...overrides,
  };
  const before = views.length;
  const editor = createFileEditor({ tag: 'host' }, options);
  assert.equal(views.length, before + 1);
  return { editor, view: views[views.length - 1], options, calls };
}

test('viewer position overrides cached pixels and reports the current logical line', () => {
  const positions = [];
  const { editor, view } = mount({
    text: Array.from({ length: 100 }, (_, i) => `line ${i + 1}`).join('\n'),
    position: { line: 50, offset: -4, left: 30 },
    onposition: position => positions.push(position),
  });
  view.flushMeasures();
  assert.equal(view.scrollDOM.scrollTop, 49 * 20 + 8 + 4);
  assert.equal(view.scrollDOM.scrollLeft, 30);
  editor.destroy();
  assert.deepEqual(positions.at(-1), { line: 50, offset: -4, left: 30 });
  assert.equal(view.scrollDOM.listeners.length, 0);
});

// Bounded by time, not turns: the first import of a language package takes
// well over 500 event-loop turns on a slow CI runner.
async function settle(predicate, message = 'condition never became true', timeoutMs = 10_000) {
  const deadline = Date.now() + timeoutMs;
  while (!predicate()) {
    if (Date.now() > deadline) assert.fail(message);
    await new Promise((resolve) => setImmediate(resolve));
  }
}

// Language support and syntax coloring load independently after async imports.
async function languageOf(view, path) {
  const start = view.dispatches;
  await settle(() => view.state.facet(language) !== null, `language for ${path} never loaded`);
  return view.state.facet(language);
}

function type(view, text, at = view.state.doc.length) {
  view.dispatch({ changes: { from: at, insert: text } });
}

function wrapped(state) {
  return state.facet(EditorView.contentAttributes).some((attrs) => attrs?.class === 'cm-lineWrapping');
}

test('switching modes preserves top padding and partially scrolled line offsets after pending editor scrolls', () => {
  for (const target of [{ line: 1, offset: 8, left: 0 }, { line: 40, offset: -7, left: 24 }]) {
    const { editor, view } = mount({
      text: Array.from({ length: 100 }, (_, i) => `line ${i}`).join('\n'),
      position: target,
    });
    editor.focus();
    view.flushMeasures();
    assert.equal(view.scrollDOM.scrollTop, (target.line - 1) * 20 + 8 - target.offset);
    assert.equal(view.scrollDOM.scrollLeft, target.left);
    editor.destroy();
  }
});

test('indentation is inferred from existing content and used by Tab and Enter', async () => {
  for (const unit of ['  ', '    ', '        ', '\t']) {
    const text = `function outer() {\n${unit}if (true) {\n${unit}${unit}run();\n${unit}}\n}\n`;
    const { editor, view } = mount({ text });
    assert.equal(view.state.facet(indentUnit), unit);
    assert.equal(view.state.tabSize, 4);
    await languageOf(view, 'a.ts');
    view.dispatch({ selection: { anchor: text.indexOf('run();') + 6 } });
    assert.equal(insertNewlineAndIndent(view), true);
    assert.ok(view.state.doc.toString().includes(`run();\n${unit}${unit}\n`), unit);
    view.dispatch({ selection: { anchor: 0 } });
    assert.equal(indentMore(view), true);
    assert.ok(view.state.doc.toString().startsWith(`${unit}function`), unit);
    editor.destroy();
  }
});

test('indent detection ignores blank lines and stays stable until external reload', () => {
  const { editor, view, options } = mount({ text: 'root\n    child\n        nested\n    child\n\nroot' });
  assert.equal(view.state.facet(indentUnit), '    ');
  type(view, '\n  temporary');
  assert.equal(view.state.facet(indentUnit), '    ');
  editor.update({ ...options, version: 2, text: 'root\n\tchild\n\t\tnested' });
  assert.equal(view.state.facet(indentUnit), '\t');
  editor.destroy();
  const empty = mount({ text: '' });
  assert.equal(empty.view.state.facet(indentUnit), '  ', 'no evidence keeps the default');
  empty.editor.destroy();
});

test('line selection crosses viewer/editor mounts and reports the cursor line on exit', () => {
  const selections = [];
  const id = {};
  const first = mount({ id, text: 'one\ntwo\nthree', selection: { anchor: 2, head: 3 }, onselection: value => selections.push(value) });
  assert.equal(first.view.state.selection.main.anchor, 4);
  assert.equal(first.view.state.selection.main.head, 8);
  first.editor.destroy();
  assert.deepEqual(selections, [{ anchor: 2, head: 3 }]);
  const second = mount({ id, text: 'one\ntwo\nthree', selection: { anchor: 1, head: 1 } });
  assert.equal(second.view.state.selection.main.head, 0, 'viewer selection overrides cached editor selection');
  second.editor.destroy();
});

test('editor starts from the given text and reports every edit as a full snapshot', () => {
  const { view, calls } = mount({ text: 'one\n' });
  assert.equal(view.state.doc.toString(), 'one\n');
  assert.equal(calls.changes.length, 0, 'configuration must not count as an edit');
  type(view, 'two\n');
  type(view, 'three\n');
  assert.deepEqual(calls.changes, ['one\ntwo\n', 'one\ntwo\nthree\n']);
});

test('history: undo/redo work and also report the restored text', () => {
  const { view, calls } = mount({ text: 'base' });
  type(view, '!');
  // Adjacent fast edits may share one history group, so undo until exhausted.
  view.dispatch({ changes: { from: 0, insert: '>' } });
  assert.equal(view.state.doc.toString(), '>base!');
  assert.ok(undoDepth(view.state) >= 1);
  while (undo(view));
  assert.equal(view.state.doc.toString(), 'base');
  assert.equal(calls.changes.at(-1), 'base');
  assert.equal(redo(view), true);
  assert.notEqual(view.state.doc.toString(), 'base');
  assert.equal(calls.changes.at(-1), view.state.doc.toString());
});

test('wrap and disabled options are applied and reconfigured without losing the document or history', () => {
  const { editor, view, options } = mount({ text: 'x', wrap: true, disabled: true });
  assert.equal(wrapped(view.state), true);
  assert.equal(view.state.readOnly, true);
  assert.equal(view.state.facet(EditorView.editable), false);
  assert.equal(undo(view), false, 'read-only editors refuse undo');

  editor.update({ ...options, disabled: false });
  assert.equal(view.state.readOnly, false);
  assert.equal(view.state.facet(EditorView.editable), true);
  type(view, 'y');

  editor.update({ ...options, disabled: false, wrap: false, text: 'xy' });
  assert.equal(wrapped(view.state), false);
  assert.equal(view.state.doc.toString(), 'xy', 'echoing the current draft back must not reset the editor');
  assert.equal(undo(view), true, 'history survives option-only updates');
  assert.equal(view.state.doc.toString(), 'x');
});

test('languages load lazily by extension, case-insensitively, with JS/TS/JSX dialects', async () => {
  const cases = [
    ['a.js', javascriptLanguage],
    ['a.mjs', javascriptLanguage],
    ['a.ts', typescriptLanguage],
    ['a.cts', typescriptLanguage],
    ['a.jsx', jsxLanguage],
    ['a.tsx', tsxLanguage],
    ['DATA.JSON', jsonLanguage],
    ['ci.yml', yamlLanguage],
  ];
  for (const [path, expected] of cases) {
    const { editor, view } = mount({ path });
    assert.equal(view.state.facet(language), null, `${path}: language must not be applied synchronously`);
    assert.equal(await languageOf(view, path), expected, path);
    editor.destroy();
  }
  // These packages build their Language per call, so compare by name.
  for (const [path, name] of [['a.py', 'python'], ['main.go', 'go'], ['index.htm', 'html'], ['site.css', 'css'], ['README.md', 'markdown'], ['x.Markdown', 'markdown']]) {
    const { editor, view } = mount({ path });
    assert.equal((await languageOf(view, path))?.name, name, path);
    editor.destroy();
  }
  for (const path of ['Makefile', 'notes.txt', 'archive.tar.unknownext']) {
    const { editor, view } = mount({ path });
    const start = view.dispatches;
    await settle(() => view.dispatches > start);
    assert.equal(view.state.facet(language), null, `${path}: plain text`);
    editor.destroy();
  }
});

test('editor uses viewer tokens for builtins, properties, entities and live edits', async () => {
  const { highlightFile } = await import('../src/lib/codeHighlight.ts');
  for (const [path, lang, text] of [
    ['a.ts', 'typescript', 'const x = console.log("<&😀");'],
    ['a.py', 'python', 'print("<&😀") # comment'],
    ['a.json', 'json', '{"key": "<&😀", "n": true}'],
    ['a.css', 'css', '.item { color: red; }'],
  ]) {
    const { editor, view } = mount({ path, text });
    const ranges = () => view.state.facet(EditorView.decorations).filter(x => typeof x !== 'function');
    await settle(() => ranges().length > 0);
    for (const suffix of ['', '\n']) {
      if (suffix) type(view, suffix);
      const doc = view.state.doc.toString();
      const actual = Array.from({ length: doc.length }, () => []);
      for (const set of ranges()) set.between(0, doc.length, (from, to, mark) => {
        for (let i = from; i < to; i++) actual[i].push(mark.spec.class);
      });
      const html = await highlightFile(doc, lang);
      const expected = [];
      const stack = [];
      for (const token of html.matchAll(/<span class="([^"]*)">|<\/span>|&[^;]+;|[^<&]+/g)) {
        if (token[1] !== undefined) stack.push(token[1]);
        else if (token[0] === '</span>') stack.pop();
        else for (let i = 0; i < (token[0].startsWith('&') ? 1 : token[0].length); i++) expected.push([...stack].sort());
      }
      assert.deepEqual(actual.map(classes => classes.sort()), expected, path);
    }
    editor.destroy();
  }
});

test('renaming the path swaps the language; the latest request wins over slower earlier ones', async () => {
  const { editor, view, options } = mount({ path: 'a.ts' });
  assert.equal(await languageOf(view, 'a.ts'), typescriptLanguage);
  // Fire two path changes back to back; the earlier one must never land last.
  editor.update({ ...options, path: 'a.json' });
  editor.update({ ...options, path: 'a.md' });
  await settle(() => view.state.facet(language)?.name === 'markdown');
  for (let i = 0; i < 20; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(view.state.facet(language)?.name, 'markdown');
  assert.equal(view.state.doc.toString(), options.text, 'a rename alone keeps the document');
});

test('a language that finishes loading after destroy is never dispatched', async () => {
  const { editor, view } = mount({ path: 'late.yaml' });
  editor.destroy();
  for (let i = 0; i < 50; i++) await new Promise((resolve) => setImmediate(resolve));
  assert.equal(view.lateDispatches, 0);
  assert.equal(view.destroyed, true);
});

test('remount of the same tab/version/text restores state, undo history and scroll', () => {
  const id = {};
  const first = mount({ id, text: 'hello', version: 3 });
  type(first.view, ' world');
  first.view.scrollDOM.scrollTop = 120;
  first.view.scrollDOM.scrollLeft = 7;
  first.editor.destroy();

  const second = mount({ id, text: 'hello world', version: 3 });
  assert.equal(second.view.state.doc.toString(), 'hello world');
  assert.deepEqual(
    { scrollTop: second.view.scrollDOM.scrollTop, scrollLeft: second.view.scrollDOM.scrollLeft },
    { scrollTop: 120, scrollLeft: 7 },
  );
  // Layout can clobber the immediate assignment; the measure pass re-applies it.
  second.view.scrollDOM.scrollTop = 0;
  second.view.flushMeasures();
  assert.equal(second.view.scrollDOM.scrollTop, 120);
  assert.equal(undo(second.view), true, 'history crossed the unmount');
  assert.equal(second.view.state.doc.toString(), 'hello');
  assert.deepEqual(second.calls.changes, ['hello'], 'the remounted editor reports to its own callbacks');
  assert.deepEqual(first.calls.changes, ['hello world'], 'the unmounted owner is not called again');
});

test('remount after a version bump or a text mismatch starts fresh', () => {
  for (const change of [{ version: 2 }, { text: 'reloaded from disk' }]) {
    const id = {};
    const first = mount({ id, text: 'draft', version: 1 });
    type(first.view, '!');
    first.view.scrollDOM.scrollTop = 50;
    first.editor.destroy();

    const next = { id, text: 'draft!', version: 1, ...change };
    const second = mount(next);
    assert.equal(second.view.state.doc.toString(), next.text);
    assert.equal(second.view.scrollDOM.scrollTop, 0);
    assert.equal(undo(second.view), false, `${JSON.stringify(change)}: stale history must not leak`);
  }
});

test('update with a new version or external text resets document and history', () => {
  const { editor, view, options, calls } = mount({ text: 'v1', version: 1 });
  type(view, '+');
  editor.update({ ...options, text: 'v2 from disk', version: 2 });
  assert.equal(view.state.doc.toString(), 'v2 from disk');
  assert.equal(undo(view), false);
  assert.deepEqual(calls.changes, ['v1+'], 'a reset is not reported as a user edit');

  view.scrollDOM.scrollTop = 99;
  editor.update({ ...options, text: 'v2 from disk', version: 3 });
  view.flushMeasures();
  assert.equal(view.scrollDOM.scrollTop, 0, 'a reset scrolls back to the top');
});

test('switching tabs in place keeps each tab\'s own state and routes callbacks to the active tab', () => {
  const a = {};
  const b = {};
  const { editor, view, options, calls } = mount({ id: a, text: 'A', version: 1 });
  type(view, 'a');
  view.scrollDOM.scrollTop = 40;

  const bCalls = [];
  const bOptions = { ...options, id: b, text: 'B', version: 1, path: 'b.md', onchange: (t) => bCalls.push(t) };
  editor.update(bOptions);
  assert.equal(view.state.doc.toString(), 'B');
  view.flushMeasures();
  assert.equal(view.scrollDOM.scrollTop, 0);
  type(view, 'b');
  assert.deepEqual(bCalls, ['Bb']);
  assert.deepEqual(calls.changes, ['Aa']);

  editor.update({ ...options, text: 'Aa' });
  assert.equal(view.state.doc.toString(), 'Aa');
  view.flushMeasures();
  assert.equal(view.scrollDOM.scrollTop, 40);
  assert.equal(undo(view), true);
  assert.equal(view.state.doc.toString(), 'A');
  assert.deepEqual(calls.changes, ['Aa', 'A']);

  editor.update({ ...bOptions, text: 'Bb' });
  assert.equal(view.state.doc.toString(), 'Bb', 'tab B was remembered when switching away');
  assert.equal(undo(view), true);
  assert.equal(view.state.doc.toString(), 'B');
});

test('keyboard: Mod-S saves, Mod-F opens search, Escape closes only an open search', () => {
  const { editor, view, calls } = mount();

  const save = key('s', { mod: true });
  assert.equal(editor.handleKey(save), true);
  assert.equal(calls.saves, 1);
  assert.equal(save.prevented, 1);
  assert.equal(save.stopped, 1);

  assert.equal(editor.handleKey(key('S', { mod: true })), true, 'Caps Lock / uppercase key still saves');
  assert.equal(calls.saves, 2);

  const escapeClosed = key('Escape');
  assert.equal(editor.handleKey(escapeClosed), false, 'Escape without a search panel bubbles to the dialog');
  assert.equal(escapeClosed.prevented, 0);

  assert.equal(editor.handleKey(key('f', { mod: true })), true);
  assert.equal(searchPanelOpen(view.state), true);

  const escapeOpen = key('Escape');
  assert.equal(editor.handleKey(escapeOpen), true);
  assert.equal(escapeOpen.stopped, 1);
  assert.equal(searchPanelOpen(view.state), false);

  for (const ev of [key('s', { mod: true, shift: true }), key('s', { mod: true, alt: true }), key('s'), key('f', { alt: true })]) {
    assert.equal(editor.handleKey(ev), false);
    assert.equal(ev.prevented, 0);
  }
  assert.equal(calls.saves, 2);

  const consumed = key('s', { mod: true, prevented: true });
  assert.equal(editor.handleKey(consumed), true, 'an event already consumed by the editor is reported as handled');
  assert.equal(calls.saves, 2, '...without saving twice');
});

test('find/closeSearch commands drive the real search panel state', () => {
  const { editor, view } = mount();
  assert.equal(editor.closeSearch(), false);
  assert.equal(editor.find(), true);
  assert.equal(searchPanelOpen(view.state), true);
  assert.equal(editor.closeSearch(), true);
  assert.equal(searchPanelOpen(view.state), false);
});

test('the capturing keydown listener saves and is removed on destroy', () => {
  const { editor, view, calls } = mount();
  const event = Object.assign(new Event('keydown', { cancelable: true }), {
    key: 's', metaKey: isMac, ctrlKey: !isMac, altKey: false, shiftKey: false,
  });
  view.dom.dispatchEvent(event);
  assert.equal(calls.saves, 1);
  assert.equal(event.defaultPrevented, true);

  editor.destroy();
  const after = Object.assign(new Event('keydown', { cancelable: true }), {
    key: 's', metaKey: isMac, ctrlKey: !isMac, altKey: false, shiftKey: false,
  });
  view.dom.dispatchEvent(after);
  assert.equal(calls.saves, 1);
  assert.equal(after.defaultPrevented, false);
});

test('after destroy every controller method is an inert no-op and destroy is idempotent', () => {
  const { editor, view, options, calls } = mount();
  editor.focus();
  assert.equal(view.focused, 1);
  editor.destroy();
  editor.destroy();
  const dispatches = view.dispatches;
  editor.focus();
  editor.update({ ...options, text: 'ignored', version: 9 });
  assert.equal(editor.find(), false);
  assert.equal(editor.closeSearch(), false);
  assert.equal(editor.handleKey(key('s', { mod: true })), false);
  assert.equal(view.focused, 1);
  assert.equal(view.dispatches, dispatches);
  assert.equal(view.state.doc.toString(), options.text);
  assert.equal(calls.saves, 0);
});

// ---------------------------------------------------------------------------
// Component: SSR render and the lazy-loading boundary.
// ---------------------------------------------------------------------------

const component = await loadServerComponent(COMPONENT_URL, [
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["import { filePrompt } from '../../../stores/filePrompt.svelte';", 'const filePrompt = { request: null };'],
]);

test('FileEditor server-renders an empty host and inert command exports before the runtime loads', async () => {
  const { render } = await import('svelte/server');
  const bound = {};
  const props = {
    id: {}, text: 'secret draft', path: 'a.ts', version: 1, wrap: false,
    onchange() { assert.fail('no edits during SSR'); },
    onsave() { assert.fail('no saves during SSR'); },
  };
  for (const name of ['focus', 'find', 'closeSearch', 'handleKey']) {
    Object.defineProperty(props, name, { get: () => undefined, set: (fn) => { bound[name] = fn; }, enumerable: true });
  }
  const html = render(component.default, { props }).body;
  assert.match(html, /<div class="file-editor[^"]*"[^>]*><\/div>/);
  assert.match(html, /autocorrect="off"/);
  assert.match(html, /autocapitalize="off"/);
  assert.match(html, /spellcheck="false"/);
  assert.doesNotMatch(html, /role="alert"/);
  assert.doesNotMatch(html, /secret draft/, 'the document is rendered by CodeMirror, not by SSR markup');
  assert.deepEqual(Object.keys(bound).sort(), ['closeSearch', 'find', 'focus', 'handleKey']);
  assert.equal(bound.find(), false);
  assert.equal(bound.closeSearch(), false);
  assert.equal(bound.handleKey(key('s', { mod: true })), false);
  assert.equal(bound.focus(), undefined);
});

function staticImports(code) {
  // import x from '…' / import { … } from '…' / import '…' / export … from '…'
  const re = /^\s*(?:import|export)\s+(type\s+)?(?:[^'";]*?\s+from\s+)?['"]([^'"]+)['"]/gm;
  return [...code.matchAll(re)].map(([, type, spec]) => ({ type: Boolean(type), spec }));
}

test('FileEditor component reaches fileEditor/CodeMirror only through a dynamic import', () => {
  const source = readFileSync(COMPONENT_URL, 'utf8');
  for (const { type, spec } of staticImports(source)) {
    if (/fileEditor|@codemirror/.test(spec)) assert.ok(type, `static value import of ${spec}`);
  }
  const client = compile(source, { generate: 'client', filename: fileURLToPath(COMPONENT_URL) }).js.code;
  const specs = staticImports(client).map((i) => i.spec);
  assert.ok(specs.length > 0, 'import scan found nothing; regex out of date?');
  for (const spec of specs) assert.doesNotMatch(spec, /fileEditor|@codemirror/);
  assert.match(client, /import\(\s*['"]\.\.\/\.\.\/\.\.\/fileEditor['"]\s*\)/);
  assert.match(source, /diagnostics, position/);
  assert.match(source, /createFileEditor\(host, options\(\)\)/, 'lazy resolution reads latest props, not a captured snapshot');
  assert.match(source, /if \(!disposed\)/);
  const viewer = readFileSync(new URL('lib/components/common/go-to-file/FileViewer.svelte', SRC), 'utf8');
  assert.match(viewer, /diagnostics=\{\{ tab: activeTab, path: activeTab.path/);
  assert.match(viewer, /!codeDiagnostics.revoking/);
  assert.match(viewer, /edit.dirty \|\| edit.uncertain \|\| edit.conflict/);
});

test('no other source file statically pulls CodeMirror or fileEditor into the main bundle', () => {
  const root = fileURLToPath(SRC);
  const offenders = [];
  for (const rel of readdirSync(root, { recursive: true })) {
    if (!/\.(ts|js|svelte)$/.test(rel) || rel.replace(/\\/g, '/') === 'lib/fileEditor.ts') continue;
    const code = readFileSync(new URL(rel.replace(/\\/g, '/'), SRC), 'utf8');
    for (const { type, spec } of staticImports(code)) {
      if (!type && (/^@codemirror\//.test(spec) || /(^|\/)fileEditor(\.ts)?$/.test(spec))) offenders.push(`${rel}: ${spec}`);
    }
  }
  assert.deepEqual(offenders, []);
});

test('diagnostic range uses exact UTF16 columns without doc/history/blame changes', () => {
  const { editor, view, calls } = mount({ text: 'a😀z\nβxy', path: 'a.txt' });
  view.dispatch({ changes: { from: 8, insert: '!' } });
  const depth = undoDepth(view.state), text = view.state.doc.toString();
  assert.equal(editor.revealRange({ start: { line: 0, character: 1 }, end: { line: 1, character: 2 } }), true);
  assert.equal(view.state.selection.main.anchor, 1);
  assert.equal(view.state.selection.main.head, 7);
  assert.equal(view.state.doc.toString(), text);
  assert.equal(undoDepth(view.state), depth);
  assert.equal(calls.changes.length, 1);
  assert.equal(editor.revealRange({ start: { line: 0, character: 2 }, end: { line: 0, character: 3 } }), false);
  editor.destroy();
});
