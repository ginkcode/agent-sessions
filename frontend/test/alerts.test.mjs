import test from 'node:test';
import assert from 'node:assert/strict';
import { AlertLog, alertText, attentionSummary, mostUrgent } from '../src/lib/alerts.ts';
import { countedTitle } from '../src/lib/hosts.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { fixtureStore, loadServerModule } from './helpers.mjs';

test('the most urgent state: needs, then done, then working', () => {
  assert.equal(mostUrgent(['idle', 'working', 'done', 'exited']), 'done');
  assert.equal(mostUrgent(['working', 'needs', 'done']), 'needs');
  assert.equal(mostUrgent(['idle', 'working']), 'working');
  assert.equal(mostUrgent(['idle', 'exited']), null);
  assert.equal(mostUrgent([]), null);
  assert.equal(attentionSummary('needs'), 'An agent needs you');
  assert.equal(attentionSummary(null), '');
});

test('notification text names the agent, the tab and what the agent said', () => {
  assert.deepEqual(alertText('needs', 'claude-code', 'fix the login', 'Claude needs your permission to use Bash'), {
    title: 'Claude Code needs you',
    body: 'fix the login\nClaude needs your permission to use Bash',
  });
  assert.deepEqual(alertText('done', 'codex', 'add tests'), { title: 'Codex finished', body: 'add tests' });
  assert.deepEqual(alertText('done', undefined, 'Codex', 'Codex'), { title: 'An agent finished', body: 'Codex' }, 'no repeats');
});

test('the window title counts the tabs that need the user', () => {
  assert.equal(countedTitle('Local — Agent Sessions', 0), 'Local — Agent Sessions');
  assert.equal(countedTitle('Local — Agent Sessions', 2), '(2) Local — Agent Sessions');
});

test('one alert of each kind per prompt', () => {
  const log = new AlertLog();
  assert.ok(log.take('a', 'needs', 1));
  assert.ok(!log.take('a', 'needs', 1), 'needs again before the user typed');
  assert.ok(log.take('a', 'done', 1));
  assert.ok(log.take('b', 'needs', 1), 'another tab');
  assert.ok(log.take('a', 'needs', 2), 'after the user answered');
  log.forget('a');
  assert.ok(log.take('a', 'needs', 2));
});

const ALERTS_URL = new URL('../src/lib/stores/alerts.svelte.ts', import.meta.url);

/** A terminals store whose agent tab states the test sets. */
function fakeTerminals() {
  const listeners = { attention: new Set(), viewing: new Set() };
  return {
    tabs: [],
    attention: {},
    focused: true,
    inputCounts: {},
    signals: {},
    shown: [],
    onAttention(fn) {
      listeners.attention.add(fn);
      return () => listeners.attention.delete(fn);
    },
    onViewing(fn) {
      listeners.viewing.add(fn);
      return () => listeners.viewing.delete(fn);
    },
    inputs(id) {
      return this.inputCounts[id] ?? 0;
    },
    signalText(id) {
      return this.signals[id] ?? '';
    },
    show(id) {
      this.shown.push(id);
    },
    set(id, state) {
      if (state === 'exited') delete this.attention[id];
      else this.attention[id] = state;
      for (const fn of listeners.attention) fn(id, state);
    },
    view(id) {
      for (const fn of listeners.viewing) fn(id);
    },
  };
}

async function withAlerts(fn) {
  const lib = (rel) => JSON.stringify(new URL(rel, ALERTS_URL).href);
  const { AlertsStore } = await loadServerModule(ALERTS_URL, [
    [
      "import { api, subscribeNotificationOpen } from '../api';",
      fixtureStore('api', '__storeFixture') +
        "const subscribeNotificationOpen = (fn) => api.onEvent('notify:open', (e) => fn(e.id));",
    ],
    ["import { setTitleCount } from '../hosts';", 'const setTitleCount = (n) => globalThis.__storeFixture.titles.push(n);'],
    ["'../alerts'", lib('../alerts.ts')],
    ["import { terminals } from './terminals.svelte';", fixtureStore('terminals', '__storeFixture')],
  ]);
  const mock = new MockBackendAPI();
  const prepared = [];
  const prepare = mock.prepareNotifications.bind(mock);
  mock.prepareNotifications = () => (prepared.push(true), prepare());
  const terminals = fakeTerminals();
  terminals.tabs = [
    { id: 't1', kind: 'agent', agent: 'claude-code', label: 'fix the login' },
    { id: 't2', kind: 'agent', agent: 'codex', label: 'add tests' },
  ];
  const bound = (target) => new Proxy(target, { get: (t, key) => (typeof t[key] === 'function' ? t[key].bind(t) : t[key]) });
  const saved = {};
  const fixture = { api: bound(mock), terminals: bound(terminals), titles: [] };
  globalThis.__storeFixture = fixture;
  const previous = globalThis.localStorage;
  globalThis.localStorage = { getItem: (k) => saved[k] ?? null, setItem: (k, v) => (saved[k] = v) };
  const store = new AlertsStore();
  store.init();
  try {
    return await fn({ mock, terminals, store, fixture, prepared, saved });
  } finally {
    store.destroy();
    delete globalThis.__storeFixture;
    globalThis.localStorage = previous;
  }
}

const settle = () => new Promise((r) => setTimeout(r, 0));

test('alerts: the title counts tabs that need the user', async () => {
  await withAlerts(async ({ terminals, fixture }) => {
    terminals.set('t1', 'working');
    terminals.set('t1', 'needs');
    terminals.set('t2', 'needs');
    terminals.set('t2', 'done');
    terminals.set('t1', 'exited');
    assert.deepEqual(fixture.titles, [0, 1, 2, 1, 0]);
  });
});

test('alerts: notify only while the window is in the background, once per prompt', async () => {
  await withAlerts(async ({ mock, terminals, store, prepared }) => {
    terminals.set('t1', 'working');
    await settle();
    assert.equal(prepared.length, 1, 'readied as the first agent tab works');

    terminals.set('t1', 'done');
    await settle();
    assert.deepEqual(mock.notifications, [], 'the window has focus: the tab shows it');

    terminals.focused = false;
    terminals.signals.t2 = 'Approve the edit?';
    terminals.set('t2', 'needs');
    terminals.set('t2', 'working');
    terminals.set('t2', 'needs');
    await settle();
    assert.deepEqual(mock.notifications, [{ tab: 't2', title: 'Codex needs you', body: 'add tests\nApprove the edit?' }]);

    // The user answered; the next stop alerts again.
    terminals.inputCounts.t2 = 1;
    terminals.set('t2', 'working');
    terminals.set('t2', 'done');
    await settle();
    assert.deepEqual(mock.notifications.map((n) => n.title), ['Codex needs you', 'Codex finished']);

    // Seeing the tab takes its notification away.
    terminals.view('t2');
    await settle();
    assert.deepEqual(mock.notifications, []);

    store.setNotifyDone(false);
    terminals.inputCounts.t1 = 1;
    terminals.set('t1', 'working');
    terminals.set('t1', 'done');
    await settle();
    assert.deepEqual(mock.notifications, [], 'turned off');
    assert.equal(prepared.length, 1, 'readied once');
  });
});

test('alerts: no notifications where they are unavailable; a click shows the tab', async () => {
  await withAlerts(async ({ mock, terminals, store, saved }) => {
    mock.notificationsOn = false;
    terminals.focused = false;
    terminals.set('t1', 'needs');
    await settle();
    assert.deepEqual(mock.notifications, []);
    assert.equal(store.status?.available, false);
    assert.match(store.status.reason, /off/);

    mock.emit('notify:open', { id: 't2' });
    assert.deepEqual(terminals.shown, ['t2']);

    store.setNotifyNeeds(false);
    assert.deepEqual(JSON.parse(saved['agent-sessions:agent-alerts']), { needs: false, done: true });
  });
});
