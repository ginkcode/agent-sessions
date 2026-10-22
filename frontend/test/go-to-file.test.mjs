import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerComponent, renderWithFixture } from './helpers.mjs';

const base = new URL('../src/lib/components/common/go-to-file/', import.meta.url);
const lib = (path) => JSON.stringify(new URL(path, base).href);
const store = ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')];
const dialog = await loadServerComponent(new URL('../GoToFileDialog.svelte', base), [
  ["import { files } from '../../stores/files.svelte';", fixtureStore('files')],
  ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
  ["import { codeDiagnostics } from '../../stores/codeDiagnostics.svelte';", 'const codeDiagnostics = {};'],
  ["import { connectionStore } from '../../stores/connection.svelte';", 'const connectionStore = {};'],
  ["import ProblemsPanel from './go-to-file/ProblemsPanel.svelte';", 'const ProblemsPanel = () => {};'],
  ["import CodeChecks from './go-to-file/CodeChecks.svelte';", 'const CodeChecks = () => {};'],
  ["import { hostLabel } from '../../hosts';", 'const hostLabel = (host) => host;'],
  ["import { copyToClipboard } from '../../portable';", 'const copyToClipboard = async () => true;'],
  ["import { directoryName } from '../../tree';", 'const directoryName = (dir) => dir;'],
  ["import ContextMenu from './ContextMenu.svelte';", 'const ContextMenu = () => {};'],
  ["import { api } from '../../api';", 'const api = {};'],
  ["import { toast } from '../../stores/toast.svelte';", 'const toast = {};'],
  ["import { absoluteFilePath } from '../../fileActions';", 'const absoluteFilePath = () => "";'],
  ["import FilePicker from './go-to-file/FilePicker.svelte';", 'const FilePicker = () => {};'],
  ["import FileTabs from './go-to-file/FileTabs.svelte';", 'const FileTabs = () => {};'],
  ["import FileViewer from './go-to-file/FileViewer.svelte';", 'const FileViewer = () => {};'],
  ["import FilePrompt from './go-to-file/FilePrompt.svelte';", 'const FilePrompt = () => {};'],
  ["import RenamePrompt from './go-to-file/RenamePrompt.svelte';", 'const RenamePrompt = () => {};'],
  ["import NewFolderPrompt from './go-to-file/NewFolderPrompt.svelte';", 'const NewFolderPrompt = () => {};'],
  ["import { fileActions } from '../../stores/fileActions.svelte';", 'const fileActions = {};'],
  ["import { tabShortcut } from '../../tabActions';", lib('../../../tabActions.ts')],
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["'../../fullscreen'", lib('../../../fullscreen.ts')],
]);

const [picker, tabs, source] = await Promise.all([
  loadServerComponent(new URL('FilePicker.svelte', base), [
    store,
    ["'../../../files'", lib('../../../files.ts')],
    ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
    ["import LoadingSpinner from '../LoadingSpinner.svelte';", 'const LoadingSpinner = () => {};'],
  ]),
  loadServerComponent(new URL('FileTabs.svelte', base), [
    ["import { files, tabHasUnsaved } from '../../../stores/files.svelte';", `${fixtureStore('files')} const tabHasUnsaved = t => !!t.edit?.dirty;`],
    ["import { fileActions } from '../../../stores/fileActions.svelte';", 'const fileActions = {};'],
    ["'../../../tabActions'", lib('../../../tabActions.ts')],
    ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
    ["import ContextMenu from '../ContextMenu.svelte';", 'const ContextMenu = () => {};'],
  ]),
  loadServerComponent(new URL('SourceView.svelte', base), [store,
    ["'../../../fileSelection'", JSON.stringify(new URL('../src/lib/fileSelection.ts', import.meta.url).href)],
  ]),
]);

test('dialog offers an unlocked Escape lock toggle and a separate close button', async () => {
  const html = await renderWithFixture(dialog, { files: { open: true, dir: '/project' }, link: {} });
  assert.match(html, /aria-label="Lock dialog against Escape"/);
  assert.match(html, /aria-pressed="false"/);
  assert.match(html, /aria-keyshortcuts="Control\+Shift\+L"/);
  assert.match(html, /Lock dialog \(Ctrl\+Shift\+L; prevent Escape from closing it\)/);
  assert.match(html, /aria-label="Close"/);
  assert.match(html, /<button[^>]*aria-label="Project directory actions"[^>]*>\/project<\/button>/);
});

function renderPicker(files = {}, props = {}) {
  return renderWithFixture(picker, { files: { expanded: new Set(), ...files } }, {
    onPick() {}, onContext() {}, ...props,
  });
}

function renderTabs(files = {}, props = {}) {
  return renderWithFixture(tabs, { files: { tabs: [], ...files } }, {
    presentation: false, onContext() {}, ...props,
  });
}

function renderSource(files = {}, props = {}) {
  return renderWithFixture(source, { files: { wrap: false, ...files } }, {
    rows: [{ kind: 'line', index: 0 }], htmlLines: null, plainLines: ['hello'],
    showDiff: false, presentation: false, presentationFontSize: 22, ...props,
  });
}

// Scoped Svelte CSS hashes can change without changing the rendered classes.
function classTag(html, tag, name) {
  return [...html.matchAll(new RegExp(`<${tag}\\b[^>]*>`, 'g'))]
    .find(([text]) => new RegExp(`class="[^"]*\\b${name}\\b`).test(text))?.[0];
}

test('picker renders explicit empty directories even when there are no files', async () => {
  const html = await renderPicker({ listing: { files: [], directories: ['empty', 'empty/nested'] }, expanded: new Set(['empty']) });
  assert.match(html, /aria-label="Project files"/);
  assert.match(html, />empty<\/span>/);
  assert.match(html, />nested<\/span>/);
  assert.doesNotMatch(html, /This directory has no files/);
});

test('picker tree renders git statuses, deleted paths and the selected file', async () => {
  const html = await renderPicker({
    listing: {
      files: ['src/main.ts', 'new.txt', 'README.md'],
      status: { 'src/main.ts': 'M', 'new.txt': 'U', 'gone.txt': 'D' },
    },
    expanded: new Set(['src']), selected: 'src/main.ts',
  });
  assert.match(html, /role="tree" aria-label="Project files"/);
  for (const [path, label] of [['src/main.ts', 'Modified'], ['new.txt', 'Untracked'], ['gone.txt', 'Deleted']]) {
    assert.ok(html.includes(`title="${path} · ${label}"`));
  }
  assert.match(html, /class="tree-name git-D[^\"]*">gone\.txt<\/span>/);
  assert.match(html, /class="tree-row file[^\"]*\bselected\b[^\"]*"[^>]*title="src\/main\.ts · Modified"/);
  assert.match(html, /class="git-dot git-M[^\"]*" title="Holds changed files"/);
  assert.match(html, /class="tree-name git-none[^\"]*">README\.md<\/span>/);
});

test('picker only renders expanded folder contents and leaves ignored files out of the tree', async () => {
  const listing = { files: ['src/nested/deep.ts'], ignored: ['.env', 'private/secret.txt'] };
  const collapsed = await renderPicker({ listing });
  assert.match(collapsed, /aria-expanded="false"/);
  assert.doesNotMatch(collapsed, /deep\.ts|nested|\.env|secret\.txt/);
  const expanded = await renderPicker({ listing, expanded: new Set(['src', 'src/nested']) });
  assert.equal((expanded.match(/aria-expanded="true"/g) ?? []).length, 2);
  assert.match(expanded, /title="src\/nested\/deep\.ts"/);
  assert.doesNotMatch(expanded, /\.env|secret\.txt/);
});

test('picker distinguishes empty, loading and escaped error states', async () => {
  const empty = await renderPicker({ listing: { files: [], ignored: ['.env'] } });
  assert.match(empty, /This directory has no files\./);
  const loading = await renderPicker({ loadingList: true, listError: 'stale error', listing: { files: [] } });
  assert.match(loading, /Listing files…/);
  assert.doesNotMatch(loading, /stale error|This directory has no files/);
  assert.match(classTag(loading, 'button', 'refresh-btn'), /\bdisabled\b/);
  const error = await renderPicker({ listError: 'Cannot list <repo> & files' });
  assert.match(error, /role="alert">Cannot list &lt;repo> &amp; files/);
  assert.doesNotMatch(error, /This directory has no files/);
  assert.match(error, /aria-label="Refresh the file list"/);
});

test('picker footer counts listed files and changes, reports truncation and disables refresh while refreshing', async () => {
  const html = await renderPicker({
    listing: { files: ['a.ts', 'b.ts'], ignored: ['.env'], git: true, truncated: true, status: { 'a.ts': 'M', 'deleted.ts': 'D' } },
    refreshing: true,
  }, { presentation: true });
  assert.match(html, /2 files \(list cut short\)/);
  assert.match(html, /· \.gitignore applied/);
  assert.match(html, /· 2 changed/);
  assert.match(classTag(html, 'button', 'refresh-btn'), /\bspinning\b/);
  assert.match(classTag(html, 'button', 'refresh-btn'), /\bdisabled\b/);
  assert.match(classTag(html, 'aside', 'picker'), /\bpresentation\b/);
  const initial = await renderPicker();
  assert.equal(classTag(initial, 'footer', 'picker-footer'), undefined);
});

test('tabs expose active and preview states, git status and accessible close labels', async () => {
  const html = await renderTabs({
    tabs: [{ path: 'src/main.ts', preview: true }, { path: 'gone.txt', preview: false }],
    selected: 'src/main.ts', listing: { status: { 'src/main.ts': 'M', 'gone.txt': 'D' } },
  });
  assert.match(html, /role="tablist" aria-label="Open files"/);
  const active = classTag(html, 'div', 'file-tab');
  assert.match(active, /\bactive\b/);
  assert.match(active, /\bpreview\b/);
  assert.match(active, /aria-selected="true"/);
  assert.match(html, /aria-selected="false" title="gone\.txt"/);
  assert.match(html, /title="src\/main\.ts · Preview: double-click to keep it open"/);
  assert.match(html, /class="tab-name git-M[^\"]*">main\.ts/);
  assert.match(html, /class="tab-name git-D[^\"]*">gone\.txt/);
  assert.match(html, /aria-label="Close main\.ts"/);
  assert.match(html, /aria-label="Close gone\.txt"/);
  assert.match(html, /title="Close \((?:Ctrl\+W|⌘W)\)"/);
});

test('tabs omit the empty bar and attach the presentation hiding class only when requested', async () => {
  assert.doesNotMatch(await renderTabs(), /role="tablist"/);
  const files = { tabs: [{ path: 'plain.txt', preview: false }] };
  const normal = await renderTabs(files);
  assert.doesNotMatch(classTag(normal, 'div', 'tab-bar'), /\bpresentation\b/);
  assert.doesNotMatch(classTag(normal, 'div', 'file-tab'), /\bactive\b|\bpreview\b/);
  assert.match(normal, /class="tab-name git-none/);
  const hidden = await renderTabs(files, { presentation: true });
  assert.match(classTag(hidden, 'div', 'tab-bar'), /\bpresentation\b/);
});

test('source wraps number/text pairs and labels change markers without diff signs', async () => {
  const marks = ['added', 'modified', 'deleted-above', 'deleted-below'];
  const html = await renderSource({ wrap: true }, {
    rows: marks.map((mark, index) => ({ kind: 'line', index, mark })),
    plainLines: ['one', 'two', 'three', 'four'], presentation: true,
  });
  assert.match(classTag(html, 'div', 'source'), /\bwrap\b/);
  assert.match(classTag(html, 'div', 'source'), /font-size: 22px/);
  assert.doesNotMatch(html, /class="gutter|class="lines|class="line [^\"]*added/);
  for (const [index, label] of ['Added line', 'Changed line', 'Lines removed above', 'Lines removed below'].entries()) {
    assert.match(html, new RegExp(`class="ln line-select mark-${marks[index]}[^\"]*"[^>]*title="${label}[^\"]*">${index + 1}</button>`));
  }
  assert.match(html, />1<\/button>[\s\S]*?>one<\/span>[\s\S]*?>2<\/button>[\s\S]*?>two<\/span>/);
});

test('source selection highlights the range and exposes accessible line buttons', async () => {
  const html = await renderSource({}, {
    rows: [0, 1, 2].map(index => ({ kind: 'line', index })),
    plainLines: ['one', 'two', 'three'], selection: { anchor: 2, head: 3 },
  });
  assert.match(html, /aria-label="Select line 1" aria-pressed="false"/);
  assert.match(html, /aria-label="Select line 2" aria-pressed="true"/);
  assert.match(html, /class="line[^\"]*selected[^\"]*" data-source-line="3"/);
});

test('unwrapped source separates gutter and lines, renders highlighted HTML and diff signs', async () => {
  const html = await renderSource({}, {
    rows: [{ kind: 'removed', text: '<old> & gone' }, { kind: 'line', index: 0, mark: 'added' }, { kind: 'line', index: 1 }],
    htmlLines: ['<span class="hljs-keyword">const</span>', 'unchanged'], showDiff: true,
  });
  assert.match(classTag(html, 'div', 'source'), /\bdiff\b/);
  assert.doesNotMatch(classTag(html, 'div', 'source'), /\bwrap\b|font-size/);
  assert.ok(classTag(html, 'div', 'gutter'));
  assert.ok(classTag(html, 'div', 'lines'));
  assert.match(html, /class="ln sign removed[^\"]*">−<\/span>/);
  assert.match(html, /class="ln[^\"]*\bsign\b[^\"]*\badded\b[^\"]*"[^>]*>\+<\/button>/);
  assert.match(html, /class="line removed[^\"]*">&lt;old> &amp; gone<\/span>/);
  assert.match(html, /class="line[^\"]*\badded\b[^\"]*"[^>]*>(?:<!--[\s\S]*?-->)*<span class="hljs-keyword">const<\/span>/);
  assert.match(html, /class="ln[^\"]*"[^>]*>2<\/button>/);
});

test('plain source escapes HTML and sizes the gutter from the original line count', async () => {
  const html = await renderSource({}, {
    plainLines: Array(10).fill('<script>bad & worse</script>'), rows: [{ kind: 'line', index: 9 }],
  });
  assert.match(html, /--digits: 2/);
  assert.match(html, /&lt;script>bad &amp; worse&lt;\/script>/);
  assert.doesNotMatch(html, /<script>/);
  assert.match(html, /class="ln line-select mark-none[^\"]*"[^>]*>10<\/button>/);
});
