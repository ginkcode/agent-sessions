// Readability pass for Go to File diagnostics (2026-10-23 user screenshot):
// file-level vs actually stale wording, compact source list, explicit recovery,
// scannable checker rows and honest per-tool setup availability.
// Server-rendered markup and pure state only; not native GUI/paint QA.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fixtureStore, loadServerComponent, renderWithFixture, renderedButtons } from './helpers.mjs';
import { problemLocationStatus, fileLevelFinding } from '../src/lib/problemNavigation.ts';
import { projectSourceDiagnostics } from '../src/lib/sourceDiagnostics.ts';
import { liveLanguages, locationLabel, longMessage, messagePreview, providerMode, providerStatus, remoteHost, staleKind, syncFailureReason, toolName } from '../src/lib/codeCheckLabels.ts';

const range = (a, b, c = a, d = b) => ({ start: { line: a, character: b }, end: { line: c, character: d } });
const diag = (extra = {}) => ({ path: 'a.go', severity: 'error', message: 'undefined: x', source: 'compiler', freshness: 'current', basis: { kind: 'saved', revision: 'r1' }, range: range(1, 2, 1, 3), ...extra });
const finding = d => ({ diagnostic: d, entries: [{ providerId: 'gopls', diagnostic: d }] });
const saved = { dirty: false, revision: 'r1' };
const live = { documentId: 'doc', version: 3, synced: true };

test('file-level findings are never called stale; real staleness is named precisely', () => {
  const noRange = problemLocationStatus(diag({ range: undefined }), saved);
  assert.equal(noRange.kind, 'fileLevel'); assert.doesNotMatch(noRange.notice, /stale/i);
  const tag = diag({ range: range(0, 0), message: 'No packages found for open file /p/a_linux.go.\nThis file may be excluded due to its build tags', basis: { kind: 'document', documentId: 'gone', version: 1 } });
  assert.equal(fileLevelFinding(tag), true);
  const status = problemLocationStatus(tag, { dirty: true }); // even with no live session and a draft
  assert.equal(status.kind, 'fileLevel'); assert.match(status.notice, /build constraints/); assert.doesNotMatch(status.notice, /stale/i);
  assert.equal(fileLevelFinding(diag({ range: range(0, 0), message: 'expected declaration' })), false, 'an ordinary 1:1 error keeps its position');
  // Document basis.
  const doc = extra => diag({ basis: { kind: 'document', documentId: 'doc', version: 3 }, ...extra });
  assert.equal(problemLocationStatus(doc(), { ...saved, live }).kind, 'shown');
  assert.equal(problemLocationStatus(doc(), saved).kind, 'staleDocument');
  assert.equal(problemLocationStatus(doc({ basis: { kind: 'document', documentId: 'doc', version: 2 } }), { ...saved, live }).kind, 'staleDocument');
  assert.equal(problemLocationStatus(doc({ freshness: 'stale' }), { ...saved, live }).kind, 'staleDocument');
  assert.equal(problemLocationStatus(doc(), { ...saved, live: { ...live, synced: false } }).kind, 'unverified');
  // Saved basis.
  assert.equal(problemLocationStatus(diag(), { dirty: true, revision: 'r1' }).kind, 'draft');
  assert.equal(problemLocationStatus(diag({ freshness: 'stale' }), saved).kind, 'staleSaved');
  assert.equal(problemLocationStatus(diag(), { dirty: false, revision: 'r2' }).kind, 'unverified');
  assert.equal(problemLocationStatus(diag({ locationIssue: 'invalidRange' }), saved).kind, 'invalidRange');
  for (const k of ['staleDocument', 'staleSaved', 'unverified']) assert.equal(staleKind(k), true);
  for (const k of ['shown', 'fileLevel', 'draft', 'invalidRange', undefined]) assert.equal(staleKind(k), false);
});

test('projection keeps file-level kind while not current and never paints unverified ranges', () => {
  const p = projectSourceDiagnostics('a\nbcd\n', [finding(diag({ range: undefined })), finding(diag())], saved, false);
  assert.deepEqual(p.messages.map(m => m.kind), ['fileLevel', 'unverified']);
  assert.equal(p.segments.length, 0);
  assert.equal(projectSourceDiagnostics('a\nbcd\n', [finding(diag())], saved).segments.length, 1);
});

test('label helpers: human tool names, consistent modes, bounded messages, remote detection', () => {
  assert.equal(toolName('gopls'), 'Go language server (gopls)');
  assert.equal(toolName('go-syntax'), 'Go syntax'); assert.equal(toolName('go-vet'), 'Go vet'); assert.equal(toolName('go-vet-0'), 'Go vet');
  assert.equal(toolName('<x>'), '<x>', 'unknown IDs pass through for escaped rendering');
  const cov = (kind, externalChanges) => ({ kind, roots: [], partial: false, externalChanges });
  assert.equal(providerMode({ providerId: 'gopls', coverage: cov('openedDocuments', 'unwatched') }), 'LSP · live, opened Go files');
  assert.equal(providerMode({ providerId: 'go-syntax', coverage: cov('savedProject', 'watching') }), 'saved Go files · automatic');
  assert.equal(providerMode({ providerId: 'go-vet', coverage: cov('savedProject', 'unwatched') }), 'saved Go files · on demand');
  assert.equal(providerStatus({ availability: 'available', phase: 'failed' }).text, 'Failed');
  assert.equal(providerStatus({ availability: 'missingTool', phase: 'idle' }).text, 'Not installed');
  assert.deepEqual(providerStatus({ availability: 'unsupported', phase: 'idle' }), { text: 'Unsupported', tone: 'warn' });
  assert.equal(toolName('typescript'), 'TypeScript/JavaScript language server (vtsls)');
  assert.equal(providerMode({ providerId: 'typescript', coverage: cov('openedDocuments', 'unwatched') }), 'LSP · live, opened TypeScript/JavaScript files');
  const live = (providerId, availability) => ({ providerId, availability, coverage: cov('openedDocuments', 'unwatched') });
  const both = [live('gopls', 'available'), live('typescript', 'missingTool'), live('svelte', 'unsupported'), { ...live('go-vet', 'available'), coverage: cov('savedProject', 'unwatched') }];
  assert.equal(liveLanguages(both), 'Go', 'a missing vtsls is never described as checking');
  assert.equal(liveLanguages([live('gopls', 'approvalRequired'), live('typescript', 'approvalRequired')], ['available', 'approvalRequired']), 'Go and TypeScript/JavaScript');
  assert.equal(liveLanguages([]), '');
  assert.equal(toolName('ruff'), 'Python linter (Ruff)');
  assert.equal(providerMode({ providerId: 'ruff', coverage: cov('openedDocuments', 'unwatched') }), 'LSP · live, opened Python files');
  assert.equal(liveLanguages([live('gopls', 'available'), live('typescript', 'available'), live('ruff', 'available')]), 'Go, TypeScript/JavaScript and Python');
  assert.equal(liveLanguages([live('gopls', 'available'), live('ruff', 'unsupported')]), 'Go', 'a Python-free project never claims Python checking');
  assert.equal(locationLabel(range(9, 4)), 'Ln 10, Col 5'); assert.equal(locationLabel(undefined), 'File'); assert.equal(locationLabel(range(0, 0), 'fileLevel'), 'File');
  assert.equal(longMessage('short'), false); assert.equal(longMessage('a\nb'), true); assert.equal(longMessage('x'.repeat(200)), true);
  assert.equal(messagePreview('first\nsecond'), 'first'); assert.ok(messagePreview('y'.repeat(300)).length <= 141);
  assert.equal(remoteHost('ssh:abc'), true); assert.equal(remoteHost('wsl:abc'), true); assert.equal(remoteHost('local:abc'), false); assert.equal(remoteHost(undefined), false);
  assert.match(syncFailureReason('invalidArgument'), /rejected/); assert.equal(syncFailureReason('weird'), 'synchronization failed');
});

const source = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/SourceView.svelte', import.meta.url), [
  ["import { files } from '../../../stores/files.svelte';", fixtureStore('files')],
  ["'../../../fileSelection'", JSON.stringify(new URL('../src/lib/fileSelection.ts', import.meta.url).href)], ["'../../../sourceNavigation'", JSON.stringify(new URL('../src/lib/sourceNavigation.ts', import.meta.url).href)],
  ["'../../../sourceDiagnostics'", JSON.stringify(new URL('../src/lib/sourceDiagnostics.ts', import.meta.url).href)],
]);
const sourceProps = diagnostics => ({ rows: [{ kind: 'line', index: 0 }, { kind: 'line', index: 1 }], htmlLines: null, plainLines: ['a', 'bcd'], showDiff: false, presentation: false, presentationFontSize: 14, diagnostics });

test('source list is compact: severity pill, Ln/Col, short source, escaped and bounded long messages', async () => {
  const long = '<img src=x onerror=alert(1)> first line\n' + 'detail '.repeat(60);
  const p = projectSourceDiagnostics('a\nbcd', [finding(diag()), finding(diag({ severity: 'warning', range: undefined, message: long }))], saved);
  const html = await renderWithFixture(source, { files: { wrap: false } }, sourceProps(p));
  assert.match(html, /class="sev sev-error[^"]*">Error</); assert.match(html, /class="sev sev-warning[^"]*">Warning</);
  assert.match(html, />Ln 2, Col 3</); assert.match(html, />File</);
  assert.match(html, /<details class="msg long[^"]*"><summary[^>]*>&lt;img src=x onerror=alert\(1\)> first line<\/summary>/);
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /File-level finding \(no line position\)/);
  assert.doesNotMatch(html, /not current|Refresh results|Reopen live sync/, 'file-level only: no stale badge or recovery');
  assert.match(html, /1 Error/); assert.match(html, /1 Warning/);
  const style = readFileSync(new URL('../src/lib/components/common/go-to-file/SourceView.svelte', import.meta.url), 'utf8');
  assert.match(style, /\.diagnostic-messages \{[^}]*font-size: 12px/); assert.match(style, /\.diagnostic-messages ul \{[^}]*max-height:[^;]+;[^}]*overflow: auto/);
});

test('truly stale results show a described recovery action; nothing runs during render', async () => {
  let runs = 0;
  const p = projectSourceDiagnostics('a\nbcd', [finding(diag({ freshness: 'stale' })), finding(diag({ range: undefined }))], saved);
  const recovery = { label: 'Refresh results', description: 'Refresh re-reads saved files and fetches the latest results; nothing is saved or re-run.', run: () => runs++ };
  const html = await renderWithFixture(source, { files: { wrap: true } }, { ...sourceProps(p), recovery });
  assert.match(html, /1 not current/);
  assert.match(html, /Stale: the file or checker changed/);
  assert.match(html, /nothing is saved/);
  assert.deepEqual(renderedButtons(html).filter(b => b.text === 'Refresh results').map(b => b.disabled), [false]);
  assert.equal(runs, 0);
  const disabled = await renderWithFixture(source, { files: { wrap: true } }, { ...sourceProps(p), recovery: { ...recovery, disabled: true } });
  assert.equal(renderedButtons(disabled).find(b => b.text === 'Refresh results').disabled, true);
});

test('viewer recovery reuses existing store reconciliation and never saves', () => {
  const viewer = readFileSync(new URL('../src/lib/components/common/go-to-file/FileViewer.svelte', import.meta.url), 'utf8');
  const block = viewer.slice(viewer.indexOf('let sourceRecovery'), viewer.indexOf('let rows: DiffRow[]'));
  assert.match(block, /codeDiagnostics\.reopenDocument\(failed\.path, failed\.documentId\)/);
  assert.match(block, /codeDiagnostics\.refresh\(\)/);
  assert.match(block, /d\.canReopen/);
  const code = block.replace(/'[^']*'/g, "''");
  assert.doesNotMatch(code, /save|fileActions|approve|managedAction|\.run\(\)/i);
  assert.match(block, /nothing is saved/);
  assert.match(viewer, /recovery=\{sourceRecovery\}/);
});

const checksURL = new URL('../src/lib/components/common/go-to-file/CodeChecks.svelte', import.meta.url);
const checksComponent = await loadServerComponent(checksURL, [["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')]]);
const cov = (kind, externalChanges) => ({ kind, roots: [''], partial: false, externalChanges });
const providers = [
  { providerId: 'gopls', availability: 'available', phase: 'idle', freshness: 'current', coverage: cov('openedDocuments', 'unwatched'), truncated: false },
  { providerId: 'go-syntax', availability: 'available', phase: 'idle', freshness: 'current', coverage: cov('savedProject', 'watching'), truncated: false },
  { providerId: 'go-vet', availability: 'available', phase: 'idle', freshness: 'current', coverage: cov('savedProject', 'unwatched'), truncated: false },
  { providerId: 'svelte-check', availability: 'missingTool', phase: 'idle', freshness: 'uncertain', coverage: cov('savedProject', 'unwatched'), truncated: false },
  { providerId: 'typescript', availability: 'missingTool', phase: 'idle', freshness: 'uncertain', coverage: cov('openedDocuments', 'unwatched'), truncated: false, notice: 'No TypeScript language server (vtsls) is installed.' },
  { providerId: 'ruff', availability: 'missingTool', phase: 'idle', freshness: 'uncertain', coverage: cov('openedDocuments', 'unwatched'), truncated: false, notice: 'Ruff is not installed.' },
];
// What the backend reports when the app has installed nothing.
const noApp = { gopls: 'missingTool', vtsls: 'missingTool', ruff: 'missingTool' };
const checks = (extra = {}) => ({ status: 'ready', project: { hostId: 'local:abc123', root: '/p' }, snapshot: { approval: { state: 'approved', mode: 'session' }, proposal: { fingerprint: 'sha256:fp', tools: [] } },
  providers, approved: true, canRun: true, canRetry: () => true, liveEnabled: true, liveDocuments: [], managed: { hostId: 'local:abc123', state: 'idle' }, managedInstalls: noApp, ...extra });

test('checker rows use human names, status pills and consistent modes; technical text stays disclosed', async () => {
  const html = await renderWithFixture(checksComponent, { checks: checks() });
  for (const text of ['Go language server (gopls)', 'Go syntax', 'Go vet', 'LSP · live, opened Go files', 'saved Go files · automatic', 'saved Go files · on demand', '>Ready<', 'Not installed'])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /Checks run only when requested/, 'no generic on-request claim contradicting the watcher');
  assert.match(html, /checked automatically when they change on disk/);
  const head = html.slice(0, html.indexOf('Coverage details'));
  assert.doesNotMatch(head, /local:abc123|sha256:fp|roots:/);
  assert.match(html.slice(html.indexOf('Technical details')), /local:abc123/);
  assert.match(html, /Setup not supported here: install it on the host yourself/);
  assert.doesNotMatch(html, /under Tools setup/, 'the install action is on the row, not in a collapsed section');
  // A missing vtsls is its own status with a manual command; live copy names only running languages.
  assert.match(html, /TypeScript\/JavaScript language server \(vtsls\)/);
  assert.match(html, /No TypeScript language server \(vtsls\) is installed/);
  assert.match(html, /npm install -g @vtsls\/language-server@0\.3\.0/);
  assert.match(html, /Python linter \(Ruff\)/);
  assert.match(html, /uv tool install ruff==0\.15\.20/);
  assert.doesNotMatch(html, /Opened [^.]*Python[^.]* files are checked live/);
  assert.match(html, /Opened Go files are checked live as you type/);
  assert.doesNotMatch(html, /Opened [^.]*TypeScript[^.]* files are checked live/);
  // Missing servers with a managed install get a review step; ready ones and Svelte get none.
  const buttons = renderedButtons(html).map(b => b.text);
  assert.ok(buttons.includes('Install vtsls…') && buttons.includes('Install ruff…'));
  assert.ok(!buttons.includes('Install gopls…'), 'gopls is already found');
  assert.ok(!buttons.some(b => /Install (TypeScript|Svelte)|svelte|Install$/.test(b)), 'no Svelte installer; Install needs a proposal');
  assert.match(html, /looked up in the project first/);
});

test('managed setup states: app copies, verification failures and hosts without managed installs', async () => {
  const rows = async extra => renderedButtons(await renderWithFixture(checksComponent, { checks: checks(extra) })).map(b => b.text);
  const installed = await renderWithFixture(checksComponent, { checks: checks({ managedInstalls: { gopls: 'available', vtsls: 'available', ruff: 'unavailable' } }) });
  assert.match(installed, /Ready · app copy installed/);
  assert.match(installed, /App copy installed · rediscover checkers/);
  assert.match(installed, /App copy failed verification and is not used/);
  assert.ok(!renderedButtons(installed).some(b => /^Install \w+…/.test(b.text)));
  assert.ok(!(await rows({ managed: { hostId: 'local:abc123', state: 'unavailable', message: 'Managed installation is unavailable on this host' } })).some(b => /^Install \w+…/.test(b)));
  assert.ok(!(await rows({ managed: undefined })).some(b => /^Install \w+…/.test(b)), 'no offer before status is known');
  // A tool-specific refusal (no Go for gopls) keeps the other offers.
  assert.ok((await rows({ managed: { hostId: 'local:abc123', state: 'unavailable', tool: 'gopls', message: 'no Go' } })).includes('Install ruff…'));
});

test('nothing approvable yet: the approval card and the missing row both offer Install; review shows near the top', async () => {
  const ts = [providers[4], { ...providers[0], availability: 'unsupported' }];
  const unapproved = { snapshot: { approval: { state: 'none' }, proposal: { tools: [] } }, approved: false, approvalBlocker: 'Nothing to approve: no supported checker was found on this host (typescript: not installed).' };
  const html = await renderWithFixture(checksComponent, { checks: checks({ ...unapproved, providers: ts }) });
  assert.equal(renderedButtons(html).filter(b => b.text === 'Install vtsls…').length, 2);
  assert.match(html, /Install a language server on this computer first/);
  const proposal = { token: 't', hostId: 'local:abc123', connectionId: 'c', tool: 'vtsls', version: '0.3.0', source: 'npm', integrity: 'lockfile', destination: '/d', expires: '2099-01-01T00:00:00Z' };
  const review = await renderWithFixture(checksComponent, { checks: checks({ ...unapproved, providers: ts, managed: { hostId: 'local:abc123', state: 'proposed', proposal } }) });
  assert.ok(!renderedButtons(review).some(b => b.text === 'Install vtsls…'), 'the review card replaces the offer');
  assert.ok(review.indexOf('Install vtsls 0.3.0 on this computer?') < review.indexOf('Checkers'), 'review card is above the checker list');
  assert.ok(review.indexOf('Install vtsls 0.3.0') < review.indexOf('Tools setup'));
});

test('remote host: managed setup installs on the remote host', async () => {
  const host = 'ssh:' + 'a'.repeat(64);
  const proposal = { token: 't', hostId: host, connectionId: 'c', tool: 'ruff', version: '0.15.20', source: 'https://example.invalid/ruff.tar.gz', integrity: 'sha256:abc', destination: '/home/u/.local/share/agent-sessions/tools/ruff-0.15.20', expires: '2099-01-01T00:00:00Z' };
  const offer = await renderWithFixture(checksComponent, { checks: checks({ project: { hostId: host, root: '/p' }, managed: { hostId: host, state: 'idle' }, providers: [{ ...providers[0], availability: 'missingTool' }] }) });
  assert.ok(renderedButtons(offer).map(b => b.text).includes('Install gopls…'));
  assert.match(offer, /install folder on the remote host/);
  const html = await renderWithFixture(checksComponent, { checks: checks({ project: { hostId: host, root: '/p' }, managed: { hostId: host, state: 'proposed', proposal } }) });
  for (const text of ['Install ruff 0.15.20 on the remote host?', proposal.source, proposal.integrity, proposal.destination, 'checks its SHA-256 before unpacking', host]) assert.ok(html.includes(text), text);
  const buttons = renderedButtons(html).map(b => b.text);
  assert.ok(buttons.includes('Install') && buttons.includes('Not now') && buttons.includes('Refresh setup status'));
});

test('live sync failures are grouped, concise and escaped with a labelled Reopen per file', async () => {
  const liveDocuments = [
    { path: 'a.go', documentId: 'd1', failure: 'invalidArgument', canReopen: true },
    { path: '<b>.go', documentId: 'd2', failure: 'toolFailed', canReopen: false },
    { path: 'ok.go', documentId: 'd3', canReopen: false },
  ];
  const html = await renderWithFixture(checksComponent, { checks: checks({ liveDocuments }) });
  assert.match(html, /Live sync stopped for 2 files/);
  assert.match(html, /\(invalidArgument\): the server rejected the update/);
  assert.match(html, /&lt;b>\.go/); assert.doesNotMatch(html, /<b>\.go/);
  assert.doesNotMatch(html, /ok\.go/);
  const reopen = [...html.matchAll(/<button[^>]*aria-label="Reopen live synchronization for ([^"]+)"/g)].map(m => m[1]);
  assert.deepEqual(reopen, ['a.go']);
  assert.match(html, /Nothing is saved/);
});

const panel = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/ProblemsPanel.svelte', import.meta.url), [
  ["'svelte'", JSON.stringify(import.meta.resolve('svelte'))],
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
  ["'../../../codeDiagnostics'", JSON.stringify(new URL('../src/lib/codeDiagnostics.ts', import.meta.url).href)],
  ["'../../../problemNavigation'", JSON.stringify(new URL('../src/lib/problemNavigation.ts', import.meta.url).href)],
  ["'../../../codeCheckSummary'", JSON.stringify(new URL('../src/lib/codeCheckSummary.ts', import.meta.url).href)],
]);

test('severity filters are grouped labels with a gap; rows show pill + location', async () => {
  const html = await renderWithFixture(panel, { checks: { analyzing: true, findings: [finding(diag()), finding(diag({ range: undefined, severity: 'hint' }))] } }, { expanded: true, onnavigate() {} });
  assert.match(html, /role="group" aria-label="Severity filters"/);
  for (const label of ['Error', 'Warning', 'Information', 'Hint']) assert.match(html, new RegExp(`<label class="filter[^"]*"><input type="checkbox"[^>]*/?><span[^>]*>${label}</span></label>`));
  assert.match(html, />Ln 2, Col 3</); assert.match(html, />File-level</);
  const style = readFileSync(new URL('../src/lib/components/common/go-to-file/ProblemsPanel.svelte', import.meta.url), 'utf8');
  assert.match(style, /\.filter \{[^}]*gap: 5px/); assert.match(style, /\.details \{[^}]*max-height: min\(/);
});

test('open details are resizable from the top edge and are the only scroll container', async () => {
  const fixture = { checks: { analyzing: true, findings: [finding(diag())] } };
  assert.match(await renderWithFixture(panel, fixture, { expanded: true, onnavigate() {} }), /role="separator" aria-label="Resize code check details"/);
  assert.doesNotMatch(await renderWithFixture(panel, fixture, { onnavigate() {} }), /role="separator"/);
  const read = name => readFileSync(new URL(`../src/lib/components/common/go-to-file/${name}`, import.meta.url), 'utf8').split('<style>')[1];
  assert.equal(read('ProblemsPanel.svelte').match(/overflow(-y)?: auto/g).length, 1);
  assert.doesNotMatch(read('CodeChecks.svelte'), /overflow(-y)?: auto/);
});
