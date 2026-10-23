// Compact Go to File code-check status: no verbose default panel, one bottom
// status button, disclosed details/settings, and red file names for any issue.
// Server-rendered markup and pure state only; not native GUI/paint/focus QA.
import test from 'node:test';
import assert from 'node:assert/strict';
import { createRawSnippet } from 'svelte';
import { fixtureStore, loadServerComponent, renderWithFixture, renderedButtons } from './helpers.mjs';
import { codeCheckSummary, issueCounts, problemCounts, toggleDisclosure } from '../src/lib/codeCheckSummary.ts';

const base = new URL('../src/lib/components/common/go-to-file/', import.meta.url);
const lib = name => JSON.stringify(new URL(`../src/lib/${name}.ts`, import.meta.url).href);
const checksStore = ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')];
const panel = await loadServerComponent(new URL('ProblemsPanel.svelte', base), [checksStore,
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["'../../../codeDiagnostics'", lib('codeDiagnostics')], ["'../../../problemNavigation'", lib('problemNavigation')],
  ["'../../../codeCheckSummary'", lib('codeCheckSummary')]]);
const settings = await loadServerComponent(new URL('CodeChecks.svelte', base), [checksStore]);
globalThis.__statusPanel = panel.default;
globalThis.__statusSettings = settings.default;
const dialog = await loadServerComponent(new URL('../GoToFileDialog.svelte', base), [
  ["import { files } from '../../stores/files.svelte';", fixtureStore('files')],
  ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
  ["import { codeDiagnostics } from '../../stores/codeDiagnostics.svelte';", 'const codeDiagnostics = {};'],
  ["import { connectionStore } from '../../stores/connection.svelte';", 'const connectionStore = {};'],
  ["import ProblemsPanel from './go-to-file/ProblemsPanel.svelte';", 'const ProblemsPanel = globalThis.__statusPanel;'],
  ["import CodeChecks from './go-to-file/CodeChecks.svelte';", 'const CodeChecks = globalThis.__statusSettings;'],
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
  ["import { tabShortcut } from '../../tabActions';", lib('tabActions')],
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["'../../fullscreen'", lib('fullscreen')],
]);
const badge = await loadServerComponent(new URL('DiagnosticBadge.svelte', base), [["'../../../codeDiagnosticCounts'", lib('codeDiagnosticCounts')]]);
globalThis.__statusBadge = badge.default;
const pickerReplacements = [checksStore,
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["import DiagnosticBadge from './DiagnosticBadge.svelte';", 'const DiagnosticBadge = globalThis.__statusBadge;'],
  ["'../../../files'", lib('files')], ["'../../../codeCheckSummary'", lib('codeCheckSummary')],
  ["import FileIcon from '../FileIcon.svelte';", 'const FileIcon = () => {};'],
  ["import LoadingSpinner from '../LoadingSpinner.svelte';", 'const LoadingSpinner = () => {};']];
const picker = await loadServerComponent(new URL('FilePicker.svelte', base), pickerReplacements);
const searchPicker = await loadServerComponent(new URL('FilePicker.svelte', base), [...pickerReplacements, ["let query = '';", "let query = 'go';"]]);

const HOST = 'local:4f9a2c1e8b7d6a5f';
const range = { start: { line: 0, character: 0 }, end: { line: 0, character: 1 } };
const finding = (path, severity = 'error') => {
  const diagnostic = { path, severity, message: `${severity} in ${path}`, source: 'go', freshness: 'current', basis: { kind: 'saved', revision: 'r' }, range };
  return { diagnostic, entries: [{ providerId: 'go', diagnostic }] };
};
const provider = { providerId: 'go', availability: 'available', phase: 'idle', freshness: 'current', coverage: { kind: 'savedProject', roots: [''], externalChanges: 'reconciled', partial: false }, truncated: false };
const required = { approval: { state: 'required' }, proposal: { fingerprint: 'sha256:deadbeef', tools: [{ providerId: 'go', executable: '/usr/bin/go', configPaths: [] }] } };
const approved = { approval: { state: 'approved', mode: 'session' } };
const checks = (extra = {}) => ({ status: 'ready', project: { hostId: HOST, root: '/project' }, snapshot: required, providers: [provider], findings: [], counts: { error: 0, warning: 0 },
  pathCounts: { files: new Map(), folders: new Map(), total: { error: 0, warning: 0 } }, canRetry: () => false, ...extra });
const settingsSnippet = createRawSnippet(() => ({ render: () => '<p>SETTINGS-BODY</p>' }));
const renderPanel = (fixture, props = {}) => renderWithFixture(panel, { checks: checks(fixture) }, { onnavigate() {}, ...props });
const statusButton = html => /<button[^>]*class="status[^"]*"[^>]*>[\s\S]*?<\/button>/.exec(html)?.[0] ?? '';

test('default dialog has no top code-check panel: only a collapsed bottom status after the body', async () => {
  const html = await renderWithFixture(dialog, { files: { open: true, dir: '/project' }, link: {}, checks: checks() });
  for (const verbose of ['Enable code checks', 'Code check settings', 'not a sandbox', 'Go language server', 'Installing a tool', HOST, 'sha256:deadbeef', 'Host:'])
    assert.ok(!html.includes(verbose), `default markup must not include ${verbose}`);
  const status = statusButton(html);
  assert.match(status, /aria-expanded="false"/);
  assert.match(status, /Code checks: approval needed/);
  assert.ok(html.indexOf('class="body') < html.indexOf('class="status'), 'status sits below the file body');
  assert.equal((html.match(/aria-label="Code checks"/g) ?? []).length, 1);
  const region = /<div id="([^"]+)"[^>]*role="region"[^>]*hidden/.exec(html);
  assert.ok(region, 'details region is hidden by default');
  assert.match(status, new RegExp(`aria-controls="${region[1]}"`));
  // A presentation dialog shows no status at all, and a closed dialog renders nothing.
  assert.doesNotMatch(await renderWithFixture(dialog, { files: { open: false, dir: '/project' }, link: {}, checks: checks() }), /class="status/);
});

test('status button counts problems concisely; tooltip conveys scope without host identity', async () => {
  const html = await renderPanel({ snapshot: approved, findings: [finding('a.go'), finding('a.go', 'warning'), finding('b.go', 'warning'), finding('c.go', 'hint')] });
  const status = statusButton(html);
  assert.match(status, />1 error · 2 warnings · 1 info</);
  assert.match(status, /title="Reported findings for this project only[^"]*outside the current listing and search[^"]*"/);
  assert.match(status, /class="status tone-error/);
  assert.doesNotMatch(html, new RegExp(HOST));
  assert.doesNotMatch(html, /error in a\.go|SETTINGS-BODY/, 'collapsed: no issue rows or settings');
  assert.equal(renderedButtons(html).length, 1, 'collapsed panel has exactly one control');
});

test('expanded details list issues; settings stay behind a second disclosure', async () => {
  const fixture = { snapshot: approved, findings: [finding('a.go'), finding('z.ts', 'hint')] };
  const open = await renderPanel(fixture, { expanded: true, settings: settingsSnippet });
  assert.match(statusButton(open), /aria-expanded="true"/);
  assert.match(open, /error in a\.go/); assert.match(open, /hint in z\.ts/);
  assert.match(open, /<button[^>]*aria-expanded="false"[^>]*aria-controls="code-check-settings-[^"]*"[^>]*>Settings</);
  assert.doesNotMatch(open, /SETTINGS-BODY/);
  const both = await renderPanel(fixture, { expanded: true, settingsOpen: true, settings: settingsSnippet });
  assert.match(both, /SETTINGS-BODY/);
  assert.match(both, /aria-expanded="true"[^>]*aria-controls="code-check-settings-/);
  // Settings never render while the details are collapsed, even if remembered open.
  assert.doesNotMatch(await renderPanel(fixture, { expanded: false, settingsOpen: true, settings: settingsSnippet }), /SETTINGS-BODY/);
});

test('approval and setup controls are reachable only once disclosed, and never auto-approve', async () => {
  const collapsed = await renderWithFixture(dialog, { files: { open: true, dir: '/project' }, link: {}, checks: checks() });
  assert.doesNotMatch(collapsed, /Enable for this session|Always allow|Review gopls|>Install</);
  const html = await renderWithFixture(settings, { checks: checks({ managed: { hostId: HOST, state: 'missingTool' } }) });
  const buttons = renderedButtons(html).map(b => b.text);
  for (const label of ['Enable for this session', 'Always allow this project', 'Not now', 'Review gopls installation']) assert.ok(buttons.includes(label), label);
  assert.ok(!buttons.includes('Install'), 'install needs a reviewed proposal first');
  assert.doesNotMatch(html, /<details[^>]*open/, 'setup and technical details start closed');
  // Opening with nothing to list reveals settings, but only as a disclosure: approving still takes a click.
  assert.deepEqual(toggleDisclosure({ expanded: false, settingsOpen: false }, { needsSettings: true }, 0), { expanded: true, settingsOpen: true });
  assert.deepEqual(toggleDisclosure({ expanded: false, settingsOpen: false }, { needsSettings: true }, 3), { expanded: true, settingsOpen: false });
  assert.deepEqual(toggleDisclosure({ expanded: false, settingsOpen: false }, { needsSettings: false }, 0), { expanded: true, settingsOpen: false });
  assert.deepEqual(toggleDisclosure({ expanded: true, settingsOpen: true }, { needsSettings: true }, 0), { expanded: false, settingsOpen: false });
});

test('unavailable, off, unapproved and failed states never claim a clean project', () => {
  const clean = /No problems/;
  const cases = {
    detached: { status: 'detached' }, attaching: { status: 'attaching' }, unsupported: { status: 'unsupported' },
    error: { status: 'error' }, disconnected: { status: 'disconnected' }, resyncing: { status: 'resyncing' },
    approval: { status: 'ready', snapshot: required }, revoking: { status: 'ready', snapshot: approved, revoking: true },
    noChecker: { status: 'ready', snapshot: approved, providers: [{ ...provider, availability: 'missingTool' }] },
    noProviders: { status: 'ready', snapshot: approved, providers: [] }, running: { status: 'ready', snapshot: approved, providers: [provider], running: true },
  };
  for (const [name, input] of Object.entries(cases)) {
    const s = codeCheckSummary(input);
    assert.doesNotMatch(s.text, clean, name); assert.doesNotMatch(s.text, /\b0 (problems|errors)/, name);
    assert.ok(s.text.length <= 40, `${name} stays compact: ${s.text}`);
  }
  assert.equal(codeCheckSummary(cases.unsupported).text, 'Code checks unavailable');
  assert.equal(codeCheckSummary(cases.approval).needsSettings, true);
  assert.equal(codeCheckSummary({ status: 'ready', snapshot: approved, providers: [provider] }).text, 'No problems reported');
  assert.equal(codeCheckSummary({ status: 'ready', snapshot: approved, providers: [provider], partial: true }).text, 'No problems reported (partial)');
  assert.equal(codeCheckSummary({ status: 'disconnected', findings: [finding('a.go')] }).text, '1 error · stale');
  assert.deepEqual(problemCounts([finding('a', 'information'), finding('b', 'hint'), finding('c', 'warning')]), { error: 0, warning: 1, other: 2, total: 3 });
});

test('file names turn red for any reported severity in tree, selected and search rows; folders get a subtle mark', async () => {
  const findings = [finding('src/hint.go', 'hint'), finding('src/info.go', 'information'), finding('go.mod', 'warning')];
  assert.deepEqual([...issueCounts(findings).folders], ['src']);
  const listing = { files: ['src/hint.go', 'src/info.go', 'src/clean.go', 'go.mod', 'readme.md'], status: { 'src/hint.go': 'M' } };
  const files = { expanded: new Set(['src']), listing, selected: 'src/hint.go' };
  const warn = { error: 0, warning: 1, stale: false, uncertain: false };
  const pathCounts = { files: new Map([['go.mod', warn]]), folders: new Map(), total: warn };
  const html = await renderWithFixture(picker, { files, checks: checks({ findings, pathCounts }) }, { onPick() {}, onContext() {} });
  assert.match(html, /class="tree-row file[^"]*selected"[^>]*title="src\/hint\.go · Modified · 1 reported code issue"/);
  assert.match(html, /class="tree-name git-M[^"]* has-issue">hint\.go</, 'selected Git-modified hint keeps Git class and is red');
  assert.match(html, /class="tree-name git-none[^"]* has-issue">info\.go</);
  assert.match(html, /class="tree-name git-none[^"]* has-issue">go\.mod</);
  assert.match(html, /class="tree-name git-none[^"]*">clean\.go</); assert.doesNotMatch(html, /has-issue">(clean\.go|readme\.md)</);
  assert.match(html, /class="tree-name git-M[^"]* folder-issue">src</);
  assert.match(html, /class="git-letter git-M[^"]*">M</, 'Git letter still shows status');
  assert.equal((html.match(/has reported code issues/g) ?? []).length, 2, 'hint/info rows get accessible text; the warning badge labels itself');
  assert.match(html, /aria-label="0 errors, 1 warnings/);
  const search = await renderWithFixture(searchPicker, { files: { expanded: new Set(), listing }, checks: checks({ findings, pathCounts }) }, { onPick() {}, onContext() {} });
  assert.match(search, /class="match-name git-M[^"]* has-issue">hint\.go</);
  assert.match(search, /class="match-name git-none[^"]* has-issue">go\.mod</);
  assert.doesNotMatch(search, /has-issue">clean\.go</);
  const none = await renderWithFixture(picker, { files, checks: checks() }, { onPick() {}, onContext() {} });
  assert.doesNotMatch(none, /has-issue|folder-issue/);
});

// Regression (2026-10-23 app report): expanded panel showed issue filters and
// "No problems reported" before any approval, and disabled approval buttons with
// no reason. Settings now state the concrete blocker; an analyzer-only proposal
// renders enabled, clearly labelled approval actions without tool spam.
test('unapproved details hide filters and clean claims; blockers and analyzer-only approval are explicit', async () => {
  const unapproved = await renderPanel({ analyzing: false }, { expanded: true, settings: settingsSnippet });
  assert.doesNotMatch(unapproved, /No problems reported|Previous problem|Next problem|type="checkbox"|Partial coverage/);
  assert.match(unapproved, /Set up code checks|Settings/);
  const listed = await renderPanel({ snapshot: approved, analyzing: true }, { expanded: true });
  assert.match(listed, /No problems reported/); assert.match(listed, /type="checkbox"/);
  const syntax = { ...provider, providerId: 'go-syntax', availability: 'approvalRequired', coverage: { ...provider.coverage, kind: 'savedProject' } };
  const missing = { ...provider, providerId: 'go-vet', availability: 'missingTool' };
  const blocker = 'Nothing to approve: no supported checker was found on this host (go-vet: not installed).';
  const none = await renderWithFixture(settings, { checks: checks({ snapshot: { approval: { state: 'required' } }, providers: [missing], approvalBlocker: blocker }) });
  assert.ok(none.includes('Nothing to approve: no supported checker was found on this host (go-vet: not installed).'));
  const buttons = renderedButtons(none).filter(b => /Enable for this session|Always allow/.test(b.text));
  assert.deepEqual(buttons.map(b => b.disabled), [true, true]);
  assert.equal(codeCheckSummary({ status: 'ready', snapshot: { approval: { state: 'required' } }, providers: [missing] }).text, 'Code checks: no checker found');
  const proposal = { fingerprint: 'sha256-v1:analyzers', tools: [], analyzers: [{ providerId: 'go-syntax', description: 'Built-in Go syntax check. Runs no program.' }] };
  const html = await renderWithFixture(settings, { checks: checks({ snapshot: { approval: { state: 'required' }, proposal }, providers: [syntax, missing] }) });
  const enabled = renderedButtons(html).filter(b => /Enable for this session|Always allow/.test(b.text));
  assert.deepEqual(enabled.map(b => b.disabled), [false, false]);
  assert.match(html, /Built-in go-syntax: Built-in Go syntax check\. Runs no program\./);
  assert.match(html, /Only built-in checks are available on this host/);
  assert.doesNotMatch(html, /Project configuration and plugins can execute code/);
  assert.doesNotMatch(html.slice(0, html.indexOf('Technical details')), new RegExp(HOST));
  assert.equal(codeCheckSummary({ status: 'ready', snapshot: { approval: { state: 'required' }, proposal }, providers: [syntax] }).text, 'Code checks: approval needed');
});
