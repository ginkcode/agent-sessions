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
  return { files, checks, bridge, calls, timers, emit, listeners, off, async flush() { for (const fn of [...timers.keys()]) { timers.delete(fn); fn(); } await tick(); } };
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
