import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { projectSourceDiagnostics, paintSourceDiagnostics } from '../src/lib/sourceDiagnostics.ts';
import { fixtureStore, loadServerComponent, renderWithFixture } from './helpers.mjs';

const saved = { dirty: false, revision: 'r1' };
const range = (a, b, c = a, d = b) => ({ start: { line: a, character: b }, end: { line: c, character: d } });
function finding(r, changes = {}) {
  const diagnostic = { path: 'x.ts', message: '<img onerror="evil"> & text', source: '<checker>', severity: 'error', freshness: 'current', basis: { kind: 'saved', revision: 'r1' }, ...(r ? { range: r } : {}), ...changes };
  return { diagnostic, entries: [{ providerId: 'checker', diagnostic }] };
}

test('projection uses UTF16 exclusive ends across CRLF, tabs, combining marks and emoji', () => {
  const text = '\t😀e\u0301<&\r\n\r\nlast\r\n';
  const p = projectSourceDiagnostics(text, [finding(range(0, 1, 2, 0)), finding(range(0, 3, 0, 5)), finding(range(3, 0))], saved);
  assert.deepEqual(p.segments, [
    { line: 0, start: 1, end: 7, finding: 0 },
    { line: 1, start: 0, end: 0, finding: 0 },
    { line: 0, start: 3, end: 5, finding: 1 },
    { line: 3, start: 0, end: 0, finding: 2 },
  ]);
  assert.ok(p.messages.every(m => !m.notice));
  assert.equal(projectSourceDiagnostics('', [finding(range(0, 0))], saved).segments.length, 1);
  for (const severity of ['error', 'warning', 'information', 'hint']) {
    assert.equal(projectSourceDiagnostics('x', [finding(range(0, 0, 0, 1), { severity })], saved).segments.length, 1);
  }
});

test('invalid endpoints never clamp or throw; file-level/stale/unverified/drafts are message-only', () => {
  const invalid = [range(-1, 0), range(0, 2), range(0, 0, 2, 0), range(0, 4, 0, 1), range(0, NaN), range(0, 0.5), { start: null, end: null }];
  for (const r of invalid) {
    const p = projectSourceDiagnostics('a😀b', [finding(r)], saved);
    assert.equal(p.segments.length, 0);
    assert.match(p.messages[0].notice, /Invalid range/);
  }
  const cases = [finding(), finding(range(0, 0), { freshness: 'stale' }), finding(range(0, 0), { freshness: 'uncertain' }), finding(range(0, 0), { basis: { kind: 'saved' } }), finding(range(0, 0), { locationIssue: 'invalidRange' }), finding(range(0, 0), { basis: { kind: 'document', documentId: 'd' } })];
  assert.equal(projectSourceDiagnostics('text', cases, saved).segments.length, 0);
  for (const state of [{ dirty: true, revision: 'r1' }, { dirty: false }, { dirty: false, revision: 'r2' }]) assert.equal(projectSourceDiagnostics('text', [finding(range(0, 0))], state).segments.length, 0);
  assert.equal(projectSourceDiagnostics('text', [finding(range(0, 0))], saved, false).segments.length, 0);
});

test('equivalent provider findings select a verified original while retaining all attribution', () => {
  const stale = finding(range(0, 0, 0, 2), { freshness: 'stale' });
  stale.entries.push({ providerId: 'second', diagnostic: finding(range(0, 0, 0, 2)).diagnostic });
  const p = projectSourceDiagnostics('text', [stale], saved);
  assert.equal(p.segments.length, 1);
  assert.equal(p.messages[0].finding.diagnostic.freshness, 'current');
  assert.equal(p.messages[0].finding.entries.length, 2);
  assert.deepEqual(projectSourceDiagnostics('text', [], saved), { messages: [], segments: [], text: 'text' });
});

const base = new URL('../src/lib/components/common/go-to-file/SourceView.svelte', import.meta.url);
const component = await loadServerComponent(base, [
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["'../../../fileSelection'", JSON.stringify(new URL('../src/lib/fileSelection.ts', import.meta.url).href)],
  ["'../../../sourceDiagnostics'", JSON.stringify(new URL('../src/lib/sourceDiagnostics.ts', import.meta.url).href)],
]);

test('SSR keeps syntax HTML intact, escapes messages and exposes overlaps without replacing Git/selection', async () => {
  const diagnostics = projectSourceDiagnostics('<&text', [finding(range(0, 0, 0, 2)), finding(range(0, 1, 0, 4), { severity: 'warning', code: '<code>' }), finding(), finding(range(0, 1), { freshness: 'stale' })], saved);
  const props = { rows: [{ kind: 'line', index: 0, mark: 'modified' }], htmlLines: ['<span class="hljs-string">&lt;&amp;text</span>'], plainLines: ['<&text'], showDiff: false, presentation: true, presentationFontSize: 24, diagnostics, selection: { anchor: 1, head: 1 } };
  for (const wrap of [true, false]) {
    const html = await renderWithFixture(component, { files: { wrap } }, props);
    assert.match(html, /<span class="hljs-string">&lt;&amp;text<\/span>/);
    assert.doesNotMatch(html, /<img|<checker>|<code>/);
    assert.match(html, /&lt;img/);
    assert.match(html, /diagnostics: error, warning/);
    assert.match(html, /aria-describedby="[^"]+ [^"]+"/);
    assert.match(html, /mark-modified/);
    assert.match(html, /aria-pressed="true"/);
    assert.match(html, /File-level finding/);
    assert.match(html, /stale or unverified/);
    assert.match(html, /font-size:\s*24px/);
    const diff = await renderWithFixture(component, { files: { wrap } }, { ...props, showDiff: true });
    assert.doesNotMatch(diff, /Source diagnostics|has-diagnostics|diagnostics: error/);
    const plain = await renderWithFixture(component, { files: { wrap } }, { ...props, htmlLines: null });
    assert.match(plain, /&lt;&amp;text/);
  }
});

test('DOM seam walks syntax text nodes, paints separate overlap ranges and cleans only owned highlights', () => {
  const registry = new Map([['file-find', 'keep']]);
  const nodes = [{ textContent: '<😀' }, { textContent: '&end' }];
  const ranges = [];
  class Highlight { constructor(...ranges) { this.ranges = ranges; } }
  const root = {
    querySelector(selector) { assert.equal(selector, '[data-source-line="1"]'); return { textContent: '<😀&end' }; },
    ownerDocument: {
      defaultView: { CSS: { highlights: registry }, Highlight },
      createTreeWalker() { let i = 0; return { nextNode: () => nodes[i++] ?? null }; },
      createRange() { const r = { setStart(n, o) { this.start = [n, o]; }, setEnd(n, o) { this.end = [n, o]; } }; ranges.push(r); return r; },
    },
  };
  const p = projectSourceDiagnostics('<😀&end', [finding(range(0, 1, 0, 5)), finding(range(0, 3, 0, 6)), finding(range(0, 7))], saved);
  const cleanup = paintSourceDiagnostics(root, p, 'test');
  assert.equal(ranges.length, 2);
  assert.deepEqual(ranges[0].start, [nodes[0], 1]);
  assert.deepEqual(ranges[0].end, [nodes[1], 2]);
  cleanup();
  assert.deepEqual([...registry], [['file-find', 'keep']]);
  root.querySelector = () => ({ textContent: 'different source' });
  paintSourceDiagnostics(root, p, 'test')();
  assert.equal(ranges.length, 2, 'mismatched rendered text cannot be painted');
  assert.doesNotThrow(() => paintSourceDiagnostics({ ownerDocument: { defaultView: null } }, p, 'test')());
});

test('viewer gates overlays to the owned source file and retains lazy editor boundary', () => {
  const viewer = readFileSync(new URL('../src/lib/components/common/go-to-file/FileViewer.svelte', import.meta.url), 'utf8');
  assert.match(viewer, /codeDiagnostics.directory === files.dir/);
  assert.match(viewer, /!showDiff && !showPreview && !blameOpen/);
  assert.match(viewer, /f.diagnostic.path === files.selected/);
  assert.match(viewer, /edit\?\.dirty \|\| edit\?\.uncertain \|\| edit\?\.conflict/);
  assert.doesNotMatch(viewer, /from ['"]@codemirror/);
});
