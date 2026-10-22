import test from 'node:test';
import assert from 'node:assert/strict';
import { fixtureStore, loadServerComponent, renderWithFixture, renderedButtons } from './helpers.mjs';

const component = await loadServerComponent(new URL('../src/lib/components/common/go-to-file/CodeChecks.svelte', import.meta.url), [
  ["import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';", fixtureStore('checks')],
]);
const executionButtons = html => renderedButtons(html).filter(b => b.text !== 'Refresh setup status');
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
  const live = await render({ liveEnabled: true, approved: false, snapshot: { approval: { state: 'required' }, proposal: { fingerprint: 'fp', tools: [] } } });
  assert.match(live, /opened Go files are also sent unsaved to the listed gopls/);
  assert.match(live, /drafts are never saved/);
  assert.match(live, /Live diagnostics \(experimental\)/);
  assert.match(live, /TypeScript and Svelte live diagnostics are not enabled/);
  assert.doesNotMatch(live, /Only saved files are checked/);
  const noProposal = await render({ approved: false, snapshot: { approval: { state: 'required' } } });
  assert.deepEqual(executionButtons(noProposal).map(b => b.disabled), [true, true, false]);
});

test('approved check/cancel/retry rules and prompt locks reflect store selectors', async () => {
  assert.deepEqual(executionButtons(await render()).map(b => b.text), ['Check Project', 'Revoke approval']);
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
