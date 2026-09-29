import test from 'node:test';
import assert from 'node:assert/strict';
import { formatTokens, formatCost, formatBytes } from '../src/lib/format.ts';
import { formatRelativeTime, formatAbsoluteTime, isKnownTime, formatAgo } from '../src/lib/date.ts';
import { highlightCode, detectLanguage } from '../src/lib/highlight.ts';
import { renderMarkdown } from '../src/lib/markdown.ts';
import {
  refKey,
  refsEqual,
  nextSelectionAfterDelete,
  filterSessionsByAge,
  summarizePreview,
  errorText,
  isPermanentDelete,
  formatDeleteResultSummary,
} from '../src/lib/manage.ts';
import { MockBackendAPI, highlightedSnippet } from '../src/lib/mock/mockApi.ts';
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
} from '../src/lib/tree.ts';
import {
  affectsSessionList,
  findGroup,
  groupSessionKeys,
  hasRef,
  isFullRefresh,
  RequestSequence,
} from '../src/lib/catalog.ts';
import { subscribeCatalogChanged, subscribeIndexProgress } from '../src/lib/api.ts';

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
  assert.deepEqual(filterSessionsByAge(sessions, 7, new Date('2026-09-28T00:00:00Z')).map(s => s.ref), [a, b]);
  assert.equal(filterSessionsByAge(sessions, 0).length, 3);
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
