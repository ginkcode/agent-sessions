import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import * as navigation from '../src/lib/problemNavigation.ts';
import { diagnosticKey } from '../src/lib/codeDiagnostics.ts';
import { fixtureStore, loadServerComponent, loadServerModule, renderWithFixture } from './helpers.mjs';
const { orderedProblems, stepProblem, problemKey, problemPath, rangeOffsets, problemLocation, matchesProblemNavigation } = navigation;
const range = { start: { line: 0, character: 1 }, end: { line: 1, character: 2 } };
const diagnostic = (extra = {}) => ({ path: 'a.md', severity: 'error', message: '<script>attack</script>', source: 'go', freshness: 'current', basis: { kind: 'saved', revision: 'r' }, range, ...extra });
const finding = d => ({ diagnostic: d, entries: [{ providerId: 'go', diagnostic: d }] });

test('stable ordering, filtered-set wrap and real keyboard handler do not change global findings', () => {
  const input = [finding(diagnostic({ path: 'z' })), finding(diagnostic({ severity: 'hint' })), finding(diagnostic())];
  const rows = orderedProblems(input, ['error']);
  assert.equal(rows.length, 2); assert.equal(input.length, 3);
  assert.equal(rows[0].diagnostic.path, 'a.md');
  assert.deepEqual(orderedProblems([...input].reverse(), ['error']), rows);
  let selected = diagnosticKey(rows[1].diagnostic), prevented = 0;
  const step = direction => selected = diagnosticKey(stepProblem(rows, selected, direction).diagnostic);
  assert.equal(problemKey({ key: 'ArrowDown', preventDefault() { prevented++; } }, step), true);
  assert.equal(selected, diagnosticKey(rows[0].diagnostic));
  problemKey({ key: 'ArrowUp', preventDefault() { prevented++; } }, step);
  assert.equal(selected, diagnosticKey(rows[1].diagnostic)); assert.equal(prevented, 2);
  assert.equal(problemKey({ key: 'Escape' }, step), false);
  assert.equal(problemKey({ key: 'ArrowDown', ctrlKey: true }, step), false);
  assert.equal(stepProblem([], selected, 1), undefined);
  assert.equal(stepProblem(rows, 'removed', -1), rows[1]);
});

test('UTF16 exclusive multiline CRLF offsets reject invalid columns and split surrogate pairs', () => {
  assert.deepEqual(rangeOffsets('a😀z\r\nβx\r\n', range), { anchor: 1, head: 8 });
  assert.deepEqual(rangeOffsets('a😀z\nβx\n', range), { anchor: 1, head: 7 });
  assert.equal(rangeOffsets('a😀z\nβx', { start: { line: 0, character: 2 }, end: { line: 0, character: 3 } }), undefined);
  assert.equal(rangeOffsets('x', range), undefined);
  assert.match(problemLocation(diagnostic(), { dirty: true, revision: 'r' }), /unsaved draft/);
  assert.match(problemLocation(diagnostic({ freshness: 'stale' }), { dirty: false, revision: 'r' }), /stale/);
  assert.equal(problemLocation(diagnostic(), { dirty: false, revision: 'r' }), '');
  for (const path of ['../x', '/etc/passwd', 'file:///x', 'C:/x', 'a//b', 'a\\b', 'a\0b']) assert.equal(problemPath(path), false);
  assert.equal(problemPath('src/😀.ts'), true);
});

const panelURL = new URL('../src/lib/components/common/go-to-file/ProblemsPanel.svelte', import.meta.url);
const panel = await loadServerComponent(panelURL, [
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
  ["'../../../codeDiagnostics'", JSON.stringify(new URL('../src/lib/codeDiagnostics.ts', import.meta.url).href)],
  ["'../../../problemNavigation'", JSON.stringify(new URL('../src/lib/problemNavigation.ts', import.meta.url).href)],
  ["'../../../codeCheckSummary'", JSON.stringify(new URL('../src/lib/codeCheckSummary.ts', import.meta.url).href)],
]);
test('expanded panel groups and escapes attributed file-level and stale findings', async () => {
  const findings = [finding(diagnostic()), finding(diagnostic({ path: 'z.ts', range: undefined, severity: 'hint', freshness: 'stale' }))];
  const html = await renderWithFixture(panel, { checks: { findings } }, { expanded: true, onnavigate() {} });
  for (const text of ['a.md', 'z.ts', 'File-level', 'stale', 'go (go)', '1:2–2:3', 'Previous problem', 'Next problem', '&lt;script>']) assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /<script>|href=/);
});

test('panel is collapsed initially and exposes count without executable content', async () => {
  const html = await renderWithFixture(panel, { checks: { findings: [finding(diagnostic())] } }, { onnavigate() {} });
  assert.match(html, /role="status"[^>]*>1 error</); assert.match(html, /aria-expanded="false"/); assert.doesNotMatch(html, /<script>|attack/);
});

// Execute the actual viewer navigation block against the real FilesStore, with
// a queued tick instead of DOM rendering. Not native focus/paint coverage.
const storeURL = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
const { FilesStore } = await loadServerModule(storeURL, [
  ["import { api } from '../api';", fixtureStore('api', '__problemFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(name => [`'../${name}'`, JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href)]),
  ["import { link } from './link.svelte';", 'const link = { dataHost: undefined, blockedReason: null };'],
]);
function viewerHarness(files) {
  const source = readFileSync(new URL('../src/lib/components/common/go-to-file/FileViewer.svelte', import.meta.url), 'utf8');
  const block = source.slice(source.indexOf('  let previewOn'), source.indexOf('  // Editing.'));
  const js = ts.transpileModule(block.replace('export function', 'function').replace('let previewOn = $state(false)', 'let previewOn = $state(true)'), { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const ticks = [], reveals = [];
  let effect;
  const ctx = { ...navigation, files, codeDiagnostics: { directory: files.dir, liveBasis: () => undefined, status: 'ready', snapshot: { context: { generation: '1' } } }, link: { dataHost: undefined }, frozen: false, blocked: false, highlightPending: false,
    fileStatus: p => files.listing?.status?.[p], pendingAnchor: null, presentation: true,
    get text() { return files.content?.text ?? ''; }, get editing() { return !!files.activeTab?.edit?.editing; },
    get showPreview() { return control.preview(); }, get showDiff() { return files.diff; },
    editor: { revealRange: (r, valid) => { if (valid()) reveals.push(r); return true; } },
    codeScroll: {}, revealSourceRange: (_, r) => { reveals.push(r); return true; },
    isTextContent: c => !!c && !c.binary && !c.tooLarge && !c.image,
    closeBlame() {}, untrack: fn => fn(), tick: () => new Promise(resolve => ticks.push(resolve)),
    $state: Object.assign(v => v, { raw: v => v }), $effect: fn => effect = fn,
  };
  ctx.$effect = fn => ctx.runEffect = fn;
  const control = new Function('ctx', `with(ctx) { ${js}; return { navigateProblem, effect: () => ctx.runEffect(), preview: () => previewOn, notice: () => navigationNotice }; }`)(ctx);
  return { ...control, ctx, reveals, async flush() { ticks.splice(0).forEach(f => f()); await Promise.resolve(); } };
}
const content = path => ({ path, text: 'a😀z\r\nβx', size: 9, revision: 'r', edit: { eligible: true, newline: 'crlf' } });

test('actual viewer async navigation keeps dirty preview and existing editor, switches diff/source, drops superseded loads', async () => {
  const reads = new Map();
  globalThis.__problemFixture = { api: { listFiles: async () => ({ files: [] }), readFile: (_, p) => new Promise(resolve => reads.set(p, resolve)) } };
  const files = new FilesStore(); await files.show('/p');
  files.openFile('draft.md'); reads.get('draft.md')(content('draft.md')); await Promise.resolve(); await Promise.resolve();
  files.startEditing('draft.md'); const draft = files.activeTab; files.updateDraft(draft, 'unsaved😀');
  const h = viewerHarness(files);
  h.navigateProblem(diagnostic({ path: 'draft.md' })); h.effect();
  assert.match(h.notice(), /unsaved draft/); assert.equal(h.reveals.length, 0); assert.equal(files.activeTab, draft);
  h.navigateProblem(diagnostic({ path: 'late.md' })); h.effect();
  h.navigateProblem(diagnostic({ path: 'next.md' })); h.effect();
  reads.get('late.md')(content('late.md')); await Promise.resolve(); await Promise.resolve(); h.effect(); await h.flush();
  assert.equal(h.reveals.length, 0);
  reads.get('next.md')(content('next.md')); await Promise.resolve(); await Promise.resolve(); files.diff = true; h.effect(); await h.flush();
  assert.equal(files.diff, false); assert.equal(h.preview(), false); assert.deepEqual(h.reveals, [range]);
  assert.equal(draft.edit.draft, 'unsaved😀'); assert.equal(files.tabs.includes(draft), true);
  const kept = files.activeTab; h.navigateProblem(diagnostic({ path: 'next.md' })); assert.equal(files.activeTab, kept);
  h.effect(); files.selectTab('draft.md'); await h.flush(); assert.equal(h.reveals.length, 1);
  delete globalThis.__problemFixture;
});

test('actual viewer live navigation rechecks document/version/sync after the render boundary', async () => {
  globalThis.__problemFixture = { api: { listFiles: async () => ({ files: [] }), readFile: async (_, path) => content(path) } };
  const files = new FilesStore(); await files.show('/p');
  files.openFile('a.go'); await Promise.resolve(); await Promise.resolve();
  files.startEditing('a.go'); const tab = files.activeTab; files.updateDraft(tab, 'a😀draft\nβx');
  const h = viewerHarness(files);
  let live = { documentId: 'doc', version: 2, synced: true };
  h.ctx.codeDiagnostics.liveBasis = path => path === 'a.go' ? live : undefined;
  const d = diagnostic({ path: 'a.go', basis: { kind: 'document', documentId: 'doc', version: 2 } });
  h.navigateProblem(d); h.effect(); await h.flush(); assert.equal(h.reveals.length, 1);
  for (const basis of [{ documentId: 'old', version: 2 }, { documentId: 'doc', version: 1 }, { documentId: 'doc' }]) {
    h.navigateProblem({ ...d, basis: { kind: 'document', ...basis } }); h.effect(); await h.flush();
    assert.equal(h.reveals.length, 1);
  }
  h.navigateProblem(d); h.effect(); live = { ...live, synced: false }; await h.flush();
  assert.equal(h.reveals.length, 1, 'typing while mounting cannot select an old range');
  assert.equal(tab.edit.draft, 'a😀draft\nβx');
  delete globalThis.__problemFixture;
});

test('pending navigation identity rejects host/root/generation/reconnect/tab/newer requests', () => {
  const identity = { dir: '/p', host: undefined, version: '1', context: 'generation1' }, tab = {};
  const request = { id: 1, identity, tab, path: 'a' };
  assert.equal(matchesProblemNavigation(request, request, identity, tab, 'a'), true);
  for (const patch of [{ dir: '/q' }, { host: 'remote' }, { version: '2' }, { context: 'generation2' }]) assert.equal(matchesProblemNavigation(request, request, { ...identity, ...patch }, tab, 'a'), false);
  assert.equal(matchesProblemNavigation(request, { ...request }, identity, tab, 'a'), false);
  assert.equal(matchesProblemNavigation(request, request, identity, {}, 'a'), false);
});

test('viewer failed/file-level/unverified targets never select or reload a draft', async () => {
  globalThis.__problemFixture = { api: { listFiles: async () => ({ files: [] }), readFile: async (_, path) => { if (path === 'missing') throw Error('unreadable'); return content(path); } } };
  const files = new FilesStore(); await files.show('/p');
  const h = viewerHarness(files);
  h.navigateProblem(diagnostic({ path: 'missing' })); await Promise.resolve(); await Promise.resolve(); h.effect();
  assert.match(h.notice(), /unreadable/);
  h.navigateProblem(diagnostic({ path: 'level', range: undefined })); await Promise.resolve(); await Promise.resolve(); h.effect();
  assert.match(h.notice(), /File-level/);
  h.navigateProblem(diagnostic({ path: 'level', basis: { kind: 'saved', revision: 'old' } })); h.effect(); assert.match(h.notice(), /unverified/);
  assert.equal(h.reveals.length, 0);
  files.startEditing('level'); const tab = files.activeTab;
  h.navigateProblem(diagnostic({ path: 'level' })); h.effect(); await h.flush();
  assert.deepEqual(h.reveals, [range]); assert.equal(files.activeTab, tab);
  h.navigateProblem(diagnostic({ path: 'level' })); h.effect(); files.updateDraft(tab, 'new draft'); await h.flush();
  assert.equal(h.reveals.length, 1); assert.match(h.notice(), /stale/);
  delete globalThis.__problemFixture;
});

test('source range walks highlighted text nodes, excluding gutters, with UTF16 multiline endpoints', () => {
  const a = { textContent: 'a' }, emoji = { textContent: '😀z' }, end = { textContent: 'βxy' };
  const lines = [{ nodes: [a, emoji], childNodes: [a, emoji], scrollIntoView() { this.scrolled = true; } }, { nodes: [end], childNodes: [end] }];
  let selected;
  const doc = {
    createTreeWalker(el) { let n = 0; return { nextNode: () => el.nodes[n++] ?? null }; },
    createRange() { return { setStart(node, offset) { this.start = [node, offset]; }, setEnd(node, offset) { this.end = [node, offset]; } }; },
    getSelection: () => ({ removeAllRanges() {}, addRange(r) { selected = r; } }),
  };
  const root = { ownerDocument: doc, querySelector: selector => lines[Number(/"(\d+)"/.exec(selector)[1]) - 1] };
  assert.equal(navigation.revealSourceRange(root, { start: { line: 0, character: 3 }, end: { line: 1, character: 2 } }), true);
  assert.deepEqual(selected.start, [emoji, 2]); assert.deepEqual(selected.end, [end, 2]); assert.equal(lines[0].scrolled, true);
});

test('actual viewer post-render request rejects context replacement and hostile paths before opening', async () => {
  let reads = 0;
  globalThis.__problemFixture = { api: { listFiles: async () => ({ files: [] }), readFile: async (_, path) => { reads++; return content(path); } } };
  const files = new FilesStore(); await files.show('/p'); const h = viewerHarness(files);
  h.navigateProblem(diagnostic({ path: '../outside' })); assert.equal(reads, 0); assert.equal(files.tabs.length, 0);
  h.navigateProblem(diagnostic()); await Promise.resolve(); await Promise.resolve(); h.effect();
  h.ctx.codeDiagnostics.snapshot.context.generation = '2'; await h.flush(); assert.equal(h.reveals.length, 0);
  h.navigateProblem(diagnostic()); h.effect(); h.ctx.link.dataHost = 'another-host'; await h.flush(); assert.equal(h.reveals.length, 0);
  delete globalThis.__problemFixture;
});
