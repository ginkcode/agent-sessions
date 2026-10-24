import test from 'node:test';
import assert from 'node:assert/strict';
import {
  refKey,
  refsEqual,
  nextSelectionAfterDelete,
  filterSessionsByAge,
  summarizePreview,
  errorText,
  isPermanentDelete,
  formatDeleteResultSummary,
  trashLabel,
  actionLabel,
  describeBlockedReason,
  groupBlockedReasons,
  deleteButtonLabel,
} from '../src/lib/manage.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { blockedReason } from '../src/lib/link.ts';
import { fixtureStore, loadServerComponent, renderWithFixture, renderedButtons } from './helpers.mjs';

test('manage helpers filter age, select next neighbour and summarize preview', () => {
  const a = { agent: 'claude-code', id: 'one' };
  const b = { agent: 'claude-code', id: 'two' };
  const c = { agent: 'codex', id: 'one' };
  assert.equal(refKey(a), 'claude-code:one');
  assert.equal(refKey(c), 'codex:one');
  assert.ok(refsEqual(a, { ...a }));
  assert.equal(refsEqual(a, c), false);
  const sessions = [
    { ref: a, updatedAt: '2026-09-01T00:00:00Z' },
    { ref: b, updatedAt: '2026-09-20T00:00:00Z' },
    { ref: c, updatedAt: '2026-09-27T00:00:00Z' },
  ];
  const now = new Date('2026-10-01T00:00:00Z');
  assert.deepEqual(filterSessionsByAge(sessions, 'within-7d', now).map(s => s.ref), [c]);
  assert.deepEqual(filterSessionsByAge(sessions, 'within-30d', now).map(s => s.ref), [a, b, c]);
  assert.deepEqual(filterSessionsByAge(sessions, 'older-30d', new Date('2026-10-05T00:00:00Z')).map(s => s.ref), [a]);
  assert.deepEqual(filterSessionsByAge(sessions, 'within-1d', new Date('2026-09-27T12:00:00Z')).map(s => s.ref), [c]);
  assert.deepEqual(filterSessionsByAge(sessions, 'within-1h', new Date('2026-09-27T00:30:00Z')).map(s => s.ref), [c]);
  assert.equal(filterSessionsByAge(sessions, 'any').length, 3);
  assert.deepEqual(nextSelectionAfterDelete(sessions, [b], b), c);
  assert.deepEqual(nextSelectionAfterDelete(sessions, [c], c), b);
  assert.deepEqual(nextSelectionAfterDelete(sessions, [a], b), b);
  assert.equal(nextSelectionAfterDelete(sessions, [a, b, c], b), null);
  const preview = {
    items: [
      { ref: a, agent: a.agent, title: 'one', paths: [], bytes: 10, reversible: true, action: 'trash' },
      { ref: b, agent: b.agent, title: 'two', paths: [], bytes: 20, reversible: false, warning: 'Permanent', action: 'delete' },
      { ref: c, agent: c.agent, title: 'three', paths: [], bytes: 30, reversible: false, warning: 'Live', blocked: 'session is live', action: 'delete' },
    ], totalBytes: 60, token: 'token',
  };
  assert.deepEqual(summarizePreview(preview), {
    total: 3, blockedCount: 1, reversibleCount: 1, permanentCount: 1,
    canProceed: true, warnings: ['Permanent'],
  });
  assert.equal(summarizePreview({ ...preview, items: [preview.items[2]] }).canProceed, false);
  assert.equal(isPermanentDelete({ ...preview, items: [preview.items[0], preview.items[2]] }), false);
  assert.equal(errorText('preview is stale; retry the operation', 'x'), 'preview is stale; retry the operation');
  assert.equal(errorText(new Error('boom'), 'x'), 'boom');
  assert.equal(errorText(undefined, 'x'), 'x');
  assert.equal(isPermanentDelete(preview), true);
  assert.equal(isPermanentDelete({ ...preview, items: [preview.items[0]] }), false);
  assert.equal(formatDeleteResultSummary({ items: [], deleted: 1, failed: 1, freedBytes: 10, forgotten: [a] }), '1 session removed, 1 failed, 1 forgotten');
});

function deletePreview(items, trashLabel) {
  const preview = { items, totalBytes: 0, token: 'token' };
  if (trashLabel !== undefined) preview.trashLabel = trashLabel;
  return preview;
}

function previewItem(id, agent, { reversible = agent === 'claude-code', blocked, action } = {}) {
  return {
    ref: { agent, id }, agent, title: id, paths: [], bytes: 1, reversible,
    action: action ?? (reversible ? 'trash' : 'delete'),
    ...(blocked ? { blocked } : {}),
  };
}

const RUNTIME_UNKNOWN = 'cannot verify whether the agent is running: a Node.js, Bun or Deno process may host an agent; close it before deleting sessions';

test('trashLabel follows the operating host and falls back to Trash', () => {
  const items = [previewItem('one', 'claude-code')];
  assert.equal(trashLabel(deletePreview(items, 'Recycle Bin')), 'Recycle Bin');
  assert.equal(trashLabel(deletePreview(items, 'Trash')), 'Trash');
  // Older servers omit the field; unknown values never invent a destination.
  assert.equal(trashLabel(deletePreview(items)), 'Trash');
  assert.equal(trashLabel(deletePreview(items, '')), 'Trash');
  assert.equal(trashLabel(deletePreview(items, 'Bin')), 'Trash');
  assert.equal(trashLabel(null), 'Trash');
  assert.equal(actionLabel('trash', 'Recycle Bin'), 'Recycle Bin');
  assert.equal(actionLabel('trash', 'Trash'), 'Trash');
  assert.equal(actionLabel('delete', 'Recycle Bin'), 'delete');
});

test('describeBlockedReason explains known reasons and keeps others raw', () => {
  assert.equal(
    describeBlockedReason('trash transport is unavailable on this platform', 'Recycle Bin'),
    'Moving sessions to Recycle Bin is not supported on this system.'
  );
  assert.equal(
    describeBlockedReason('trash is not supported on this platform', 'Trash'),
    'Moving sessions to Trash is not supported on this system.'
  );
  assert.equal(
    describeBlockedReason('manage: safe Recycle Bin support is unavailable', 'Recycle Bin'),
    'Moving sessions to Recycle Bin is not supported on this system.'
  );
  assert.match(
    describeBlockedReason("this drive's Recycle Bin is turned off", 'Recycle Bin'),
    /^The Recycle Bin is turned off for this drive .*deleted permanently\.$/
  );
  assert.match(
    describeBlockedReason("the session is larger than this drive's Recycle Bin", 'Recycle Bin'),
    /^The session is larger than this drive's Recycle Bin.*maximum size/
  );
  assert.match(
    describeBlockedReason("this drive's Recycle Bin settings could not be verified", 'Recycle Bin'),
    /could not be verified, so the session might be deleted permanently\.$/
  );
  assert.match(
    describeBlockedReason('path is not supported by the Recycle Bin transport', 'Recycle Bin'),
    /network or removable drive/
  );
  assert.match(
    describeBlockedReason('permanent deletion is not allowed; enable it in settings first', 'Trash'),
    /^Permanent deletion is turned off\. Enable "Allow permanent deletion" in Settings/
  );
  assert.equal(
    describeBlockedReason(RUNTIME_UNKNOWN, 'Recycle Bin'),
    'Cannot verify whether the agent is running. A Node.js, Bun or Deno process may host an agent; close it before deleting sessions.'
  );
  assert.equal(
    describeBlockedReason('cannot verify whether the agent is running: process snapshot failed', 'Trash'),
    'Cannot verify whether the agent is running. Process snapshot failed.'
  );
  assert.equal(
    describeBlockedReason('cannot verify whether the agent is running', 'Trash'),
    'Cannot verify whether the agent is running.'
  );
  // Older servers report unavailable process checks as a live session.
  assert.match(
    describeBlockedReason('session is live: process liveness unavailable', 'Trash'),
    /^Cannot verify whether the agent is running/
  );
  assert.equal(
    describeBlockedReason('session is live: agent process is running', 'Trash'),
    'The agent is running. Close it before deleting its sessions.'
  );
  assert.equal(
    describeBlockedReason('session is live: agent process is running: opencode-cli.exe', 'Recycle Bin'),
    'An agent process is running (opencode-cli.exe). Close it before deleting these sessions.'
  );
  assert.equal(
    describeBlockedReason('session is live: session was active within 10 minutes', 'Trash'),
    'The session was active within the last 10 minutes.'
  );
  assert.equal(
    describeBlockedReason('session is live: session was updated within 10 minutes', 'Trash'),
    'The session was updated within the last 10 minutes.'
  );
  assert.equal(
    describeBlockedReason('session is live: child session was active within 10 minutes', 'Trash'),
    'A child session was active within the last 10 minutes.'
  );
  assert.equal(
    describeBlockedReason('session is live: session was active within 10 minutes: child cannot be safely deleted', 'Recycle Bin'),
    'The session was active within the last 10 minutes. A child session cannot be safely deleted.'
  );
  assert.equal(
    describeBlockedReason('trash transport is unavailable on this platform: child cannot be safely deleted', 'Recycle Bin'),
    'Moving sessions to Recycle Bin is not supported on this system. A child session cannot be safely deleted.'
  );
  for (const raw of [
    'codex session has ambiguous rollout paths',
    'invalid subagent session ID format: child cannot be safely deleted',
    ': child cannot be safely deleted',
    'Cannot Verify whether the agent is running: case differs',
  ]) {
    assert.equal(describeBlockedReason(raw, 'Recycle Bin'), raw);
  }
});

test('groupBlockedReasons lists distinct readable reasons with counts', () => {
  const preview = deletePreview([
    previewItem('a', 'codex', { blocked: 'permanent deletion is not allowed; enable it in settings first' }),
    previewItem('b', 'claude-code', { blocked: 'trash transport is unavailable on this platform' }),
    previewItem('c', 'opencode', { blocked: 'permanent deletion is not allowed; enable it in settings first' }),
    previewItem('d', 'claude-code'),
    previewItem('e', 'claude-code', { blocked: RUNTIME_UNKNOWN }),
    previewItem('f', 'claude-code', { blocked: 'some new backend reason' }),
  ], 'Recycle Bin');
  const groups = groupBlockedReasons(preview);
  assert.deepEqual(groups.map(g => g.count), [2, 1, 1, 1]);
  assert.match(groups[0].reason, /^Permanent deletion is turned off/);
  assert.equal(groups[1].reason, 'Moving sessions to Recycle Bin is not supported on this system.');
  assert.match(groups[2].reason, /Node\.js, Bun or Deno/);
  assert.equal(groups[3].reason, 'some new backend reason');
  assert.deepEqual(groupBlockedReasons(deletePreview([previewItem('d', 'claude-code')])), []);
  assert.deepEqual(groupBlockedReasons(null), []);
});

test('deleteButtonLabel never promises a destination when nothing can proceed', () => {
  const claude = previewItem('a', 'claude-code');
  const codex = previewItem('b', 'codex');
  const blockedClaude = previewItem('c', 'claude-code', { blocked: 'trash transport is unavailable on this platform' });
  const blockedCodex = previewItem('d', 'codex', { blocked: 'permanent deletion is not allowed; enable it in settings first' });

  // All blocked: neutral label, whatever the host or remote capability.
  for (const label of ['Recycle Bin', 'Trash', undefined]) {
    const allBlocked = deletePreview([blockedClaude, blockedCodex], label);
    assert.equal(summarizePreview(allBlocked).canProceed, false);
    assert.equal(deleteButtonLabel(allBlocked), 'Delete');
    assert.equal(deleteButtonLabel(allBlocked, { trashUnavailable: true }), 'Delete');
  }
  assert.equal(deleteButtonLabel(null), 'Delete');
  assert.equal(deleteButtonLabel(deletePreview([])), 'Delete');

  // Reversible only: destination follows the host, with Trash fallback.
  assert.equal(deleteButtonLabel(deletePreview([claude], 'Recycle Bin')), 'Move to Recycle Bin');
  assert.equal(deleteButtonLabel(deletePreview([claude, blockedCodex], 'Recycle Bin')), 'Move to Recycle Bin');
  assert.equal(deleteButtonLabel(deletePreview([claude], 'Trash')), 'Move to Trash');
  assert.equal(deleteButtonLabel(deletePreview([claude])), 'Move to Trash');

  // Permanent or mixed actionable selections are always permanent.
  assert.equal(deleteButtonLabel(deletePreview([codex], 'Recycle Bin')), 'Delete Permanently');
  assert.equal(deleteButtonLabel(deletePreview([claude, codex], 'Recycle Bin')), 'Delete Permanently');
  assert.equal(deleteButtonLabel(deletePreview([codex, blockedClaude], 'Trash')), 'Delete Permanently');
  assert.equal(isPermanentDelete(deletePreview([claude, blockedCodex])), false);

  // A remote Linux host without trash support deletes permanently; one with
  // trash support still keeps Codex/OpenCode permanent.
  assert.equal(deleteButtonLabel(deletePreview([claude], 'Trash'), { trashUnavailable: true }), 'Delete Permanently');
  assert.equal(deleteButtonLabel(deletePreview([claude], 'Trash'), { trashUnavailable: false }), 'Move to Trash');
  assert.equal(deleteButtonLabel(deletePreview([codex], 'Trash'), { trashUnavailable: false }), 'Delete Permanently');
  // Disabled by the remote permanent-delete policy: no permanent promise.
  assert.equal(deleteButtonLabel(deletePreview([claude], 'Trash'), { trashUnavailable: true, blockedByPolicy: true }), 'Delete');

  assert.equal(deleteButtonLabel(deletePreview([claude], 'Recycle Bin'), { deleting: true }), 'Deleting…');
});

const DELETE_DIALOG_URL = new URL('../src/lib/components/common/DeleteConfirmDialog.svelte', import.meta.url);

let deleteDialogModule;

// Server-renders the real dialog with plain store fixtures.
async function renderDeleteDialog(fixture) {
  deleteDialogModule ??= await loadServerComponent(DELETE_DIALOG_URL, [
    ["'../../manage'", JSON.stringify(new URL('../../manage.ts', DELETE_DIALOG_URL).href)],
    ["'../../format'", JSON.stringify(new URL('../../format.ts', DELETE_DIALOG_URL).href)],
    ["'../../modalFocus'", JSON.stringify(new URL('../../modalFocus.ts', DELETE_DIALOG_URL).href)],
    ["'../../hosts'", JSON.stringify(new URL('../../hosts.ts', DELETE_DIALOG_URL).href)],
    ["import { manage } from '../../stores/manage.svelte';", fixtureStore('manage')],
    ["import { connectionStore } from '../../stores/connection.svelte';", fixtureStore('connectionStore')],
    ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
  ]);
  return renderWithFixture(deleteDialogModule, fixture, { open: true, onClose() {} });
}

function deleteDialogFixture({ preview = null, lastResult = null, allowPermanentDelete = false, canTrash = true, dataHost } = {}) {
  const calls = [];
  return {
    calls,
    manage: {
      preview, lastResult, previewLoading: false, previewError: null, deleting: false,
      deleteError: null, deleteOutcomeUnknown: false, settings: { enabled: true, allowPermanentDelete },
      dismissDialog: () => calls.push('dismiss'),
      executeDelete: async () => { calls.push('execute'); return true; },
    },
    connectionStore: { canTrash },
    link: { dataHost, stale: false, blockedReason: null },
  };
}

test('delete dialog renders uncertain Recycle Bin results separately and only offers Done', async () => {
  const uncertain = 'C:\\Users\\tester\\.claude\\projects\\demo\\uncertain.jsonl';
  const remaining = 'C:\\Users\\tester\\.claude\\projects\\demo\\uncertain';
  const fixture = deleteDialogFixture({
    lastResult: {
      items: [{
        ref: { agent: 'claude-code', id: 'synthetic' }, title: 'Synthetic session', ok: false,
        error: 'Recycle Bin outcome is unknown; inspect the original location and Recycle Bin before retrying',
        moved: [], remaining: [remaining], unknown: [uncertain],
      }],
      deleted: 0, failed: 1, freedBytes: 0, forgotten: null,
    },
  });
  const html = await renderDeleteDialog(fixture);

  assert.match(html, /outcome unknown/);
  assert.doesNotMatch(html, />failed</);
  assert.match(html, /Recycle Bin outcome is unknown; inspect the original location and Recycle Bin before retrying/);
  const notRemoved = html.indexOf('Not removed:');
  const unverified = html.indexOf('Outcome could not be verified for:');
  assert.ok(notRemoved >= 0 && unverified > notRemoved);
  assert.ok(html.indexOf(remaining) > notRemoved && html.indexOf(remaining) < unverified);
  assert.ok(html.indexOf(uncertain) > unverified);
  assert.equal(html.split(uncertain).length - 1, 2, 'unknown path appears once as text and once as its title');
  const buttons = renderedButtons(html);
  assert.equal(buttons.length, 2);
  assert.match(html, /aria-label="Close dialog"/);
  assert.match(buttons[0].text, /<svg/);
  assert.equal(buttons[1].text, 'Done');
  assert.doesNotMatch(html, /Retry|Cancel|Move to|Delete Permanently/);
  assert.deepEqual(fixture.calls, []);
});

test('delete dialog shows the notes of a finished delete', async () => {
  const note = 'Claude Code prompt history still lists the deleted sessions: Claude Code is running or could not be checked.';
  const html = await renderDeleteDialog(deleteDialogFixture({
    lastResult: {
      items: [{ ref: { agent: 'claude-code', id: 'synthetic' }, title: 'Synthetic session', ok: true }],
      deleted: 1, failed: 0, freedBytes: 10, forgotten: null, notes: [note],
    },
  }));
  assert.ok(html.includes(note));
});

test('delete dialog uses the backend Recycle Bin label for previews', async () => {
  const preview = deletePreview([{ ...previewItem('synthetic', 'claude-code'), paths: ['C:\\Users\\tester\\.claude\\projects\\demo\\synthetic.jsonl'] }], 'Recycle Bin');
  const html = await renderDeleteDialog(deleteDialogFixture({ preview }));
  assert.match(html, /Selected sessions move to Recycle Bin\. You can restore them from\s+Recycle Bin\./);
  assert.match(html, /Confirm to move the selected\s+session to\s+Recycle Bin\./);
  assert.deepEqual(renderedButtons(html).at(-1), { text: 'Move to Recycle Bin', disabled: false });
  assert.doesNotMatch(html, /Trash/);
});

test('delete dialog keeps disabled remote policy and all-blocked actions neutral', async () => {
  const remotePolicy = await renderDeleteDialog(deleteDialogFixture({
    preview: deletePreview([previewItem('synthetic', 'claude-code')], 'Trash'),
    dataHost: 'linux-host', canTrash: false, allowPermanentDelete: false,
  }));
  assert.match(remotePolicy, /Trash is not supported on linux-host\./);
  assert.match(remotePolicy, /permanent deletion is disabled in Settings/);
  assert.deepEqual(renderedButtons(remotePolicy).at(-1), { text: 'Delete', disabled: true });

  const remoteAllowed = await renderDeleteDialog(deleteDialogFixture({
    preview: deletePreview([previewItem('synthetic', 'claude-code')], 'Trash'),
    dataHost: 'linux-host', canTrash: false, allowPermanentDelete: true,
  }));
  assert.deepEqual(renderedButtons(remoteAllowed).at(-1), { text: 'Delete Permanently', disabled: false });

  // A WSL distribution is named by its label, never the raw wsl: host.
  const wsl = await renderDeleteDialog(deleteDialogFixture({
    preview: deletePreview([previewItem('synthetic', 'claude-code')], 'Trash'),
    dataHost: 'wsl:Ubuntu', canTrash: false, allowPermanentDelete: false,
  }));
  assert.match(wsl, /<span class="host-pill[^"]*">Ubuntu \(WSL\)<\/span>/);
  assert.match(wsl, /Trash is not supported on Ubuntu \(WSL\)\./);
  assert.doesNotMatch(wsl, /wsl:Ubuntu/);

  const allBlocked = await renderDeleteDialog(deleteDialogFixture({
    preview: deletePreview([previewItem('synthetic', 'claude-code', { blocked: RUNTIME_UNKNOWN })], 'Recycle Bin'),
  }));
  assert.match(allBlocked, /Cannot verify whether the agent is running\. A Node\.js, Bun or Deno process may host an agent/);
  assert.deepEqual(renderedButtons(allBlocked).at(-1), { text: 'Delete', disabled: true });
  assert.doesNotMatch(allBlocked, /Confirm to move|Move to Recycle Bin/);
});

test('mock settings gate deletion and preview tokens bind to selection', async () => {
  const backend = new MockBackendAPI();
  const ref = { agent: 'claude-code', id: 'session-claude-2' };
  assert.deepEqual(await backend.getSettings(), { enabled: false, allowPermanentDelete: false, prunePromptHistory: false });
  await assert.rejects(() => backend.previewDelete([ref]), /disabled/);
  await assert.rejects(() => backend.setAllowPermanentDelete(true), /enabled first/);
  await backend.setManageEnabled(true);
  const preview = await backend.previewDelete([ref]);
  assert.equal(preview.items[0].blocked, undefined);
  assert.equal(preview.items[0].reversible, true);
  assert.ok(preview.totalBytes > 0);
  await assert.rejects(() => backend.deleteSessions([ref], 'wrong'), /token/);
  await assert.rejects(() => backend.deleteSessions([{ agent: 'codex', id: 'other' }], preview.token), /changed/);
  const result = await backend.deleteSessions([ref], preview.token);
  assert.equal(result.deleted, 1);
  assert.equal(result.failed, 0);
  assert.deepEqual(result.forgotten, [ref]);
  assert.ok(result.freedBytes > 0);
  assert.equal(result.items[0].moved.length, 1);
  assert.equal((await backend.listSessions('', {})).some(s => refsEqual(s.ref, ref)), false);
  await assert.rejects(() => backend.getSessionMeta(ref), /unknown session/);
  await assert.rejects(() => backend.deleteSessions([ref], preview.token), /token/);
  assert.deepEqual(await backend.setManageEnabled(false), { enabled: false, allowPermanentDelete: false, prunePromptHistory: false });
  assert.equal((await backend.setPrunePromptHistory(true)).prunePromptHistory, true);
});

test('mock preview blocks live sessions and excludes them from the token', async () => {
  const backend = new MockBackendAPI();
  await backend.setManageEnabled(true);
  const ref = { agent: 'claude-code', id: 'session-claude-1' };
  const preview = await backend.previewDelete([ref]);
  assert.match(preview.items[0].blocked, /live/);
  await assert.rejects(() => backend.deleteSessions([ref], preview.token), /changed/);
});
