import test from 'node:test';
import assert from 'node:assert/strict';
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
  affectsSessionList,
  findGroup,
  groupSessionKeys,
  hasRef,
  isFullRefresh,
  RequestSequence,
} from '../src/lib/catalog.ts';
import { subscribeCatalogChanged, subscribeIndexProgress, subscribeMouseNavigate } from '../src/lib/api.ts';
import { WINDOWS_DIRS } from './helpers.mjs';

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

test('searchFilterFromApp passes Windows directories through unchanged', () => {
  for (const dir of WINDOWS_DIRS) {
    assert.deepEqual(searchFilterFromApp('codex', dir, 15), { agents: ['codex'], dir, limit: 15 });
  }
  // Only surrounding whitespace is trimmed; inner spaces and separators stay.
  assert.deepEqual(searchFilterFromApp(undefined, '  C:\\Program Files\\app\\  '), {
    dir: 'C:\\Program Files\\app\\', limit: 30,
  });
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
