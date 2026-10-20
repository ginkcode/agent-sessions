import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerComponent, renderWithFixture, renderedButtons, unescapeHTML } from './helpers.mjs';

// FileViewer's editing controls, server-rendered against fixture stores. The
// pure helpers (files, fileEditing, fileFind) are the real modules; child
// components and the browser-only find/zoom controllers are stand-ins.
const VIEWER_URL = new URL('../src/lib/components/common/go-to-file/FileViewer.svelte', import.meta.url);
const lib = (rel) => JSON.stringify(new URL(rel, VIEWER_URL).href);

// Child stand-ins record the props the viewer hands them as data attributes.
const stub = (tag, attrs) =>
  `($$renderer, $$props) => $$renderer.push(\`<div ${tag}${attrs}></div>\`)`;
const attr = (name, expr) => ` ${name}="\${String(${expr}).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')}"`;

const viewer = await loadServerComponent(VIEWER_URL, [
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["import { highlightFile, splitHighlightedLines, splitLines } from '../../../codeHighlight';",
    "const highlightFile = async () => ''; const splitHighlightedLines = (h) => h.split('\\n'); const splitLines = (t) => t.split('\\n');"],
  ["import { copyToClipboard } from '../../../portable';", 'const copyToClipboard = async () => true;'],
  ["'../../../fileFind'", lib('../../../fileFind.ts')],
  ["'../../../files'", lib('../../../files.ts')],
  ["import { toast } from '../../../stores/toast.svelte';", fixtureStore('toast')],
  ["import { markdownFileTarget } from '../../../fileActions';", 'const markdownFileTarget = () => null;'],
  ["import { api } from '../../../api';", fixtureStore('api')],
  ["import { epoch } from '../../../link';", 'const epoch = () => 1;'],
  ["import { errorText } from '../../../manage';", 'const errorText = (_, fallback) => fallback;'],
  ["import { link } from '../../../stores/link.svelte';", fixtureStore('link')],
  ["import { connectionStore } from '../../../stores/connection.svelte';", fixtureStore('connectionStore')],
  ["import { fileActions } from '../../../stores/fileActions.svelte';", fixtureStore('fileActions')],
  ["'../../../fileEditing'", lib('../../../fileEditing.ts')],
  ["'../../../fullscreen'", lib('../../../fullscreen.ts')],
  ["import LoadingSpinner from '../LoadingSpinner.svelte';", `const LoadingSpinner = ${stub('data-spinner', '')};`],
  ["import MarkdownDoc from '../MarkdownDoc.svelte';", `const MarkdownDoc = ${stub('data-markdown-doc', '')};`],
  ["import SourceView from './SourceView.svelte';",
    `const SourceView = ${stub('data-source-view', attr('data-show-diff', '$$props.showDiff'))};`],
  ["import FileEditor from './FileEditor.svelte';",
    `const FileEditor = ${stub('data-file-editor', [
      attr('data-text', '$$props.text'),
      attr('data-path', '$$props.path'),
      attr('data-version', '$$props.version'),
      attr('data-wrap', '$$props.wrap'),
      attr('data-disabled', '$$props.disabled'),
    ].join(''))};`],
  ["import { createImagePreview } from './imagePreview.svelte';", `const createImagePreview = () => ({
    imgZoom: 1, MIN_ZOOM: 0.1, MAX_ZOOM: 16, ZOOM_STEP: 1.25, imgFitted: true, imgFailed: false, imgDragging: false,
    zoomImage() {}, fitImage() {}, onImageLoad() {}, onImagePointerDown() {}, onImagePointerMove() {}, onImagePointerUp() {},
    handleImageZoomKey: () => false,
  });`],
  ["import { createFileFind } from './fileFind.svelte';", `const createFileFind = () => ({
    findActive: false, findQuery: '', findCase: false, findCurrent: 0, found: { matches: [] },
    openFind() {}, closeFind() {}, stepMatch() {}, handleFindKey() {}, schedulePaint() {},
  });`],
]);

function content(path, text, extra = {}) {
  return {
    path, size: new TextEncoder().encode(text).length, text,
    revision: 'rev-1', edit: { eligible: true, newline: text.includes('\n') ? 'lf' : 'none' },
    ...extra,
  };
}

function editState(baseline, draft = baseline, extra = {}) {
  return {
    editing: true, baseline, revision: 'rev-1', draft,
    get dirty() { return this.draft !== this.baseline; },
    saving: false, saveError: null, conflict: null, uncertain: null, version: 3,
    ...extra,
  };
}

function render({ files = {}, link = {}, connectionStore = {}, fileActions = {} } = {}, props = {}) {
  const fileContent = files.content ?? null;
  const selected = files.selected ?? fileContent?.path;
  return renderWithFixture(viewer, {
    files: {
      open: true, dir: '/project', wrap: false, diff: false,
      listing: { files: selected ? [selected] : [], status: {} },
      selected, content: fileContent, loadingFile: false, fileError: null,
      activeTab: selected ? { path: selected, preview: false, content: fileContent, loading: false, error: null, token: 1 } : undefined,
      toggleWrap() {}, toggleDiff() {}, openFile() {}, keepTab() {}, startEditing: () => true, updateDraft() {},
      ...files,
    },
    link: { dataHost: undefined, blockedReason: null, ...link },
    connectionStore: { capabilities: { saveFile: true }, ...connectionStore },
    fileActions: { busy: false, quitting: false, save() {}, discard() {}, done() {}, ...fileActions },
    toast: { show() {} },
    api: { enableFileEditing: async () => true },
  }, { presentation: false, presentationFontSize: 20, ...props });
}

function editingFiles(path, baseline, draft, { status = {}, edit = {}, contentExtra = {}, ...files } = {}) {
  const fileContent = content(path, baseline, contentExtra);
  return {
    selected: path,
    content: fileContent,
    listing: { files: [path], status },
    activeTab: { path, preview: false, content: fileContent, loading: false, error: null, token: 1, edit: editState(baseline, draft, edit) },
    ...files,
  };
}

// Icon buttons have no text, so name each button by its text or aria-label.
function namedButtons(html) {
  const labels = [...html.matchAll(/<button\b([^>]*)>/g)].map(([, attrs]) => attrs.match(/\saria-label="([^"]*)"/)?.[1]);
  return renderedButtons(html).map((b, i) => ({
    text: b.text.replace(/<[^>]*>/g, '').trim() || unescapeHTML(labels[i] ?? ''),
    disabled: b.disabled,
  }));
}

function button(html, text) {
  return namedButtons(html).find((b) => b.text === text);
}

function buttonTexts(html) {
  return namedButtons(html).map((b) => b.text);
}

function editorProps(html) {
  // Attribute values may hold a literal '>', so match whole quoted attributes.
  const tag = html.match(/<div data-file-editor(?:\s+[a-z-]+="[^"]*")*>/)?.[0];
  if (!tag) return null;
  return Object.fromEntries([...tag.matchAll(/data-([a-z]+)="([^"]*)"/g)].map(([, k, v]) => [k, unescapeHTML(v)]));
}

test('an eligible empty file offers Edit beside the empty state', async () => {
  const html = await render({ files: { content: content('notes/empty.txt', '') } });
  assert.match(html, /This file is empty\./);
  assert.deepEqual(button(html, 'Edit'), { text: 'Edit', disabled: false });
  assert.match(html, /title="Edit this file"/);
  for (const text of ['Save', 'Discard', 'Done']) assert.equal(button(html, text), undefined, text);
  assert.equal(editorProps(html), null);
});

test('an eligible text file offers Edit alongside the read-only viewer tools', async () => {
  const html = await render({ files: { content: content('src/main.ts', 'const a = 1;\n') } });
  assert.deepEqual(button(html, 'Edit'), { text: 'Edit', disabled: false });
  assert.ok(button(html, 'Find') && button(html, 'Wrap') && button(html, 'Copy'));
  assert.match(html, /data-source-view/);
});

test('an older server without the save capability shows no Edit, only that the host cannot save', async () => {
  for (const capabilities of [{}, { saveFile: false }]) {
    const html = await render({ connectionStore: { capabilities }, files: { content: content('src/main.ts', 'x\n') } });
    assert.equal(button(html, 'Edit'), undefined, JSON.stringify(capabilities));
    assert.match(html, />Read-only: this host cannot save files<\/span>/);
    assert.match(html, /data-source-view/);
  }
  // An ineligible file names its own reason rather than the host's.
  const ineligible = await render({
    connectionStore: { capabilities: {} },
    files: { content: content('src/main.ts', 'x\n', { edit: { eligible: false, reason: 'protected' } }) },
  });
  assert.match(ineligible, />Read-only: protected file<\/span>/);
  assert.doesNotMatch(ineligible, /this host cannot save/);
});

test('content from an older server, without revision or edit info, stays plainly read-only', async () => {
  const old = { path: 'src/main.ts', size: 2, text: 'x\n' };
  const html = await render({ files: { content: old } });
  assert.equal(button(html, 'Edit'), undefined);
  assert.doesNotMatch(html, /Read-only/);
  const empty = await render({ files: { content: { path: 'empty.txt', size: 0, text: '' } } });
  assert.equal(button(empty, 'Edit'), undefined);
  assert.match(empty, /This file is empty\./);
});

test('an ineligible text file names why it is read-only instead of offering Edit', async () => {
  const cases = [
    ['too_large', 'Read-only: too large to edit'],
    ['unsupported_newlines', 'Read-only: mixed line endings'],
    ['protected', 'Read-only: protected file'],
    ['linked', 'Read-only: linked file'],
    ['read_only', 'Read-only file'],
    ['not_checked', 'Read-only: editing not available'],
    ['some_future_reason', 'Read-only'],
  ];
  for (const [reason, label] of cases) {
    const html = await render({ files: { content: content('a.txt', 'x\n', { edit: { eligible: false, reason } }) } });
    assert.equal(button(html, 'Edit'), undefined, reason);
    assert.ok(html.includes(`>${label}</span>`), `${reason}: ${label}`);
  }
});

test('binary, image and too-large reads neither offer Edit nor claim a read-only reason', async () => {
  const cases = [
    { path: 'bin.dat', size: 10, binary: true },
    { path: 'big.log', size: 9_000_000, tooLarge: true },
    { path: 'pic.png', size: 4, image: 'image/png', data: 'AAAA' },
  ];
  for (const c of cases) {
    const html = await render({ files: { content: { ...c, edit: { eligible: false, reason: 'too_large' } } } });
    assert.equal(button(html, 'Edit'), undefined, c.path);
    assert.doesNotMatch(html, /Read-only/, c.path);
  }
});

test('Edit is shown but disabled while the link is blocked or the coordinator is busy', async () => {
  const fileContent = content('a.txt', 'x\n');
  const blocked = await render({ link: { blockedReason: 'Reconnecting to host' }, files: { content: fileContent } });
  assert.deepEqual(button(blocked, 'Edit'), { text: 'Edit', disabled: true });
  assert.match(blocked, /title="Reconnecting to host"/);
  for (const fileActions of [{ busy: true }, { quitting: true }]) {
    const html = await render({ fileActions, files: { content: fileContent } });
    assert.deepEqual(button(html, 'Edit'), { text: 'Edit', disabled: true }, JSON.stringify(fileActions));
  }
});

test('no Edit appears while the file is loading or failed to load', async () => {
  const fileContent = content('a.txt', 'x\n');
  const loading = await render({ files: { content: fileContent, loadingFile: true } });
  assert.equal(button(loading, 'Edit'), undefined);
  assert.match(loading, /data-spinner/);
  const failed = await render({ files: { content: fileContent, fileError: 'Cannot read <a.txt>' } });
  assert.equal(button(failed, 'Edit'), undefined);
  assert.match(failed, /role="alert">Cannot read &lt;a\.txt>/);
});

test('editing shows the editor with the draft and Save, Discard and Done', async () => {
  const html = await render({ files: editingFiles('src/main.ts', 'old\n', 'new <draft> & more\n', { wrap: true }) });
  assert.deepEqual(editorProps(html), {
    text: 'new <draft> & more\n', path: 'src/main.ts', version: '3', wrap: 'true', disabled: 'false',
  });
  assert.deepEqual(button(html, 'Save'), { text: 'Save', disabled: false });
  assert.deepEqual(button(html, 'Discard'), { text: 'Discard', disabled: false });
  assert.deepEqual(button(html, 'Done'), { text: 'Done', disabled: false });
  assert.ok(button(html, 'Wrap'), 'wrap stays available in the editor');
  assert.equal(button(html, 'Edit'), undefined);
  assert.match(html, />Unsaved<\/span>/);
  assert.doesNotMatch(html, /data-source-view/);
});

test('an editing draft overrides the deleted listing state', async () => {
  const html = await render({ files: editingFiles('gone.txt', 'was here\n', 'kept draft\n', { status: { 'gone.txt': 'D' } }) });
  assert.doesNotMatch(html, /This file was deleted since the last commit\./);
  assert.equal(editorProps(html)?.text, 'kept draft\n');
  for (const text of ['Save', 'Discard', 'Done']) assert.ok(button(html, text), text);
  // The same file, not editing, still shows the deleted state.
  const viewing = await render({
    files: { content: content('gone.txt', 'was here\n'), listing: { files: [], status: { 'gone.txt': 'D' } } },
  });
  assert.match(viewing, /This file was deleted since the last commit\./);
  assert.equal(editorProps(viewing), null);
});

test('an editing draft of an empty file replaces the empty state', async () => {
  const clean = await render({ files: editingFiles('empty.txt', '', '') });
  assert.doesNotMatch(clean, /This file is empty\./);
  assert.equal(editorProps(clean)?.text, '');
  assert.deepEqual(button(clean, 'Save'), { text: 'Save', disabled: true }, 'nothing to save yet');
  assert.ok(button(clean, 'Discard') && button(clean, 'Done'));
  assert.doesNotMatch(clean, />Unsaved</);

  const typed = await render({ files: editingFiles('empty.txt', '', 'first line') });
  assert.doesNotMatch(typed, /This file is empty\./);
  assert.equal(editorProps(typed)?.text, 'first line');
  assert.deepEqual(button(typed, 'Save'), { text: 'Save', disabled: false });
});

test('an editing draft overrides loading and read-error states', async () => {
  const loading = await render({ files: editingFiles('a.txt', 'x\n', 'y\n', { loadingFile: true }) });
  assert.doesNotMatch(loading, /data-spinner/);
  assert.equal(editorProps(loading)?.text, 'y\n');
  const failed = await render({ files: editingFiles('a.txt', 'x\n', 'y\n', { fileError: 'gone' }) });
  assert.doesNotMatch(failed, /role="alert">gone/);
  assert.equal(editorProps(failed)?.text, 'y\n');
});

test('editing hides preview, Git diff, find, copy and presentation controls', async () => {
  const changes = [{ kind: 'modified', start: 1, count: 1, removed: ['# Old'] }];
  const files = (editing) => {
    const base = editingFiles('README.md', '# Title\n', '# Draft\n', {
      diff: true, status: { 'README.md': 'M' }, contentExtra: { changes },
    });
    return editing ? base : { ...base, activeTab: { ...base.activeTab, edit: undefined } };
  };

  // Baseline: the same Markdown file, viewing, does show every read-only tool.
  const viewing = await render({ files: files(false) }, { presentation: true });
  for (const text of ['Preview', 'Git diff', 'Find', 'Copy', 'Edit']) assert.ok(button(viewing, text), `viewing: ${text}`);
  assert.match(viewing, /aria-label="Exit presentation mode"/);
  assert.match(viewing, /aria-label="Increase presentation font size"/);
  assert.match(viewing, /data-source-view data-show-diff="true"/);

  const editing = await render({ files: files(true) }, { presentation: true });
  const texts = buttonTexts(editing);
  for (const text of ['Preview', 'Git diff', 'Find', 'Copy', 'Edit']) assert.ok(!texts.includes(text), `editing: ${text}`);
  assert.doesNotMatch(editing, /presentation mode|presentation font size/i);
  assert.doesNotMatch(editing, /data-source-view|data-markdown-doc/);
  assert.doesNotMatch(editing, /role="search"/);
  for (const text of ['Wrap', 'Save', 'Discard', 'Done']) assert.ok(texts.includes(text), `editing: ${text}`);
});

test('saving disables Save, Discard and Done and announces the save', async () => {
  const html = await render({ files: editingFiles('a.txt', 'x\n', 'y\n', { edit: { saving: true } }) });
  assert.match(html, /aria-live="polite">Saving…<\/span>/);
  assert.doesNotMatch(html, />Unsaved</);
  for (const text of ['Save', 'Discard', 'Done']) assert.deepEqual(button(html, text), { text, disabled: true }, text);
});

test('Save is disabled for an uncertain save, a blocked link, a frozen coordinator or a host without saves', async () => {
  const cases = [
    [{ files: editingFiles('a.txt', 'x\n', 'y\n', { edit: { uncertain: { text: 'y\n', expectedRevision: 'rev-1' } } }) }, 'uncertain'],
    [{ files: editingFiles('a.txt', 'x\n', 'y\n'), link: { blockedReason: 'Disconnected' } }, 'blocked'],
    [{ files: editingFiles('a.txt', 'x\n', 'y\n'), fileActions: { busy: true } }, 'busy'],
    [{ files: editingFiles('a.txt', 'x\n', 'y\n'), connectionStore: { capabilities: {} } }, 'no capability'],
  ];
  for (const [fixture, label] of cases) {
    const html = await render(fixture);
    assert.deepEqual(button(html, 'Save'), { text: 'Save', disabled: true }, label);
    assert.ok(editorProps(html), `${label}: the draft stays visible`);
  }
  const blocked = await render(cases[1][0]);
  assert.match(blocked, /title="Disconnected"/);
  const busy = await render(cases[2][0]);
  assert.equal(editorProps(busy)?.disabled, 'true');
  for (const text of ['Discard', 'Done']) assert.equal(button(busy, text)?.disabled, true, `busy: ${text}`);
});

test('a save conflict keeps the draft and offers Resolve and Reload', async () => {
  const html = await render({
    files: editingFiles('a.txt', 'x\n', 'y\n', { edit: { saveError: 'The file changed on disk.', conflict: { observedRevision: 'rev-2' } } }),
  });
  assert.match(html, /role="alert">[\s\S]*The file changed on disk\./);
  assert.ok(button(html, 'Resolve…') && button(html, 'Reload'));
  assert.equal(editorProps(html)?.text, 'y\n');
  const uncertain = await render({
    files: editingFiles('a.txt', 'x\n', 'y\n', {
      edit: { saveError: 'The connection changed; the save outcome is unknown.', uncertain: { text: 'y\n', expectedRevision: 'rev-1' } },
    }),
  });
  assert.ok(button(uncertain, 'Reload'));
  assert.equal(button(uncertain, 'Resolve…'), undefined);
});
