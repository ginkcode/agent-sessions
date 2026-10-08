import test from 'node:test';
import assert from 'node:assert/strict';
import { formatTokens, formatCost, formatBytes, formatVersion } from '../src/lib/format.ts';
import { isThemeMode, nextThemeMode, resolveTheme, themeButtonTitle } from '../src/lib/theme.ts';
import {
  copiedGuidance,
  copyFailed,
  openedGuidance,
  openFailed,
  connectionBanner,
  connectionErrorDetail,
  CONNECTION_LOST,
} from '../src/lib/guidance.ts';
import { formatRelativeTime, formatAbsoluteTime, isKnownTime, formatAgo, isTimeZoneMode } from '../src/lib/date.ts';
import { highlightCode, detectLanguage } from '../src/lib/highlight.ts';
import { renderMarkdown } from '../src/lib/markdown.ts';
import { reachesLineCount } from '../src/lib/layout.ts';
import { isTranscriptMode, messageMarkdown, messageText, visibleTranscriptParts } from '../src/lib/transcript.ts';
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
import { MockBackendAPI, highlightedSnippet } from '../src/lib/mock/mockApi.ts';
import { canOpenTerminal, terminalOptions } from '../src/lib/terminal.ts';
import { hostLabel, shellCommand, wslDistro } from '../src/lib/hosts.ts';
import {
  StaleReplyError,
  blockedReason,
  filterHosts,
  guardEpoch,
  isDisconnectedError,
  isLocked,
  isStale,
  isStaleReply,
  nextLink,
  resumeButtonTitle,
} from '../src/lib/link.ts';
import {
  LatestRequestGate,
  jumpOffset,
  progressIncomplete,
  safeSnippetHTML,
  searchFilterFromApp,
  searchTerms,
} from '../src/lib/search.ts';
import {
  DEFAULT_COLLAPSE_THRESHOLD,
  defaultCollapsedKeys,
  isCollapsed,
  nodeKind,
  pruneKeys,
  sessionToSelect,
  hashHost,
  targetKey,
  storageKeyForCollapsed,
  BASE_COLLAPSED_STORAGE_KEY,
  loadCollapsedKeys,
  saveCollapsedKeys,
} from '../src/lib/tree.ts';
import {
  affectsSessionList,
  findGroup,
  groupSessionKeys,
  hasRef,
  isFullRefresh,
  RequestSequence,
} from '../src/lib/catalog.ts';
import {
  createAPI,
  subscribeCatalogChanged,
  subscribeIndexProgress,
  subscribeConnectionState,
  subscribeAskpassPrompt,
} from '../src/lib/api.ts';
import { BUDGET_PRESETS, ALL_AGENTS, targetAgentsFor } from '../src/lib/portable.ts';

test('formatTokens formats numbers into compact string representations', () => {
  assert.equal(formatTokens(0), '0');
  assert.equal(formatTokens(950), '950');
  assert.equal(formatTokens(1000), '1k');
  assert.equal(formatTokens(1500), '1.5k');
  assert.equal(formatTokens(128400), '128.4k');
  assert.equal(formatTokens(1000000), '1M');
  assert.equal(formatTokens(2500000), '2.5M');
});

test('formatCost formats USD cost accurately', () => {
  assert.equal(formatCost(0), '$0.00');
  assert.equal(formatCost(0.423), '$0.42');
  assert.equal(formatCost(0.004), '$0.004');
  assert.equal(formatCost(12.5), '$12.50');
});

test('formatBytes formats byte sizes into readable units', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(1024), '1.0 KB');
  assert.equal(formatBytes(1048576), '1.0 MB');
});

test('date formatting provides accurate relative and absolute strings', () => {
  const now = new Date();
  const fiveMinAgo = new Date(now.getTime() - 5 * 60 * 1000).toISOString();
  assert.equal(formatRelativeTime(fiveMinAgo), '5m');

  const twoHoursAgo = new Date(now.getTime() - 2 * 3600 * 1000).toISOString();
  assert.equal(formatRelativeTime(twoHoursAgo), '2h');

  const abs = formatAbsoluteTime('2026-09-28T14:30:00Z');
  assert.ok(abs.includes('2026'));
  assert.ok(abs.includes('UTC'));
});

test('formatAbsoluteTime shows UTC or this computer\'s local time with its offset', () => {
  const saved = process.env.TZ;
  try {
    process.env.TZ = 'Asia/Ho_Chi_Minh';
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z'), 'Mon 2026-09-28 20:30:05 UTC');
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z', 'utc'), 'Mon 2026-09-28 20:30:05 UTC');
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z', 'local'), 'Tue 2026-09-29 03:30:05 +07:00');
    process.env.TZ = 'Pacific/Marquesas';
    assert.equal(formatAbsoluteTime('2026-09-28T20:30:05Z', 'local'), 'Mon 2026-09-28 11:00:05 -09:30');
    assert.equal(formatAbsoluteTime('', 'local'), '');
  } finally {
    if (saved === undefined) delete process.env.TZ;
    else process.env.TZ = saved;
  }
  assert.ok(isTimeZoneMode('utc') && isTimeZoneMode('local'));
  assert.ok(!isTimeZoneMode('UTC') && !isTimeZoneMode(null));
});

test('detectLanguage detects formats from content', () => {
  assert.equal(detectLanguage('{"key": "value"}'), 'json');
  assert.equal(detectLanguage('diff --git a/foo b/foo'), 'diff');
  assert.equal(detectLanguage('package main\nfunc main() {}'), 'go');
  assert.equal(detectLanguage('import { foo } from \'bar\''), 'typescript');
  assert.equal(detectLanguage('def hello():\n  pass'), 'python');
});

test('highlightCode highlights JSON, diff, and Go safely', () => {
  const json = highlightCode('{"hello": 123}', 'json');
  assert.ok(json.includes('hljs-attr'));
  assert.ok(json.includes('hljs-number'));

  const diff = highlightCode('+ added\n- deleted\n@@ -1,3 +1,3 @@', 'diff');
  assert.ok(diff.includes('hljs-addition'));
  assert.ok(diff.includes('hljs-deletion'));
  assert.ok(diff.includes('hljs-meta'));

  const go = highlightCode('func Run() error { return nil }', 'go');
  assert.ok(go.includes('hljs-keyword'));
  assert.ok(go.includes('hljs-title'));
});

test('highlightCode generic scanner escapes markup and keeps token boundaries', () => {
  const ts = highlightCode('if (a <= 0x1F && s === "<b>") { x = 1.5e3; } // it\'s & done', 'typescript');
  assert.equal(
    ts,
    '<span class="hljs-keyword">if</span> (a &lt;= <span class="hljs-number">0x1F</span> &amp;&amp; s === ' +
      '<span class="hljs-string">&quot;&lt;b&gt;&quot;</span>) { x = <span class="hljs-number">1.5e3</span>; } ' +
      '<span class="hljs-comment">// it&#39;s &amp; done</span>'
  );
  assert.ok(!/<(?!\/?span)/.test(highlightCode('<script>alert(1)</script>', 'go')));
});

test('reachesLineCount counts wrapped visual lines without overcounting', () => {
  assert.equal(reachesLineCount('', 24, 160), false);
  assert.equal(reachesLineCount('x\n'.repeat(24), 24, 160), true);
  assert.equal(reachesLineCount('x\n'.repeat(23), 24, 160), false);
  // A trailing newline adds no visible line; an empty line in between does.
  assert.equal(reachesLineCount('x\n'.repeat(23) + '\n', 24, 160), true);
  assert.equal(reachesLineCount('\n'.repeat(24), 24, 160), true);
  assert.equal(reachesLineCount('\n'.repeat(23), 24, 160), false);
  // Long lines wrap at maxCols.
  assert.equal(reachesLineCount('y'.repeat(23 * 160 + 1), 24, 160), true);
  assert.equal(reachesLineCount('y'.repeat(23 * 160), 24, 160), false);
  assert.equal(reachesLineCount(('z'.repeat(161) + '\n').repeat(12), 24, 160), true);
  assert.equal(reachesLineCount('short', 24, 160), false);
});

test('renderMarkdown converts markdown and strictly neutralizes XSS vectors', () => {
  // Test GFM structures
  const md = renderMarkdown('# Heading 1\n\nThis is **bold** and *italic* and `code`.\n\n- item 1\n- item 2\n\n```go\nfunc Main() {}\n```');
  assert.ok(md.includes('<h1>Heading 1</h1>'));
  assert.ok(md.includes('<strong>bold</strong>'));
  assert.ok(md.includes('<em>italic</em>'));
  assert.ok(md.includes('<code class="inline-code">code</code>'));
  assert.ok(md.includes('<ul><li>item 1</li><li>item 2</li></ul>'));
  assert.ok(md.includes('class="code-block-container"'));

  // Test XSS neutralization: <script> must be escaped and cannot execute
  const xss1 = renderMarkdown('<script>alert("pwned")</script>');
  assert.ok(!xss1.includes('<script>'));
  assert.ok(xss1.includes('&lt;script&gt;'));

  // Test XSS neutralization: <img onerror=...> must be escaped
  const xss2 = renderMarkdown('<img src="x" onerror="alert(1)">');
  assert.ok(!xss2.includes('<img'));
  assert.ok(xss2.includes('&lt;img'));

  // Test XSS neutralization: javascript: href in links must be sanitized to #
  const xss3 = renderMarkdown('[click me](javascript:alert(1))');
  assert.ok(!xss3.includes('javascript:'));
  assert.ok(xss3.includes('href="#"'));

  // Test valid http links
  const validLink = renderMarkdown('[Claude](https://claude.ai)');
  assert.ok(validLink.includes('href="https://claude.ai"'));
  assert.ok(validLink.includes('target="_blank"'));
});

test('renderMarkdown leaves intraword underscores, code and URLs literal', () => {
  const cases = [
    ['Set API_KEY_NAME first', '<p>Set API_KEY_NAME first</p>'],
    ['call snake_case_fn()', '<p>call snake_case_fn()</p>'],
    ['Grüße_und_Tschüss', '<p>Grüße_und_Tschüss</p>'],
    ['Use `API_KEY_NAME` and `a*b*c`', '<p>Use <code class="inline-code">API_KEY_NAME</code> and <code class="inline-code">a*b*c</code></p>'],
    ['2 * 3 * 4', '<p>2 * 3 * 4</p>'],
    ['_it_ (_it_) _foo_bar_', '<p><em>it</em> (<em>it</em>) <em>foo_bar</em></p>'],
    ['__b__ ___bi___ un*frigging*believable', '<p><strong>b</strong> <strong><em>bi</em></strong> un<em>frigging</em>believable</p>'],
  ];
  for (const [input, want] of cases) {
    assert.equal(renderMarkdown(input), want, input);
  }

  const link = renderMarkdown('[**docs**](https://x.dev/a_b_c)');
  assert.ok(link.includes('href="https://x.dev/a_b_c"'), link);
  assert.ok(link.includes('<strong>docs</strong></a>'), link);
});

test('renderMarkdown renders tables, including one right after a line of text', () => {
  const md = renderMarkdown('**Verification**\n| Check | Result |\n|---|---|\n| tests | `ok` |\n| build | pass |');
  assert.equal(
    md,
    '<p><strong>Verification</strong></p>\n' +
      '<div class="table-container"><table><thead><tr><th>Check</th><th>Result</th></tr></thead><tbody>' +
      '<tr><td>tests</td><td><code class="inline-code">ok</code></td></tr>' +
      '<tr><td>build</td><td>pass</td></tr></tbody></table></div>'
  );

  const first = renderMarkdown('| A | B |\n| :-- | --: |\n| 1 | 2 |');
  assert.ok(first.startsWith('<div class="table-container"><table>'), first);
  assert.ok(first.includes('<th>A</th><th>B</th>'), first);
  assert.ok(first.includes('<td>1</td><td>2</td>'), first);

  const code = renderMarkdown('| a | b |\n|---|---|\n| a | `x|y` |');
  assert.ok(code.includes('<tr><td>a</td><td><code class="inline-code">x|y</code></td></tr>'), code);

  const escaped = renderMarkdown('| a \\| b | c |\n|---|---|\n| 1 | 2 |');
  assert.ok(escaped.includes('<th>a | b</th><th>c</th></tr>'), escaped);
  assert.ok(escaped.includes('<tr><td>1</td><td>2</td></tr>'), escaped);
});

test('renderMarkdown keeps table cells safe and leaves non-tables alone', () => {
  const xss = renderMarkdown('| a | b |\n|---|---|\n| <script>alert(1)</script> | [x](javascript:alert(1)) |');
  assert.ok(!xss.includes('<script>'), xss);
  assert.ok(xss.includes('&lt;script&gt;'), xss);
  assert.ok(!xss.includes('javascript:'), xss);
  assert.ok(xss.includes('href="#"'), xss);

  assert.equal(renderMarkdown('a | b'), '<p>a | b</p>');
  assert.equal(renderMarkdown('text\na | b\nmore'), '<p>text<br />a | b<br />more</p>');

  const fence = renderMarkdown('```\n| a | b |\n|---|---|\n```');
  assert.ok(fence.includes('class="code-block-container"'), fence);
  assert.ok(!fence.includes('<table>'), fence);
});

test('handleCopyCodeClick copies code from delegated click and ignores other targets', async () => {
  const { handleCopyCodeClick } = await import('../src/lib/copycode.ts');

  const written = [];
  const origClipboard = globalThis.navigator?.clipboard;
  Object.defineProperty(globalThis, 'navigator', {
    value: { clipboard: { writeText: (t) => { written.push(t); return Promise.resolve(); } } },
    configurable: true,
  });

  try {
    function makeBtn(code) {
      const classes = new Set(['copy-code-btn']);
      const btn = {
        textContent: 'Copy',
        getAttribute: (k) => (k === 'data-code' ? code : null),
        closest: (selector) => (selector === '.copy-code-btn' ? btn : null),
        classList: {
          contains: (c) => classes.has(c),
          add: (c) => classes.add(c),
          remove: (c) => classes.delete(c),
        },
      };
      return btn;
    }

    const btn = makeBtn('const x = 1;');

    // Click on the button itself
    handleCopyCodeClick({ target: btn, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, ['const x = 1;']);

    // Click on a child inside the button (delegation via closest)
    written.length = 0;
    const child = {
      closest: (sel) => (sel === '.copy-code-btn' ? btn : null),
    };
    handleCopyCodeClick({ target: child, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, ['const x = 1;']);

    // Click elsewhere (closest returns null) must not copy
    written.length = 0;
    const outside = { closest: () => null };
    handleCopyCodeClick({ target: outside, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, []);

    // Button without data-code must not copy
    written.length = 0;
    const noData = makeBtn(null);
    handleCopyCodeClick({ target: noData, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, []);
  } finally {
    if (origClipboard !== undefined) {
      Object.defineProperty(globalThis, 'navigator', { value: origClipboard, configurable: true });
    }
  }
});

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

// Store imports resolve per render to the fixture set for that render.
function fixtureStore(name, fixture = '__componentFixture') {
  return `const ${name} = new Proxy({}, { get: (_, key) => globalThis.${fixture}.${name}[key] });`;
}

// Compiles a real component for server rendering. Only the listed imports
// are replaced: store imports with fixtures, helpers with the real modules.
async function loadServerComponent(url, replacements) {
  const [{ readFileSync }, { fileURLToPath }, { compile }] = await Promise.all([
    import('node:fs'),
    import('node:url'),
    import('svelte/compiler'),
  ]);
  const filename = fileURLToPath(url);
  const code = compile(readFileSync(filename, 'utf8'), { generate: 'server', filename }).js.code;
  return importCompiled(filename, code, replacements);
}

// Compiles a real .svelte.ts store module for the server the same way: types
// are stripped, runes compiled, and only the listed imports replaced.
async function loadServerModule(url, replacements) {
  const [{ readFileSync }, { fileURLToPath }, { compileModule }, { default: ts }] = await Promise.all([
    import('node:fs'),
    import('node:url'),
    import('svelte/compiler'),
    import('typescript'),
  ]);
  const filename = fileURLToPath(url);
  const js = ts.transpileModule(readFileSync(filename, 'utf8'), {
    fileName: filename,
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext, useDefineForClassFields: true },
  }).outputText;
  const code = compileModule(js, { generate: 'server', filename: filename.replace(/\.ts$/, '.js') }).js.code;
  return importCompiled(filename, code, replacements);
}

async function importCompiled(filename, code, replacements) {
  replacements = [
    ["'svelte/internal/server'", JSON.stringify(import.meta.resolve('svelte/internal/server'))],
    ...replacements,
  ];
  for (const [from, to] of replacements) {
    assert.ok(code.includes(from), `compiled ${filename} no longer contains ${from}`);
    code = code.replace(from, to);
  }
  return import(`data:text/javascript,${encodeURIComponent(code)}`);
}

async function renderWithFixture(module, fixture, props = {}) {
  const { render } = await import('svelte/server');
  globalThis.__componentFixture = fixture;
  try {
    return render(module.default, { props }).body;
  } finally {
    delete globalThis.__componentFixture;
  }
}

// Server-renders the real dialog with plain store fixtures.
async function renderDeleteDialog(fixture) {
  deleteDialogModule ??= await loadServerComponent(DELETE_DIALOG_URL, [
    ["'../../manage'", JSON.stringify(new URL('../../manage.ts', DELETE_DIALOG_URL).href)],
    ["'../../format'", JSON.stringify(new URL('../../format.ts', DELETE_DIALOG_URL).href)],
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

function renderedButtons(html) {
  return [...html.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)].map(([, attrs, text]) => ({
    text: text.replace(/<!--[\s\S]*?-->/g, '').trim(),
    disabled: /\sdisabled(?:=|\s|$)/.test(attrs),
  }));
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
  assert.deepEqual(renderedButtons(html).map(b => b.text), ['✕', 'Done']);
  assert.doesNotMatch(html, /Retry|Cancel|Move to|Delete Permanently/);
  assert.deepEqual(fixture.calls, []);
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

// Synthetic Windows OpenSSH failure: CRLF stderr wrapped in the probe error.
const WINDOWS_SSH_ERROR =
  'probe handshake: exit status 255 (stderr: ssh: connect to host box.example port 22: Connection timed out\r\n' +
  'kex_exchange_identification: read: Connection reset by peer   \r\n\r\n\r\n\r\n' +
  'C:\\Windows\\System32\\OpenSSH\\ssh.exe: Permission denied (publickey,keyboard-interactive).\r\n)';

test('connectionErrorDetail keeps line breaks but drops CRLF and blank edges', () => {
  assert.equal(connectionErrorDetail(undefined), undefined);
  assert.equal(connectionErrorDetail('  \r\n '), undefined);
  assert.equal(
    connectionErrorDetail(WINDOWS_SSH_ERROR),
    'probe handshake: exit status 255 (stderr: ssh: connect to host box.example port 22: Connection timed out\n' +
      'kex_exchange_identification: read: Connection reset by peer\n\n' +
      'C:\\Windows\\System32\\OpenSSH\\ssh.exe: Permission denied (publickey,keyboard-interactive).\n)',
  );
});

test('connectionBanner separates a failed connect from a lost session', () => {
  assert.equal(connectionBanner({ phase: 'local' }), null);
  assert.equal(connectionBanner({ phase: 'connecting', host: 'box', error: 'x' }), null);
  assert.equal(connectionBanner({ phase: 'connected', host: 'box' }), null);

  const failed = connectionBanner({ phase: 'disconnected', host: 'box', error: WINDOWS_SSH_ERROR });
  assert.equal(failed.kind, 'failed');
  assert.equal(failed.retrying, false);
  assert.equal(`${failed.before}${failed.host}${failed.after}`, 'Could not connect to box.');
  assert.equal(failed.detail, connectionErrorDetail(WINDOWS_SSH_ERROR));
  assert.equal(failed.detailLabel, 'Error:');

  const retrying = connectionBanner({ phase: 'reconnecting', host: 'box', error: 'dial failed' });
  assert.equal(retrying.kind, 'failed');
  assert.equal(`${retrying.before}${retrying.host}${retrying.after}`, 'Could not connect to box. Retrying…');
  assert.equal(retrying.detail, 'dial failed');

  const lost = connectionBanner({ phase: 'reconnecting', host: 'box', error: CONNECTION_LOST });
  assert.equal(lost.kind, 'lost');
  assert.equal(`${lost.before}${lost.host}${lost.after}`, 'Connection to box lost. Automatically reconnecting…');
  assert.equal(lost.detail, undefined, 'the lost marker is summary, not detail');
  assert.equal(connectionBanner({ phase: 'disconnected', host: 'box', error: CONNECTION_LOST }).after, ' lost.');

  // A reconnect that fails after a live session dropped is still a lost session.
  const lostThenFailed = connectionBanner({ phase: 'reconnecting', host: 'box', error: 'dial failed', wasConnected: true });
  assert.equal(lostThenFailed.kind, 'lost');
  assert.equal(lostThenFailed.detail, 'dial failed');
  assert.equal(lostThenFailed.detailLabel, 'Last reconnect attempt failed:');

  const plain = connectionBanner({ phase: 'disconnected', host: 'box' });
  assert.equal(plain.kind, 'disconnected');
  assert.equal(`${plain.before}${plain.host}${plain.after}`, 'Disconnected from box.');
  assert.equal(plain.detail, undefined);
});

const RECONNECT_BANNER_URL = new URL('../src/lib/components/common/ReconnectBanner.svelte', import.meta.url);
let reconnectBannerModule;

async function renderReconnectBanner(connectionStore) {
  reconnectBannerModule ??= await loadServerComponent(RECONNECT_BANNER_URL, [
    ["'../../guidance'", JSON.stringify(new URL('../../guidance.ts', RECONNECT_BANNER_URL).href)],
    ["import { connectionStore } from '../../stores/connection.svelte';", fixtureStore('connectionStore')],
  ]);
  return renderWithFixture(reconnectBannerModule, {
    connectionStore: { generation: 1, connect() {}, disconnect() {}, ...connectionStore },
  });
}

function bannerDetail(html) {
  return html.match(/<pre class="detail-text[^"]*">([\s\S]*?)<\/pre>/)?.[1];
}

function unescapeHTML(text) {
  return text.replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&amp;/g, '&');
}

test('reconnect banner shows a failed connect error as wrapping detail with Retry', async () => {
  const html = await renderReconnectBanner({ phase: 'disconnected', host: 'box', error: WINDOWS_SSH_ERROR });
  const summary = html.match(/<p class="banner-summary[^"]*">([\s\S]*?)<\/p>/)[1].replace(/<!--[\s\S]*?-->/g, '');
  assert.equal(summary, 'Could not connect to <strong>box</strong>.');
  assert.doesNotMatch(summary, /Connection timed out/, 'the raw error stays out of the summary');
  assert.match(html, /Error:/);
  assert.equal(unescapeHTML(bannerDetail(html)), connectionErrorDetail(WINDOWS_SSH_ERROR));
  assert.doesNotMatch(html, /\btitle=/, 'no single-line tooltip copy of the error');
  assert.doesNotMatch(html, /Disconnected from|lost/);
  assert.deepEqual(renderedButtons(html).map(b => b.text), ['Retry', 'Switch to Local']);
});

test('reconnect banner keeps a lost session apart from a retrying failed connect', async () => {
  const lost = await renderReconnectBanner({ phase: 'reconnecting', host: 'box', error: CONNECTION_LOST });
  assert.match(lost, /Connection to <strong>box<\/strong> lost\. Automatically reconnecting…/);
  assert.equal(bannerDetail(lost), undefined);
  assert.match(lost, /class="spinner/);
  assert.deepEqual(renderedButtons(lost).map(b => b.text), ['Switch to Local']);

  const retrying = await renderReconnectBanner({ phase: 'reconnecting', host: 'box', error: 'dial tcp: i/o timeout' });
  assert.match(retrying, /Could not connect to <strong>box<\/strong>\. Retrying…/);
  assert.equal(bannerDetail(retrying), 'dial tcp: i/o timeout');
  assert.deepEqual(renderedButtons(retrying).map(b => b.text), ['Switch to Local']);

  assert.doesNotMatch(await renderReconnectBanner({ phase: 'connected', host: 'box' }), /reconnect-banner/);
  assert.doesNotMatch(await renderReconnectBanner({ phase: 'local' }), /reconnect-banner/);
});

test('mock settings gate deletion and preview tokens bind to selection', async () => {
  const backend = new MockBackendAPI();
  const ref = { agent: 'claude-code', id: 'session-claude-2' };
  assert.deepEqual(await backend.getSettings(), { enabled: false, allowPermanentDelete: false });
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
  assert.deepEqual(await backend.setManageEnabled(false), { enabled: false, allowPermanentDelete: false });
});

test('mock preview blocks live sessions and excludes them from the token', async () => {
  const backend = new MockBackendAPI();
  await backend.setManageEnabled(true);
  const ref = { agent: 'claude-code', id: 'session-claude-1' };
  const preview = await backend.previewDelete([ref]);
  assert.match(preview.items[0].blocked, /live/);
  await assert.rejects(() => backend.deleteSessions([ref], preview.token), /changed/);
});

function treeNode(key, kind, childCount = 0, childKind = 'session') {
  const children = Array.from({ length: childCount }, (_, i) => ({
    key: `${key}/${i}`,
    label: `${key}/${i}`,
    kind: childKind,
    sessionCount: 1,
  }));
  return { key, label: key, kind, sessionCount: childCount, children };
}

test('defaultCollapsedKeys collapses only groups at or above the threshold', () => {
  assert.equal(defaultCollapsedKeys([]).size, 0);

  const small = treeNode('small', 'directory', DEFAULT_COLLAPSE_THRESHOLD - 1, 'agent');
  const big = treeNode('big', 'directory', DEFAULT_COLLAPSE_THRESHOLD, 'agent');
  const bigAgent = treeNode('bigAgent', 'agent', DEFAULT_COLLAPSE_THRESHOLD);
  const keys = defaultCollapsedKeys([small, big, bigAgent]);
  assert.deepEqual([...keys].sort(), ['big', 'bigAgent']);

  assert.deepEqual([...defaultCollapsedKeys([small], 2)], ['small']);
});

test('defaultCollapsedKeys recurses into nested groups', () => {
  const nestedAgent = treeNode('dir/agent', 'agent', 10);
  const dir = { ...treeNode('dir', 'directory'), children: [nestedAgent] };
  assert.deepEqual([...defaultCollapsedKeys([dir])], ['dir/agent']);
});

test('defaultCollapsedKeys collapses every session with subagents', () => {
  const withOne = treeNode('sess1', 'session', 1);
  const withMany = treeNode('sessN', 'session', 7);
  const leaf = treeNode('leaf', 'session');
  const agent = { ...treeNode('agent', 'agent'), children: [withOne, withMany, leaf] };

  const keys = defaultCollapsedKeys([agent]);
  assert.deepEqual([...keys].sort(), ['sess1', 'sessN']);
});

test('isCollapsed flips the default state for manually toggled keys', () => {
  const defaults = new Set(['big']);
  assert.equal(isCollapsed('big', new Set(), defaults), true);
  assert.equal(isCollapsed('big', new Set(['big']), defaults), false);
  assert.equal(isCollapsed('small', new Set(), defaults), false);
  assert.equal(isCollapsed('small', new Set(['small']), defaults), true);
});

test('nodeKind prefers kind and falls back to field inference', () => {
  assert.equal(nodeKind({ key: 'a', label: 'a', kind: 'session', agent: 'codex', sessionCount: 1 }), 'session');
  assert.equal(nodeKind({ key: 'a', label: 'a', agent: 'codex', sessionCount: 1 }), 'agent');
  assert.equal(nodeKind({ key: 'a', label: 'a', cwd: '/x', sessionCount: 1 }), 'directory');
  assert.equal(nodeKind({ key: 'a', label: 'a', sessionCount: 1 }), 'session');
});

test('pruneKeys drops keys whose nodes no longer exist', () => {
  const tree = [treeNode('dir', 'directory', 2, 'agent')];
  const pruned = pruneKeys(new Set(['dir', 'dir/1', 'gone']), tree);
  assert.deepEqual([...pruned].sort(), ['dir', 'dir/1']);
});

test('MockBackendAPI listGroups emits node kinds', async () => {
  const backend = new MockBackendAPI();
  const dirAgent = await backend.listGroups('dir-agent');
  assert.ok(dirAgent.length > 0);
  for (const root of dirAgent) {
    assert.equal(root.kind, 'directory');
    for (const child of root.children ?? []) assert.equal(child.kind, 'agent');
  }
  const agentDir = await backend.listGroups('agent-dir');
  for (const root of agentDir) {
    assert.equal(root.kind, 'agent');
    for (const child of root.children ?? []) assert.equal(child.kind, 'directory');
  }
  for (const node of await backend.listGroups('flat')) assert.equal(node.kind, 'session');
});

test('MockBackendAPI path filter narrows groups and sessions by cwd', async () => {
  const backend = new MockBackendAPI();
  const all = await backend.listSessions('');
  const cwd = all[0].cwd;
  const needle = cwd.split('/').pop().toUpperCase();
  const expected = all.filter((s) => s.cwd.toLowerCase().includes(needle.toLowerCase()));
  const filtered = await backend.listSessions('', { path: ` ${needle} ` });
  assert.ok(filtered.length > 0);
  assert.equal(filtered.length, expected.length);
  const groups = await backend.listGroups('dir-agent', { path: needle });
  for (const g of groups) assert.ok(g.cwd.toLowerCase().includes(needle.toLowerCase()));
  assert.deepEqual(await backend.listSessions('', { path: 'no-such-dir-xyz' }), []);
});

test('MockBackendAPI search finds safe title, text, and tool hits with filters', async () => {
  const backend = new MockBackendAPI();

  const titles = await backend.search('SQLite', {});
  assert.equal(titles.length, 1);
  assert.equal(titles[0].kind, 'title');
  assert.equal(titles[0].messageIndex, -1);
  assert.match(titles[0].snippet, /<mark>SQLite<\/mark>/);

  const text = await backend.search('thread-safe', { agents: ['claude-code'] });
  assert.equal(text.length, 1);
  assert.equal(text[0].ref.id, 'session-claude-1');
  assert.equal(text[0].messageIndex, 2);
  assert.equal(text[0].kind, 'text');

  const tool = await backend.search('git status', { dir: '/home/haith/Workspaces/ginkcode/tools/agent-sessions' });
  assert.equal(tool.length, 1);
  assert.equal(tool[0].messageIndex, 1);
  // A mixed text/tool message is intentionally classified as text.
  assert.equal(tool[0].kind, 'text');

  assert.deepEqual(await backend.search('scan', { agents: ['codex'] }), []);
  assert.deepEqual(await backend.search('scan', { dir: '/home/haith/Workspaces/ginkcode/tool' }), []);
  assert.equal((await backend.search('session', { limit: 1 })).length, 1);
});

test('mock snippets escape markup and only inject their own mark pair', () => {
  assert.equal(
    highlightedSnippet('<img onerror="boom"> needle & tail', 21, 6),
    '&lt;img onerror=&quot;boom&quot;&gt; <mark>needle</mark> &amp; tail'
  );
});

test('search helpers preserve global jump indexes and active filters', () => {
  assert.equal(jumpOffset(-1), 0);
  assert.equal(jumpOffset(10), 0);
  assert.equal(jumpOffset(9000), 8980);
  assert.deepEqual(searchFilterFromApp('codex', '/repo', 15), {
    agents: ['codex'], dir: '/repo', limit: 15,
  });
  assert.deepEqual(searchTerms('agent:codex "exact phrase" alpha'), ['exact phrase', 'alpha']);
  assert.equal(progressIncomplete({ done: 4, pending: 1, failed: 0, running: false }), true);
  assert.equal(progressIncomplete({ done: 5, pending: 0, failed: 0, running: false }), false);
});

const WINDOWS_DIRS = [
  'C:\\Users\\me\\repo',
  'C:\\',
  '\\\\fileserver\\share\\team-repo\\',
  '\\\\?\\D:\\Workspaces\\x',
  'C:/Users/me\\mixed/repo',
];

test('searchFilterFromApp passes Windows directories through unchanged', () => {
  for (const dir of WINDOWS_DIRS) {
    assert.deepEqual(searchFilterFromApp('codex', dir, 15), { agents: ['codex'], dir, limit: 15 });
  }
  // Only surrounding whitespace is trimmed; inner spaces and separators stay.
  assert.deepEqual(searchFilterFromApp(undefined, '  C:\\Program Files\\app\\  '), {
    dir: 'C:\\Program Files\\app\\', limit: 30,
  });
});

test('tree helpers treat Windows and UNC keys as opaque strings', () => {
  const drive = treeNode('dir-agent:C:\\Users\\me\\repo', 'directory', DEFAULT_COLLAPSE_THRESHOLD, 'agent');
  const unc = treeNode('dir-agent:\\\\fileserver\\share\\team-repo\\', 'directory', 1, 'agent');
  const root = { ...treeNode('dir-agent:C:\\', 'directory'), cwd: 'C:\\' };
  const tree = [drive, unc, root];

  assert.deepEqual([...defaultCollapsedKeys(tree)], ['dir-agent:C:\\Users\\me\\repo']);
  assert.equal(isCollapsed('dir-agent:C:\\Users\\me\\repo', new Set(), defaultCollapsedKeys(tree)), true);
  assert.equal(
    isCollapsed('dir-agent:\\\\fileserver\\share\\team-repo\\', new Set(['dir-agent:\\\\fileserver\\share\\team-repo\\']), new Set()),
    true
  );

  // Keys are matched exactly: no case folding or separator normalization.
  const pruned = pruneKeys(
    new Set([
      'dir-agent:C:\\Users\\me\\repo',
      'dir-agent:C:\\Users\\me\\repo/0',
      'dir-agent:c:\\users\\me\\repo',
      'dir-agent:C:/Users/me/repo',
      'dir-agent:\\\\fileserver\\share\\team-repo\\',
      'dir-agent:\\\\fileserver\\share\\team-repo',
    ]),
    tree
  );
  assert.deepEqual([...pruned].sort(), [
    'dir-agent:C:\\Users\\me\\repo',
    'dir-agent:C:\\Users\\me\\repo/0',
    'dir-agent:\\\\fileserver\\share\\team-repo\\',
  ].sort());

  for (const cwd of WINDOWS_DIRS) {
    assert.equal(nodeKind({ key: `dir-agent:${cwd}`, label: cwd, cwd, sessionCount: 1 }), 'directory');
  }

  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
    clear: () => store.clear(),
  };
  try {
    const keys = new Set(WINDOWS_DIRS.map((cwd) => `dir-agent:${cwd}`));
    saveCollapsedKeys(keys);
    assert.deepEqual([...loadCollapsedKeys()], [...keys]);
  } finally {
    delete globalThis.localStorage;
  }
});

test('MockBackendAPI path filter matches Windows cwd case-insensitively', async () => {
  const backend = new MockBackendAPI();
  const driveCwd = 'C:\\Users\\Me\\Repos\\Win-Client';
  const uncCwd = '\\\\FileServer\\Share\\Team-Repo\\';
  // Replace entries (not mutate them): the mock shares session objects
  // between instances.
  backend.sessions = backend.sessions.map((s, i) =>
    i === 0 ? { ...s, cwd: driveCwd, archived: false } : i === 1 ? { ...s, cwd: uncCwd, archived: false } : s
  );
  const [drive, unc] = backend.sessions;

  const byDrive = await backend.listSessions('', { path: ' c:\\users\\me\\repos ' });
  assert.deepEqual(byDrive.map((s) => s.ref), [drive.ref]);
  assert.equal(byDrive[0].cwd, driveCwd);

  const byUNC = await backend.listSessions('', { path: '\\\\fileserver\\share' });
  assert.deepEqual(byUNC.map((s) => s.ref), [unc.ref]);
  assert.equal(byUNC[0].cwd, uncCwd);

  const groups = await backend.listGroups('dir-agent', { path: 'WIN-CLIENT' });
  assert.equal(groups.length, 1);
  assert.equal(groups[0].key, `dir-agent:${driveCwd}`);
  assert.equal(groups[0].cwd, driveCwd);

  // The forward-slash spelling is a different substring.
  assert.deepEqual(await backend.listSessions('', { path: 'c:/users/me' }), []);
});

test('safeSnippetHTML keeps only bare mark tags', () => {
  assert.equal(
    safeSnippetHTML('a <mark>b</mark> <img src=x onerror=alert(1)> <MARK>c</MARK>'),
    'a <mark>b</mark> &lt;img src=x onerror=alert(1)> <MARK>c</MARK>'
  );
  assert.equal(safeSnippetHTML('<mark onclick=x>b</mark>'), '&lt;mark onclick=x>b</mark>');
});

test('LatestRequestGate debounces and invalidates stale generations', async () => {
  const gate = new LatestRequestGate(20);
  let runs = 0;
  gate.schedule(() => runs++);
  gate.schedule(() => runs++);
  await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(runs, 0);
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(runs, 1);

  const first = gate.begin();
  const second = gate.begin();
  assert.equal(gate.isCurrent(first), false);
  assert.equal(gate.isCurrent(second), true);
  gate.schedule(() => runs++);
  assert.equal(gate.isCurrent(second), false);
  gate.cancel();
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(runs, 1);
});

test('MockBackendAPI pages a long transcript at the arbitrary requested offset', async () => {
  const backend = new MockBackendAPI();
  backend.messages['long-test'] = Array.from({ length: 10_000 }, (_, index) => ({
    id: `message-${index}`,
    role: 'assistant',
    time: '2026-09-28T09:00:00Z',
    parts: [{ kind: 'text', text: `Message ${index}` }],
  }));
  const page = await backend.getMessages({ agent: 'claude-code', id: 'long-test' }, 8980, 50);
  assert.equal(page.offset, 8980);
  assert.equal(page.messages.length, 50);
  assert.equal(page.messages[20].id, 'message-9000');
  assert.equal(page.hasMore, true);
});

test('MockBackendAPI scan resolves after a visible delay', async () => {
  const backend = new MockBackendAPI();
  const start = Date.now();
  assert.equal(await backend.scan(), undefined);
  assert.ok(Date.now() - start > 0);
});

test('sessionToSelect picks the node itself for sessions, else the first visible', () => {
  const parent = { agent: 'claude-code', id: 'parent' };
  const sub = { agent: 'claude-code', id: 'sub' };
  const other = { agent: 'codex', id: 'x' };
  // Subagent sorts first (more recently updated) but the session node wins.
  const visible = [{ ref: sub }, { ref: parent }];
  const sessionNode = { key: 's', label: 's', kind: 'session', sessionCount: 2, sessions: [parent, sub] };
  assert.deepEqual(sessionToSelect(sessionNode, visible), parent);

  const group = { key: 'g', label: 'g', kind: 'agent', sessionCount: 2, sessions: [parent, sub] };
  assert.deepEqual(sessionToSelect(group, visible), sub);

  // Session hidden by the age filter falls back to the first visible row.
  assert.deepEqual(sessionToSelect(sessionNode, [{ ref: other }]), other);
  assert.equal(sessionToSelect(group, []), null);
});

test('isKnownTime rejects Go zero time and invalid strings', () => {
  assert.equal(isKnownTime('0001-01-01T00:00:00Z'), false);
  assert.equal(isKnownTime(''), false);
  assert.equal(isKnownTime('not a date'), false);
  assert.equal(isKnownTime('2026-09-28T10:00:00Z'), true);
});

test('formatAgo drops "ago" for just now', () => {
  assert.equal(formatAgo(new Date().toISOString()), 'just now');
  assert.equal(formatAgo(new Date(Date.now() - 5 * 60 * 1000).toISOString()), '5m ago');
  assert.equal(formatAgo(''), '');
});

const cref = (id) => ({ agent: 'claude-code', id });
const catalogTree = [
  {
    key: 'dir:/a',
    label: '/a',
    kind: 'directory',
    sessionCount: 2,
    sessions: [cref('a1'), cref('a2')],
    children: [
      { key: 'sess:a1', label: 'a1', kind: 'session', sessionCount: 1, sessions: [cref('a1')] },
    ],
  },
  { key: 'dir:/b', label: '/b', kind: 'directory', sessionCount: 1, sessions: [cref('b1')] },
];

test('isFullRefresh accepts only groupsDirty events without refs', () => {
  assert.equal(isFullRefresh({ changed: null, removed: null, groupsDirty: true }), true);
  assert.equal(isFullRefresh({ changed: [], removed: [], groupsDirty: true }), true);
  assert.equal(isFullRefresh({ changed: [cref('a1')], removed: [], groupsDirty: true }), false);
  assert.equal(isFullRefresh({ changed: [], removed: [cref('a1')], groupsDirty: true }), false);
  assert.equal(isFullRefresh({ changed: [], removed: [], groupsDirty: false }), false);
});

test('hasRef matches by agent and id and tolerates missing inputs', () => {
  assert.equal(hasRef([cref('a1')], cref('a1')), true);
  assert.equal(hasRef([cref('a1')], { agent: 'codex', id: 'a1' }), false);
  assert.equal(hasRef([cref('a1')], null), false);
  assert.equal(hasRef(null, cref('a1')), false);
});

test('findGroup and groupSessionKeys walk the whole tree', () => {
  assert.equal(findGroup(catalogTree, 'sess:a1')?.label, 'a1');
  assert.equal(findGroup(catalogTree, 'missing'), null);
  assert.deepEqual([...groupSessionKeys(catalogTree, 'dir:/b')], ['claude-code:b1']);
  assert.deepEqual(
    [...groupSessionKeys(catalogTree, null)].sort(),
    ['claude-code:a1', 'claude-code:a2', 'claude-code:b1']
  );
  assert.equal(groupSessionKeys(catalogTree, 'missing').size, 0);
});

test('affectsSessionList reloads only for refs listed or placed in the group', () => {
  const listed = [{ ref: cref('b1') }];
  const ev = (changed, removed = []) => ({ changed, removed, groupsDirty: true });

  // Full refresh always reloads.
  assert.equal(affectsSessionList(ev([], []), listed, catalogTree, 'dir:/b'), true);
  // A listed session updated or removed.
  assert.equal(affectsSessionList(ev([cref('b1')]), listed, catalogTree, 'dir:/b'), true);
  assert.equal(affectsSessionList(ev([], [cref('b1')]), listed, catalogTree, 'dir:/b'), true);
  // A change in another group is ignored.
  assert.equal(affectsSessionList(ev([cref('a1')]), listed, catalogTree, 'dir:/b'), false);
  assert.equal(affectsSessionList(ev([], [cref('zz')]), listed, catalogTree, 'dir:/b'), false);
  // A new session the fresh tree places in the selected group.
  const grown = [
    catalogTree[0],
    { ...catalogTree[1], sessions: [cref('b1'), cref('b2')] },
  ];
  assert.equal(affectsSessionList(ev([cref('b2')]), listed, grown, 'dir:/b'), true);
  // The all-sessions list covers every group.
  assert.equal(affectsSessionList(ev([cref('a2')]), listed, catalogTree, null), true);
});

test('RequestSequence drops responses from superseded requests', async () => {
  const seq = new RequestSequence();
  let applied = null;
  const load = async (value, delayMs) => {
    const id = seq.next();
    await new Promise((resolve) => setTimeout(resolve, delayMs));
    if (seq.isCurrent(id)) applied = value;
  };
  // The slow first response resolves after the fast second one.
  await Promise.all([load('stale', 30), load('fresh', 5)]);
  assert.equal(applied, 'fresh');
});

test('MockBackendAPI onEvent delivers events and unsubscribes one listener', () => {
  const mock = new MockBackendAPI();
  const seen = [];
  const offA = mock.onEvent('x', (v) => seen.push(`a:${v}`));
  mock.onEvent('x', (v) => seen.push(`b:${v}`));
  mock.onEvent('y', (v) => seen.push(`y:${v}`));
  mock.emit('x', 1);
  offA();
  offA();
  mock.emit('x', 2);
  assert.deepEqual(seen, ['a:1', 'b:1', 'b:2']);
});

test('typed subscriptions forward objects and ignore malformed payloads', () => {
  const mock = new MockBackendAPI();
  const catalog = [];
  const progress = [];
  const offCatalog = subscribeCatalogChanged((e) => catalog.push(e), mock);
  subscribeIndexProgress((p) => progress.push(p), mock);

  const event = { changed: [cref('a1')], removed: [], groupsDirty: true };
  mock.emit('catalog:changed', event);
  mock.emit('catalog:changed', null);
  mock.emit('index:progress', { done: 1, pending: 0, failed: 0, running: false });
  offCatalog();
  mock.emit('catalog:changed', event);

  assert.deepEqual(catalog, [event]);
  assert.equal(progress.length, 1);
});

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

test('hashHost and targetKey provide deterministic namespaces per host', () => {
  assert.equal(targetKey(undefined), 'local');
  assert.equal(targetKey(''), 'local');
  assert.equal(targetKey('local'), 'local');

  const devHash = hashHost('dev-box');
  assert.equal(typeof devHash, 'string');
  assert.equal(devHash.length, 8);
  assert.equal(targetKey('dev-box'), devHash);

  const prodHash = hashHost('prod-server');
  assert.notEqual(devHash, prodHash);

  // Deterministic
  assert.equal(hashHost('dev-box'), devHash);
});

test('storageKeyForCollapsed namespaces keys and migrates legacy un-namespaced key', () => {
  // Mock localStorage for node test environment
  const store = new Map();
  globalThis.localStorage = {
    getItem: (k) => store.get(k) ?? null,
    setItem: (k, v) => store.set(k, String(v)),
    removeItem: (k) => store.delete(k),
    clear: () => store.clear(),
  };

  try {
    // 1. Remote host uses hashed namespace
    const remoteKey = storageKeyForCollapsed('dev-box');
    assert.equal(remoteKey, `${BASE_COLLAPSED_STORAGE_KEY}:${hashHost('dev-box')}`);

    // 2. Legacy key migration to local
    store.set(BASE_COLLAPSED_STORAGE_KEY, JSON.stringify(['group:1', 'group:2']));
    assert.equal(store.has(`${BASE_COLLAPSED_STORAGE_KEY}:local`), false);

    const localKey = storageKeyForCollapsed();
    assert.equal(localKey, `${BASE_COLLAPSED_STORAGE_KEY}:local`);
    assert.equal(store.has(BASE_COLLAPSED_STORAGE_KEY), false); // Cleaned up
    assert.deepEqual(JSON.parse(store.get(localKey)), ['group:1', 'group:2']);

    // 3. loadCollapsedKeys and saveCollapsedKeys per target host
    saveCollapsedKeys(new Set(['remote:node']), 'dev-box');
    assert.deepEqual([...loadCollapsedKeys('dev-box')], ['remote:node']);
    assert.deepEqual([...loadCollapsedKeys()], ['group:1', 'group:2']); // Local untouched
  } finally {
    delete globalThis.localStorage;
  }
});

test('MockBackendAPI remote hosts and connection lifecycle', async () => {
  const mock = new MockBackendAPI();

  const hosts = await mock.listHosts();
  assert.ok(hosts.length >= 2);
  assert.equal(hosts[0].name, 'dev-box');

  const initial = await mock.connectionState();
  assert.equal(initial.phase, 'local');
  assert.equal(initial.generation, 0);
  assert.equal(initial.capabilities.trash, true);

  const states = [];
  mock.onEvent('connection:state', (s) => states.push({ ...s }));

  // Successful connect emits connecting then connected.
  await mock.connect('dev-box');
  assert.equal(states.length, 2);
  assert.equal(states[0].phase, 'connecting');
  assert.equal(states[0].host, 'dev-box');
  assert.equal(states[1].phase, 'connected');
  assert.equal(states[1].host, 'dev-box');

  // Failed connect emits connecting and then nothing else.
  states.length = 0;
  await mock.connect('prod-server', false);
  assert.equal(states.length, 1);
  assert.equal(states[0].phase, 'connecting');
  assert.equal(states[0].generation, 2); // gen was already 1 after dev-box

  // Set a failed state explicitly for the remaining assertions.
  mock.setMockConnectionState({ phase: 'connected', host: 'dev-box' });

  await mock.disconnect();
  assert.equal(states.length, 3);
  assert.equal(states[1].phase, 'connected');
  assert.equal(states[1].host, 'dev-box');
  assert.equal(states[2].phase, 'local');
  assert.equal(states[2].generation, 3);
  assert.equal(states[2].host, undefined);
});

test('MockBackendAPI askpass event simulation and reply', async () => {
  const mock = new MockBackendAPI();
  const prompts = [];
  mock.onEvent('askpass:prompt', (p) => prompts.push(p));

  mock.simulateAskpass('ask-1', 'Password for dev@192.168.1.50:');
  assert.equal(prompts.length, 1);
  assert.equal(prompts[0].id, 'ask-1');
  assert.equal(prompts[0].prompt, 'Password for dev@192.168.1.50:');

  const ok = await mock.askpassReply('ask-1', 'secret123');
  assert.equal(ok, true);
});

test('subscribeConnectionState and subscribeAskpassPrompt handle events correctly', () => {
  const mock = new MockBackendAPI();
  const connEvents = [];
  const askpassEvents = [];

  const unsubConn = subscribeConnectionState((s) => connEvents.push(s), mock);
  const unsubAsk = subscribeAskpassPrompt((p) => askpassEvents.push(p), mock);

  mock.emit('connection:state', { phase: 'connecting', generation: 1 });
  mock.emit('connection:state', null); // Invalid, ignored
  mock.emit('askpass:prompt', { id: 'p1', prompt: 'Passphrase:' });
  mock.emit('askpass:prompt', 'not-an-object'); // Invalid, ignored

  assert.equal(connEvents.length, 1);
  assert.equal(connEvents[0].phase, 'connecting');
  assert.equal(askpassEvents.length, 1);
  assert.equal(askpassEvents[0].id, 'p1');

  unsubConn();
  unsubAsk();
  mock.emit('connection:state', { phase: 'connected', generation: 1 });
  assert.equal(connEvents.length, 1);
});



test('guardEpoch rejects replies that land after the epoch moved', async () => {
  let current = 0;
  const deferred = () => {
    let resolve, reject;
    const promise = new Promise((res, rej) => {
      resolve = res;
      reject = rej;
    });
    return { promise, resolve, reject };
  };
  const pending = [];
  const backend = {
    call() {
      const d = deferred();
      pending.push(d);
      return d.promise;
    },
    sync() {
      return 'sync';
    },
    connect() {
      const d = deferred();
      pending.push(d);
      return d.promise;
    },
  };
  const api = guardEpoch(backend, () => current, new Set(['connect']));

  // Same epoch: the reply and the error pass through.
  const ok = api.call();
  pending.at(-1).resolve(1);
  assert.equal(await ok, 1);
  const failed = api.call();
  pending.at(-1).reject(new Error('boom'));
  await assert.rejects(failed, /boom/);

  // Epoch moved while in flight: both outcomes become StaleReplyError.
  const lateOk = api.call();
  current++;
  pending.at(-1).resolve(2);
  await assert.rejects(lateOk, (err) => isStaleReply(err) && err instanceof StaleReplyError);
  const lateErr = api.call();
  current++;
  pending.at(-1).reject(new Error('boom'));
  await assert.rejects(lateErr, (err) => isStaleReply(err));

  // Passthrough methods and sync methods are untouched.
  const conn = api.connect();
  current++;
  pending.at(-1).resolve('c');
  assert.equal(await conn, 'c');
  assert.equal(api.sync(), 'sync');
  assert.equal(isStaleReply(new Error('x')), false);
});

test('isDisconnectedError matches the backend disconnected error', () => {
  assert.equal(isDisconnectedError(new Error('delete: disconnected from remote host mac')), true);
  assert.equal(isDisconnectedError('disconnected from remote host mac'), true);
  assert.equal(isDisconnectedError(new Error('permission denied')), false);
  assert.equal(isDisconnectedError(undefined), false);
});

test('nextLink reloads only when a host connects or the app returns to Local', () => {
  const step = (prev, phase, host) => {
    const next = nextLink(prev, phase, host);
    return { link: { dataHost: next.dataHost, phase }, reload: next.reload };
  };
  let link = { dataHost: undefined, phase: 'local' };
  let r;

  // First connect from Local: Local data stays on screen but is locked inert
  // so nothing on screen passes for the host's sessions.
  r = step(link, 'connecting', 'a');
  assert.deepEqual(r, { link: { dataHost: undefined, phase: 'connecting' }, reload: false });
  assert.equal(isStale(r.link), false);
  assert.equal(isLocked(r.link), true);
  assert.equal(blockedReason(r.link, 'a'), 'Not connected to a. The sessions shown are Local; changes are disabled until a connects or you switch back to Local.');
  r = step(r.link, 'connected', 'a');
  assert.equal(r.reload, true);
  assert.equal(r.link.dataHost, 'a');
  assert.equal(isStale(r.link), false);
  assert.equal(isLocked(r.link), false);

  // A drop keeps a's data, marked stale, and reconnecting reloads it.
  r = step(r.link, 'disconnected', 'a');
  assert.equal(r.reload, false);
  assert.equal(isStale(r.link), true);
  assert.equal(isLocked(r.link), true);
  assert.equal(blockedReason(r.link, 'a'), 'Not connected to a. Changes are disabled until it reconnects.');
  r = step(r.link, 'reconnecting', 'a');
  assert.equal(r.reload, false);
  assert.equal(isStale(r.link), true);
  assert.equal(isLocked(r.link), true);
  r = step(r.link, 'connected', 'a');
  assert.equal(r.reload, true);
  assert.equal(isStale(r.link), false);

  // A repeated connected update for the same host does not reload.
  assert.equal(step(r.link, 'connected', 'a').reload, false);

  // Switching a -> b keeps a's data stale until b connects.
  r = step(r.link, 'connecting', 'b');
  assert.equal(r.link.dataHost, 'a');
  assert.equal(isStale(r.link), true);
  assert.equal(isLocked(r.link), true);
  r = step(r.link, 'connected', 'b');
  assert.equal(r.reload, true);
  assert.equal(r.link.dataHost, 'b');

  // Disconnect returns to Local and reloads.
  r = step(r.link, 'local', undefined);
  assert.deepEqual(r, { link: { dataHost: undefined, phase: 'local' }, reload: true });
  assert.equal(isStale(r.link), false);
  assert.equal(isLocked(r.link), false);
  assert.equal(blockedReason(r.link, undefined), null);

  // A failed first connect never touches the Local data, but is still locked.
  r = step(r.link, 'connecting', 'c');
  assert.equal(r.reload, false);
  assert.equal(isStale(r.link), false);
  assert.equal(isLocked(r.link), true);
  assert.equal(blockedReason(r.link, 'c'), 'Not connected to c. The sessions shown are Local; changes are disabled until c connects or you switch back to Local.');
  assert.equal(step(r.link, 'local', undefined).reload, false);
});

test('filterHosts matches alias, host name and user without case', () => {
  const hosts = [
    { name: 'dev-box', hostName: '10.0.0.5', user: 'alice' },
    { name: 'Prod-API', hostName: 'api.example.com' },
    { name: 'build' },
  ];
  assert.deepEqual(filterHosts(hosts, ''), hosts);
  assert.deepEqual(filterHosts(hosts, '   '), hosts);
  assert.deepEqual(filterHosts(hosts, 'prod').map((h) => h.name), ['Prod-API']);
  assert.deepEqual(filterHosts(hosts, ' EXAMPLE ').map((h) => h.name), ['Prod-API']);
  assert.deepEqual(filterHosts(hosts, 'ALICE').map((h) => h.name), ['dev-box']);
  assert.deepEqual(filterHosts(hosts, 'b').map((h) => h.name), ['dev-box', 'build']);
  assert.deepEqual(filterHosts(hosts, 'nope'), []);
});

test('formatVersion prefixes release versions with v', () => {
  assert.equal(formatVersion('0.3.2'), 'v0.3.2');
  assert.equal(formatVersion(' 1.2.0-beta.1 '), 'v1.2.0-beta.1');
  assert.equal(formatVersion('v0.3.2'), 'v0.3.2');
  assert.equal(formatVersion('dev'), 'dev');
  assert.equal(formatVersion(''), '');
  assert.equal(formatVersion(undefined), '');
});

test('MockBackendAPI appVersion reports a dev build', async () => {
  assert.equal(await new MockBackendAPI().appVersion(), 'dev');
});

test('nextThemeMode cycles system, light, dark', () => {
  assert.equal(nextThemeMode('system'), 'light');
  assert.equal(nextThemeMode('light'), 'dark');
  assert.equal(nextThemeMode('dark'), 'system');
});

test('resolveTheme follows the OS only in system mode', () => {
  assert.equal(resolveTheme('system', true), 'dark');
  assert.equal(resolveTheme('system', false), 'light');
  assert.equal(resolveTheme('light', true), 'light');
  assert.equal(resolveTheme('dark', false), 'dark');
});

test('isThemeMode accepts only known saved values', () => {
  assert.ok(isThemeMode('system'));
  assert.ok(isThemeMode('dark'));
  assert.ok(!isThemeMode('auto'));
  assert.ok(!isThemeMode(null));
});

test('themeButtonTitle names the current mode and the next one', () => {
  assert.equal(themeButtonTitle('system', 'dark'), 'Theme: System (dark). Click for Light');
  assert.equal(themeButtonTitle('light', 'light'), 'Theme: Light. Click for Dark');
  assert.equal(themeButtonTitle('dark', 'dark'), 'Theme: Dark. Click for System');
});

test('resumeButtonTitle tells remote users to paste into a shell on the host', () => {
  assert.equal(resumeButtonTitle(undefined), 'Copy shell command to resume this session');
  assert.equal(
    resumeButtonTitle('plgl'),
    'Copy shell command to resume this session. Run it on plgl: open a shell with "ssh plgl", then paste it.',
  );
});

test('copiedGuidance tells local users to paste into a terminal', () => {
  const resume = copiedGuidance('resume');
  assert.equal(resume.title, 'Resume command copied');
  assert.match(resume.body, /^Paste it into a terminal\./);
  assert.equal(resume.code, undefined);

  const launch = copiedGuidance('launch', { agent: 'codex' });
  assert.equal(launch.title, 'Launch command copied');
  assert.match(launch.body, /Codex starts in the project directory, restores the context and waits/);
});

test('copiedGuidance sends remote users to a shell on the host', () => {
  const launch = copiedGuidance('launch', { host: 'plgl', agent: 'opencode' });
  assert.match(launch.body, /^Open a shell on plgl, then paste it there\. OpenCode starts/);
  assert.equal(launch.code, 'ssh plgl');

  const prompt = copiedGuidance('prompt', { host: 'plgl', agent: 'claude-code' });
  assert.equal(prompt.body, 'Start Claude Code in the project directory on plgl and paste it as your first message.');
  assert.equal(prompt.code, undefined);
});

test('copiedGuidance distinguishes a full document from a launch prompt', () => {
  const doc = copiedGuidance('doc', { host: 'plgl', agent: 'codex' });
  assert.equal(doc.title, 'Document copied');
  assert.equal(doc.body, 'The full handoff document is on your clipboard.');
  assert.equal(doc.code, undefined);
});

test('resumeButtonTitle offers the terminal for local sessions, never over SSH', () => {
  assert.equal(
    resumeButtonTitle(undefined, true),
    'Resume this session in a new terminal window. Use ▾ to copy the command instead',
  );
  assert.match(resumeButtonTitle('plgl', true), /^Copy shell command .* Run it on plgl/);
});

test('copiedGuidance tells local Windows users to paste into PowerShell', () => {
  const resume = copiedGuidance('resume', { shell: 'powershell' });
  assert.match(resume.body, /^Paste it into PowerShell\. It opens the session/);
  const launch = copiedGuidance('launch', { shell: 'powershell', agent: 'codex' });
  assert.match(launch.body, /^Paste it into PowerShell\. Codex starts/);
  // A remote command is built for the host's shell, not the local one.
  const remote = copiedGuidance('resume', { host: 'plgl', shell: 'powershell' });
  assert.match(remote.body, /^Open a shell on plgl, then paste it there\./);
  assert.match(copiedGuidance('resume', { shell: 'posix' }).body, /^Paste it into a terminal\./);
});

test('openedGuidance and openFailed describe Open in terminal', () => {
  assert.equal(openedGuidance('resume').title, 'Opened in a terminal');
  assert.match(openedGuidance('resume').body, /resumes in its directory in a new terminal window/);
  assert.match(openedGuidance('launch', 'opencode').body, /^OpenCode starts in the project directory in a new terminal window/);
  const failed = openFailed(new Error('Codex was not found.'));
  assert.equal(failed.title, "Couldn't open a terminal");
  assert.equal(failed.body, 'Codex was not found.');
  assert.equal(failed.tone, 'error');
});

test('MockBackendAPI opens terminals only when supported and not over SSH', async () => {
  const mock = new MockBackendAPI();
  const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
  assert.deepEqual(await mock.launchInfo(), { terminal: false, shell: 'posix' });
  await assert.rejects(mock.openResumeInTerminal(meta.ref), /not supported on this platform/);

  mock.launch = { terminal: true, shell: 'powershell' };
  await mock.openResumeInTerminal(meta.ref);
  await mock.openHandoffInTerminal({ ref: meta.ref, target: 'codex', budget: 80000, includeReasoning: false, redactSecrets: false });
  assert.equal(mock.openedTerminals.length, 2);
  assert.match(mock.openedTerminals[1], /codex/);

  await mock.connect('dev-box');
  await assert.rejects(mock.openResumeInTerminal(meta.ref), /local and WSL sessions only/);
  assert.equal(mock.openedTerminals.length, 2);
});

test('MockBackendAPI chooses only installed terminals and refuses a missing one', async () => {
  const mock = new MockBackendAPI();
  const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
  assert.equal((await mock.terminalSettings()).choose, false);
  await assert.rejects(mock.setTerminal('kitty'), /not supported/);

  mock.launch = { terminal: true, chooseTerminal: true, shell: 'posix' };
  mock.terminals = [{ id: 'konsole', name: 'Konsole' }, { id: 'kitty', name: 'kitty' }];
  let s = await mock.terminalSettings();
  assert.deepEqual([s.selected, s.auto?.id, s.options.length, s.missing], ['', 'konsole', 2, false]);
  s = await mock.setTerminal('kitty');
  assert.deepEqual([s.selected, s.selectedName, s.missing], ['kitty', 'kitty', false]);
  await assert.rejects(mock.setTerminal('/bin/sh'), /not installed/);
  await mock.openResumeInTerminal(meta.ref);
  assert.equal(mock.openedTerminals.length, 1);

  mock.terminals = [{ id: 'konsole', name: 'Konsole' }];
  s = await mock.terminalSettings();
  assert.deepEqual([s.selected, s.missing], ['kitty', true]);
  await assert.rejects(mock.openResumeInTerminal(meta.ref), /kitty was not found\. Choose another terminal in Settings/);
  assert.equal(mock.openedTerminals.length, 1);
  assert.equal((await mock.setTerminal('')).selected, '');
});

test('terminalOptions names what Automatic opens and keeps a missing choice', () => {
  const base = { choose: true, selected: '', selectedName: '', missing: false, auto: null, options: [] };
  assert.deepEqual(terminalOptions(base), [{ value: '', label: 'Automatic (none found)' }]);
  const found = { ...base, auto: { id: 'ptyxis', name: 'Ptyxis' }, options: [{ id: 'ptyxis', name: 'Ptyxis' }, { id: 'kitty', name: 'kitty' }] };
  assert.deepEqual(terminalOptions(found).map((o) => o.label), ['Automatic (Ptyxis)', 'Ptyxis', 'kitty']);
  const missing = { ...found, selected: 'ghostty', selectedName: 'Ghostty', missing: true };
  assert.deepEqual(terminalOptions(missing).at(-1), { value: 'ghostty', label: 'Ghostty (not found)' });
});

const SETTINGS_DIALOG_URL = new URL('../src/lib/components/common/ManageSettingsDialog.svelte', import.meta.url);
const DROPDOWN_URL = new URL('../src/lib/components/common/Dropdown.svelte', import.meta.url);
let settingsDialogModule;

const TRANSLATE_DEFAULTS = {
  settings: { baseURL: '', model: '', language: 'Vietnamese', apiKeySet: false, configured: false },
  configured: false, error: null, saving: false, testing: false, testResult: null, testError: null,
};

async function renderSettingsDialog(
  launcher,
  preferences = { transcriptMode: 'activity', timeZone: 'utc' },
  { section = 'general', translateSettings = {} } = {},
) {
  if (!settingsDialogModule) {
    globalThis.__dropdown = (await loadServerComponent(DROPDOWN_URL, [])).default;
    const lib = (rel) => JSON.stringify(new URL(rel, SETTINGS_DIALOG_URL).href);
    settingsDialogModule = await loadServerComponent(SETTINGS_DIALOG_URL, [
      ["'../../format'", lib('../../format.ts')],
      ["'../../terminal'", lib('../../terminal.ts')],
      ["'../../transcript'", lib('../../transcript.ts')],
      ["'../../date'", lib('../../date.ts')],
      ["import Dropdown from './Dropdown.svelte';", 'const Dropdown = globalThis.__dropdown;'],
      ["import { manage } from '../../stores/manage.svelte';", fixtureStore('manage')],
      ["import { launcher } from '../../stores/launcher.svelte';", fixtureStore('launcher')],
      ["import { translateSettings } from '../../stores/translate.svelte';", fixtureStore('translateSettings')],
      ["import { preferences } from '../../stores/preferences.svelte';", fixtureStore('preferences')],
    ]);
  }
  const manage = {
    settingsDialogOpen: true, settingsSection: section, settings: { enabled: false, allowPermanentDelete: false }, loadingSettings: false,
    settingsError: null, firstEnableWarningVisible: false, handoffCache: null, handoffCacheError: null, handoffCacheBusy: false,
  };
  return renderWithFixture(settingsDialogModule, {
    manage,
    launcher: { terminalError: null, terminal: null, ...launcher },
    translateSettings: { ...TRANSLATE_DEFAULTS, ...translateSettings },
    preferences,
  });
}

test('Settings shows the default transcript display level', async () => {
  const info = { terminal: true, chooseTerminal: false, shell: 'powershell' };
  const html = await renderSettingsDialog({ info }, { transcriptMode: 'chat', timeZone: 'utc' });
  assert.match(html, /<h3[^>]*>Transcript<\/h3>/);
  assert.match(html, /aria-label="Default transcript display"/);
  assert.match(html, />Chat</);
  assert.match(html, /User messages and assistant answers.*The header buttons change it for the open session only\./);
});

test('Settings shows the time zone used for timestamps', async () => {
  const info = { terminal: true, chooseTerminal: false, shell: 'powershell' };
  const utc = await renderSettingsDialog({ info });
  assert.match(utc, /<h3[^>]*>Time zone<\/h3>/);
  assert.match(utc, /aria-label="Time zone for timestamps"/);
  const local = await renderSettingsDialog({ info }, { transcriptMode: 'activity', timeZone: 'local' });
  assert.match(local, /Local time \(this computer\)/);
});

test('Settings shows the terminal choice only where it can be chosen', async () => {
  const windows = await renderSettingsDialog({ info: { terminal: true, chooseTerminal: false, shell: 'powershell' } }, undefined, { section: 'terminal' });
  assert.match(windows, /<h2 id="settings-title"[^>]*>Settings<\/h2>/);
  assert.doesNotMatch(windows, /Terminal \(this computer\)|>Terminal</);
  // Without a terminal section, Settings falls back to General.
  assert.match(windows, /<h3[^>]*>Transcript<\/h3>/);

  const info = { terminal: true, chooseTerminal: true, shell: 'posix' };
  const terminal = {
    choose: true, selected: '', selectedName: '', missing: false,
    auto: { id: 'konsole', name: 'Konsole' }, options: [{ id: 'konsole', name: 'Konsole' }],
  };
  const auto = await renderSettingsDialog({ info, terminal }, undefined, { section: 'terminal' });
  assert.match(auto, /Terminal \(this computer\)/);
  assert.match(auto, /Automatic \(Konsole\)/);
  assert.doesNotMatch(auto, /no longer installed|macOS/);

  const missing = await renderSettingsDialog({ info, terminal: { ...terminal, selected: 'kitty', selectedName: 'kitty', missing: true } }, undefined, { section: 'terminal' });
  assert.match(missing, /kitty \(not found\)/);
  assert.match(missing, /kitty is no longer installed/);

  const none = await renderSettingsDialog({ info, terminal: { ...terminal, auto: null, options: [] } }, undefined, { section: 'terminal' });
  assert.match(none, /Automatic \(none found\)/);
  assert.match(none, /No supported terminal app was found/);

  const mac = await renderSettingsDialog({ info, terminal: { ...terminal, hint: 'The first time, macOS asks whether Agent Sessions may control the terminal app.' } }, undefined, { section: 'terminal' });
  assert.match(mac, /macOS asks whether Agent Sessions may control/);

  const failed = await renderSettingsDialog({ info, terminalError: 'read settings: permission denied' }, undefined, { section: 'terminal' });
  assert.match(failed, /read settings: permission denied/);
  assert.doesNotMatch(failed, /Looking for terminal apps/);
});

test('Settings lists its sections in a side navigation', async () => {
  const info = { terminal: true, chooseTerminal: true, shell: 'posix' };
  const html = await renderSettingsDialog({ info });
  const tabs = [...html.matchAll(/role="tab"[^>]*>([^<]+)<\/button>/g)].map((m) => m[1]);
  assert.deepEqual(tabs, ['General', 'Translation', 'Terminal', 'Session management', 'Handoff files']);
  assert.match(html, /aria-selected="true"[^>]*>General</);
  assert.doesNotMatch(html, /Enable session management|Handoff files<\/h3>/);

  const management = await renderSettingsDialog({ info }, undefined, { section: 'management' });
  assert.match(management, /Enable session management/);
  assert.match(management, /Allow permanent deletion/);
  assert.doesNotMatch(management, /<h3[^>]*>Transcript<\/h3>/);

  const handoff = await renderSettingsDialog({ info }, undefined, { section: 'handoff' });
  assert.match(handoff, /<h3[^>]*>Handoff files<\/h3>/);
  assert.match(handoff, /Checking…/);
});

test('Settings Translation shows a saved key as Saved, never the key', async () => {
  const info = { terminal: true, chooseTerminal: false, shell: 'powershell' };
  const empty = await renderSettingsDialog({ info }, undefined, { section: 'translation' });
  assert.match(empty, /<h3[^>]*>Translation<\/h3>/);
  assert.match(empty, /type="password"/);
  assert.match(empty, /Message text is sent to this provider from this computer/);
  assert.match(empty, /<button[^>]*disabled[^>]*>Test<\/button>/);

  const settings = { baseURL: 'https://api.example.com/v1', model: 'gpt-test', language: 'French', apiKeySet: true, configured: true };
  const saved = await renderSettingsDialog({ info }, undefined, { section: 'translation', translateSettings: { settings, configured: true } });
  assert.match(saved, /Saved/);
  assert.match(saved, />Replace</);
  assert.match(saved, />Clear</);
  assert.doesNotMatch(saved, /type="password"/);
  assert.doesNotMatch(saved, /<button[^>]*disabled[^>]*>Test<\/button>/);

  const failed = await renderSettingsDialog({ info }, undefined, { section: 'translation', translateSettings: { testError: 'translate: 401 Unauthorized: bad key' } });
  assert.match(failed, /401 Unauthorized: bad key/);
});

const SESSION_HEADER_URL = new URL('../src/lib/components/transcript/SessionHeader.svelte', import.meta.url);
let sessionHeaderModule;

async function renderSessionHeader(fixture, props = {}) {
  const lib = (rel) => JSON.stringify(new URL(rel, SESSION_HEADER_URL).href);
  sessionHeaderModule ??= await loadServerComponent(SESSION_HEADER_URL, [
    ["'../../format'", lib('../../format.ts')],
    ["'../../date'", lib('../../date.ts')],
    ["'../../portable'", lib('../../portable.ts')],
    ["'../../link'", lib('../../link.ts')],
    ["'../../transcript'", lib('../../transcript.ts')],
    ["import AgentIcon from '../common/AgentIcon.svelte';", 'const AgentIcon = () => {};'],
    ["import { appState } from '../../stores/appState.svelte';", fixtureStore('appState')],
    ["import { manage } from '../../stores/manage.svelte';", fixtureStore('manage')],
    ["import { handoff } from '../../stores/handoff.svelte';", fixtureStore('handoff')],
    ["import { exporter } from '../../stores/export.svelte';", fixtureStore('exporter')],
    ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
    ["import { launcher } from '../../stores/launcher.svelte';", fixtureStore('launcher')],
    ["import { preferences } from '../../stores/preferences.svelte';", fixtureStore('preferences')],
  ]);
  const meta = {
    ref: { agent: 'codex', id: 's1' },
    title: 'Fix the build',
    cwd: 'C:\\work\\proj',
    counts: { user: 1, assistant: 1 },
    tokens: { input: 10, output: 5 },
    createdAt: '2026-10-01T10:00:00Z',
    updatedAt: '2026-10-01T11:00:00Z',
  };
  return renderWithFixture(
    sessionHeaderModule,
    { appState: {}, manage: { settings: { enabled: false } }, handoff: {}, exporter: {}, preferences: { timeZone: 'utc' }, ...fixture },
    { meta, mode: 'activity', onModeChange() {}, onResume() {}, onReveal() {}, onDelete() {}, ...props },
  );
}

test('SessionHeader shows the selected transcript display mode', async () => {
  const html = await renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: false } }, { mode: 'chat' });
  assert.match(html, /aria-label="Transcript display"/);
  assert.match(html, /class="action-btn mode-btn[^"]* active"[^>]*aria-pressed="true"[^>]*>\s*Chat/);
  assert.match(html, /aria-pressed="false"[^>]*>\s*Activity/);
  assert.match(html, /aria-pressed="false"[^>]*>\s*All/);
  assert.doesNotMatch(html, /Show Meta|Hide Meta/);
});

test('SessionHeader splits Resume into open and copy when a terminal is available', async () => {
  const split = await renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: true } });
  assert.match(split, /class="action-btn resume-btn resume-main[^"]*"[^>]*title="Resume this session in a new terminal window/);
  assert.match(split, /aria-label="More ways to resume"/);

  const opening = await renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: true } }, { resumeOpening: true });
  assert.match(opening, /resume-main[^"]*"[^>]*disabled[^>]*>\s*Opening…/);

  // Without a terminal, or for a remote host's session, Resume only copies.
  for (const fixture of [
    { link: { dataHost: undefined }, launcher: { canOpen: false } },
    { link: { dataHost: 'plgl' }, launcher: { canOpen: false } },
  ]) {
    const html = await renderSessionHeader(fixture);
    assert.doesNotMatch(html, /More ways to resume/);
    assert.match(html, /title="Copy shell command to resume this session/);
  }
});

const HANDOFF_DIALOG_URL = new URL('../src/lib/components/common/HandoffDialog.svelte', import.meta.url);
let handoffDialogModule;

async function renderHandoffDialog(fixture) {
  const lib = (rel) => JSON.stringify(new URL(rel, HANDOFF_DIALOG_URL).href);
  globalThis.__remoteCommandNote ??= (await loadRemoteCommandNote()).default;
  handoffDialogModule ??= await loadServerComponent(HANDOFF_DIALOG_URL, [
    ["'../../portable'", lib('../../portable.ts')],
    ["'../../format'", lib('../../format.ts')],
    ["import AgentIcon from './AgentIcon.svelte';", 'const AgentIcon = () => {};'],
    ["import RemoteCommandNote from './RemoteCommandNote.svelte';", 'const RemoteCommandNote = globalThis.__remoteCommandNote;'],
    ["import MarkdownDoc from './MarkdownDoc.svelte';", 'const MarkdownDoc = () => {};'],
    ["import { handoff } from '../../stores/handoff.svelte';", fixtureStore('handoff')],
    ["import { link } from '../../stores/link.svelte';", fixtureStore('link')],
    ["import { launcher } from '../../stores/launcher.svelte';", fixtureStore('launcher')],
  ]);
  const handoff = {
    dialogOpen: true,
    session: { ref: { agent: 'claude-code', id: 's1' }, cwd: 'C:\\work' },
    target: 'codex',
    budget: 80000,
    cwd: 'C:\\work',
    launching: false,
    preview: {
      command: "Set-Location -LiteralPath C:\\work -ErrorAction Stop; codex 'Read it'",
      promptMarkdown: 'Read it',
      fullMarkdown: '# Handoff',
      promptBytes: 7,
      report: { estimatedTokens: 100, trimmed: false, droppedItems: [] },
    },
    ...fixture.handoff,
  };
  return renderWithFixture(handoffDialogModule, { link: { dataHost: undefined }, ...fixture, handoff });
}

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

test('copyFailed reports the error as an error toast', () => {
  const t = copyFailed('resume command', new Error('not connected'));
  assert.equal(t.title, "Couldn't copy resume command");
  assert.equal(t.body, 'not connected');
  assert.equal(t.tone, 'error');
});

// --- WSL distributions as hosts ---

test('host helpers name WSL distributions and the shell that opens them', () => {
  assert.equal(wslDistro('wsl:Ubuntu'), 'Ubuntu');
  assert.equal(wslDistro('wsl:Ubuntu-22.04'), 'Ubuntu-22.04');
  for (const host of [undefined, '', 'wsl:', 'plgl', 'wsl-box', 'my-wsl:Ubuntu']) {
    assert.equal(wslDistro(host), undefined, `${host} is not a WSL distribution`);
  }

  assert.equal(hostLabel('wsl:Ubuntu'), 'Ubuntu (WSL)');
  assert.equal(hostLabel('wsl:Ubuntu-22.04'), 'Ubuntu-22.04 (WSL)');
  assert.equal(hostLabel('plgl'), 'plgl');
  assert.equal(hostLabel('wsl-box'), 'wsl-box');

  assert.equal(shellCommand('wsl:Ubuntu'), 'wsl -d Ubuntu');
  assert.equal(shellCommand('wsl:Ubuntu-22.04'), 'wsl -d Ubuntu-22.04');
  assert.equal(shellCommand('plgl'), 'ssh plgl');
  assert.equal(shellCommand('wsl-box'), 'ssh wsl-box');
});

test('canOpenTerminal gates Open in terminal for local, WSL, SSH and older backends', () => {
  const cases = [
    // [name, launch info, data host, expected]
    ['local with a terminal', { terminal: true, shell: 'posix' }, undefined, true],
    ['local without a terminal', { terminal: false, shell: 'posix' }, undefined, false],
    ['local ignores WSL support', { terminal: false, wsl: true, shell: 'powershell' }, undefined, false],
    ['WSL with a terminal', { terminal: true, wsl: true, shell: 'powershell' }, 'wsl:Ubuntu', true],
    ['WSL without a local terminal', { terminal: false, wsl: true, shell: 'powershell' }, 'wsl:Ubuntu', true],
    ['WSL not installed', { terminal: true, wsl: false, shell: 'powershell' }, 'wsl:Ubuntu', false],
    ['older backend without wsl', { terminal: true, shell: 'powershell' }, 'wsl:Ubuntu', false],
    ['empty WSL distribution', { terminal: true, wsl: true, shell: 'powershell' }, 'wsl:', false],
    ['SSH host', { terminal: true, wsl: true, shell: 'powershell' }, 'plgl', false],
    ['SSH host named like WSL', { terminal: true, wsl: true, shell: 'powershell' }, 'wsl-box', false],
    ['SSH from an older backend', { terminal: true, shell: 'posix' }, 'plgl', false],
  ];
  for (const [name, info, host, expected] of cases) {
    assert.equal(canOpenTerminal(info, host), expected, name);
  }
});

const LAUNCHER_STORE_URL = new URL('../src/lib/stores/launcher.svelte.ts', import.meta.url);
let launcherStoreModule;

// The real LauncherStore with plain api and link fixtures.
async function loadLauncherStore() {
  const lib = (rel) => JSON.stringify(new URL(rel, LAUNCHER_STORE_URL).href);
  launcherStoreModule ??= await loadServerModule(LAUNCHER_STORE_URL, [
    ["import { api } from '../api';", fixtureStore('api', '__storeFixture')],
    ["'../manage'", lib('../manage.ts')],
    ["import { link } from './link.svelte';", fixtureStore('link', '__storeFixture')],
    ["'../terminal'", lib('../terminal.ts')],
  ]);
  return launcherStoreModule;
}

test('LauncherStore offers Open in terminal for local and WSL data only', async () => {
  const { LauncherStore } = await loadLauncherStore();
  const run = async (launchInfo, dataHost) => {
    globalThis.__storeFixture = { api: { launchInfo }, link: { dataHost } };
    try {
      const store = new LauncherStore();
      await store.init();
      return { canOpen: store.canOpen, copyShell: store.copyShell };
    } finally {
      delete globalThis.__storeFixture;
    }
  };
  const windows = async () => ({ terminal: true, wsl: true, shell: 'powershell' });
  assert.deepEqual(await run(windows, undefined), { canOpen: true, copyShell: 'powershell' });
  assert.deepEqual(await run(windows, 'wsl:Ubuntu'), { canOpen: true, copyShell: 'posix' });
  assert.deepEqual(await run(windows, 'plgl'), { canOpen: false, copyShell: 'posix' });
  // An older backend reports no wsl field, or lacks launchInfo entirely.
  assert.equal((await run(async () => ({ terminal: true, shell: 'powershell' }), 'wsl:Ubuntu')).canOpen, false);
  const missing = async () => { throw new Error('ListWSLDistros is not a function'); };
  assert.deepEqual(await run(missing, undefined), { canOpen: false, copyShell: 'posix' });
  assert.equal((await run(missing, 'wsl:Ubuntu')).canOpen, false);
});

const CONNECTION_STORE_URL = new URL('../src/lib/stores/connection.svelte.ts', import.meta.url);
let connectionStoreModule;

// The real ConnectionStore with fixture api and stores; nothing global.
async function loadConnectionStore() {
  const lib = (rel) => JSON.stringify(new URL(rel, CONNECTION_STORE_URL).href);
  const store = (name, file) => [`import { ${name} } from './${file}.svelte';`, fixtureStore(name, '__storeFixture')];
  connectionStoreModule ??= await loadServerModule(CONNECTION_STORE_URL, [
    [
      "import { api, subscribeConnectionState, subscribeAskpassPrompt } from '../api';",
      fixtureStore('api', '__storeFixture') +
        'const subscribeConnectionState = () => () => {}; const subscribeAskpassPrompt = () => () => {};',
    ],
    ["'../tree'", lib('../tree.ts')],
    ["'../hosts'", lib('../hosts.ts')],
    store('appState', 'appState'),
    store('manage', 'manage'),
    store('search', 'search'),
    store('handoff', 'handoff'),
    store('exporter', 'export'),
    store('importer', 'importer'),
    store('link', 'link'),
  ]);
  return connectionStoreModule;
}

function deferred() {
  let resolve, reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

async function withStoreFixture(api, fn) {
  globalThis.__storeFixture = { api, link: {} };
  try {
    return await fn();
  } finally {
    delete globalThis.__storeFixture;
  }
}

test('ConnectionStore lists WSL distributions and SSH hosts independently', async () => {
  const { ConnectionStore } = await loadConnectionStore();
  const distros = [{ name: 'Ubuntu', host: 'wsl:Ubuntu', default: true }, { name: 'Debian', host: 'wsl:Debian' }];

  // SSH config fails: the WSL section still fills.
  await withStoreFixture({
    listHosts: async () => { throw new Error('read ~/.ssh/config: permission denied'); },
    listWSLDistros: async () => distros,
  }, async () => {
    const store = new ConnectionStore();
    await store.refreshHosts();
    assert.equal(store.hostsError, 'read ~/.ssh/config: permission denied');
    assert.deepEqual(store.hosts, []);
    assert.deepEqual(store.wslDistros, distros);
    assert.equal(store.loadingHosts, false);
  });

  // WSL listing fails: the SSH hosts list without an error.
  await withStoreFixture({
    listHosts: async () => [{ name: 'plgl' }],
    listWSLDistros: async () => { throw new Error('wsl.exe failed'); },
  }, async () => {
    const store = new ConnectionStore();
    store.wslDistros = distros;
    await store.refreshHosts();
    assert.equal(store.hostsError, null);
    assert.deepEqual(store.hosts, [{ name: 'plgl' }]);
    assert.deepEqual(store.wslDistros, [], 'a failed listing drops stale distributions');
  });

  // Neither waits for the other: each list lands as soon as it arrives.
  const ssh = deferred();
  const wsl = deferred();
  await withStoreFixture({ listHosts: () => ssh.promise, listWSLDistros: () => wsl.promise }, async () => {
    const store = new ConnectionStore();
    const refreshing = store.refreshHosts();
    assert.equal(store.loadingHosts, true);
    wsl.resolve(distros);
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(store.wslDistros, distros, 'WSL lands while SSH is still loading');
    assert.equal(store.loadingHosts, true);
    ssh.resolve(null);
    await refreshing;
    assert.deepEqual(store.hosts, []);
    assert.equal(store.loadingHosts, false);
  });

  // An older backend's api reports no distributions.
  await withStoreFixture({ listHosts: async () => [{ name: 'plgl' }], listWSLDistros: async () => null }, async () => {
    const store = new ConnectionStore();
    await store.refreshHosts();
    assert.deepEqual(store.wslDistros, []);
    assert.deepEqual(store.hosts, [{ name: 'plgl' }]);
  });
});

test('ConnectionStore shows a WSL host by its distribution label', async () => {
  const { ConnectionStore } = await loadConnectionStore();
  const store = new ConnectionStore();
  assert.equal(store.currentHost, 'Local');
  store.host = 'wsl:Ubuntu';
  assert.equal(store.currentHost, 'Ubuntu (WSL)');
  store.host = 'plgl';
  assert.equal(store.currentHost, 'plgl');
});

const HOST_SELECTOR_URL = new URL('../src/lib/components/sidebar/HostSelector.svelte', import.meta.url);
let hostSelectorModule;

// Server-renders the real HostSelector with its menu open: the compiled
// initial state is flipped here, in the test, not by a production prop.
async function renderHostSelector(connectionStore) {
  hostSelectorModule ??= await loadServerComponent(HOST_SELECTOR_URL, [
    ["import { connectionStore } from '../../stores/connection.svelte';", fixtureStore('connectionStore')],
    ["import HostEnvDialog from '../common/HostEnvDialog.svelte';", 'const HostEnvDialog = () => {};'],
    ["'../../link'", JSON.stringify(new URL('../../link.ts', HOST_SELECTOR_URL).href)],
    ['let menuOpen = false;', 'let menuOpen = true;'],
  ]);
  return renderWithFixture(hostSelectorModule, { connectionStore });
}

// A real ConnectionStore whose lists come from the mock backend.
async function hostSelectorStore(mock, state = {}) {
  const { ConnectionStore } = await loadConnectionStore();
  const store = new ConnectionStore();
  await withStoreFixture(mock, () => store.refreshHosts());
  Object.assign(store, state);
  return store;
}

function textOf(html) {
  return unescapeHTML(html.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ')).trim();
}

test('HostSelector lists WSL distributions above SSH hosts', async () => {
  const html = await renderHostSelector(await hostSelectorStore(new MockBackendAPI()));
  const text = textOf(html);
  const wsl = text.indexOf('WSL 🐧 Ubuntu');
  const ssh = text.indexOf('SSH Hosts');
  assert.ok(text.indexOf('Local') < wsl && wsl >= 0 && wsl < ssh, text);
  assert.match(text, /Local ✓ WSL 🐧 Ubuntu Default distribution ⚙ 🐧 Debian ⚙ SSH Hosts/);
  assert.equal(text.split('Default distribution').length - 1, 1, 'only the default distribution is marked');
  assert.ok(text.indexOf('dev-box') > ssh && text.indexOf('prod-server') > ssh);
  assert.match(html, /aria-label="Configure environment overrides for Ubuntu"/);
  assert.match(html, /aria-label="Configure environment overrides for Debian"/);
  assert.equal(html.split('title="Configure environment overrides"').length - 1, 4, 'two WSL and two SSH env buttons');
  assert.match(html, /placeholder="Connect to SSH alias…"/);
  assert.doesNotMatch(html, /wsl:Ubuntu|wsl:Debian/, 'raw wsl: hosts stay out of the menu');
  assert.match(html, /<span class="host-name[^"]*">Local<\/span>/);
});

test('HostSelector shows a connected distribution by label and checks its row', async () => {
  const store = await hostSelectorStore(new MockBackendAPI(), { phase: 'connected', host: 'wsl:Debian' });
  const html = await renderHostSelector(store);
  assert.match(html, /<span class="host-name[^"]*">Debian \(WSL\)<\/span>/);
  assert.match(html, /title="Connected to Debian \(WSL\)"/);
  assert.match(textOf(html), /💻 Local WSL 🐧 Ubuntu Default distribution ⚙ 🐧 Debian ⚙ ✓ SSH Hosts/);
  assert.equal(textOf(html).split('✓').length - 1, 1, 'only the connected distribution is checked');
  assert.match(textOf(html), /Disconnect/);
});

test('HostSelector leaves out the WSL section when there are no distributions', async () => {
  const mock = new MockBackendAPI();
  mock.setMockWSLDistros([]);
  const html = await renderHostSelector(await hostSelectorStore(mock));
  const text = textOf(html);
  assert.doesNotMatch(text, /\bWSL\b|Default distribution|Ubuntu|Debian/);
  assert.match(text, /Local ✓ SSH Hosts ↻ 🌐 dev-box/);

  // A failed SSH listing is shown in the SSH section; WSL still lists.
  const failed = await hostSelectorStore({
    listHosts: async () => { throw new Error('ssh config unreadable'); },
    listWSLDistros: async () => [{ name: 'Ubuntu', host: 'wsl:Ubuntu', default: true }],
  });
  const failedText = textOf(await renderHostSelector(failed));
  assert.match(failedText, /WSL 🐧 Ubuntu Default distribution ⚙ SSH Hosts ↻ ssh config unreadable Go/);
  assert.doesNotMatch(failedText, /No SSH config hosts found/);
});

const REMOTE_COMMAND_NOTE_URL = new URL('../src/lib/components/common/RemoteCommandNote.svelte', import.meta.url);
let remoteCommandNoteModule;

async function loadRemoteCommandNote() {
  remoteCommandNoteModule ??= await loadServerComponent(REMOTE_COMMAND_NOTE_URL, [
    ["'../../hosts'", JSON.stringify(new URL('../../hosts.ts', REMOTE_COMMAND_NOTE_URL).href)],
  ]);
  return remoteCommandNoteModule;
}

test('RemoteCommandNote opens a WSL shell with wsl -d and an SSH host with ssh', async () => {
  const note = await loadRemoteCommandNote();
  const wsl = await renderWithFixture(note, {}, { host: 'wsl:Ubuntu' });
  assert.match(wsl, /Run this on <strong>Ubuntu \(WSL\)<\/strong>/);
  assert.match(wsl, /<code[^>]*>wsl -d Ubuntu<\/code>/);
  assert.doesNotMatch(wsl, /ssh|wsl:Ubuntu/);

  const ssh = await renderWithFixture(note, {}, { host: 'plgl' });
  assert.match(ssh, /Run this on <strong>plgl<\/strong>/);
  assert.match(ssh, /<code[^>]*>ssh plgl<\/code>/);
  assert.doesNotMatch(ssh, /wsl/);
});

test('guidance names a WSL distribution and opens its shell with wsl -d', () => {
  const launch = copiedGuidance('launch', { host: 'wsl:Ubuntu', agent: 'codex', shell: 'powershell' });
  assert.match(launch.body, /^Open a shell on Ubuntu \(WSL\), then paste it there\. Codex starts/);
  assert.equal(launch.code, 'wsl -d Ubuntu');
  const resume = copiedGuidance('resume', { host: 'wsl:Ubuntu' });
  assert.match(resume.body, /^Open a shell on Ubuntu \(WSL\), then paste it there\./);
  assert.equal(resume.code, 'wsl -d Ubuntu');
  const prompt = copiedGuidance('prompt', { host: 'wsl:Ubuntu', agent: 'claude-code' });
  assert.equal(prompt.body, 'Start Claude Code in the project directory on Ubuntu (WSL) and paste it as your first message.');

  assert.equal(
    resumeButtonTitle('wsl:Ubuntu', true),
    'Resume this session in a new terminal window. Use ▾ to copy the command instead',
  );
  assert.equal(
    resumeButtonTitle('wsl:Ubuntu'),
    'Copy shell command to resume this session. Run it on Ubuntu (WSL): open a shell with "wsl -d Ubuntu", then paste it.',
  );
  assert.equal(
    resumeButtonTitle('plgl', true),
    'Copy shell command to resume this session. Run it on plgl: open a shell with "ssh plgl", then paste it.',
  );
});

test('connection banner and blocked reasons name a WSL distribution by label', async () => {
  const failed = connectionBanner({ phase: 'disconnected', host: 'wsl:Ubuntu', error: 'wsl.exe: distribution not found' });
  assert.equal(failed.host, 'Ubuntu (WSL)');
  assert.equal(`${failed.before}${failed.host}${failed.after}`, 'Could not connect to Ubuntu (WSL).');
  const lost = connectionBanner({ phase: 'reconnecting', host: 'wsl:Ubuntu', error: CONNECTION_LOST });
  assert.equal(`${lost.before}${lost.host}${lost.after}`, 'Connection to Ubuntu (WSL) lost. Automatically reconnecting…');
  assert.equal(connectionBanner({ phase: 'disconnected' }).host, 'the host');

  const html = await renderReconnectBanner({ phase: 'disconnected', host: 'wsl:Ubuntu', error: 'wsl.exe: distribution not found' });
  assert.match(html, /Could not connect to <strong>Ubuntu \(WSL\)<\/strong>\./);
  assert.doesNotMatch(html, /wsl:Ubuntu/);

  // Switching from Local to a distribution locks the Local data.
  let r = nextLink({ dataHost: undefined, phase: 'local' }, 'connecting', 'wsl:Debian');
  let link = { dataHost: r.dataHost, phase: 'connecting' };
  assert.equal(
    blockedReason(link, 'wsl:Debian'),
    'Not connected to Debian (WSL). The sessions shown are Local; changes are disabled until Debian (WSL) connects or you switch back to Local.',
  );
  // A dropped distribution keeps its data stale.
  r = nextLink({ ...link, phase: 'connecting' }, 'connected', 'wsl:Debian');
  link = { dataHost: r.dataHost, phase: 'disconnected' };
  assert.equal(link.dataHost, 'wsl:Debian');
  assert.equal(blockedReason(link, 'wsl:Debian'), 'Not connected to Debian (WSL). Changes are disabled until it reconnects.');
});

test('SessionHeader offers Open in terminal for a WSL session where the computer can open it', async () => {
  const wslInfo = { terminal: false, wsl: true, shell: 'powershell' };
  const split = await renderSessionHeader({
    link: { dataHost: 'wsl:Ubuntu' },
    launcher: { canOpen: canOpenTerminal(wslInfo, 'wsl:Ubuntu') },
  });
  assert.match(split, /class="action-btn resume-btn resume-main[^"]*"[^>]*title="Resume this session in a new terminal window/);
  assert.match(split, /aria-label="More ways to resume"/);

  // An older backend without WSL support only copies, with WSL guidance.
  const older = await renderSessionHeader({
    link: { dataHost: 'wsl:Ubuntu' },
    launcher: { canOpen: canOpenTerminal({ terminal: true, shell: 'powershell' }, 'wsl:Ubuntu') },
  });
  assert.doesNotMatch(older, /More ways to resume/);
  assert.match(unescapeHTML(older), /title="Copy shell command to resume this session\. Run it on Ubuntu \(WSL\): open a shell with "wsl -d Ubuntu", then paste it\."/);
});

test('HandoffDialog offers Open in Terminal for a WSL session and notes wsl -d', async () => {
  const wsl = await renderHandoffDialog({
    link: { dataHost: 'wsl:Ubuntu' },
    launcher: { canOpen: canOpenTerminal({ terminal: false, wsl: true, shell: 'powershell' }, 'wsl:Ubuntu') },
  });
  assert.match(wsl, /class="btn primary-btn[^"]*"[^>]*>\s*Open in Terminal/);
  assert.match(wsl, /Run this on <strong>Ubuntu \(WSL\)<\/strong>/);
  assert.match(wsl, /<code[^>]*>wsl -d Ubuntu<\/code>/);

  const ssh = await renderHandoffDialog({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: canOpenTerminal({ terminal: true, wsl: true, shell: 'powershell' }, 'plgl') },
  });
  assert.doesNotMatch(ssh, /Open in Terminal/);
  assert.match(ssh, /<code[^>]*>ssh plgl<\/code>/);

  const local = await renderHandoffDialog({ launcher: { canOpen: true } });
  assert.doesNotMatch(local, /Run this on/);
});

test('MockBackendAPI lists WSL distributions and opens a connected one in a terminal', async () => {
  const mock = new MockBackendAPI();
  const distros = await mock.listWSLDistros();
  assert.deepEqual(distros, [
    { name: 'Ubuntu', host: 'wsl:Ubuntu', default: true },
    { name: 'Debian', host: 'wsl:Debian' },
  ]);
  distros[0].name = 'changed';
  assert.equal((await mock.listWSLDistros())[0].name, 'Ubuntu', 'callers get copies');
  for (const d of await mock.listWSLDistros()) {
    assert.equal(wslDistro(d.host), d.name);
  }

  const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
  mock.launch = { terminal: false, wsl: true, shell: 'powershell' };
  await mock.connect('wsl:Ubuntu');
  assert.deepEqual(
    [(await mock.connectionState()).phase, (await mock.connectionState()).host],
    ['connected', 'wsl:Ubuntu'],
  );
  // WSL opens without a local terminal app.
  await mock.openResumeInTerminal(meta.ref);
  await mock.openHandoffInTerminal({ ref: meta.ref, target: 'codex', budget: 80000, includeReasoning: false, redactSecrets: false });
  assert.equal(mock.openedTerminals.length, 2);

  // A dropped distribution refuses until it reconnects.
  mock.setMockConnectionState({ phase: 'reconnecting' });
  await assert.rejects(mock.openResumeInTerminal(meta.ref), /remote disconnected/);
  mock.setMockConnectionState({ phase: 'connected' });
  mock.launch = { terminal: true, wsl: false, shell: 'powershell' };
  await assert.rejects(mock.openResumeInTerminal(meta.ref), /WSL is not installed/);
  assert.equal(mock.openedTerminals.length, 2);

  // SSH never opens, even where WSL and a terminal do.
  mock.launch = { terminal: true, wsl: true, shell: 'powershell' };
  await mock.connect('dev-box');
  await assert.rejects(mock.openResumeInTerminal(meta.ref), /local and WSL sessions only/);
  assert.equal(mock.openedTerminals.length, 2);

  await mock.disconnect();
  await mock.openResumeInTerminal(meta.ref);
  assert.equal(mock.openedTerminals.length, 3);

  mock.setMockWSLDistros([]);
  assert.deepEqual(await mock.listWSLDistros(), []);
});

test('Wails listWSLDistros reports no distributions on an older backend', async () => {
  const hadWindow = 'window' in globalThis;
  const previous = globalThis.window;
  const App = {};
  globalThis.window = { go: { app: { App } } };
  try {
    const backend = createAPI();
    assert.ok(!(backend instanceof MockBackendAPI));
    assert.deepEqual(await backend.listWSLDistros(), [], 'binding missing');
    App.ListWSLDistros = async () => null;
    assert.deepEqual(await backend.listWSLDistros(), [], 'null list');
    App.ListWSLDistros = async () => [{ name: 'Ubuntu', host: 'wsl:Ubuntu', default: true }];
    assert.deepEqual(await backend.listWSLDistros(), [{ name: 'Ubuntu', host: 'wsl:Ubuntu', default: true }]);
    App.ListWSLDistros = () => Promise.reject('wsl.exe failed');
    await assert.rejects(backend.listWSLDistros(), (err) => err instanceof Error && err.message === 'wsl.exe failed');
  } finally {
    if (hadWindow) globalThis.window = previous;
    else delete globalThis.window;
  }
});

test('isTranscriptMode accepts only the display levels', () => {
  for (const mode of ['chat', 'activity', 'all']) assert.equal(isTranscriptMode(mode), true, mode);
  for (const value of [null, '', 'meta']) assert.equal(isTranscriptMode(value), false, String(value));
});

test('visibleTranscriptParts filters chat, activity and all display levels', () => {
  const text = { kind: 'text', text: 'Answer' };
  const blank = { kind: 'text', text: ' \n' };
  const thinking = { kind: 'reasoning', text: 'Thinking' };
  const tool = { kind: 'tool', tool: { id: 'call', name: 'Read', status: 'completed' } };
  const file = { kind: 'file', file: { name: 'image.png' } };
  const error = { kind: 'notice', text: 'Assistant error' };
  const agentNotice = { kind: 'notice', text: 'Agent: build' };
  const assistant = { id: 'a', role: 'assistant', time: '', parts: [thinking, text, blank, tool, error, agentNotice] };
  const user = { id: 'u', role: 'user', time: '', parts: [text, file] };
  const toolOnly = { id: 't', role: 'assistant', time: '', parts: [thinking, tool] };
  const meta = { id: 'm', role: 'user', time: '', isMeta: true, parts: [text] };
  const system = { id: 's', role: 'system', time: '', parts: [{ kind: 'compaction', text: 'Summary' }] };

  assert.deepEqual(visibleTranscriptParts(assistant, 'chat'), [text, error]);
  assert.deepEqual(visibleTranscriptParts(user, 'chat'), [text, file]);
  assert.deepEqual(visibleTranscriptParts(toolOnly, 'chat'), []);
  assert.deepEqual(visibleTranscriptParts(system, 'chat'), []);
  assert.deepEqual(visibleTranscriptParts(meta, 'chat'), []);

  assert.deepEqual(visibleTranscriptParts(toolOnly, 'activity'), toolOnly.parts);
  assert.deepEqual(visibleTranscriptParts(system, 'activity'), system.parts);
  assert.deepEqual(visibleTranscriptParts(meta, 'activity'), []);
  assert.deepEqual(visibleTranscriptParts(meta, 'all'), meta.parts);

  // A search target shows in full, whatever the selected level.
  assert.deepEqual(visibleTranscriptParts(toolOnly, 'chat', true), toolOnly.parts);
  const empty = { id: 'n', role: 'assistant', time: '', parts: null };
  for (const mode of ['chat', 'activity', 'all']) assert.deepEqual(visibleTranscriptParts(empty, mode), []);
  assert.deepEqual(visibleTranscriptParts(meta, 'activity', true), meta.parts);
});

test('messageMarkdown copies what Chat level shows', () => {
  const assistant = {
    id: 'a', role: 'assistant', time: '',
    parts: [
      { kind: 'reasoning', text: 'Thinking' },
      { kind: 'text', text: '\nFirst **bold**\n' },
      { kind: 'tool', tool: { id: 'c', name: 'Read', status: 'completed' } },
      { kind: 'text', text: '```go\nfmt.Println()\n```' },
      { kind: 'notice', text: 'Assistant error' },
    ],
  };
  assert.equal(messageMarkdown(assistant), 'First **bold**\n\n```go\nfmt.Println()\n```\n\n> Assistant error');
  assert.equal(messageText(assistant), 'First **bold**\n\n```go\nfmt.Println()\n```');

  const user = {
    id: 'u', role: 'user', time: '',
    parts: [
      { kind: 'text', text: 'See these' },
      { kind: 'file', file: { name: 'shot.png', path: '/tmp/shot.png' } },
      { kind: 'file', file: { path: '/tmp/my file (1).txt' } },
      { kind: 'file', file: { name: 'pasted [1]' } },
    ],
  };
  assert.equal(messageMarkdown(user), 'See these\n\n[shot.png](/tmp/shot.png)\n\n[my file (1).txt](</tmp/my file (1).txt>)\n\npasted [1]');
  assert.equal(messageText(user), 'See these');

  const fileOnly = { id: 'f', role: 'user', time: '', parts: [{ kind: 'file', file: { name: 'a.png' } }] };
  assert.equal(messageMarkdown(fileOnly), 'a.png');
  assert.equal(messageText(fileOnly), '');
  for (const empty of [
    { id: 't', role: 'assistant', time: '', parts: [{ kind: 'tool', tool: { id: 'c', name: 'Read', status: 'completed' } }] },
    { id: 's', role: 'system', time: '', parts: [{ kind: 'text', text: 'System' }] },
    { id: 'm', role: 'user', time: '', isMeta: true, parts: [{ kind: 'text', text: 'Meta' }] },
    { id: 'n', role: 'assistant', time: '', parts: null },
  ]) {
    assert.equal(messageMarkdown(empty), '', empty.id);
    assert.equal(messageText(empty), '', empty.id);
  }
});

const TRANSLATIONS_STORE_URL = new URL('../src/lib/stores/translations.svelte.ts', import.meta.url);
let translationsStoreModule;

async function loadTranslationsStore() {
  translationsStoreModule ??= await loadServerModule(TRANSLATIONS_STORE_URL, [
    ["import { api } from '../api';", fixtureStore('api', '__storeFixture')],
    ["'../manage'", JSON.stringify(new URL('../manage.ts', TRANSLATIONS_STORE_URL).href)],
  ]);
  return translationsStoreModule;
}

test('translationKey separates sessions, messages and languages', async () => {
  const { translationKey } = await loadTranslationsStore();
  const ref = { agent: 'claude', id: 's1' };
  assert.equal(translationKey(ref, { id: 'm1' }, 3, 'Vietnamese'), 'claude:s1|id:m1|Vietnamese');
  assert.equal(translationKey(ref, { id: '' }, 3, 'Vietnamese'), 'claude:s1|#3|Vietnamese');
  const keys = new Set([
    translationKey(ref, { id: 'm1' }, 0, 'Vietnamese'),
    translationKey(ref, { id: 'm1' }, 0, 'French'),
    translationKey({ agent: 'codex', id: 's1' }, { id: 'm1' }, 0, 'Vietnamese'),
    translationKey(ref, { id: 'm2' }, 0, 'Vietnamese'),
    translationKey(ref, { id: '' }, 0, 'Vietnamese'),
  ]);
  assert.equal(keys.size, 5);
});

test('TranslationsStore tracks loading, done and error and keeps the newest', async () => {
  const { TranslationsStore, MAX_TRANSLATIONS } = await loadTranslationsStore();
  const pending = [];
  globalThis.__storeFixture = {
    api: { translate: (text) => new Promise((resolve, reject) => pending.push({ text, resolve, reject })) },
  };
  try {
    const store = new TranslationsStore();
    const done = store.translate('k', 'Hello');
    assert.equal(store.get('k').status, 'loading');
    // A second request while one runs is ignored.
    void store.translate('k', 'Hello');
    assert.equal(pending.length, 1);
    pending[0].resolve('Xin chào');
    await done;
    assert.deepEqual({ ...store.get('k') }, { status: 'done', text: 'Xin chào', error: '', showOriginal: false });

    store.toggleOriginal('k');
    assert.equal(store.get('k').showOriginal, true);
    store.toggleOriginal('k');
    assert.equal(store.get('k').showOriginal, false);

    const failed = store.translate('bad', 'Hi');
    pending[1].reject('translate: 401 Unauthorized');
    await failed;
    assert.equal(store.get('bad').status, 'error');
    assert.equal(store.get('bad').error, 'translate: 401 Unauthorized');
    store.toggleOriginal('bad');
    assert.equal(store.get('bad').showOriginal, false);

    // A result that lands after clear() is dropped.
    const late = store.translate('late', 'Hi');
    store.clear();
    pending[2].resolve('late');
    await late;
    assert.equal(store.get('late'), undefined);

    // The oldest entries go once the cap is reached.
    for (let i = 0; i <= MAX_TRANSLATIONS; i++) void store.translate(`n${i}`, 'x');
    assert.equal(store.get('n0'), undefined);
    assert.equal(store.get('n1').status, 'loading');
    assert.equal(Object.keys(store.entries).length, MAX_TRANSLATIONS);
  } finally {
    delete globalThis.__storeFixture;
  }
});

test('MockBackendAPI keeps the translation key and translates once set up', async () => {
  const mock = new MockBackendAPI();
  mock.translateDelayMs = 0;
  let s = await mock.translateSettings();
  assert.deepEqual(s, { baseURL: '', model: '', language: 'Vietnamese', apiKeySet: false, configured: false });
  await assert.rejects(mock.translate('Hello'), /not set up/);
  await assert.rejects(mock.setTranslateSettings({ baseURL: 'api.example.com', model: 'm', language: '', apiKey: 'k', clearApiKey: false }), /http or https/);

  s = await mock.setTranslateSettings({ baseURL: 'https://api.example.com/v1', model: 'm', language: '', apiKey: 'sk-1', clearApiKey: false });
  assert.deepEqual(s, { baseURL: 'https://api.example.com/v1', model: 'm', language: 'Vietnamese', apiKeySet: true, configured: true });
  assert.equal(JSON.stringify(s).includes('sk-1'), false);
  assert.equal(await mock.translate('Hello'), '[Vietnamese] Hello');

  s = await mock.setTranslateSettings({ baseURL: 'https://api.example.com/v1', model: 'm', language: 'French', apiKey: '', clearApiKey: false });
  assert.equal(s.apiKeySet, true);
  s = await mock.setTranslateSettings({ baseURL: 'https://api.example.com/v1', model: 'm', language: 'French', apiKey: '', clearApiKey: true });
  assert.deepEqual([s.apiKeySet, s.configured], [false, false]);
});

const GROUP_TREE_URL = new URL('../src/lib/components/sidebar/GroupTree.svelte', import.meta.url);
let groupTreeModule;

async function renderGroupTree(filter) {
  groupTreeModule ??= await loadServerComponent(GROUP_TREE_URL, [
    ["import { appState } from '../../stores/appState.svelte';", fixtureStore('appState')],
    ["import { manage } from '../../stores/manage.svelte';", 'const manage = {};'],
    ["import { filterSessionsByAge } from '../../manage';", 'const filterSessionsByAge = () => [];'],
    ["import { sessionToSelect } from '../../tree';", 'const sessionToSelect = () => null;'],
    ["import GroupNodeItem from './GroupNodeItem.svelte';", 'const GroupNodeItem = () => {};'],
  ]);
  return renderWithFixture(groupTreeModule, {
    appState: { loadingGroups: false, groups: [], filter: { query: '', path: '', liveOnly: false, archived: false, ...filter } },
  });
}

test('the empty tree offers to clear only the directory filter', async () => {
  const byPath = await renderGroupTree({ path: 'api', query: 'deploy' });
  assert.match(byPath, /No sessions match current filter/);
  assert.match(byPath, /Clear directory filter/);
  assert.doesNotMatch(byPath, /Clear filters/);

  // Without a directory filter the session list's own button clears the rest.
  const byQuery = await renderGroupTree({ query: 'deploy' });
  assert.match(byQuery, /No sessions match current filter/);
  assert.doesNotMatch(byQuery, /Clear directory filter|Clear filters/);
});
