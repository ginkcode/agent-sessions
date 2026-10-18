import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { BUDGET_PRESETS, targetAgentsFor } from '../src/lib/portable.ts';
import { renderHandoffDialog } from './helpers.mjs';

test('MockBackendAPI copyResumeCommand formats provider-specific resume commands', async () => {
  const mock = new MockBackendAPI();
  const claudeCmd = await mock.copyResumeCommand({ agent: 'claude-code', id: 'session-claude-1' });
  assert.match(claudeCmd, /claude --resume 'session-claude-1'/);

  const codexCmd = await mock.copyResumeCommand({ agent: 'codex', id: '01a0e61e-e703-75a2-bc1d-6349e32f6dd4' });
  assert.match(codexCmd, /codex resume '01a0e61e-e703-75a2-bc1d-6349e32f6dd4'/);

  const opencodeCmd = await mock.copyResumeCommand({ agent: 'opencode', id: 'opencode-sess-101' });
  assert.match(opencodeCmd, /opencode --session 'opencode-sess-101'/);
});

test('MockBackendAPI buildHandoff, handoffCommand, and saveHandoff produce deliverables', async () => {
  const mock = new MockBackendAPI();
  const req = {
    ref: { agent: 'claude-code', id: 'session-claude-1' },
    target: 'codex',
    budget: 80000,
  };

  const preview = await mock.buildHandoff(req);
  assert.ok(preview.promptMarkdown.includes('Handoff to codex'));
  assert.ok(preview.fullMarkdown.includes('Timeline'));
  assert.ok(preview.command.endsWith(`&& codex 'Read ${preview.promptFile} completely to restore the context of an earlier session, then follow its instructions and wait for my next request.'`));
  assert.equal(preview.report.estimatedTokens, 1250);

  const cmd = await mock.handoffCommand(req);
  assert.match(cmd, /codex 'Read \/tmp\/handoffs\/session-claude-1-handoff\.md completely/);

  const path = await mock.saveHandoff(req);
  assert.match(path, /session-claude-1-handoff\.md$/);

  // The written handoff files show up in the cache and can be cleared.
  assert.equal((await mock.handoffCache()).files, 2);
  assert.equal((await mock.clearHandoffCache()).files, 0);
});

test('portable helpers define budget presets and target agent options', () => {
  assert.equal(BUDGET_PRESETS.length, 4);
  const ids = BUDGET_PRESETS.map((b) => b.id);
  assert.deepEqual(ids, ['compact', 'detailed', 'full', 'unlimited']);

  const targets = targetAgentsFor('claude-code');
  assert.deepEqual(targets.map((t) => t.id), ['codex', 'opencode']);

  const allTargets = targetAgentsFor();
  assert.equal(allTargets.length, 3);
});

test('MockBackendAPI previewExport and exportBundle support complete and share-safe profiles', async () => {
  const mock = new MockBackendAPI();
  const ref = { agent: 'claude-code', id: 'session-claude-1' };

  // Complete profile (default): warning present, native files included
  const completePreview = await mock.previewExport({
    ref,
    profile: 'complete',
    budget: 80000,
    includeReasoning: false,
    redactSecrets: false,
  });
  assert.equal(completePreview.profile, 'complete');
  assert.ok(completePreview.nativeFiles > 0);
  assert.ok(completePreview.nativeBytes > 0);
  assert.ok(completePreview.warning && completePreview.warning.length > 0);
  assert.equal(completePreview.redaction?.token ?? 0, 0);

  // Complete profile with redaction enabled
  const completeRedacted = await mock.previewExport({
    ref,
    profile: 'complete',
    budget: 80000,
    includeReasoning: false,
    redactSecrets: true,
  });
  assert.ok(completeRedacted.redaction);
  assert.ok((completeRedacted.redaction.token ?? 0) > 0);

  // Share-safe profile: forces redaction, drops native files and warning
  const shareSafePreview = await mock.previewExport({
    ref,
    profile: 'share-safe',
    budget: 80000,
    includeReasoning: false,
    redactSecrets: false,
  });
  assert.equal(shareSafePreview.profile, 'share-safe');
  assert.equal(shareSafePreview.nativeFiles, 0);
  assert.equal(shareSafePreview.nativeBytes, 0);
  assert.equal(shareSafePreview.warning, undefined);
  assert.ok(shareSafePreview.redaction);
  assert.ok((shareSafePreview.redaction.token ?? 0) > 0);

  // exportBundle returns zip path
  const exportPath = await mock.exportBundle({
    ref,
    profile: 'complete',
    budget: 80000,
    includeReasoning: false,
    redactSecrets: false,
  });
  assert.match(exportPath, /\/claude-code_[^_/]+_session\.agent-session\.zip$/);
});

test('MockBackendAPI openBundle, buildBundleHandoff, and saveBundleHandoff', async () => {
  const mock = new MockBackendAPI();
  const summary = await mock.openBundle();
  assert.ok(summary);
  assert.equal(summary.bundleId, 'mock-bundle-1234');
  assert.equal(summary.profile, 'complete');
  assert.equal(summary.verified, true);
  assert.equal(summary.restoreAvailable, true);
  assert.equal(summary.handoffAvailable, true);
  assert.equal(summary.sessionsCount, 1);
  assert.equal(summary.sessions[0].title, 'Imported Mock Session');

  const preview = await mock.buildBundleHandoff({
    bundleId: summary.bundleId,
    target: 'codex',
    budget: 80000,
  });
  assert.ok(preview.promptMarkdown.includes('Handoff to codex'));
  assert.ok(preview.promptMarkdown.includes('Imported Mock Session'));
  assert.match(preview.command, /codex 'Read \/tmp\/handoffs\/bundle-mock-bundle-1234-handoff\.md completely/);

  const cmd = await mock.bundleHandoffCommand({
    bundleId: summary.bundleId,
    target: 'opencode',
  });
  assert.match(cmd, /opencode --prompt 'Read /);

  const savePath = await mock.saveBundleHandoff({
    bundleId: summary.bundleId,
    target: 'claude-code',
  });
  assert.match(savePath, /bundle-mock-bundle-1234-handoff\.md$/);
});

test('HandoffDialog offers Open in Terminal when the session can open locally', async () => {
  const open = await renderHandoffDialog({ launcher: { canOpen: true } });
  assert.match(open, /class="btn primary-btn[^"]*"[^>]*>\s*Open in Terminal/);
  assert.match(open, /class="btn secondary-btn[^"]*"[^>]*>\s*Copy Launch Command/);

  const busy = await renderHandoffDialog({ launcher: { canOpen: true }, handoff: { launching: true } });
  assert.match(busy, /primary-btn[^"]*"[^>]*disabled[^>]*>\s*Opening…/);

  const copyOnly = await renderHandoffDialog({ launcher: { canOpen: false } });
  assert.doesNotMatch(copyOnly, /Open in Terminal/);
  assert.match(copyOnly, /class="btn primary-btn[^"]*"[^>]*>\s*Copy Launch Command/);
});
