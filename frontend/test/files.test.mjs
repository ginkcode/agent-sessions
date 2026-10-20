import test from 'node:test';
import assert from 'node:assert/strict';
import { copyToClipboard } from '../src/lib/portable.ts';
import { splitHighlightedLines, splitLines } from '../src/lib/codeHighlight.ts';
import {
  buildFileTree,
  diffRows,
  fileChanges,
  fileLanguage,
  filePreview,
  filterFiles,
  isImageFile,
  folderStatus,
  lineMarks,
  matchScore,
  parentFolders,
  searchFiles,
  withDeleted,
} from '../src/lib/files.ts';
import { fixtureStore, loadServerModule } from './helpers.mjs';

test('buildFileTree nests folders, folders before files', () => {
  const tree = buildFileTree(['main.go', 'src/b.ts', 'src/a.ts', 'src/lib/x.ts', 'README.md']);
  assert.deepEqual(tree.map((n) => n.name), ['src', 'main.go', 'README.md']);
  const src = tree[0];
  assert.deepEqual(src.children.map((n) => n.path), ['src/lib', 'src/a.ts', 'src/b.ts']);
  assert.deepEqual(src.children[0].children.map((n) => n.path), ['src/lib/x.ts']);
  assert.equal(tree[1].children, undefined);
});

test('filterFiles matches characters in order and ranks file names first', () => {
  const paths = ['src/lib/apps/x.ts', 'src/app.ts', 'docs/a-p-p.md', 'main.go'];
  assert.deepEqual(filterFiles(paths, 'app'), ['src/app.ts', 'docs/a-p-p.md', 'src/lib/apps/x.ts']);
  assert.deepEqual(filterFiles(paths, 'src app'), ['src/app.ts', 'src/lib/apps/x.ts']);
  assert.equal(matchScore('main.go', 'xyz'), -1);
  assert.deepEqual(filterFiles(paths, ''), paths);
  assert.equal(filterFiles(paths, '', 2).length, 2);
});

test('fileLanguage supports source and text files only', () => {
  assert.equal(fileLanguage('internal/app.go'), 'go');
  assert.equal(fileLanguage('src/App.svelte'), 'xml');
  assert.equal(fileLanguage('x.TSX'), 'typescript');
  assert.equal(fileLanguage('README.md'), 'markdown');
  assert.equal(fileLanguage('Makefile'), 'makefile');
  assert.equal(fileLanguage('Cargo.lock'), '');
  assert.equal(fileLanguage('.env.local'), '');
  assert.equal(fileLanguage('assets/logo.png'), null);
  assert.equal(fileLanguage('app.exe'), null);
  assert.equal(fileLanguage('noext'), null);
});

test('filePreview offers a rendered view for Markdown and SVG files', () => {
  assert.equal(filePreview('README.md'), 'markdown');
  assert.equal(filePreview('docs/guide.MDX'), 'markdown');
  assert.equal(filePreview('assets/logo.svg'), 'svg');
  assert.equal(filePreview('assets/Logo.SVG'), 'svg');
  assert.equal(filePreview('src/app.ts'), null);
  assert.equal(filePreview('assets/logo.png'), null);
});

test('isImageFile knows the image types the backend sends as data', () => {
  for (const path of ['a.png', 'b/c.JPG', 'd.jpeg', 'e.gif', 'f.webp', 'g.bmp', 'favicon.ico', 'h.avif']) {
    assert.equal(isImageFile(path), true, path);
  }
  for (const path of ['logo.svg', 'notes.md', 'png', 'archive.png.zip', 'app.exe']) {
    assert.equal(isImageFile(path), false, path);
  }
});

test('parentFolders lists the folders above a file', () => {
  assert.deepEqual(parentFolders('a/b/c.ts'), ['a', 'a/b']);
  assert.deepEqual(parentFolders('c.ts'), []);
});

test('splitHighlightedLines keeps every line complete HTML', () => {
  const html = 'a <span class="hljs-comment">/* one\ntwo <span class="hljs-doctag">@x\ny</span> three\n*/</span> b\n';
  assert.deepEqual(splitHighlightedLines(html), [
    'a <span class="hljs-comment">/* one</span>',
    '<span class="hljs-comment">two <span class="hljs-doctag">@x</span></span>',
    '<span class="hljs-comment"><span class="hljs-doctag">y</span> three</span>',
    '<span class="hljs-comment">*/</span> b',
  ]);
  assert.deepEqual(splitHighlightedLines('x\n\ny'), ['x', '', 'y']);
  assert.deepEqual(splitHighlightedLines(''), ['']);
  assert.deepEqual(splitLines('a\nb\n'), ['a', 'b']);
  assert.deepEqual(splitLines('a\n\n'), ['a', '']);
  assert.deepEqual(splitLines(''), ['']);
});

test('copyToClipboard writes natively in the desktop app, falling back to the web clipboard', async () => {
  const written = [];
  const web = { writeText: async (t) => { written.push(['web', t]); } };
  const saved = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  Object.defineProperty(globalThis, 'navigator', { value: { clipboard: web }, configurable: true });
  try {
    globalThis.runtime = { ClipboardSetText: async (t) => { written.push(['native', t]); return true; } };
    assert.equal(await copyToClipboard('a'), true);
    globalThis.runtime = { ClipboardSetText: async () => false };
    assert.equal(await copyToClipboard('b'), true);
    delete globalThis.runtime;
    web.writeText = async () => { throw new Error('NotAllowedError'); };
    assert.equal(await copyToClipboard('c'), false);
    assert.deepEqual(written, [['native', 'a'], ['web', 'b']]);
  } finally {
    delete globalThis.runtime;
    if (saved) Object.defineProperty(globalThis, 'navigator', saved);
    else delete globalThis.navigator;
  }
});

test('git marks: deleted files listed, folders take their most important change', () => {
  const status = { 'src/a.ts': 'U', 'src/lib/b.ts': 'M', 'gone.txt': 'D', 'docs/new.md': 'A' };
  assert.deepEqual(withDeleted(['src/a.ts', 'src/lib/b.ts'], status), ['gone.txt', 'src/a.ts', 'src/lib/b.ts']);
  assert.deepEqual(withDeleted(['x'], undefined), ['x']);
  assert.deepEqual(Object.fromEntries(folderStatus(status)), { src: 'M', 'src/lib': 'M', docs: 'A' });
  assert.equal(folderStatus(undefined).size, 0);
});

test('lineMarks places gutter bars for changed lines and markers for removed ones', () => {
  const marks = lineMarks(
    [
      { kind: 'modified', start: 2, count: 1 },
      { kind: 'added', start: 4, count: 2 },
      { kind: 'deleted', start: 0, count: 0 },
      { kind: 'deleted', start: 6, count: 0 },
    ],
    6
  );
  assert.deepEqual(marks, ['deleted-above', 'modified', undefined, 'added', 'added', 'deleted-below']);
  assert.deepEqual(lineMarks(undefined, 2), [undefined, undefined]);
  assert.deepEqual(lineMarks([{ kind: 'deleted', start: 0, count: 0 }], 0), []);
});

test('diffRows puts removed lines above what replaced them', () => {
  const changes = [
    { kind: 'deleted', start: 0, count: 0, removed: ['first'] },
    { kind: 'modified', start: 2, count: 1, removed: ['old two'] },
    { kind: 'added', start: 3, count: 1 },
    { kind: 'deleted', start: 3, count: 0, removed: ['old end'] },
  ];
  const rows = diffRows(changes, 3).map((r) => (r.kind === 'removed' ? `-${r.text}` : `${r.index + 1}${r.mark ? ':' + r.mark : ''}`));
  assert.deepEqual(rows, ['-first', '1', '-old two', '2:modified', '3:added', '-old end']);
  assert.deepEqual(diffRows(undefined, 2).map((r) => r.index), [0, 1]);
});

test('fileChanges marks an untracked file as all added', () => {
  assert.deepEqual(fileChanges('U', undefined, 3), [{ kind: 'added', start: 1, count: 3 }]);
  assert.equal(fileChanges('M', undefined, 3), undefined);
  const c = [{ kind: 'added', start: 1, count: 1 }];
  assert.equal(fileChanges('M', c, 3), c);
});

test('searchFiles finds ignored files, below listed ones that match as well', () => {
  const paths = ['src/env.ts', 'README.md'];
  const ignored = ['.env', 'src/.env.local'];
  const got = searchFiles(paths, ignored, '.env');
  assert.ok(got.includes('.env') && got.includes('src/.env.local'));
  assert.ok(!got.includes('README.md'));
  assert.deepEqual(searchFiles(paths, ignored, 'env.ts'), ['src/env.ts']);
  assert.deepEqual(searchFiles(['a/x.ts'], ['b/x.ts'], 'x.ts'), ['a/x.ts', 'b/x.ts']);
  assert.deepEqual(searchFiles(paths, undefined, 'readme'), ['README.md']);
});

const FILES_STORE_URL = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);

let filesStoreModule;

async function loadFilesStore() {
  const lib = (rel) => JSON.stringify(new URL(rel, FILES_STORE_URL).href);
  filesStoreModule ??= await loadServerModule(FILES_STORE_URL, [
    ["import { api } from '../api';", fixtureStore('api', '__storeFixture')],
    ["'../manage'", lib('../manage.ts')],
    ["'../link'", lib('../link.ts')],
    ["'../files'", lib('../files.ts')],
  ]);
  return filesStoreModule;
}

test('FilesStore: one click previews, double click keeps a tab, folders are remembered', async () => {
  const { FilesStore } = await loadFilesStore();
  globalThis.__storeFixture = {
    api: {
      listFiles: async (dir) => ({ root: dir, files: ['a.ts', 'b.ts', 'c.ts', 'd.ts'] }),
      readFile: async (_dir, path) => ({ path, size: 1, text: path }),
    },
  };
  try {
    const store = new FilesStore();
    await store.show('/p');
    const tabs = () => store.tabs.map((t) => `${t.path}${t.preview ? '*' : ''}`);

    store.openFile('a.ts');
    store.openFile('b.ts');
    assert.deepEqual(tabs(), ['b.ts*'], 'a preview is replaced by the next file');
    store.openFile('b.ts', { keep: true });
    assert.deepEqual(tabs(), ['b.ts'], 'double click keeps the preview');
    store.openFile('c.ts');
    store.openFile('d.ts', { keep: true });
    assert.deepEqual(tabs(), ['b.ts', 'c.ts*', 'd.ts']);
    store.openFile('a.ts');
    assert.deepEqual(tabs(), ['b.ts', 'a.ts*', 'd.ts']);
    store.openFile('d.ts');
    assert.deepEqual(tabs(), ['b.ts', 'a.ts*', 'd.ts'], 'a file with a tab is just shown');
    assert.equal(store.selected, 'd.ts');
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(store.content?.text, 'd.ts');

    store.closeTab('d.ts');
    assert.equal(store.selected, 'a.ts', 'closing the shown tab shows its neighbour');
    store.closeTab('b.ts');
    store.closeTab('a.ts');
    assert.equal(store.selected, null);

    store.openFile('a.ts', { keep: true });
    store.openFile('b.ts');
    store.setExpanded(new Set(['src']));
    store.close();
    assert.equal(store.expanded.size, 0);
    assert.equal(store.tabs.length, 0);
    await store.show('/p');
    assert.deepEqual([...store.expanded], ['src'], 'open folders come back with the directory');
    assert.deepEqual(tabs(), ['a.ts', 'b.ts*'], 'and so do the tabs');
    assert.equal(store.selected, 'b.ts');

    let listed = 0;
    globalThis.__storeFixture.api.listFiles = async (dir) => {
      listed++;
      return { root: dir, files: ['a.ts', 'b.ts', 'e.ts'] };
    };
    globalThis.__storeFixture.api.readFile = async (_dir, path) => ({ path, size: 2, text: path + '!' });
    await store.refresh();
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(listed, 1);
    assert.deepEqual(store.listing.files, ['a.ts', 'b.ts', 'e.ts'], 'refresh lists the files again');
    assert.equal(store.content?.text, 'b.ts!', 'and reloads the open ones');
    assert.equal(store.refreshing, false);
    store.reset();
    await store.show('/p');
    assert.equal(store.expanded.size, 0, 'another host forgets them');

    store.setExpanded(new Set(['unrelated']));
    store.openFile('docs/nested/formats.md', { keep: true });
    assert.deepEqual([...store.expanded], ['unrelated', 'docs', 'docs/nested'], 'programmatic opens reveal every ancestor without collapsing other folders');
    assert.equal(store.selected, 'docs/nested/formats.md');
    store.setExpanded(new Set());
    store.openFile('docs/nested/formats.md', { keep: true });
    assert.deepEqual([...store.expanded], ['docs', 'docs/nested'], 'reopening an existing tab reveals its tree path');
    store.setExpanded(new Set());
    store.selectTab('a.ts');
    store.selectTab('docs/nested/formats.md');
    assert.deepEqual([...store.expanded], ['docs', 'docs/nested'], 'switching file tabs reveals the selected file');
  } finally {
    delete globalThis.__storeFixture;
  }
});
