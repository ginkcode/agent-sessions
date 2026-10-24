import test from 'node:test';
import assert from 'node:assert/strict';
import {
  LINK_ALLOWANCE_MS,
  matchNewSessions,
  mayHoldNewSession,
  sameDirectory,
  touchedLinks,
} from '../src/lib/sessionLinks.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { fixtureStore, loadServerModule, renderSessionHeader, unescapeHTML } from './helpers.mjs';

const T0 = Date.parse('2026-10-24T10:00:00Z');
const at = (ms) => new Date(T0 + ms).toISOString();
const session = (id, agent, cwd, createdMs, extra = {}) => ({ ref: { agent, id }, cwd, title: id, createdAt: at(createdMs), ...extra });
const tab = (id, agent, dir, startedMs) => ({ id, agent, dir, startedAt: T0 + startedMs });
const links = (map) => Object.fromEntries([...map].map(([id, s]) => [id, s.ref.id]));

test('a tab links to the first session of its agent in its directory created after it started', () => {
  const sessions = [
    session('old', 'codex', '/p', -60_000),
    session('other-agent', 'claude-code', '/p', 1000),
    session('other-dir', 'codex', '/q', 1000),
    session('sub', 'codex', '/p', 1500, { parentId: 'x' }),
    session('later', 'codex', '/p', 9000),
    session('mine', 'codex', '/p/', 2000),
  ];
  assert.deepEqual(links(matchNewSessions([tab('t1', 'codex', '/p', 0)], sessions, new Set())), { t1: 'mine' });
});

test('the allowance covers clock rounding, not older sessions', () => {
  const early = [session('rounded', 'codex', '/p', -LINK_ALLOWANCE_MS + 1)];
  assert.deepEqual(links(matchNewSessions([tab('t1', 'codex', '/p', 0)], early, new Set())), { t1: 'rounded' });
  const old = [session('old', 'codex', '/p', -LINK_ALLOWANCE_MS - 1)];
  assert.equal(matchNewSessions([tab('t1', 'codex', '/p', 0)], old, new Set()).size, 0);
});

test('two tabs in one directory link in start order, and taken sessions are skipped', () => {
  const sessions = [session('b', 'codex', '/p', 3000), session('a', 'codex', '/p', 2000)];
  const pending = [tab('second', 'codex', '/p', 1000), tab('first', 'codex', '/p', 0)];
  assert.deepEqual(links(matchNewSessions(pending, sessions, new Set())), { first: 'a', second: 'b' });
  assert.deepEqual(links(matchNewSessions(pending, sessions, new Set(['codex:a']))), { first: 'b' });
});

test('directories match as written, less trailing separators', () => {
  assert.ok(sameDirectory('/p/', '/p'));
  assert.ok(sameDirectory('C:\\work\\', 'C:\\work'));
  assert.ok(sameDirectory('/', '/'));
  assert.ok(sameDirectory('C:\\', 'C:\\'));
  assert.ok(!sameDirectory('/p', '/pq'));
  assert.ok(!sameDirectory('', ''));
});

test('only changes that may hold a pending agent session are looked at', () => {
  const agents = new Set(['codex']);
  assert.ok(mayHoldNewSession({ changed: null, removed: null, groupsDirty: true }, agents), 'a rebuilt catalog');
  assert.ok(mayHoldNewSession({ changed: [{ agent: 'codex', id: 'x' }], removed: null, groupsDirty: false }, agents));
  assert.ok(!mayHoldNewSession({ changed: [{ agent: 'opencode', id: 'x' }], removed: null, groupsDirty: true }, agents));
  const linked = [{ agent: 'codex', id: 'a' }, { agent: 'codex', id: 'b' }];
  assert.deepEqual(
    touchedLinks({ changed: [{ agent: 'codex', id: 'a' }], removed: [{ agent: 'codex', id: 'b' }], groupsDirty: false }, linked),
    { changed: [linked[0]], removed: [linked[1]] },
  );
});

const TERMINALS_URL = new URL('../src/lib/stores/terminals.svelte.ts', import.meta.url);
const LINKS_URL = new URL('../src/lib/stores/sessionLinks.svelte.ts', import.meta.url);

async function withLinks(fn) {
  const lib = (url, rel) => JSON.stringify(new URL(rel, url).href);
  const { TerminalsStore } = await loadServerModule(TERMINALS_URL, [
    [
      "import { api, subscribeTerminalData, subscribeTerminalExit } from '../api';",
      fixtureStore('api', '__storeFixture') +
        "const subscribeTerminalData = (fn) => api.onEvent('term:data', fn);" +
        "const subscribeTerminalExit = (fn) => api.onEvent('term:exit', fn);",
    ],
    ["'../link'", lib(TERMINALS_URL, '../link.ts')],
    ["'../portable'", lib(TERMINALS_URL, '../portable.ts')],
    ["'../guidance'", lib(TERMINALS_URL, '../guidance.ts')],
    ["'../hosts'", lib(TERMINALS_URL, '../hosts.ts')],
    ["'../attention'", lib(TERMINALS_URL, '../attention.ts')],
    ["import { toast } from './toast.svelte';", fixtureStore('toast', '__storeFixture')],
  ]);
  const { SessionLinksStore } = await loadServerModule(LINKS_URL, [
    [
      "import { api, subscribeCatalogChanged } from '../api';",
      fixtureStore('api', '__storeFixture') + "const subscribeCatalogChanged = (fn) => api.onEvent('catalog:changed', fn);",
    ],
    ["'../manage'", lib(LINKS_URL, '../manage.ts')],
    ["'../sessionLinks'", lib(LINKS_URL, '../sessionLinks.ts')],
    ["import { link } from './link.svelte';", fixtureStore('link', '__storeFixture')],
    ["import { terminals } from './terminals.svelte';", fixtureStore('terminals', '__storeFixture')],
  ]);
  const mock = new MockBackendAPI();
  // The fixture proxy calls methods on itself; bind them to their objects.
  const bound = (target) => new Proxy(target, { get: (t, key) => (typeof t[key] === 'function' ? t[key].bind(t) : t[key]) });
  const fixture = { api: bound(mock), toast: { show() {} }, link: { dataHost: undefined } };
  globalThis.__storeFixture = fixture;
  const previous = globalThis.localStorage;
  globalThis.localStorage = { getItem: () => null, setItem() {} };
  const terminals = new TerminalsStore();
  fixture.terminals = bound(terminals);
  terminals.init();
  const store = new SessionLinksStore();
  store.init();
  try {
    return await fn({ mock, terminals, store, fixture });
  } finally {
    store.destroy();
    terminals.destroy();
    delete globalThis.__storeFixture;
    globalThis.localStorage = previous;
  }
}

async function until(check) {
  for (let i = 0; i < 100 && !check(); i++) await new Promise((r) => setTimeout(r, 2));
  assert.ok(check(), 'condition reached');
}

test('a new session tab links when the agent saves its session, and is named after it', async () => {
  await withLinks(async ({ mock, terminals }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', desc: true });
    const first = await terminals.runNewSession('codex', meta.cwd);
    const second = await terminals.runNewSession('codex', meta.cwd);
    assert.equal(terminals.tabs[0].linkPending, true);
    assert.equal(terminals.tabFor({ agent: 'codex', id: 'x' }, ''), undefined);

    // The second tab's prompt comes first; the earlier tab still takes the
    // earlier session, the documented order.
    terminals.write(second.id, 'add a login page\r');
    await until(() => terminals.tabs.find((t) => t.id === first.id).session);
    const linked = terminals.tabs.find((t) => t.id === first.id);
    assert.equal(linked.label, 'add a login page');
    assert.equal(linked.linkPending, false);
    assert.equal(terminals.tabFor(linked.session, '')?.id, first.id);
    assert.equal(terminals.tabFor(linked.session, 'dev-box'), undefined, 'another host has its own sessions');

    terminals.write(first.id, 'fix the tests\r');
    await until(() => terminals.tabs.find((t) => t.id === second.id).session);
    assert.notDeepEqual(terminals.tabs.find((t) => t.id === second.id).session, linked.session);

    // Closing the tab ends its link.
    terminals.close(first.id, true);
    assert.equal(terminals.tabFor(linked.session, ''), undefined);
  });
});

test('resumed tabs are linked from the start; tabs of a host not shown wait', async () => {
  await withLinks(async ({ mock, terminals, store, fixture }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', desc: true });
    const resumed = await terminals.runResume(meta.ref, meta.cwd, meta.title);
    assert.deepEqual(terminals.tabFor(meta.ref, '')?.id, resumed.id);

    const fresh = await terminals.runNewSession('claude-code', meta.cwd);
    fixture.link = { dataHost: 'dev-box' };
    terminals.write(fresh.id, 'hello\r');
    await new Promise((r) => setTimeout(r, 20));
    assert.equal(terminals.tabs.find((t) => t.id === fresh.id).session, undefined, 'the shown catalog is not its host');

    fixture.link = { dataHost: undefined };
    await store.apply({ changed: null, removed: null, groupsDirty: true });
    assert.equal(terminals.tabs.find((t) => t.id === fresh.id).session?.agent, 'claude-code');

    // A removed session unlinks; the tab keeps running.
    await store.apply({ changed: null, removed: [meta.ref], groupsDirty: true });
    assert.equal(terminals.tabFor(meta.ref, ''), undefined);
    assert.equal(terminals.tabs.find((t) => t.id === resumed.id).status, 'running');
  });
});

test('the header shows a session running in the panel and a button to its tab', async () => {
  const shown = [];
  const html = await renderSessionHeader(
    {
      link: { dataHost: undefined },
      launcher: { canOpen: true, openMode: 'panel', canOpenPanel: true },
      terminals: { tabFor: (ref, host) => (ref.id === 's1' && host === '' ? { id: 't9' } : undefined), show: (id) => shown.push(id) },
    },
  );
  assert.match(html, /● Running</);
  assert.match(unescapeHTML(html), /resume-main[^"]*"[^>]*title="Show the terminal tab running this session"[^>]*>\s*Show terminal/);
  assert.doesNotMatch(html, />\s*Resume\s*</);

  const idle = await renderSessionHeader({
    link: { dataHost: undefined },
    launcher: { canOpen: true, openMode: 'panel', canOpenPanel: true },
    terminals: { tabFor: () => undefined },
  });
  assert.doesNotMatch(idle, /● Running</);
  assert.match(idle, />\s*Resume\s*</);
});

test('agent tabs: a bell needs the user unless they are looking, and the session reports its turn', async () => {
  await withLinks(async ({ mock, terminals, store }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', desc: true });
    const away = await terminals.runResume(meta.ref, meta.cwd, meta.title);
    const shown = await terminals.runNewSession('codex', meta.cwd);
    const shell = await terminals.newShell();
    terminals.select(shown.id);
    const ring = (id) => mock.emit('term:data', { id, data: btoa('\x07') });

    ring(shown.id);
    ring(shell.id);
    ring(away.id);
    assert.equal(terminals.attention[shown.id] ?? 'idle', 'idle', 'the user is looking at it');
    assert.equal(terminals.attention[shell.id], undefined, 'shells are not followed');
    assert.equal(terminals.attention[away.id], 'needs');

    terminals.select(away.id);
    assert.equal(terminals.attention[away.id], 'idle', 'seen');
    ring(shown.id);
    assert.equal(terminals.attention[shown.id], 'needs');
    terminals.setWindowFocused(false);
    ring(away.id);
    assert.equal(terminals.attention[away.id], 'needs', 'the window is in the background');
    terminals.setOpen(true);
    terminals.setWindowFocused(true);
    assert.equal(terminals.attention[away.id], 'idle');

    // A report after a prompt and the work it started: the turn is done.
    terminals.write(away.id, 'hi\r');
    await new Promise((r) => setTimeout(r, 260));
    mock.emit('term:data', { id: away.id, data: btoa('answer') });
    assert.equal(terminals.attention[away.id], 'working');
    terminals.sessionReported(meta.ref, { title: meta.title, turn: 'tool' });
    assert.equal(terminals.attention[away.id], 'working', 'still drawing');
    await store.apply({ changed: [meta.ref], removed: null, groupsDirty: false });

    terminals.close(away.id, true);
    assert.equal(terminals.attention[away.id], undefined);
  });
});

test('every pane of the shown tab counts as viewed, not only the focused one', async () => {
  await withLinks(async ({ mock, terminals }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', desc: true });
    const beside = await terminals.runResume(meta.ref, meta.cwd, meta.title);
    const shell = await terminals.split(beside.id);
    const other = await terminals.runNewSession('codex', meta.cwd);
    const viewed = [];
    terminals.onViewing((id) => viewed.push(id));
    terminals.select(shell.id);
    assert.deepEqual(viewed.sort(), [beside.id, shell.id].sort(), 'showing a tab views each of its panes');
    assert.deepEqual([terminals.viewedTab, terminals.viewedPanes], [shell.id, [beside.id, shell.id]]);

    const ring = (id) => mock.emit('term:data', { id, data: btoa('\x07') });
    ring(beside.id);
    ring(other.id);
    assert.equal(terminals.attention[beside.id] ?? 'idle', 'idle', 'the pane beside the focused one is in view');
    assert.equal(terminals.attention[other.id], 'needs');

    viewed.length = 0;
    terminals.select(beside.id);
    assert.deepEqual(viewed, [], 'focusing another pane of the same tab views nothing new');
    terminals.select(other.id);
    assert.deepEqual([viewed, terminals.attention[other.id]], [[other.id], 'idle']);
    ring(beside.id);
    assert.equal(terminals.attention[beside.id], 'needs', 'its tab is no longer shown');
  });
});

test('a mock agent turn ends waiting for approval: the tab needs the user', async () => {
  await withLinks(async ({ mock, terminals }) => {
    mock.agentTurnMs = 700;
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', desc: true });
    const agent = await terminals.runNewSession('codex', meta.cwd);
    terminals.write(agent.id, 'approve the cleanup\r');
    await until(() => terminals.tabs.find((t) => t.id === agent.id).session);
    for (let i = 0; i < 50 && terminals.attention[agent.id] !== 'working'; i++) await new Promise((r) => setTimeout(r, 20));
    assert.equal(terminals.attention[agent.id], 'working');
    // The user looks elsewhere, so the bell counts too.
    await terminals.newShell();
    for (let i = 0; i < 150 && terminals.attention[agent.id] !== 'needs'; i++) await new Promise((r) => setTimeout(r, 20));
    assert.equal(terminals.attention[agent.id], 'needs');
    const linked = terminals.tabs.find((t) => t.id === agent.id).session;
    assert.equal((await mock.getSessionMeta(linked)).turn, 'tool');
    terminals.close(agent.id, true);
  });
});
