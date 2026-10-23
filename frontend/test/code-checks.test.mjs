import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerComponent, renderWithFixture, renderedButtons } from './helpers.mjs';

const component = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/CodeChecks.svelte', import.meta.url), [
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
]);
// Setup status, rediscovery and status refresh read or discover only; they never approve or execute.
const executionButtons = html => renderedButtons(html).filter(b => !['Refresh setup status', 'Rediscover checkers', 'Refresh check status'].includes(b.text));
const provider = { providerId: 'go', availability: 'available', phase: 'idle', freshness: 'current', coverage: { kind: 'savedProject', roots: [''], externalChanges: 'unwatched', partial: false }, truncated: false };
function render(overrides = {}, props = {}) {
  return renderWithFixture(component, { checks: { status: 'ready', project: { hostId: 'local:actual', root: '/canonical/<root>' }, snapshot: { approval: { state: 'approved', mode: 'session' } }, providers: [provider], approved: true, canRun: true, canRetry: () => true, counts: { error: 0, warning: 0 }, ...overrides } }, props);
}

test('every attachment state remains textual and non-actionable until ready', async () => {
  for (const [status, text] of Object.entries({ detached: 'disabled', attaching: 'Discovering', unsupported: 'unsupported', resyncing: 'Reconciling', disconnected: 'disconnected', error: 'uncertain' })) {
    const html = await render({ status, project: undefined, snapshot: undefined, approved: false, providers: [] });
    assert.match(html, new RegExp(text));
    assert.equal(executionButtons(html).length, 0);
  }
});

test('approval choices expose canonical scope, tools and non-sandbox risk without run', async () => {
  const html = await render({ approved: false, snapshot: { approval: { state: 'revoked' }, proposal: { fingerprint: 'fp', tools: [{ providerId: 'go', executable: '/bin/go', configPaths: ['go.work'] }] } } });
  assert.deepEqual(executionButtons(html).map(b => b.text), ['Enable for this session', 'Always allow this project', 'Not now']);
  for (const text of ['local:actual', '/canonical/&lt;root>', '/bin/go', 'go.work', 'network', 'caches', 'not a sandbox', 'drafts are never saved']) assert.ok(html.includes(text));
  assert.doesNotMatch(html, /Check Project/);
  assert.match(html, /Only saved files are checked/);
  assert.match(html, /Live diagnostics are not available on this connection/);
  const lsp = id => ({ ...provider, providerId: id, availability: 'approvalRequired', coverage: { ...provider.coverage, kind: 'openedDocuments' } });
  const live = await render({ liveEnabled: true, approved: false, providers: [provider, lsp('gopls'), lsp('typescript')], snapshot: { approval: { state: 'required' }, proposal: { fingerprint: 'fp', tools: [] } } });
  assert.match(live, /opened Go and TypeScript\/JavaScript files are also sent unsaved to the listed language servers/);
  assert.match(live, /drafts are never saved/);
  assert.match(live, /Live diagnostics \(experimental\)/);
  assert.match(live, /vtsls for TypeScript\/JavaScript/);
  assert.match(live, /Ruff plus Pyright for Python \(lint\/syntax and type errors, respectively\)/);
  assert.match(live, /Svelte live diagnostics are not enabled/);
  const noLive = await render({ liveEnabled: true, approved: false, snapshot: { approval: { state: 'required' }, proposal: { fingerprint: 'fp', tools: [] } } });
  assert.match(noLive, /Only saved files are checked/, 'no live claim without a live provider');
  assert.doesNotMatch(live, /Only saved files are checked/);
  const noProposal = await render({ approved: false, snapshot: { approval: { state: 'required' } } });
  assert.deepEqual(executionButtons(noProposal).map(b => b.disabled), [true, true, false]);
  // Nothing approvable: the card offers discovery again (also under Tools setup).
  assert.equal(renderedButtons(noProposal).filter(b => b.text === 'Rediscover checkers').length, 2);
  assert.equal(renderedButtons(html).filter(b => b.text === 'Rediscover checkers').length, 1);
});

test('approved check/cancel/retry rules and prompt locks reflect store selectors', async () => {
  assert.deepEqual(executionButtons(await render()).map(b => b.text), ['Check Project', 'Revoke approval']);
  // Fixes made outside the app are picked up by re-reading open files.
  const refresh = renderedButtons(await render()).filter(b => b.text === 'Refresh check status');
  assert.deepEqual(refresh.map(b => b.disabled), [false]);
  assert.equal(renderedButtons(await render({ pending: 'run' })).find(b => b.text === 'Refresh check status').disabled, true);
  const busy = executionButtons(await render({ running: true, canRun: false }));
  assert.equal(busy.find(b => b.text === 'Check Project').disabled, true);
  assert.equal(busy.find(b => b.text === 'Cancel check').disabled, false);
  const failed = await render({ providers: [{ ...provider, phase: 'failed', failure: { message: '<failed>' } }] });
  assert.ok(executionButtons(failed).some(b => b.text === 'Retry go'));
  assert.match(failed, /&lt;failed>/);
  assert.ok(executionButtons(await render({}, { blocked: true })).every(b => b.disabled));
  const revoking = executionButtons(await render({ approved: false, revoking: true }));
  assert.deepEqual(revoking.map(b => b.text), ['Revoke approval']);
  assert.equal(revoking[0].disabled, true);
});

test('settings never promise full coverage; provider availability and freshness stay independent', async () => {
  for (const availability of ['approvalRequired', 'available', 'missingTool', 'unsupported', 'disabled']) {
    const html = await render({ partial: true, truncated: true, providers: [{ ...provider, availability, phase: 'canceled', freshness: 'stale', coverage: { ...provider.coverage, partial: true } }] });
    for (const text of ['partial coverage', 'truncated', 'stale', 'unopened-file coverage stale', 'Check canceled', 'no complete-coverage claim']) assert.ok(html.includes(text), text);
    assert.doesNotMatch(html, /all clean|No problems reported/i);
    if (availability !== 'available') assert.doesNotMatch(html, />Check Project</);
  }
});

test('settings keep raw host identity and proposal hash inside a collapsed technical disclosure', async () => {
  const html = await render({ approved: false, snapshot: { approval: { state: 'required' }, proposal: { fingerprint: 'sha256:abc', tools: [] } } });
  const technical = html.slice(html.indexOf('Technical details'));
  assert.ok(technical.includes('local:actual') && technical.includes('sha256:abc'));
  assert.doesNotMatch(html.slice(0, html.indexOf('Technical details')), /local:actual|sha256:abc/);
  assert.doesNotMatch(html, /<details[^>]*open[^>]*>\s*<summary[^>]*>Technical/);
});

test('approval card puts the buttons under one risk line; the explanation and tools stay collapsed', async () => {
  const workspace = n => `workspace:${'a'.repeat(64)}:${String(n).repeat(64)}`;
  const tools = [
    { providerId: 'typescript', executable: '/managed/vtsls.js', configPaths: ['tsconfig.json'] },
    { providerId: workspace(1), executable: '/node/bin/node', configPaths: ['frontend/package.json'] },
    { providerId: workspace(2), executable: '/node/bin/node', configPaths: ['site/package.json'] },
    { providerId: workspace(3), executable: '/managed/vtsls.js', configPaths: [] },
  ];
  const html = await render({ approved: false, snapshot: { approval: { state: 'required' }, proposal: { fingerprint: 'fp', tools } } });
  const card = html.slice(html.indexOf('Enable code checks for this project?'));
  const buttons = card.indexOf('>Enable for this session<');
  assert.match(card.slice(0, buttons), /Runs 2 tools on this host with your account's permissions\. This is not a sandbox/);
  for (const text of ['Project configuration and plugins can execute code', 'Session approval lasts', '/node/bin/node']) {
    assert.ok(card.indexOf(text) > buttons, `${text} follows the buttons`);
  }
  assert.match(card, /<details[^>]*>\s*<summary[^>]*>What will run and what approval means<\/summary>/);
  assert.doesNotMatch(html, /<details[^>]*open/);
  // One row per executable, named; hashed workspace IDs are not shown.
  assert.doesNotMatch(html, /workspace:a{64}/);
  assert.equal((card.match(/\/node\/bin\/node ·/g) ?? []).length, 1);
  assert.match(card, /<span class="name[^"]*"[^>]*>node<\/span>(<!--[^>]*-->)?<span class="pill[^"]*">2 workspaces<\/span>/);
  assert.match(card, /<span class="name[^"]*"[^>]*>TypeScript\/JavaScript language server \(vtsls\)<\/span>/);
  assert.match(card, /frontend\/package\.json, site\/package\.json/);
});

test('checker list collapses to one line of status counts, and opens itself only for an install or a failure', async () => {
  const ready = providerId => ({ ...provider, providerId });
  const quiet = await render({ providers: [ready('go-syntax'), ready('gopls'), { ...ready('ruff'), availability: 'unsupported', notice: 'No matching language project was found in this workspace.' }], managedInstalls: {}, partial: true });
  assert.match(quiet, /<details class="checkers[^"]*"><summary[^>]*><span class="heading[^"]*">Checkers<\/span>(<!--[^>]*-->)*<span class="pill tone-ok[^"]*">2 ready<\/span><span class="pill tone-warn[^"]*">1 unsupported<\/span>(<!--[^>]*-->)*<span class="muted[^"]*">partial coverage<\/span>/);
  assert.ok(quiet.indexOf('No matching language project') > quiet.indexOf('</summary>', quiet.indexOf('class="checkers')), 'notes sit inside the collapsed list');
  const failed = await render({ providers: [{ ...ready('go-vet'), phase: 'failed', failure: { message: 'vet crashed' } }] });
  assert.match(failed, /<details class="checkers[^"]*" open/);
});
