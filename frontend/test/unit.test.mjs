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
import { isTranscriptMode, messageMarkdown, messageText, visibleTranscriptParts, withTranslation } from '../src/lib/transcript.ts';
import { printBlocks, printMarkdown, printTitle } from '../src/lib/print.ts';
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
import { NavHistory, navigationButton, navigationShortcut } from '../src/lib/navHistory.ts';
import {
  canOpenExternal,
  canOpenOther,
  canOpenPanel,
  canOpenTerminal,
  isPanelToggle,
  openMode,
  terminalKeyAction,
  terminalTabShortcut,
  isNewShellShortcut,
  newShellShortcutLabel,
  terminalOptions,
  terminalTheme,
} from '../src/lib/terminal.ts';
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
  defaultCollapsedKeys,
  isCollapsed,
  nodeKind,
  directoryName,
  pruneKeys,
  sessionToSelect,
  nodesHolding,
  nodeToHighlight,
  knownDirectories,
  directoryNodeKey,
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
  subscribeMouseNavigate,
} from '../src/lib/api.ts';
import { BUDGET_PRESETS, ALL_AGENTS, copyToClipboard, targetAgentsFor } from '../src/lib/portable.ts';
import { renameSvgId } from '../src/lib/mermaid.ts';
import { splitHighlightedLines, splitLines } from '../src/lib/codeHighlight.ts';
import { buildFileTree, diffRows, fileChanges, fileLanguage, filterFiles, folderStatus, lineMarks, matchScore, parentFolders, searchFiles, withDeleted } from '../src/lib/files.ts';
import { ImageCache, base64Blob, blobImage, isImageFile, rasterScale, sniffImageFormat } from '../src/lib/images.ts';

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

test('renderMarkdown leaves mermaid blocks for the diagram renderer', () => {
  const md = renderMarkdown('```Mermaid\ngraph TD\n  A["<img src=x onerror=alert(1)>"] --> B\n```');
  assert.ok(md.includes('class="code-block-container mermaid-block"'));
  assert.ok(md.includes('<div class="mermaid-diagram" hidden></div>'));
  assert.ok(md.includes('<pre class="mermaid-source">'));
  assert.ok(md.includes('class="mermaid-toggle-btn" hidden'));
  assert.ok(md.includes('class="mermaid-expand-btn"'));
  // The source is shown and copied as text; nothing in it becomes markup.
  assert.ok(!md.includes('<img'));
  assert.ok(md.includes('data-code="graph TD\n  A[&quot;&lt;img src=x onerror=alert(1)&gt;&quot;] --&gt; B"'));

  // An empty block has nothing to draw and stays a plain code block.
  const empty = renderMarkdown('```mermaid\n\n```');
  assert.ok(!empty.includes('mermaid-block'));
  assert.ok(empty.includes('data-lang="mermaid"'));
});

test('renderMarkdown keeps lines that only look like headings as text', () => {
  // These once matched no block and were never read, which hung the page.
  assert.equal(renderMarkdown('###'), '<p>###</p>');
  assert.equal(renderMarkdown('#tag'), '<p>#tag</p>');
  assert.equal(renderMarkdown('####### seven'), '<p>####### seven</p>');
  assert.equal(renderMarkdown('Intro\n###\n## Next'), '<p>Intro</p>\n<p>###</p>\n<h2>Next</h2>');
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

test('directoryName takes the last name of a directory label', () => {
  assert.equal(directoryName('~/Workspaces/app'), 'app');
  assert.equal(directoryName('/srv/app/'), 'app');
  assert.equal(directoryName('C:\\Users\\me\\app'), 'app');
  for (const label of ['~', '/', 'C:\\', '(unknown directory)']) {
    assert.equal(directoryName(label), label);
  }
});

test('renameSvgId renames a diagram id and its references only', () => {
  const svg =
    '<svg id="mermaid-diagram-3" width="100%"><style>#mermaid-diagram-3 .node{fill:red}</style>' +
    '<path marker-end="url(#mermaid-diagram-3_flowchart-pointEnd)"/><g id="mermaid-diagram-30"/></svg>';
  assert.equal(
    renameSvgId(svg, 'viewer'),
    '<svg id="viewer" width="100%"><style>#viewer .node{fill:red}</style>' +
      '<path marker-end="url(#viewer_flowchart-pointEnd)"/><g id="mermaid-diagram-30"/></svg>'
  );
  assert.equal(renameSvgId('<svg width="1"></svg>', 'viewer'), '<svg width="1"></svg>');
});

test('rasterScale keeps a PNG within the canvas limits', () => {
  assert.equal(rasterScale(800, 600, 2), 2);
  // 4000 x 4000 at 2x would be 64M pixels; WebKit takes 16M.
  assert.equal(rasterScale(4000, 4000, 2), 1.024);
  // A very wide diagram is held to 16384 pixels across.
  assert.equal(rasterScale(20000, 100, 2), 16384 / 20000);
});

const PNG_B64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==';

test('isImageFile shows PNG, JPEG, GIF and WebP attachments only', () => {
  assert.equal(isImageFile({ mime: 'image/png' }), true);
  assert.equal(isImageFile({ mime: 'IMAGE/JPEG; q=1' }), true);
  assert.equal(isImageFile({ mime: 'image/jpg' }), true);
  assert.equal(isImageFile({ mime: 'image/webp', name: 'x.txt' }), true);
  assert.equal(isImageFile({ mime: 'image/svg+xml' }), false);
  assert.equal(isImageFile({ mime: 'application/pdf', name: 'a.png' }), false);
  assert.equal(isImageFile({ name: 'Shot.GIF' }), true);
  assert.equal(isImageFile({ path: '/tmp/a.jpeg' }), true);
  assert.equal(isImageFile({ name: 'a.svg' }), false);
  assert.equal(isImageFile({}), false);
});

test('sniffImageFormat goes by the first bytes', () => {
  const b64 = (s) => Buffer.from(s, 'latin1').toString('base64');
  assert.equal(sniffImageFormat(PNG_B64), 'png');
  assert.equal(sniffImageFormat(b64('\xff\xd8\xff\xe0 rest of it')), 'jpeg');
  assert.equal(sniffImageFormat(b64('GIF87a rest')), 'gif');
  assert.equal(sniffImageFormat(b64('RIFF\x10\x00\x00\x00WEBPVP8 ')), 'webp');
  assert.equal(sniffImageFormat(b64('<svg></svg>')), null);
  assert.equal(sniffImageFormat('not base64!'), null);
  assert.equal(sniffImageFormat(''), null);
});

test('blobImage makes a data URL from a binary blob and refuses others', () => {
  const image = blobImage({ data: PNG_B64, mime: 'application/octet-stream', isBinary: true });
  assert.deepEqual(image, { url: `data:image/png;base64,${PNG_B64}`, data: PNG_B64, format: 'png' });
  assert.throws(() => blobImage({ data: PNG_B64, mime: 'text/plain', isBinary: false }), /not a PNG/);
  assert.throws(() => blobImage({ data: 'aGVsbG8=', mime: 'application/octet-stream', isBinary: true }), /not a PNG/);
});

test('base64Blob holds the decoded bytes', async () => {
  const blob = base64Blob(PNG_B64, 'image/png');
  assert.equal(blob.type, 'image/png');
  assert.deepEqual(Buffer.from(await blob.arrayBuffer()), Buffer.from(PNG_B64, 'base64'));
});

test('ImageCache shares a request per key and drops the least recently used', async () => {
  const blob = { data: PNG_B64, mime: 'application/octet-stream', isBinary: true };
  const cache = new ImageCache(PNG_B64.length * 2);
  let fetches = 0;
  const fetch = async () => {
    fetches++;
    return blob;
  };
  const [a, b] = await Promise.all([cache.load('a', fetch), cache.load('a', fetch)]);
  assert.equal(fetches, 1);
  assert.equal(a, b);
  assert.equal(cache.get('a'), a);

  await cache.load('b', fetch);
  cache.get('a'); // a is now the most recent
  await cache.load('c', fetch);
  assert.equal(fetches, 3);
  assert.ok(cache.get('a'));
  assert.equal(cache.get('b'), undefined);
  assert.ok(cache.get('c'));

  // A failed load isn't kept, so it can be tried again.
  await assert.rejects(cache.load('d', async () => ({ ...blob, data: 'aGVsbG8=' })), /not a PNG/);
  await cache.load('d', fetch);
  assert.ok(cache.get('d'));

  cache.clear();
  assert.equal(cache.get('d'), undefined);
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

const navAt = (id, groupKey = null) => ({ ref: id ? { agent: 'claude', id } : null, groupKey, groupMode: 'dir-agent' });
const navId = (entry) => entry?.ref?.id ?? null;

test('NavHistory goes back and forward and drops the places ahead on a new move', () => {
  const h = new NavHistory();
  assert.equal(h.canGoBack, false);
  h.replace(navAt('a')); // the session selected on load
  h.push(navAt('b'));
  h.push(navAt('b')); // the same place again is not a new entry
  h.push(navAt('c'));
  assert.equal(navId(h.back()), 'b');
  assert.equal(navId(h.back()), 'a');
  assert.equal(h.back(), null);
  assert.equal(navId(h.forward()), 'b');
  h.push(navAt('d'));
  assert.equal(h.canGoForward, false);
  assert.equal(navId(h.back()), 'b');
  assert.equal(navId(h.back()), 'a');

  const groups = new NavHistory();
  groups.push(navAt('a'));
  groups.push(navAt('a', 'g1')); // picking a group is a place of its own
  groups.replace(navAt('x', 'g1')); // ...and its first session updates it
  assert.deepEqual(groups.back(), navAt('a'));
  assert.deepEqual(groups.forward(), navAt('x', 'g1'));
});

test('NavHistory records a run of arrow-key steps as one place', () => {
  const h = new NavHistory();
  h.push(navAt('a'));
  h.step(navAt('b'));
  h.step(navAt('c'));
  h.step(navAt('d'));
  assert.equal(navId(h.back()), 'a');
  assert.equal(navId(h.forward()), 'd');
  h.push(navAt('e'));
  h.step(navAt('f'));
  assert.equal(navId(h.back()), 'e');
});

test('NavHistory caps its length', () => {
  const h = new NavHistory(3);
  for (const id of ['a', 'b', 'c', 'd']) h.push(navAt(id));
  assert.equal(navId(h.back()), 'c');
  assert.equal(navId(h.back()), 'b');
  assert.equal(h.back(), null);
});

test('NavHistory forgets deleted sessions and their subagents', () => {
  const h = new NavHistory();
  for (const id of ['a', 'b', 'b/agent-1', 'a', 'c']) h.push(navAt(id));
  h.back(); // at the second "a"
  h.remove([{ agent: 'claude', id: 'b' }]);
  // b and its subagent are gone and the two a's merge into one place.
  assert.equal(h.canGoBack, false);
  assert.equal(navId(h.forward()), 'c');
  h.remove([{ agent: 'codex', id: 'a' }]); // another agent's session
  assert.equal(navId(h.back()), 'a');

  // Deleting the session shown: the neighbour selected next takes its place
  // between the remaining ones.
  const shown = new NavHistory();
  for (const id of ['a', 'b', 'c']) shown.push(navAt(id));
  shown.back(); // at b
  shown.remove([{ agent: 'claude', id: 'b' }]);
  assert.equal(shown.canGoBack, true);
  shown.replace(navAt('n'));
  assert.equal(navId(shown.back()), 'a');
  assert.equal(navId(shown.forward()), 'n');
  assert.equal(navId(shown.forward()), 'c');

  // Deleting every session leaves nothing to go to.
  const all = new NavHistory();
  all.push(navAt('a'));
  all.push(navAt('b'));
  all.remove([{ agent: 'claude', id: 'a' }, { agent: 'claude', id: 'b' }]);
  assert.equal(all.canGoBack || all.canGoForward, false);
  assert.equal(all.back(), null);
  all.replace(navAt(null));
  assert.equal(all.canGoBack || all.canGoForward, false);

  // Deleting the shown first place, then going forward without a new one.
  const first = new NavHistory();
  first.push(navAt('a'));
  first.push(navAt('b'));
  first.back();
  first.remove([{ agent: 'claude', id: 'a' }]);
  assert.equal(first.canGoBack, false);
  assert.equal(navId(first.forward()), 'b');
});

test('the drop guard keeps drags from other apps from navigating the app', async () => {
  const { setupDropGuard } = await import('../src/lib/navigation.ts');
  const target = new EventTarget();
  setupDropGuard(target);
  const drag = (type) => {
    const e = new Event(type, { cancelable: true });
    e.dataTransfer = { dropEffect: 'none' };
    target.dispatchEvent(e);
    return e;
  };
  const over = drag('dragover');
  assert.deepEqual([over.defaultPrevented, over.dataTransfer.dropEffect], [true, 'copy']);
  assert.equal(drag('drop').defaultPrevented, true);

  // Dragging selected text inside the app keeps its default.
  target.dispatchEvent(new Event('dragstart'));
  assert.equal(drag('dragover').defaultPrevented, false);
  assert.equal(drag('drop').defaultPrevented, false);
  target.dispatchEvent(new Event('dragend'));
  assert.equal(drag('drop').defaultPrevented, true);
});

test('navigation shortcuts and mouse side buttons', () => {
  const key = (key, mods = {}) => ({ key, altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, ...mods });
  assert.equal(navigationShortcut(key('BrowserBack'), false), 'back');
  assert.equal(navigationShortcut(key('BrowserForward'), true), 'forward');
  assert.equal(navigationShortcut(key('ArrowLeft', { altKey: true }), false), 'back');
  assert.equal(navigationShortcut(key('ArrowRight', { altKey: true }), false), 'forward');
  assert.equal(navigationShortcut(key('ArrowLeft', { altKey: true, shiftKey: true }), false), null);
  assert.equal(navigationShortcut(key('ArrowLeft', { altKey: true }), true), null);
  assert.equal(navigationShortcut(key('ArrowLeft'), false), null);
  assert.equal(navigationShortcut(key('[', { metaKey: true }), true), 'back');
  assert.equal(navigationShortcut(key(']', { metaKey: true }), true), 'forward');
  assert.equal(navigationShortcut(key('[', { ctrlKey: true }), false), null);
  assert.equal(navigationButton(3), 'back');
  assert.equal(navigationButton(4), 'forward');
  assert.equal(navigationButton(0), null);
  assert.equal(navigationButton(2), null);
});

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

function treeNode(key, kind, childCount = 0, childKind = 'session') {
  const children = Array.from({ length: childCount }, (_, i) => ({
    key: `${key}/${i}`,
    label: `${key}/${i}`,
    kind: childKind,
    sessionCount: 1,
  }));
  return { key, label: key, kind, sessionCount: childCount, children };
}

test('defaultCollapsedKeys opens top-level groups and closes the groups inside them', () => {
  assert.equal(defaultCollapsedKeys([]).size, 0);

  // By directory: each directory lists its agents, closed, however many.
  const small = { ...treeNode('small', 'directory'), children: [treeNode('small/claude', 'agent', 1)] };
  const big = { ...treeNode('big', 'directory'), children: [treeNode('big/claude', 'agent', 30), treeNode('big/codex', 'agent', 2)] };
  assert.deepEqual([...defaultCollapsedKeys([small, big])].sort(), ['big/claude', 'big/codex', 'small/claude']);

  // By agent: each agent lists its directories, closed.
  const agent = { ...treeNode('claude', 'agent'), children: [treeNode('claude/be', 'directory', 18), treeNode('claude/tmp', 'directory', 1)] };
  assert.deepEqual([...defaultCollapsedKeys([agent])].sort(), ['claude/be', 'claude/tmp']);

  // A top-level group of sessions stays open; an empty group has nothing to close.
  assert.equal(defaultCollapsedKeys([treeNode('flat', 'agent', 50), treeNode('empty', 'directory')]).size, 0);
});

test('defaultCollapsedKeys collapses every session with subagents', () => {
  const withOne = treeNode('sess1', 'session', 1);
  const withMany = treeNode('sessN', 'session', 7);
  const leaf = treeNode('leaf', 'session');
  const agent = { ...treeNode('agent', 'agent'), children: [withOne, withMany, leaf] };

  const keys = defaultCollapsedKeys([agent]);
  assert.deepEqual([...keys].sort(), ['sess1', 'sessN']);
});

test('the tree highlights the row where the open session is', () => {
  const a = { agent: 'claude-code', id: 'a' };
  const b = { agent: 'codex', id: 'b' };
  const sub = { agent: 'claude-code', id: 'a-sub' };
  const session = (ref, children) => ({ key: `session:${ref.id}`, label: ref.id, kind: 'session', sessionCount: 1, sessions: children ? [ref, ...children.flatMap((c) => c.sessions)] : [ref], children });
  const tree = [
    {
      key: 'dir:/x', label: '/x', kind: 'directory', sessionCount: 3, sessions: [a, sub, b],
      children: [
        { key: 'dir-agent:/x:claude', label: 'Claude', kind: 'agent', sessionCount: 2, sessions: [a, sub], children: [session(a, [session(sub)])] },
        { key: 'dir-agent:/x:codex', label: 'Codex', kind: 'agent', sessionCount: 1, sessions: [b] },
      ],
    },
    { key: 'dir:/y', label: '/y', kind: 'directory', sessionCount: 0, sessions: [] },
  ];
  assert.deepEqual([...nodesHolding(tree, b)], ['dir:/x', 'dir-agent:/x:codex']);
  assert.deepEqual([...nodesHolding(tree, sub)], ['dir:/x', 'dir-agent:/x:claude', 'session:a', 'session:a-sub']);
  assert.deepEqual([...nodesHolding(tree, { ...b })], ['dir:/x', 'dir-agent:/x:codex'], 'refs compare by value');
  assert.equal(nodesHolding(tree, { agent: 'codex', id: 'gone' }).size, 0);
  assert.equal(nodesHolding(tree, null).size, 0);

  const none = new Set();
  assert.equal(nodeToHighlight(tree, b, 'dir:/x', none, none), 'dir:/x', 'the clicked group holds it');
  assert.equal(nodeToHighlight(tree, b, 'dir:/y', none, none), 'dir-agent:/x:codex', 'opened from outside the group');
  assert.equal(nodeToHighlight(tree, b, null, none, none), 'dir-agent:/x:codex');
  assert.equal(nodeToHighlight(tree, sub, null, none, none), 'session:a-sub');
  assert.equal(nodeToHighlight(tree, b, null, new Set(['dir:/x']), none), 'dir:/x', 'a closed group stands for its rows');
  assert.equal(nodeToHighlight(tree, sub, null, none, new Set(['session:a'])), 'session:a', 'closed by default');
  assert.equal(nodeToHighlight(tree, { agent: 'codex', id: 'gone' }, 'dir:/y', none, none), 'dir:/y', 'filtered out of the tree');
  assert.equal(nodeToHighlight(tree, null, null, none, none), null);
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
  const driveKey = 'dir-agent:C:\\Users\\me\\repo';
  const drive = { ...treeNode(driveKey, 'directory'), children: [treeNode(`${driveKey}/0`, 'agent', 1)] };
  const unc = treeNode('dir-agent:\\\\fileserver\\share\\team-repo\\', 'directory', 1, 'agent');
  const root = { ...treeNode('dir-agent:C:\\', 'directory'), cwd: 'C:\\' };
  const tree = [drive, unc, root];

  assert.deepEqual([...defaultCollapsedKeys(tree)], [`${driveKey}/0`]);
  assert.equal(isCollapsed(`${driveKey}/0`, new Set(), defaultCollapsedKeys(tree)), true);
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

test('knownDirectories lists existing directories once, sorted', () => {
  const groups = [
    { key: 'a:claude', label: 'claude', kind: 'agent', agent: 'claude-code', sessionCount: 3, children: [
      { key: 'a:claude:/b', label: '/b', kind: 'directory', cwd: '/b', sessionCount: 1 },
      { key: 'a:claude:/a', label: '/a', kind: 'directory', cwd: '/a', sessionCount: 1 },
      { key: 'a:claude:/gone', label: '/gone', kind: 'directory', cwd: '/gone', cwdMissing: true, sessionCount: 1 },
    ] },
    { key: 'a:codex', label: 'codex', kind: 'agent', agent: 'codex', sessionCount: 1, children: [
      { key: 'a:codex:/b', label: '/b', kind: 'directory', cwd: '/b', sessionCount: 1 },
    ] },
  ];
  assert.deepEqual(knownDirectories(groups), ['/a', '/b']);
  assert.deepEqual(knownDirectories([]), []);
});

test('directoryNodeKey prefers the directory under the matching agent', () => {
  const byAgent = [
    { key: 'a:claude', label: 'claude', kind: 'agent', agent: 'claude-code', sessionCount: 1, children: [
      { key: 'a:claude:/p', label: '/p', kind: 'directory', cwd: '/p', sessionCount: 1 },
    ] },
    { key: 'a:codex', label: 'codex', kind: 'agent', agent: 'codex', sessionCount: 1, children: [
      { key: 'a:codex:/p', label: '/p', kind: 'directory', cwd: '/p', sessionCount: 1 },
    ] },
  ];
  assert.equal(directoryNodeKey(byAgent, '/p', 'codex'), 'a:codex:/p');
  assert.equal(directoryNodeKey(byAgent, '/p', 'opencode'), 'a:claude:/p');
  assert.equal(directoryNodeKey(byAgent, '/missing', 'codex'), null);

  const byDir = [
    { key: 'd:/p', label: '/p', kind: 'directory', cwd: '/p', sessionCount: 2, children: [
      { key: 'd:/p:codex', label: 'codex', kind: 'agent', agent: 'codex', sessionCount: 1 },
    ] },
  ];
  assert.equal(directoryNodeKey(byDir, '/p', 'codex'), 'd:/p');
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

test('mouse navigation events forward only back and forward', () => {
  const mock = new MockBackendAPI();
  const seen = [];
  subscribeMouseNavigate((d) => seen.push(d), mock);
  for (const payload of ['back', 'forward', 'up', null, { direction: 'back' }]) mock.emit('nav:mouse', payload);
  assert.deepEqual(seen, ['back', 'forward']);
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

test('storageKeyForCollapsed namespaces keys and drops legacy toggles', () => {
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

    // 2. Toggles saved by earlier versions flip other defaults; they're dropped.
    const legacy = 'agent-sessions:tree-collapsed';
    store.set(legacy, '["a"]');
    store.set(`${legacy}:local`, '["b"]');
    store.set(`${legacy}:${hashHost('dev-box')}`, '["c"]');
    assert.equal(storageKeyForCollapsed(), `${BASE_COLLAPSED_STORAGE_KEY}:local`);
    assert.deepEqual([...store.keys()], [`${legacy}:${hashHost('dev-box')}`]);
    storageKeyForCollapsed('dev-box');
    assert.equal(store.size, 0);

    // 3. loadCollapsedKeys and saveCollapsedKeys per target host
    saveCollapsedKeys(new Set(['group:1', 'group:2']));
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
  // The built-in panel opens sessions from every host, SSH included.
  assert.equal(
    resumeButtonTitle(undefined, true, 'panel'),
    'Resume this session in the built-in terminal. Use ▾ for other ways to resume',
  );
  assert.equal(
    resumeButtonTitle('plgl', true, 'panel'),
    'Resume this session in the built-in terminal on plgl. Use ▾ for other ways to resume',
  );
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
  assert.deepEqual(await mock.launchInfo(), { terminal: false, shell: 'posix', panel: true, open: 'panel' });
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
      ["import { terminals } from '../../stores/terminals.svelte';", fixtureStore('terminals')],
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
    terminals: { position: 'bottom' },
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

test('Settings chooses where agents open when the built-in panel runs', async () => {
  const checked = (html, label) => new RegExp(`<input[^>]*type="radio"[^>]*checked[^>]*/?>\\s*${label}`).test(html);
  // Windows: the panel makes the section appear, without a terminal app choice.
  const windows = await renderSettingsDialog(
    { info: { terminal: true, chooseTerminal: false, shell: 'powershell', panel: true }, openMode: 'panel', savingOpenMode: false },
    undefined,
    { section: 'terminal' },
  );
  assert.match(windows, /Terminal \(this computer\)/);
  assert.match(windows, /<legend[^>]*>Open agents in<\/legend>/);
  assert.ok(checked(windows, 'Built-in panel'), 'the panel is chosen');
  assert.ok(!checked(windows, 'External terminal'));
  assert.match(windows, /in a tab of the terminal panel, for sessions on this computer, WSL and SSH hosts/);
  assert.doesNotMatch(windows, /External terminal app|aria-label="Terminal app"|Looking for terminal apps/);

  // Linux: the external terminal app is chosen below the mode.
  const terminal = {
    choose: true, selected: '', selectedName: '', missing: false,
    auto: { id: 'konsole', name: 'Konsole' }, options: [{ id: 'konsole', name: 'Konsole' }],
  };
  const linux = await renderSettingsDialog(
    { info: { terminal: true, chooseTerminal: true, shell: 'posix', panel: true, open: 'external' }, openMode: 'external', savingOpenMode: false, terminal },
    undefined,
    { section: 'terminal' },
  );
  assert.ok(checked(linux, 'External terminal'), 'the external terminal is chosen');
  assert.ok(!checked(linux, 'Built-in panel'));
  assert.match(linux, /<h4[^>]*>External terminal app<\/h4>/);
  assert.match(linux, /Automatic \(Konsole\)/);
  assert.match(linux, /The Resume ▾ menu still offers the built-in panel/);

  const tabs = [...windows.matchAll(/role="tab"[^>]*>([^<]+)<\/button>/g)].map((m) => m[1]);
  assert.ok(tabs.includes('Terminal'));
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
  assert.match(management, /Also remove deleted sessions from prompt history/);
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
    ["'../../guidance'", lib('../../guidance.ts')],
    ["import { toast } from '../../stores/toast.svelte';", fixtureStore('toast')],
    ["import AgentIcon from '../common/AgentIcon.svelte';", 'const AgentIcon = () => {};'],
    ["import NavButtons from './NavButtons.svelte';", 'const NavButtons = () => {};'],
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
    { appState: {}, manage: { settings: { enabled: false } }, handoff: {}, exporter: {}, toast: {}, preferences: { timeZone: 'utc' }, ...fixture },
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

test('SessionHeader Resume ▾ offers the place agents do not open by default', async () => {
  // A server render keeps the menu closed: check the button and its toggle.
  const panel = await renderSessionHeader({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: true, openMode: 'panel', canOpenPanel: true, canOpenExternal: false },
  });
  assert.match(panel, /resume-main[^"]*"[^>]*title="Resume this session in the built-in terminal on plgl/);
  assert.match(panel, /aria-label="More ways to resume"/);

  // External mode where the session has no external terminal (SSH): the
  // main button copies and ▾ still offers the built-in panel.
  const ssh = await renderSessionHeader({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: false, openMode: 'external', canOpenPanel: true, canOpenExternal: false },
  });
  assert.match(ssh, /resume-main[^"]*"[^>]*title="Copy shell command to resume this session/);
  assert.match(ssh, /aria-label="More ways to resume"/);

  // Neither place opens: Resume only copies.
  const none = await renderSessionHeader({
    link: { dataHost: 'plgl' },
    launcher: { canOpen: false, openMode: 'external', canOpenPanel: false, canOpenExternal: false },
  });
  assert.doesNotMatch(none, /More ways to resume/);
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

test('canOpenTerminal follows where agents open; the panel reaches SSH hosts', () => {
  const panel = { terminal: true, wsl: true, shell: 'powershell', panel: true };
  const external = { ...panel, open: 'external' };
  assert.equal(openMode(panel), 'panel');
  assert.equal(openMode({ ...panel, open: 'panel' }), 'panel');
  assert.equal(openMode(external), 'external');
  // An older backend has no panel, whatever the saved choice.
  assert.equal(openMode({ terminal: true, shell: 'posix', open: 'panel' }), 'external');

  const cases = [
    // [name, launch info, data host, terminal, external, panel, other]
    ['panel, local', panel, undefined, true, true, true, true],
    ['panel, WSL', panel, 'wsl:Ubuntu', true, true, true, true],
    ['panel, SSH', panel, 'plgl', true, false, true, false],
    ['panel, WSL not installed', { ...panel, wsl: false }, 'wsl:Ubuntu', false, false, false, false],
    ['panel, empty WSL distribution', panel, 'wsl:', false, false, false, false],
    ['panel, no local terminal app', { ...panel, terminal: false }, undefined, true, false, true, false],
    ['external, local', external, undefined, true, true, true, true],
    ['external, SSH', external, 'plgl', false, false, true, true],
    ['no panel, SSH', { terminal: true, shell: 'posix' }, 'plgl', false, false, false, false],
  ];
  for (const [name, info, host, terminal, ext, pan, other] of cases) {
    assert.deepEqual(
      [canOpenTerminal(info, host), canOpenExternal(info, host), canOpenPanel(info, host), canOpenOther(info, host)],
      [terminal, ext, pan, other],
      name,
    );
  }
});

test('terminal keys: Ctrl+` toggles the panel; copy and paste follow the platform', () => {
  const key = (k, mods = {}) => ({ type: 'keydown', key: k, code: '', ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods });
  assert.ok(isPanelToggle(key('`', { ctrlKey: true })));
  assert.ok(isPanelToggle({ ...key('Dead', { ctrlKey: true }), code: 'Backquote' }), 'layouts with a dead backquote');
  assert.ok(!isPanelToggle(key('`')));
  assert.ok(!isPanelToggle(key('`', { ctrlKey: true, altKey: true })));

  assert.equal(terminalKeyAction(key('`', { ctrlKey: true }), false), 'app');
  assert.equal(terminalKeyAction(key(',', { ctrlKey: true }), false), 'app');
  assert.equal(terminalKeyAction(key(',', { metaKey: true }), true), 'app');
  // Elsewhere Ctrl+C stays an interrupt; Ctrl+Shift+C copies.
  assert.equal(terminalKeyAction(key('c', { ctrlKey: true }), false), null);
  assert.equal(terminalKeyAction(key('C', { ctrlKey: true, shiftKey: true }), false), 'copy');
  assert.equal(terminalKeyAction(key('V', { ctrlKey: true, shiftKey: true }), false), 'paste');
  assert.equal(terminalKeyAction({ ...key('C', { ctrlKey: true, shiftKey: true }), type: 'keyup' }), null);
  // macOS: Cmd+C and Cmd+V; Ctrl+C reaches the terminal.
  assert.equal(terminalKeyAction(key('c', { metaKey: true }), true), 'copy');
  assert.equal(terminalKeyAction(key('v', { metaKey: true }), true), 'paste');
  assert.equal(terminalKeyAction(key('c', { ctrlKey: true }), true), null);
  assert.equal(terminalKeyAction(key('x', { metaKey: true }), true), null);
});

test('New shell shortcut: Cmd+T on macOS, Ctrl+Shift+T elsewhere', () => {
  const key = (k, code, mods = {}) => ({ key: k, code, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods });
  assert.ok(isNewShellShortcut(key('t', 'KeyT', { metaKey: true }), true));
  assert.ok(isNewShellShortcut(key('T', 'KeyT', { ctrlKey: true, shiftKey: true }), false));
  assert.ok(isNewShellShortcut(key('t', 'KeyK', { metaKey: true }), true), 'layouts with T elsewhere');
  assert.ok(!isNewShellShortcut(key('y', 'KeyT', { metaKey: true }), true), 'their key in T\'s place types another letter');
  assert.ok(isNewShellShortcut(key('е', 'KeyT', { metaKey: true }), true), 'a layout without Latin letters');
  assert.ok(!isNewShellShortcut(key('t', 'KeyT', { ctrlKey: true }), false), 'Ctrl+T reaches the terminal');
  assert.ok(!isNewShellShortcut(key('t', 'KeyT', { ctrlKey: true }), true));
  assert.ok(!isNewShellShortcut(key('T', 'KeyT', { metaKey: true, shiftKey: true }), true));
  assert.ok(!isNewShellShortcut(key('t', 'KeyT', { metaKey: true }), false));
  assert.ok(!isNewShellShortcut(key('T', 'KeyT', { ctrlKey: true, shiftKey: true, altKey: true }), false));
  assert.equal(terminalKeyAction({ ...key('t', 'KeyT', { metaKey: true }), type: 'keydown' }, true), 'app');
  assert.equal(terminalKeyAction({ ...key('T', 'KeyT', { ctrlKey: true, shiftKey: true }), type: 'keydown' }, false), 'app');
  assert.deepEqual([newShellShortcutLabel(true), newShellShortcutLabel(false)], ['⌘T', 'Ctrl+Shift+T']);
});

test('terminal tab shortcuts: Cmd+1–9 on macOS, Alt+1–9 elsewhere', () => {
  const key = (code, mods = {}) => ({ code, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods });
  assert.equal(terminalTabShortcut(key('Digit1', { metaKey: true }), true), 1);
  assert.equal(terminalTabShortcut(key('Digit9', { metaKey: true }), true), 9);
  assert.equal(terminalTabShortcut(key('Numpad3', { metaKey: true }), true), 3);
  assert.equal(terminalTabShortcut(key('Digit2', { altKey: true }), false), 2);
  assert.equal(terminalTabShortcut(key('Digit0', { metaKey: true }), true), null);
  assert.equal(terminalTabShortcut(key('KeyA', { metaKey: true }), true), null);
  assert.equal(terminalTabShortcut(key('Digit1', { altKey: true }), true), null, 'Option+digit types on macOS');
  assert.equal(terminalTabShortcut(key('Digit1', { metaKey: true }), false), null);
  assert.equal(terminalTabShortcut(key('Digit1', { metaKey: true, shiftKey: true }), true), null);
  assert.equal(terminalTabShortcut(key('Digit1', { altKey: true, ctrlKey: true }), false), null, 'AltGr');
  assert.equal(terminalTabShortcut(key('Digit1'), false), null);
});

test('terminalTheme takes the app colors and falls back per theme', () => {
  const vars = { '--bg-primary': '#101010', '--text-primary': '#eeeeee', '--accent-color': '#3b82f6' };
  const dark = terminalTheme('dark', (name) => vars[name] ?? '');
  assert.deepEqual([dark.background, dark.foreground, dark.cursor, dark.cursorAccent], ['#101010', '#eeeeee', '#3b82f6', '#101010']);
  const light = terminalTheme('light', () => '');
  assert.deepEqual([light.background, light.foreground, light.cursor], ['#ffffff', '#0b1220', '#0b1220']);
  assert.notEqual(light.red, dark.red);
});

test('LauncherStore saves where agents open and reports a failure', async () => {
  const { LauncherStore } = await loadLauncherStore();
  const calls = [];
  let fail = false;
  globalThis.__storeFixture = {
    api: {
      launchInfo: async () => ({ terminal: true, shell: 'posix', panel: true }),
      setTerminalOpen: async (mode) => {
        calls.push(mode);
        if (fail) throw new Error('config.toml: permission denied');
        return { terminal: true, shell: 'posix', panel: true, open: mode };
      },
    },
    link: { dataHost: 'plgl' },
  };
  try {
    const store = new LauncherStore();
    await store.init();
    assert.deepEqual([store.openMode, store.canOpen, store.canOpenPanel, store.canOpenExternal], ['panel', true, true, false]);
    await store.setOpenMode('external');
    assert.deepEqual([store.openMode, store.canOpen, store.savingOpenMode], ['external', false, false]);
    fail = true;
    await store.setOpenMode('panel');
    assert.equal(store.openMode, 'external');
    assert.match(store.terminalError, /permission denied/);
    assert.deepEqual(calls, ['external', 'panel']);
  } finally {
    delete globalThis.__storeFixture;
  }
});

const TERMINALS_STORE_URL = new URL('../src/lib/stores/terminals.svelte.ts', import.meta.url);
let terminalsStoreModule;

// The real TerminalsStore on a mock backend with its fake terminals.
async function loadTerminalsStore() {
  const lib = (rel) => JSON.stringify(new URL(rel, TERMINALS_STORE_URL).href);
  terminalsStoreModule ??= await loadServerModule(TERMINALS_STORE_URL, [
    [
      "import { api, subscribeTerminalData, subscribeTerminalExit } from '../api';",
      fixtureStore('api', '__storeFixture') +
        "const subscribeTerminalData = (fn) => api.onEvent('term:data', fn);" +
        "const subscribeTerminalExit = (fn) => api.onEvent('term:exit', fn);",
    ],
    ["'../link'", lib('../link.ts')],
    ["'../portable'", lib('../portable.ts')],
    ["'../guidance'", lib('../guidance.ts')],
    ["'../hosts'", lib('../hosts.ts')],
    ["import { toast } from './toast.svelte';", fixtureStore('toast', '__storeFixture')],
  ]);
  return terminalsStoreModule;
}

async function withTerminals(fn) {
  const { TerminalsStore, ...rest } = await loadTerminalsStore();
  const mock = new MockBackendAPI();
  const toasts = [];
  const storage = new Map();
  const hadStorage = 'localStorage' in globalThis;
  const previous = globalThis.localStorage;
  globalThis.localStorage = {
    getItem: (k) => storage.get(k) ?? null,
    setItem: (k, v) => storage.set(k, String(v)),
  };
  // The fixture proxy calls methods on itself; bind them to the mock.
  const api = new Proxy(mock, { get: (m, key) => (typeof m[key] === 'function' ? m[key].bind(m) : m[key]) });
  const toast = {
    current: null,
    show: (t) => {
      toasts.push(t);
      toast.current = { ...t, id: toasts.length };
    },
    dismiss: () => (toast.current = null),
  };
  globalThis.__storeFixture = { api, toast };
  const store = new TerminalsStore();
  store.init();
  try {
    return await fn({ store, mock, toasts, toast, storage, TerminalsStore, ...rest });
  } finally {
    store.destroy();
    delete globalThis.__storeFixture;
    if (hadStorage) globalThis.localStorage = previous;
    else delete globalThis.localStorage;
  }
}

function sinkFor(store, id) {
  const sink = { text: '', exits: [], pastes: [] };
  const decoder = new TextDecoder();
  sink.detach = store.attach(id, {
    write: (data) => (sink.text += decoder.decode(data)),
    exit: (code) => sink.exits.push(code),
    paste: (text) => sink.pastes.push(text),
  });
  return sink;
}

test('TerminalsStore buffers output until the view attaches, then streams it', async () => {
  await withTerminals(async ({ store, mock }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
    store.setSize(100, 30);
    const tab = await store.runResume(meta.ref);
    assert.deepEqual([tab.kind, tab.status, tab.label, store.active, store.open], ['agent', 'running', ALL_AGENTS.find((a) => a.id === meta.ref.agent).label, tab.id, true]);
    assert.deepEqual(mock.panelTerminals.get(tab.id).size, { cols: 100, rows: 30 }, 'the tab starts at the views size');

    // The banner arrived before the start reply and waits for the view.
    const sink = sinkFor(store, tab.id);
    assert.match(sink.text, /\[mock\] .*mock\x1b\[0m \$ $/s);
    await mock.terminalWrite(tab.id, 'ls\r');
    assert.match(sink.text, /ls\r\nmock: ls\r\n/);

    sink.detach();
  });
});

test('TerminalsStore closes a tab when its process exits, keeping a quick failure readable', async () => {
  const realNow = Date.now;
  let now = 1_000_000;
  Date.now = () => now;
  try {
    await withTerminals(async ({ store, mock, KEEP_FAILED_MS }) => {
      const first = await store.newShell();
      const second = await store.newShell();
      store.select(first.id);
      // Typing exit closes the tab; the neighbour stays.
      await mock.terminalWrite(first.id, 'exit\r');
      assert.deepEqual(store.tabs.map((t) => t.id), [second.id]);
      assert.deepEqual([store.active, store.open], [second.id, true]);

      // A failure right after the start stays, with its exit code shown.
      const failed = await store.newShell();
      const sink = sinkFor(store, failed.id);
      mock.emit('term:exit', { id: failed.id, code: 127 });
      assert.deepEqual([store.tabs.at(-1).status, store.tabs.at(-1).exitCode, sink.exits], ['exited', 127, [127]]);
      // A view mounted later still learns how the tab ended.
      sink.detach();
      assert.deepEqual(sinkFor(store, failed.id).exits, [127]);

      // A failure later on, like exit after a failed command, closes.
      now += KEEP_FAILED_MS;
      mock.emit('term:exit', { id: second.id, code: 1 });
      assert.deepEqual(store.tabs.map((t) => t.id), [failed.id]);

      // The last tab closing hides the panel.
      store.close(failed.id);
      assert.deepEqual([store.tabs.length, store.open], [0, false]);

      // A process that finished before its start returned never shows a tab.
      const panelShell = mock.panelShell.bind(mock);
      mock.panelShell = async (cwd, size) => {
        const tab = await panelShell(cwd, size);
        mock.emit('term:exit', { id: tab.id, code: 0 });
        return tab;
      };
      assert.equal((await store.newShell()).status, 'exited');
      assert.equal(store.tabs.length, 0);
    });
  } finally {
    Date.now = realNow;
  }
});

test('TerminalsStore asks before closing a running agent, not a shell', async () => {
  await withTerminals(async ({ store, mock }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
    const agent = await store.runResume(meta.ref, meta.cwd);
    const shell = await store.newShell('/work');
    assert.equal(store.active, shell.id);
    // The backend's directory wins; the caller's fills in where it has none.
    assert.deepEqual([agent.dir, shell.dir], [meta.cwd, '/work']);

    assert.equal(store.close(agent.id), false);
    assert.deepEqual([store.confirming, store.active, store.tabs.length], [agent.id, agent.id, 2]);
    store.cancelClose();
    assert.equal(store.confirming, null);
    assert.ok(mock.panelTerminals.has(agent.id));

    store.close(agent.id);
    store.confirmClose();
    assert.deepEqual(store.tabs.map((t) => t.id), [shell.id]);
    assert.equal(store.active, shell.id);
    assert.ok(!mock.panelTerminals.has(agent.id), 'closing ended the agent');

    // A shell closes at once; the last tab hides the panel.
    assert.equal(store.close(shell.id), true);
    assert.deepEqual([store.tabs.length, store.active, store.open], [0, null, false]);
    assert.equal(mock.panelTerminals.size, 0);
  });
});

test('TerminalsStore selects a tab by its position while the panel shows', async () => {
  await withTerminals(async ({ store }) => {
    const first = await store.newShell('/a');
    const second = await store.newShell('/b');
    assert.equal(store.active, second.id);
    assert.equal(store.selectNth(1), true);
    assert.equal(store.active, first.id);
    assert.equal(store.selectNth(3), false, 'no third tab');
    assert.equal(store.active, first.id);
    store.setOpen(false);
    assert.equal(store.selectNth(2), false, 'a hidden panel keeps its tab');
    assert.equal(store.active, first.id);
  });
});

test('TerminalsStore reports a failed start and opens a shell when toggled empty', async () => {
  await withTerminals(async ({ store, mock, toasts }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
    mock.launch = { ...mock.launch, panel: false };
    await assert.rejects(store.runResume(meta.ref), /not available/);
    assert.equal(await store.newShell(), null);
    assert.equal(toasts.at(-1).title, "Couldn't open a terminal");
    assert.equal(store.tabs.length, 0);

    mock.launch = { ...mock.launch, panel: true };
    await store.toggle('/work');
    assert.deepEqual([store.open, store.tabs.length, store.tabs[0].kind], [true, 1, 'shell']);
    await store.toggle();
    assert.deepEqual([store.open, store.tabs.length], [false, 1], 'hiding keeps the tabs running');
    await store.toggle();
    assert.equal(store.tabs.length, 1, 'showing again opens no second shell');
  });
});

test('TerminalsStore ends terminals whose start reply went stale', async () => {
  await withTerminals(async ({ store, mock }) => {
    const panelShell = mock.panelShell.bind(mock);
    mock.panelShell = async (cwd, size) => {
      await panelShell(cwd, size);
      throw new StaleReplyError();
    };
    assert.equal(await store.newShell(), null);
    assert.equal(store.tabs.length, 0);
    assert.equal(mock.panelTerminals.size, 0, 'the orphan was closed');
  });
});

test('TerminalsStore sends a tab its input one write at a time, in order', async () => {
  await withTerminals(async ({ store, mock }) => {
    const tab = await store.newShell();
    const sent = [];
    const pending = [];
    mock.terminalWrite = (id, data) => {
      sent.push(`${id}:${data}`);
      return new Promise((resolve) => pending.push(resolve));
    };
    store.write(tab.id, 'a');
    store.write(tab.id, 'b');
    store.write(tab.id, 'c');
    assert.deepEqual(sent, [`${tab.id}:a`], 'later input waits for the write in flight');
    pending.shift()();
    await new Promise((r) => setTimeout(r));
    assert.deepEqual(sent, [`${tab.id}:a`, `${tab.id}:bc`], 'what waited goes together');
    pending.shift()();
    await new Promise((r) => setTimeout(r));
    store.write(tab.id, 'd');
    assert.deepEqual(sent.at(-1), `${tab.id}:d`, 'an idle tab writes at once');
    pending.shift()();
  });
});

test('TerminalsStore types dropped files into the active tab, copying them to an SSH host', async () => {
  await withTerminals(async ({ store, mock, toasts, toast }) => {
    const local = await store.newShell();
    const sink = sinkFor(store, local.id);
    await store.dropFiles(['/home/me/shot.png', '/home/me/my shot.png']);
    assert.deepEqual(sink.pastes, ['/home/me/shot.png ', "'/home/me/my shot.png' "], 'each path is its own paste');
    assert.equal(toasts.length, 0);

    mock.connState = { ...mock.connState, phase: 'connected', host: 'box' };
    const remote = await store.newShell();
    const remoteSink = sinkFor(store, remote.id);
    const drop = mock.terminalDropFiles.bind(mock);
    let shownWhileCopying;
    mock.terminalDropFiles = async (id, paths) => {
      shownWhileCopying = toast.current?.title;
      return drop(id, paths);
    };
    await store.dropFiles(['C:\\Users\\me\\shot.png']);
    assert.equal(shownWhileCopying, 'Copying the file to box');
    assert.equal(toast.current, null, 'the progress toast goes once the copy is done');
    assert.deepEqual(remoteSink.pastes, ['/tmp/agent-sessions-drop.mock/shot.png ']);
    assert.deepEqual(sink.pastes.length, 2, 'only the active tab gets the paths');

    mock.terminalDropFiles = async () => {
      throw new Error('Permission denied (publickey)');
    };
    await store.dropFiles(['/a.png', '/b.png']);
    assert.deepEqual([toasts.at(-2).title, toasts.at(-1).title, toasts.at(-1).body], ['Copying 2 files to box', "Couldn't add the dropped files", 'Permission denied (publickey)']);
    assert.equal(remoteSink.pastes.length, 1);

    // An ended tab takes no files.
    mock.terminalDropFiles = drop;
    store.select(local.id);
    const failed = mock.droppedFiles.length;
    store.tabs = store.tabs.map((t) => (t.id === local.id ? { ...t, status: 'exited', exitCode: 1 } : t));
    await store.dropFiles(['/c.png']);
    assert.equal(mock.droppedFiles.length, failed);
  });
});

test('TerminalsStore keeps its height within the window and remembers the layout', async () => {
  const { clampPanelHeight, PANEL_HEIGHT, decodeTerminalData } = await loadTerminalsStore();
  assert.equal(clampPanelHeight(50, 1000), PANEL_HEIGHT.min);
  assert.equal(clampPanelHeight(900, 1000), 700);
  assert.equal(clampPanelHeight(900, 800), 500, 'the sessions keep 300px');
  assert.equal(clampPanelHeight(333.4, 1000), 333);
  assert.deepEqual(new TextDecoder().decode(decodeTerminalData(Buffer.from('héllo ✓').toString('base64'))), 'héllo ✓');

  await withTerminals(async ({ store, storage, TerminalsStore }) => {
    assert.deepEqual([store.open, store.height, store.position], [false, PANEL_HEIGHT.default, 'bottom']);
    store.setHeight(400, 1000);
    store.setPosition('left');
    store.setOpen(true);
    assert.deepEqual(JSON.parse(storage.get('agent-sessions:terminal-panel')), {
      open: true,
      height: 400,
      position: 'left',
    });
    const restored = new TerminalsStore();
    restored.init();
    assert.deepEqual([restored.open, restored.height, restored.position], [true, 400, 'left']);
    restored.destroy();

    storage.set('agent-sessions:terminal-panel', '{not json');
    const fallback = new TerminalsStore();
    fallback.init();
    assert.deepEqual([fallback.open, fallback.height, fallback.position], [false, PANEL_HEIGHT.default, 'bottom']);
    fallback.destroy();
  });
});

test('MockBackendAPI runs fake panel terminals on every host', async () => {
  const mock = new MockBackendAPI();
  const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
  const events = [];
  mock.onEvent('term:data', (e) => events.push(['data', e.id, Buffer.from(e.data, 'base64').toString()]));
  mock.onEvent('term:exit', (e) => events.push(['exit', e.id, e.code]));

  const local = await mock.panelResume(meta.ref, { cols: 80, rows: 24 });
  assert.deepEqual([local.host, local.kind], ['', 'agent']);
  assert.ok(events.some(([kind, id]) => kind === 'data' && id === local.id), 'output can precede the reply');
  await mock.terminalWrite(local.id, 'ab\x7f\r');
  assert.match(events.at(-1)[2], /^ab\x08 \x08\r\nmock: a\r\n/);
  assert.ok(mock.panelTerminals.get(local.id).unacked > 0);
  await mock.terminalAck(local.id, 1 << 20);
  assert.equal(mock.panelTerminals.get(local.id).unacked, 0);
  await mock.terminalClose(local.id);
  assert.deepEqual(events.at(-1), ['exit', local.id, 129]);
  await assert.rejects(mock.terminalWrite(local.id, 'x'), /terminal not found/);

  // SSH works in the panel; agents need the server, a shell does not.
  await mock.connect('dev-box');
  assert.equal((await mock.panelResume(meta.ref, { cols: 80, rows: 24 })).host, 'dev-box');
  mock.setMockConnectionState({ phase: 'reconnecting' });
  await assert.rejects(mock.panelResume(meta.ref, { cols: 80, rows: 24 }), /remote disconnected/);
  assert.equal((await mock.panelShell('', { cols: 80, rows: 24 })).kind, 'shell');

  assert.equal((await mock.setTerminalOpen('external')).open, 'external');
  await assert.rejects(mock.setTerminalOpen('tmux'), /unknown terminal mode/);
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
    store('files', 'files'),
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
  // A pasted image has no name or path; its blob reference isn't copied.
  const pasted = (mime) => ({ id: 'p', role: 'user', time: '', parts: [{ kind: 'file', file: { mime, ref: 'rec:10:0' } }] });
  assert.equal(messageMarkdown(pasted('image/png')), 'Image');
  assert.equal(messageMarkdown(pasted('application/pdf')), 'Attachment');
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
    ["import { filterSessionsByAge, refsEqual } from '../../manage';", 'const filterSessionsByAge = () => [];const refsEqual = (a, b) => a === b;'],
    ["import { nodeToHighlight, sessionToSelect } from '../../tree';", 'const nodeToHighlight = () => null;const sessionToSelect = () => null;'],
    ["import GroupNodeItem from './GroupNodeItem.svelte';", 'const GroupNodeItem = () => {};'],
  ]);
  return renderWithFixture(groupTreeModule, {
    appState: { loadingGroups: false, groups: [], selectedSessionRef: null, filter: { query: '', path: '', liveOnly: false, archived: false, ...filter } },
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

test('a translation replaces the text parts where the first was', () => {
  const file = { kind: 'file', file: { name: 'a.png', mime: 'image/png', ref: 'r' } };
  assert.deepEqual(
    withTranslation([{ kind: 'text', text: 'one' }, file, { kind: 'text', text: 'two' }], 'xin chào'),
    [{ kind: 'text', text: 'xin chào' }, file],
  );
  assert.deepEqual(withTranslation([file], 'xin chào'), [{ kind: 'text', text: 'xin chào' }, file]);
});

test('the print title is the edited title without unsafe file name characters', () => {
  assert.equal(printTitle('Fix: a/b "c"?'), 'Fix a b c');
  assert.equal(printTitle('  '), 'Message');
  assert.equal(printTitle('x'.repeat(200)), 'x'.repeat(100));
});

test('a message to print becomes Markdown with its images as attachment lines', () => {
  const shot = { mime: 'image/png', ref: 'r1' };
  const named = { name: 'diagram [v2].png', mime: 'image/png', ref: 'r2' };
  const { markdown, attachments } = printMarkdown([
    { kind: 'text', text: '  Hello  ' },
    { kind: 'file', file: shot },
    { kind: 'file', file: { name: 'notes.txt', mime: 'text/plain' } },
    { kind: 'file', file: named },
    { kind: 'notice', text: 'Assistant error' },
  ]);
  assert.equal(
    markdown,
    'Hello\n\n![Image](attachment:1)\n\n📎 notes.txt\n\n![diagram v2.png](attachment:2)\n\n> Assistant error',
  );
  assert.deepEqual(attachments, [shot, named]);
});

test('print blocks split the Markdown at attachment lines outside code', () => {
  assert.deepEqual(printBlocks('Intro\n\n![a.png](attachment:2)\nAfter\n```\n![x](attachment:1)\n```'), [
    { kind: 'markdown', text: 'Intro\n' },
    { kind: 'image', index: 2, alt: 'a.png' },
    { kind: 'markdown', text: 'After\n```\n![x](attachment:1)\n```' },
  ]);
  assert.deepEqual(printBlocks('Inline ![a](attachment:1) stays'), [{ kind: 'markdown', text: 'Inline ![a](attachment:1) stays' }]);
  assert.deepEqual(printBlocks(''), []);
});

test('buildFileTree nests folders, folders before files', () => {
  const tree = buildFileTree(['main.go', 'src/b.ts', 'src/a.ts', 'src/lib/x.ts', 'README.md']);
  assert.deepEqual(tree.map((n) => n.name), ['src', 'main.go', 'README.md']);
  const src = tree[0];
  assert.deepEqual(src.children.map((n) => n.path), ['src/lib', 'src/a.ts', 'src/b.ts']);
  assert.deepEqual(src.children[0].children.map((n) => n.path), ['src/lib/x.ts']);
  assert.equal(tree[1].children, undefined);
});

test('filterFiles matches characters in order and ranks file names first', () => {
  const paths = ['src/lib/apps/x.ts', 'src/app.ts', 'docs/a-p-p.md', 'main.go'];
  assert.deepEqual(filterFiles(paths, 'app'), ['src/app.ts', 'docs/a-p-p.md', 'src/lib/apps/x.ts']);
  assert.deepEqual(filterFiles(paths, 'src app'), ['src/app.ts', 'src/lib/apps/x.ts']);
  assert.equal(matchScore('main.go', 'xyz'), -1);
  assert.deepEqual(filterFiles(paths, ''), paths);
  assert.equal(filterFiles(paths, '', 2).length, 2);
});

test('fileLanguage supports source and text files only', () => {
  assert.equal(fileLanguage('internal/app.go'), 'go');
  assert.equal(fileLanguage('src/App.svelte'), 'xml');
  assert.equal(fileLanguage('x.TSX'), 'typescript');
  assert.equal(fileLanguage('README.md'), 'markdown');
  assert.equal(fileLanguage('Makefile'), 'makefile');
  assert.equal(fileLanguage('Cargo.lock'), '');
  assert.equal(fileLanguage('.env.local'), '');
  assert.equal(fileLanguage('assets/logo.png'), null);
  assert.equal(fileLanguage('app.exe'), null);
  assert.equal(fileLanguage('noext'), null);
});

test('parentFolders lists the folders above a file', () => {
  assert.deepEqual(parentFolders('a/b/c.ts'), ['a', 'a/b']);
  assert.deepEqual(parentFolders('c.ts'), []);
});

test('splitHighlightedLines keeps every line complete HTML', () => {
  const html = 'a <span class="hljs-comment">/* one\ntwo <span class="hljs-doctag">@x\ny</span> three\n*/</span> b\n';
  assert.deepEqual(splitHighlightedLines(html), [
    'a <span class="hljs-comment">/* one</span>',
    '<span class="hljs-comment">two <span class="hljs-doctag">@x</span></span>',
    '<span class="hljs-comment"><span class="hljs-doctag">y</span> three</span>',
    '<span class="hljs-comment">*/</span> b',
  ]);
  assert.deepEqual(splitHighlightedLines('x\n\ny'), ['x', '', 'y']);
  assert.deepEqual(splitHighlightedLines(''), ['']);
  assert.deepEqual(splitLines('a\nb\n'), ['a', 'b']);
  assert.deepEqual(splitLines('a\n\n'), ['a', '']);
  assert.deepEqual(splitLines(''), ['']);
});

test('copyToClipboard writes natively in the desktop app, falling back to the web clipboard', async () => {
  const written = [];
  const web = { writeText: async (t) => { written.push(['web', t]); } };
  const saved = Object.getOwnPropertyDescriptor(globalThis, 'navigator');
  Object.defineProperty(globalThis, 'navigator', { value: { clipboard: web }, configurable: true });
  try {
    globalThis.runtime = { ClipboardSetText: async (t) => { written.push(['native', t]); return true; } };
    assert.equal(await copyToClipboard('a'), true);
    globalThis.runtime = { ClipboardSetText: async () => false };
    assert.equal(await copyToClipboard('b'), true);
    delete globalThis.runtime;
    web.writeText = async () => { throw new Error('NotAllowedError'); };
    assert.equal(await copyToClipboard('c'), false);
    assert.deepEqual(written, [['native', 'a'], ['web', 'b']]);
  } finally {
    delete globalThis.runtime;
    if (saved) Object.defineProperty(globalThis, 'navigator', saved);
    else delete globalThis.navigator;
  }
});

test('git marks: deleted files listed, folders take their most important change', () => {
  const status = { 'src/a.ts': 'U', 'src/lib/b.ts': 'M', 'gone.txt': 'D', 'docs/new.md': 'A' };
  assert.deepEqual(withDeleted(['src/a.ts', 'src/lib/b.ts'], status), ['gone.txt', 'src/a.ts', 'src/lib/b.ts']);
  assert.deepEqual(withDeleted(['x'], undefined), ['x']);
  assert.deepEqual(Object.fromEntries(folderStatus(status)), { src: 'M', 'src/lib': 'M', docs: 'A' });
  assert.equal(folderStatus(undefined).size, 0);
});

test('lineMarks places gutter bars for changed lines and markers for removed ones', () => {
  const marks = lineMarks(
    [
      { kind: 'modified', start: 2, count: 1 },
      { kind: 'added', start: 4, count: 2 },
      { kind: 'deleted', start: 0, count: 0 },
      { kind: 'deleted', start: 6, count: 0 },
    ],
    6
  );
  assert.deepEqual(marks, ['deleted-above', 'modified', undefined, 'added', 'added', 'deleted-below']);
  assert.deepEqual(lineMarks(undefined, 2), [undefined, undefined]);
  assert.deepEqual(lineMarks([{ kind: 'deleted', start: 0, count: 0 }], 0), []);
});

test('diffRows puts removed lines above what replaced them', () => {
  const changes = [
    { kind: 'deleted', start: 0, count: 0, removed: ['first'] },
    { kind: 'modified', start: 2, count: 1, removed: ['old two'] },
    { kind: 'added', start: 3, count: 1 },
    { kind: 'deleted', start: 3, count: 0, removed: ['old end'] },
  ];
  const rows = diffRows(changes, 3).map((r) => (r.kind === 'removed' ? `-${r.text}` : `${r.index + 1}${r.mark ? ':' + r.mark : ''}`));
  assert.deepEqual(rows, ['-first', '1', '-old two', '2:modified', '3:added', '-old end']);
  assert.deepEqual(diffRows(undefined, 2).map((r) => r.index), [0, 1]);
});

test('fileChanges marks an untracked file as all added', () => {
  assert.deepEqual(fileChanges('U', undefined, 3), [{ kind: 'added', start: 1, count: 3 }]);
  assert.equal(fileChanges('M', undefined, 3), undefined);
  const c = [{ kind: 'added', start: 1, count: 1 }];
  assert.equal(fileChanges('M', c, 3), c);
});

test('searchFiles finds ignored files, below listed ones that match as well', () => {
  const paths = ['src/env.ts', 'README.md'];
  const ignored = ['.env', 'src/.env.local'];
  const got = searchFiles(paths, ignored, '.env');
  assert.ok(got.includes('.env') && got.includes('src/.env.local'));
  assert.ok(!got.includes('README.md'));
  assert.deepEqual(searchFiles(paths, ignored, 'env.ts'), ['src/env.ts']);
  assert.deepEqual(searchFiles(['a/x.ts'], ['b/x.ts'], 'x.ts'), ['a/x.ts', 'b/x.ts']);
  assert.deepEqual(searchFiles(paths, undefined, 'readme'), ['README.md']);
});

const FILES_STORE_URL = new URL('../src/lib/stores/files.svelte.ts', import.meta.url);
let filesStoreModule;

async function loadFilesStore() {
  const lib = (rel) => JSON.stringify(new URL(rel, FILES_STORE_URL).href);
  filesStoreModule ??= await loadServerModule(FILES_STORE_URL, [
    ["import { api } from '../api';", fixtureStore('api', '__storeFixture')],
    ["'../manage'", lib('../manage.ts')],
    ["'../link'", lib('../link.ts')],
  ]);
  return filesStoreModule;
}

test('FilesStore: one click previews, double click keeps a tab, folders are remembered', async () => {
  const { FilesStore } = await loadFilesStore();
  globalThis.__storeFixture = {
    api: {
      listFiles: async (dir) => ({ root: dir, files: ['a.ts', 'b.ts', 'c.ts', 'd.ts'] }),
      readFile: async (_dir, path) => ({ path, size: 1, text: path }),
    },
  };
  try {
    const store = new FilesStore();
    await store.show('/p');
    const tabs = () => store.tabs.map((t) => `${t.path}${t.preview ? '*' : ''}`);

    store.openFile('a.ts');
    store.openFile('b.ts');
    assert.deepEqual(tabs(), ['b.ts*'], 'a preview is replaced by the next file');
    store.openFile('b.ts', { keep: true });
    assert.deepEqual(tabs(), ['b.ts'], 'double click keeps the preview');
    store.openFile('c.ts');
    store.openFile('d.ts', { keep: true });
    assert.deepEqual(tabs(), ['b.ts', 'c.ts*', 'd.ts']);
    store.openFile('a.ts');
    assert.deepEqual(tabs(), ['b.ts', 'a.ts*', 'd.ts']);
    store.openFile('d.ts');
    assert.deepEqual(tabs(), ['b.ts', 'a.ts*', 'd.ts'], 'a file with a tab is just shown');
    assert.equal(store.selected, 'd.ts');
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(store.content?.text, 'd.ts');

    store.closeTab('d.ts');
    assert.equal(store.selected, 'a.ts', 'closing the shown tab shows its neighbour');
    store.closeTab('b.ts');
    store.closeTab('a.ts');
    assert.equal(store.selected, null);

    store.openFile('a.ts', { keep: true });
    store.openFile('b.ts');
    store.setExpanded(new Set(['src']));
    store.close();
    assert.equal(store.expanded.size, 0);
    assert.equal(store.tabs.length, 0);
    await store.show('/p');
    assert.deepEqual([...store.expanded], ['src'], 'open folders come back with the directory');
    assert.deepEqual(tabs(), ['a.ts', 'b.ts*'], 'and so do the tabs');
    assert.equal(store.selected, 'b.ts');

    let listed = 0;
    globalThis.__storeFixture.api.listFiles = async (dir) => {
      listed++;
      return { root: dir, files: ['a.ts', 'b.ts', 'e.ts'] };
    };
    globalThis.__storeFixture.api.readFile = async (_dir, path) => ({ path, size: 2, text: path + '!' });
    await store.refresh();
    await new Promise((r) => setTimeout(r, 0));
    assert.equal(listed, 1);
    assert.deepEqual(store.listing.files, ['a.ts', 'b.ts', 'e.ts'], 'refresh lists the files again');
    assert.equal(store.content?.text, 'b.ts!', 'and reloads the open ones');
    assert.equal(store.refreshing, false);
    store.reset();
    await store.show('/p');
    assert.equal(store.expanded.size, 0, 'another host forgets them');
  } finally {
    delete globalThis.__storeFixture;
  }
});
