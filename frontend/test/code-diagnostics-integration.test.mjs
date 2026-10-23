import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerComponent, loadServerModule, renderWithFixture } from './helpers.mjs';
import { projectSourceDiagnostics } from '../src/lib/sourceDiagnostics.ts';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')],
  ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')], ["'../codeNavigation'", lib('codeNavigation')],
]);
const base = new URL('../src/lib/components/common/go-to-file/', import.meta.url);
const checks = ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')];
const controls = await loadServerComponent(new URL('CodeChecks.svelte', base), [checks]);
const panel = await loadServerComponent(new URL('ProblemsPanel.svelte', base), [checks,
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["'../../../codeDiagnostics'", lib('codeDiagnostics')], ["'../../../problemNavigation'", lib('problemNavigation')], ["'../../../codeCheckSummary'", lib('codeCheckSummary')]]);
const badge = await loadServerComponent(new URL('DiagnosticBadge.svelte', base), [["'../../../codeDiagnosticCounts'", lib('codeDiagnosticCounts')]]);
globalThis.__stage1Badge = badge.default;
const picker = await loadServerComponent(new URL('FilePicker.svelte', base), [checks,
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["import DiagnosticBadge from './DiagnosticBadge.svelte';", 'const DiagnosticBadge = globalThis.__stage1Badge;'],
  ["'../../../files'", lib('files')], ["'../../../codeCheckSummary'", lib('codeCheckSummary')],
  ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
  ["import LoadingSpinner from '../LoadingSpinner.svelte';", 'const LoadingSpinner = () => {};']]);
const source = await loadServerComponent(new URL('SourceView.svelte', base), [
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["'../../../fileSelection'", lib('fileSelection')], ["'../../../sourceNavigation'", lib('sourceNavigation')], ["'../../../sourceDiagnostics'", lib('sourceDiagnostics')]]);

const context = { connectionId: 'c', attachmentId: 'a', generation: 'g' };
const range = { start: { line: 0, character: 1 }, end: { line: 0, character: 3 } };
const diagnostic = { path: 'src/unopened.go', range, message: '<script> & 😀', source: 'go', severity: 'error', freshness: 'current', basis: { kind: 'saved', revision: 'r1' } };
const provider = id => ({ providerId: id, availability: 'available', phase: 'idle', freshness: 'uncertain', coverage: { kind: 'savedProject', roots: [''], partial: false, externalChanges: 'unwatched' }, truncated: false });
const set = id => ({ providerId: id, runId: 'run1', origin: 'checker', scope: { kind: 'project' }, diagnostics: [{ ...diagnostic, source: id }] });
function snapshot(sequence = 0) { return { contractVersion: 1, context, sequence, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers: [provider('go'), provider('second'), { ...provider('svelte'), availability: 'missingTool' }], resultSets: [set('go'), set('second')] }; }

test('real store drives controls, panel, picker and source through independent replacement and failed/canceled retention', async () => {
  let listener;
  let detached = 0;
  const initial = snapshot();
  const store = new CodeDiagnosticsStore({
    onEvent(name, fn) { if (name === 'codeDiagnostics:updated') { listener = fn; return () => { listener = undefined; }; } return () => {}; },
    async attachCodeDiagnostics() { return { project: { hostId: 'local:fixture', root: '/fixture' }, context, snapshot: initial }; },
    async detachCodeDiagnostics() { detached++; return { context }; },
  }, () => 0);
  await store.attach('/fixture', { phase: 'local', generation: 0, hostId: 'local:fixture', capabilities: { codeDiagnostics: true } });
  const emit = s => listener({ contractVersion: 1, context, sequence: s.sequence, updateId: `u${s.sequence}`, partIndex: 0, partCount: 1, snapshot: s });
  const text = '\t😀é\r\n';
  async function views(count, markers) {
    assert.equal(store.counts.error, count);
    assert.equal(store.pathCounts.files.get('src/unopened.go')?.error ?? 0, count);
    assert.equal(store.pathCounts.folders.get('src')?.error ?? 0, count);
    assert.equal(store.countsUnder('src'), count);
    const fixtures = { checks: store, files: { wrap: false, expanded: new Set(['src']), listing: { files: ['src/unopened.go'] } } };
    const html = await renderWithFixture(controls, fixtures);
    assert.match(html, /svelte/);
    const problems = await renderWithFixture(panel, fixtures, { expanded: true, onnavigate() {} });
    assert.match(problems, count ? /role="status"[^>]*>1 error · partial</ : /role="status"[^>]*>No problems reported \(partial\)</);
    const tree = await renderWithFixture(picker, fixtures, { onPick() {}, onContext() {} });
    assert.equal((tree.match(/class="diagnostic-badge/g) ?? []).length, count ? 2 : 0);
    const findings = store.findings.filter(f => f.diagnostic.path === diagnostic.path);
    const projection = projectSourceDiagnostics(text, findings, { dirty: false, revision: 'r1' });
    assert.equal(projection.segments.length, markers);
    assert.equal(store.inline(diagnostic.path, { dirty: false, revision: 'r1' }).length, markers);
    const rendered = await renderWithFixture(source, fixtures, { rows: [{ kind: 'line', index: 0 }], plainLines: ['\t😀é'], htmlLines: null, showDiff: false, diagnostics: projection });
    assert.doesNotMatch(problems + rendered, /<script>/);
    if (count) assert.match(problems, /&lt;script>/);
  }
  assert.equal(store.findings[0].entries.length, 2);
  await views(1, 1);
  // Same saved evidence is not a draft overlay. These states must not mutate text.
  const draft = { text: 'unsaved😀', undo: ['original'], uncertain: true };
  const before = structuredClone(draft);
  for (const dirty of [true, draft.uncertain]) {
    assert.equal(store.inline(diagnostic.path, { dirty, revision: 'r1' }).length, 0);
    assert.equal(projectSourceDiagnostics(draft.text, store.findings, { dirty, revision: 'r1' }).segments.length, 0);
  }
  assert.deepEqual(draft, before);
  const emptyGo = snapshot(1); emptyGo.resultSets[0].diagnostics = []; emit(emptyGo);
  assert.equal(store.findings[0].entries[0].providerId, 'second'); await views(1, 1);
  for (const [sequence, phase] of [[2, 'failed'], [3, 'canceled']]) {
    const stale = structuredClone(emptyGo); stale.sequence = sequence;
    stale.providers[1].phase = phase; stale.providers[1].freshness = 'stale';
    stale.providers[1].coverage.partial = true; stale.providers[1].truncated = true;
    stale.resultSets[1].diagnostics[0].freshness = 'stale'; emit(stale);
    await views(1, 0);
    const html = await renderWithFixture(controls, { checks: store });
    assert.match(html, /partial coverage/); assert.match(html, /truncated/);
  }
  const empty = snapshot(4); empty.resultSets.forEach(s => s.diagnostics = []); emit(empty);
  await views(0, 0);
  emit(snapshot(3)); await views(0, 0); // superseded payload cannot resurrect badges
  const late = listener;
  await store.detach(); late({ contractVersion: 1, context, sequence: 5, updateId: 'late', partIndex: 0, partCount: 1, snapshot: snapshot(5) });
  assert.equal(store.findings.length, 0); assert.equal(detached, 1); assert.equal(listener, undefined);
});
