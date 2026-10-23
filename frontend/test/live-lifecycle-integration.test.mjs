import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerModule, loadServerComponent, renderWithFixture } from './helpers.mjs';
import { projectSourceDiagnostics } from '../src/lib/sourceDiagnostics.ts';
import { problemLocation } from '../src/lib/problemNavigation.ts';
import { editorDiagnostics } from '../src/lib/fileEditor.ts';
import { EditorState } from '@codemirror/state';
import { LiveDocumentBridge } from '../src/lib/liveDocuments.ts';

const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const { CodeDiagnosticsStore } = await loadServerModule(new URL('../src/lib/stores/codeDiagnostics.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", 'const api = {};'],
  ["import { epoch } from '../link';", 'const epoch = () => 0;'],
  ["'../codeDiagnostics'", lib('codeDiagnostics')], ["'../codeDiagnosticCounts'", lib('codeDiagnosticCounts')],
]);
const { FilesStore } = await loadServerModule(new URL('../src/lib/stores/files.svelte.ts', import.meta.url), [
  ["import { api } from '../api';", fixtureStore('api', '__lifecycleFixture')],
  ["import { link } from './link.svelte';", fixtureStore('link', '__lifecycleFixture')],
  ...['manage', 'link', 'files', 'fileEditing'].map(n => [`'../${n}'`, lib(n)]),
]);
const tick = async () => { for (let i = 0; i < 8; i++) await new Promise(r => setImmediate(r)); };
const context = { connectionId: 'c', attachmentId: 'a', generation: 'g' };
const provider = id => ({ providerId: id, availability: 'available', phase: 'idle', freshness: 'uncertain', truncated: false, coverage: { kind: 'openedDocuments', roots: [''], partial: false, externalChanges: 'unwatched' } });
const content = text => ({ path: 'a.go', text, revision: 'r1', size: text.length, edit: { eligible: true, newline: 'lf' } });

async function harness(live = true) {
  const calls = [], listeners = new Map(), timers = new Map();
  let seq = 0, id = 0, doc;
  globalThis.__lifecycleFixture = {
    link: { dataHost: 'h', blockedReason: null },
    api: { listFiles: async () => ({ root: '/p', files: ['a.go'] }), readFile: async () => content('a😀\r\nb'), saveFile: async (_d, r) => ({ status: 'saved', content: { ...content(r.text), revision: 'r2' } }) },
  };
  const snapshot = sets => ({ contractVersion: 1, context, sequence: seq++, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers: [provider('lsp'), provider('checker')], resultSets: sets });
  const api = {
    onEvent(n, fn) { listeners.set(n, fn); return () => listeners.delete(n); },
    async attachCodeDiagnostics() { return { project: { hostId: 'h', root: '/p' }, context, snapshot: snapshot([]) }; },
    async detachCodeDiagnostics() { return { context }; },
    async openCodeDiagnosticDocument(c, path, documentId, version, text) { calls.push(['open', version, text]); doc = { documentId, version }; return { context: c, ...doc }; },
    async changeCodeDiagnosticDocument(c, documentId, version, text) { calls.push(['change', version, text]); doc = { documentId, version }; return { context: c, ...doc }; },
    async saveCodeDiagnosticDocument(c, documentId, version) { calls.push(['save', version]); return { context: c, documentId }; },
    async closeCodeDiagnosticDocument(c, documentId) { calls.push(['close']); return { context: c, documentId }; },
  };
  const files = new FilesStore(), checks = new CodeDiagnosticsStore(api, () => 0);
  const bridge = new LiveDocumentBridge({ api, store: checks, source: () => ({ dir: files.dir, documents: files.displayedDocuments() }), newId: () => `d${++id}`, timers: { set(fn) { timers.set(fn, fn); return fn; }, clear(fn) { timers.delete(fn); } } });
  checks.setLiveBasis(path => bridge.basis(path));
  checks.setLiveRecovery(() => bridge.statuses, (path, id) => bridge.reopen(path, id));
  bridge.onChange(() => checks.liveChanged());
  files.observe(bridge);
  const off = checks.onLifecycle(() => bridge.sync());
  await files.show('/p');
  await checks.attach('/p', { phase: 'connected', generation: 1, hostId: 'h', capabilities: { codeDiagnostics: true, ...(live ? { liveCodeDiagnostics: true } : {}) } });
  function emit(version = doc?.version, documentId = doc?.documentId, providers = ['lsp']) {
    const d = { path: 'a.go', severity: 'error', message: '<script>&😀', source: 'fixture', freshness: version ? 'current' : 'uncertain', basis: { kind: 'document', documentId, ...(version ? { version } : {}) }, range: { start: { line: 0, character: 1 }, end: { line: 0, character: 3 } } };
    const s = snapshot(providers.map(providerId => ({ providerId, origin: 'lsp', scope: { kind: 'document', path: 'a.go', documentId }, diagnostics: [d] })));
    listeners.get('codeDiagnostics:updated')?.({ contractVersion: 1, context, sequence: s.sequence, updateId: `u${s.sequence}`, partIndex: 0, partCount: 1, snapshot: s });
  }
  return { files, checks, bridge, api, snapshot, calls, timers, emit, listeners, off, async flush() { for (const fn of [...timers.keys()]) { timers.delete(fn); fn(); } await tick(); } };
}

test('real files + diagnostics stores: freshness, attribution, save, close and host drop', async () => {
  const h = await harness();
  h.files.openFile('a.go', { keep: true }); await tick();
  const inline = () => h.checks.liveInline('a.go', h.bridge.basis('a.go'));
  h.emit(undefined, undefined, ['lsp', 'checker']);
  assert.equal(h.checks.counts.error, 1);
  assert.equal(h.checks.findings[0].entries.length, 2);
  assert.equal(inline().length, 1);
  h.emit(undefined, undefined, ['checker']); // full replacement leaves second attribution
  assert.equal(h.checks.counts.error, 1);
  assert.equal(h.checks.findings[0].entries[0].providerId, 'checker');
  h.files.startEditing('a.go'); h.files.updateDraft('a.go', 'draft😀');
  assert.equal(inline().length, 0); // before debounce
  h.emit(); assert.equal(inline().length, 0); // even a matching old push cannot restore it
  await h.flush();
  const version = h.bridge.basis('a.go').version;
  h.emit(version - 1); assert.equal(inline().length, 0);
  h.emit(null); assert.equal(inline().length, 0);
  h.emit(version, 'old-document'); assert.equal(inline().length, 0);
  h.emit(); assert.equal(inline().length, 1);
  h.files.updateDraft('a.go', 'saved😀');
  await h.files.saveEdited('a.go'); await tick();
  assert.deepEqual(h.calls.slice(-2), [['change', version + 1, 'saved😀'], ['save', version + 1]]);
  h.files.updateDraft('a.go', 'retained draft');
  const late = h.listeners.get('codeDiagnostics:updated');
  h.checks.disconnect();
  assert.equal(h.timers.size, 0);
  await h.flush();
  assert.equal(h.files.activeTab.edit.draft, 'retained draft');
  assert.equal(inline().length, 0);
  late({ contractVersion: 1, context, sequence: 999 });
  assert.equal(h.checks.status, 'disconnected');
  assert.equal(h.calls.at(-1)[0], 'save');
  h.off(); await h.checks.detach();
});

test('R07 real store source/component/editor projection and navigation require the displayed live identity', async () => {
  const component = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/SourceView.svelte', import.meta.url), [
    ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
    ["'../../../fileSelection'", lib('fileSelection')], ["'../../../sourceDiagnostics'", lib('sourceDiagnostics')],
  ]);
  const h = await harness();
  h.files.openFile('a.go', { keep: true }); await tick();
  h.files.startEditing('a.go');
  h.files.updateDraft('a.go', 'a😀\nb\n'); await h.flush();
  const tab = h.files.activeTab, edit = tab.edit, version = edit.version;
  function projection(expected) {
    const basis = { dirty: true, revision: edit.revision, live: h.checks.liveBasis(tab.path) };
    const p = projectSourceDiagnostics(edit.draft, h.checks.findings, basis, h.checks.status === 'ready');
    assert.equal(p.segments.length, expected);
    const options = { id: tab, path: tab.path, text: edit.draft, diagnostics: { tab, path: tab.path, context: 'fixture', findings: h.checks.findings, ...basis, current: h.checks.status === 'ready' } };
    assert.equal(editorDiagnostics(EditorState.create({ doc: edit.draft }), options).length, expected);
    if (h.checks.findings.length) assert.equal(!problemLocation(h.checks.findings[0].diagnostic, basis), !!expected);
    assert.equal(edit.version, version);
    return p;
  }
  h.emit();
  const p = projection(1);
  const html = await renderWithFixture(component, { files: { wrap: false } }, { rows: [{ kind: 'line', index: 0 }], plainLines: ['a😀'], htmlLines: null, showDiff: false, diagnostics: p });
  assert.match(html, /has-diagnostics/);
  assert.doesNotMatch(html, /<script>/);
  const live = h.checks.liveBasis('a.go');
  h.emit(live.version, 'another-document'); projection(0);
  h.emit(live.version - 1); projection(0);
  h.emit(live.version + 1); projection(0);
  h.emit(null); projection(0);
  h.emit(); projection(1);
  assert.equal(h.checks.liveBasis('other.go'), undefined);
  // Wrong URI remains panel-only even with a valid document ID/version.
  const wrongPath = h.checks.findings.map(f => ({ ...f, diagnostic: { ...f.diagnostic, path: 'other.go' } }));
  assert.equal(editorDiagnostics(EditorState.create({ doc: edit.draft }), { id: tab, path: 'a.go', diagnostics: { tab, path: 'a.go', findings: wrongPath, dirty: true, live, current: true } }).length, 0);
  h.files.updateDraft(tab, 'a😀\nnew\n'); projection(0); // immediate, before debounce
  h.emit(); projection(0);
  await h.flush(); h.emit(); projection(1);
  h.checks.revoking = true; // pending revoke suppresses basis before its async reply
  assert.equal(h.checks.liveBasis('a.go'), undefined);
  projection(0);
  h.checks.disconnect(); projection(0);
  assert.equal(edit.draft, 'a😀\nnew\n');
  h.off(); await h.checks.detach();
});

const checksComponent = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/CodeChecks.svelte', import.meta.url), [
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
]);
const problemsComponent = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/ProblemsPanel.svelte', import.meta.url), [
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
  ["'../../../codeDiagnostics'", lib('codeDiagnostics')], ["'../../../problemNavigation'", lib('problemNavigation')], ["'../../../codeCheckSummary'", lib('codeCheckSummary')],
]);

test('R08 provider crash/truncation propagates through actual store, panel, counts and marker projections', async () => {
  const h = await harness();
  h.files.openFile('a.go', { keep: true }); await tick(); h.emit();
  const s = h.snapshot(h.checks.snapshot.resultSets);
  Object.assign(s.providers[0], { phase: 'failed', failure: { code: 'toolFailed', message: '<script>crash</script>' }, truncated: true, notice: '<img src=x> shortened' });
  s.providers[1].availability = 'missingTool';
  h.listeners.get('codeDiagnostics:updated')({ contractVersion: 1, context, sequence: s.sequence, updateId: 'failure', partIndex: 0, partCount: 1, snapshot: s });
  assert.equal(h.checks.status, 'ready'); // provider failure is not a browsing failure
  assert.equal(h.checks.counts.error, 1); assert.equal(h.checks.countsUnder('a.go'), 1);
  assert.equal(h.checks.findings[0].diagnostic.freshness, 'stale');
  assert.equal(h.checks.partial, true); assert.equal(h.checks.truncated, true);
  assert.equal(h.checks.liveInline('a.go', h.checks.liveBasis('a.go')).length, 0);
  const basis = { dirty: false, revision: 'r1', live: h.checks.liveBasis('a.go') };
  assert.equal(projectSourceDiagnostics(h.files.content.text, h.checks.findings, basis, true).segments.length, 0);
  assert.equal(editorDiagnostics(EditorState.create({ doc: h.files.content.text }), { id: 't', path: 'a.go', diagnostics: { tab: 't', path: 'a.go', findings: h.checks.findings, ...basis, current: true } }).length, 0);
  const html = await renderWithFixture(checksComponent, { checks: h.checks });
  for (const text of ['failed', 'Not installed', 'partial coverage', 'truncated', '&lt;script>', '&lt;img', 'No provider restart action']) assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /<script>|<img|Retry lsp/);
  const panel = await renderWithFixture(problemsComponent, { checks: h.checks }, { expanded: true, onnavigate() {} });
  assert.match(panel, /stale/); assert.match(panel, /results truncated/); assert.doesNotMatch(panel, /<script>/);
  await assert.rejects(h.checks.retry('lsp'), /live providers cannot be retried/);
  h.files.startEditing('a.go'); h.files.updateDraft('a.go', 'still browsable');
  assert.equal(h.files.activeTab.edit.draft, 'still browsable');
  h.off(); await h.checks.detach();
});

test('R08 bridge failure is rendered without raw error text; explicit recovery is bounded and identity guarded', async () => {
  const h = await harness();
  h.files.openFile('a.go', { keep: true }); await tick(); h.emit();
  h.api.changeCodeDiagnosticDocument = async () => { throw new Error('toolFailed: SECRET DOCUMENT TEXT'); };
  h.files.startEditing('a.go'); h.files.updateDraft('a.go', 'latest displayed text'); await h.flush();
  const failed = h.checks.liveDocuments[0];
  assert.equal(failed.failure, 'toolFailed'); assert.equal(failed.canReopen, true);
  assert.equal(h.checks.findings[0].diagnostic.freshness, 'stale'); assert.equal(h.checks.partial, true);
  const html = await renderWithFixture(checksComponent, { checks: h.checks });
  assert.match(html, /Live document synchronization failed \(toolFailed\)/); assert.match(html, /Reopen live synchronization/);
  assert.doesNotMatch(html, /SECRET DOCUMENT TEXT|latest displayed text/);
  h.checks.reopenDocument('a.go', 'wrong-id'); await tick();
  assert.equal(h.checks.liveDocuments[0].documentId, failed.documentId);
  h.checks.reopenDocument('a.go', failed.documentId); await tick();
  assert.notEqual(h.checks.liveDocuments[0].documentId, failed.documentId);
  assert.deepEqual(h.calls.at(-1), ['open', 1, 'latest displayed text']);
  const fresh = h.checks.liveBasis('a.go');
  assert.equal(h.checks.findings[0].diagnostic.freshness, 'stale');
  h.emit(1, failed.documentId); assert.equal(h.checks.liveInline('a.go', fresh).length, 0);
  h.emit(1, fresh.documentId); assert.equal(h.checks.liveInline('a.go', fresh).length, 1);
  for (let n = 0; n < 2; n++) {
    h.files.updateDraft('a.go', `next${n}`); await h.flush();
    const d = h.checks.liveDocuments[0];
    h.checks.reopenDocument(d.path, d.documentId); await tick();
  }
  assert.equal(h.checks.liveDocuments[0].canReopen, false);
  const calls = h.calls.length, d = h.checks.liveDocuments[0];
  h.checks.reopenDocument(d.path, d.documentId); await tick(); assert.equal(h.calls.length, calls);
  h.checks.revoking = true;
  assert.deepEqual(h.checks.liveDocuments, []); h.checks.reopenDocument(d.path, d.documentId);
  await tick(); assert.equal(h.calls.length, calls);
  h.off(); await h.checks.detach();
});

test('R08 superseded open/change promises cannot poison a new attachment or restore markers', async () => {
  for (const operation of ['openCodeDiagnosticDocument', 'changeCodeDiagnosticDocument']) {
    const h = await harness();
    let reject;
    if (operation === 'changeCodeDiagnosticDocument') { h.files.openFile('a.go', { keep: true }); await tick(); }
    h.api[operation] = () => new Promise((_resolve, no) => { reject = no; });
    if (operation === 'openCodeDiagnosticDocument') { h.files.openFile('a.go', { keep: true }); await tick(); }
    else { h.files.startEditing('a.go'); h.files.updateDraft('a.go', 'held'); await h.flush(); }
    const old = h.checks.liveDocuments[0].documentId;
    h.checks.disconnect();
    await h.checks.attach('/p', { phase: 'connected', generation: 2, hostId: 'h', capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } });
    h.api.openCodeDiagnosticDocument = async () => ({});
    reject(new Error('toolFailed: old body')); await tick();
    assert.equal(h.checks.status, 'ready');
    assert.notEqual(h.checks.liveDocuments[0].documentId, old);
    assert.equal(h.checks.liveDocuments[0].failure, undefined);
    h.emit(1, old); assert.equal(h.checks.liveInline('a.go', h.checks.liveBasis('a.go')).length, 0);
    h.off(); await h.checks.detach();
  }
});

test('old remote missing live flag keeps bridge entirely inert through edit/save/close', async () => {
  const h = await harness(false);
  h.files.openFile('a.go', { keep: true }); await tick();
  h.files.startEditing('a.go'); h.files.updateDraft('a.go', 'draft');
  await h.flush(); await h.files.saveEdited('a.go');
  h.files.closeTab('a.go'); await tick();
  assert.deepEqual(h.calls, []);
  assert.equal(h.timers.size, 0);
  assert.equal(h.checks.liveAttachment, undefined);
  h.off(); await h.checks.detach();
});

test('vtsls shape: TS/JS extensions go live inside covered packages; unversioned pushes count in the tree but never draw inline', async () => {
  const listeners = new Map();
  let seq = 0;
  const lsp = (providerId, extra) => ({ ...provider(providerId), coverage: { kind: 'openedDocuments', roots: [''], partial: true, externalChanges: 'unwatched' }, ...extra });
  const providers = [lsp('gopls', { documentExtensions: ['.go'] }), lsp('typescript', { coverage: { kind: 'openedDocuments', roots: ['web'], partial: true, externalChanges: 'unwatched' }, documentExtensions: ['.ts', '.mts', '.cts', '.tsx', '.js', '.mjs', '.cjs', '.jsx'] }),
    lsp('svelte', { availability: 'unsupported' })];
  const snapshot = sets => ({ contractVersion: 1, context, sequence: seq++, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers, resultSets: sets });
  const api = { onEvent(n, fn) { listeners.set(n, fn); return () => listeners.delete(n); }, async attachCodeDiagnostics() { return { project: { hostId: 'h', root: '/p' }, context, snapshot: snapshot([]) }; }, async detachCodeDiagnostics() { return { context }; } };
  const checks = new CodeDiagnosticsStore(api, () => 0);
  await checks.attach('/p', { phase: 'connected', generation: 1, hostId: 'h', capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } });
  for (const [path, live] of [['web/b.mjs', true], ['web/src/a.TS', true], ['web/c.cjs', true], ['a.go', true], ['b.mjs', false], ['web/c.svelte', false], ['web/readme.md', false]])
    assert.equal(checks.liveSupports(path), live, path);
  // vtsls publishes without a version: uncertain, no range, still a red tree count.
  const d = { path: 'web/b.mjs', severity: 'error', message: 'Expression expected.', code: '1109', source: 'typescript/ts', freshness: 'uncertain', basis: { kind: 'document', documentId: 'd1' } };
  const s = snapshot([{ providerId: 'typescript', origin: 'lsp', scope: { kind: 'document', path: 'web/b.mjs', documentId: 'd1' }, diagnostics: [d] }]);
  listeners.get('codeDiagnostics:updated')({ contractVersion: 1, context, sequence: s.sequence, updateId: 'ts', partIndex: 0, partCount: 1, snapshot: s });
  assert.equal(checks.counts.error, 1);
  assert.equal(checks.countsUnder('web'), 1); assert.equal(checks.countsUnder('web/b.mjs'), 1);
  assert.equal(checks.pathCounts.folders.get('web').uncertain, true);
  assert.equal(checks.liveInline('web/b.mjs', { documentId: 'd1', version: 1, synced: true }).length, 0);
  // The empty publication clears it.
  const clear = snapshot([{ providerId: 'typescript', origin: 'lsp', scope: { kind: 'document', path: 'web/b.mjs', documentId: 'd1' }, diagnostics: [] }]);
  listeners.get('codeDiagnostics:updated')({ contractVersion: 1, context, sequence: clear.sequence, updateId: 'clear', partIndex: 0, partCount: 1, snapshot: clear });
  assert.equal(checks.counts.error, 0); assert.equal(checks.countsUnder('web'), 0);
  await checks.detach();
});

test('Ruff shape: .py/.pyi go live inside covered projects; versioned pushes draw inline for the matching version only', async () => {
  const listeners = new Map();
  let seq = 0;
  const lsp = (providerId, extra) => ({ ...provider(providerId), coverage: { kind: 'openedDocuments', roots: [''], partial: true, externalChanges: 'unwatched' }, ...extra });
  const providers = [lsp('gopls', { documentExtensions: ['.go'] }), lsp('typescript', { availability: 'unsupported' }), lsp('svelte', { availability: 'unsupported' }),
    lsp('ruff', { coverage: { kind: 'openedDocuments', roots: ['svc'], partial: false, externalChanges: 'unwatched' }, documentExtensions: ['.py', '.pyi'] })];
  const snapshot = sets => ({ contractVersion: 1, context, sequence: seq++, approval: { state: 'approved', mode: 'session', fingerprint: 'fp' }, providers, resultSets: sets });
  const api = { onEvent(n, fn) { listeners.set(n, fn); return () => listeners.delete(n); }, async attachCodeDiagnostics() { return { project: { hostId: 'h', root: '/p' }, context, snapshot: snapshot([]) }; }, async detachCodeDiagnostics() { return { context }; } };
  const checks = new CodeDiagnosticsStore(api, () => 0);
  await checks.attach('/p', { phase: 'connected', generation: 1, hostId: 'h', capabilities: { codeDiagnostics: true, liveCodeDiagnostics: true } });
  for (const [path, live] of [['svc/app/a.py', true], ['svc/a.pyi', true], ['x.py', false], ['svc/n.ipynb', false], ['svc/b.ts', false], ['svc/pyproject.toml', false]])
    assert.equal(checks.liveSupports(path), live, path);
  // Ruff publishes with the document version: current, positioned, inline for that version.
  const range = { start: { line: 0, character: 7 }, end: { line: 0, character: 9 } };
  const d = { path: 'svc/app/a.py', severity: 'warning', message: '`os` imported but unused\n\nhelp: Remove unused import: `os`', code: 'F401', source: 'ruff/Ruff', freshness: 'current', range, basis: { kind: 'document', documentId: 'd1', version: 2 } };
  const s = snapshot([{ providerId: 'ruff', origin: 'lsp', scope: { kind: 'document', path: 'svc/app/a.py', documentId: 'd1' }, diagnostics: [d] }]);
  listeners.get('codeDiagnostics:updated')({ contractVersion: 1, context, sequence: s.sequence, updateId: 'ruff', partIndex: 0, partCount: 1, snapshot: s });
  assert.equal(checks.counts.warning, 1); assert.equal(checks.countsUnder('svc'), 1);
  assert.equal(checks.liveInline('svc/app/a.py', { documentId: 'd1', version: 2, synced: true }).length, 1);
  assert.equal(checks.liveInline('svc/app/a.py', { documentId: 'd1', version: 3, synced: true }).length, 0, 'never drawn on newer text');
  const clear = snapshot([{ providerId: 'ruff', origin: 'lsp', scope: { kind: 'document', path: 'svc/app/a.py', documentId: 'd1' }, diagnostics: [] }]);
  listeners.get('codeDiagnostics:updated')({ contractVersion: 1, context, sequence: clear.sequence, updateId: 'clear', partIndex: 0, partCount: 1, snapshot: clear });
  assert.equal(checks.counts.warning, 0); assert.equal(checks.countsUnder('svc'), 0);
  await checks.detach();
});
