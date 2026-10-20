import test from 'node:test';
import assert from 'node:assert/strict';
import { connectionBanner, connectionErrorDetail, CONNECTION_LOST } from '../src/lib/guidance.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
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
} from '../src/lib/link.ts';
import { subscribeConnectionState, subscribeAskpassPrompt } from '../src/lib/api.ts';
import {
  fixtureStore,
  loadServerComponent,
  loadServerModule,
  renderReconnectBanner,
  renderWithFixture,
  renderedButtons,
  unescapeHTML,
} from './helpers.mjs';

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

function bannerDetail(html) {
  return html.match(/<pre class="detail-text[^"]*">([\s\S]*?)<\/pre>/)?.[1];
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
    ["'../guidance'", lib('../guidance.ts')],
    store('toast', 'toast'),
    store('appState', 'appState'),
    store('manage', 'manage'),
    store('search', 'search'),
    store('handoff', 'handoff'),
    store('exporter', 'export'),
    store('importer', 'importer'),
    store('files', 'files'),
    ["import { fileActions } from './fileActions.svelte';", 'const fileActions = { changeHost: async (action) => { await action(); return true; } };'],
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

test('ConnectionStore names the selected connection in the window title', async () => {
  const { ConnectionStore } = await loadConnectionStore();
  const titles = [];
  const hadWindow = 'window' in globalThis;
  const hadDocument = 'document' in globalThis;
  globalThis.window = { runtime: { WindowSetTitle: (t) => titles.push(t) } };
  globalThis.document = { title: '' };
  globalThis.__storeFixture = { api: {}, link: { seed() {}, update: () => false } };
  try {
    const store = new ConnectionStore();
    store.applyState({ phase: 'local', generation: 0 }, false);
    assert.equal(document.title, 'Local — Agent Sessions');
    store.applyState({ phase: 'connecting', host: 'plgl', generation: 1 });
    store.applyState({ phase: 'connected', host: 'plgl', generation: 1 });
    store.applyState({ phase: 'reconnecting', host: 'plgl', generation: 1 });
    store.applyState({ phase: 'connecting', host: 'wsl:Ubuntu', generation: 2 });
    store.applyState({ phase: 'local', generation: 3 });
    assert.deepEqual(titles, [
      'Local — Agent Sessions',
      'plgl — Agent Sessions',
      'Ubuntu (WSL) — Agent Sessions',
      'Local — Agent Sessions',
    ], 'the title changes only when the selected connection does');
    assert.equal(document.title, 'Local — Agent Sessions');
  } finally {
    delete globalThis.__storeFixture;
    if (!hadWindow) delete globalThis.window;
    if (!hadDocument) delete globalThis.document;
  }
});

test('ConnectionStore points to the window that has the host open', async () => {
  const { ConnectionStore } = await loadConnectionStore();
  const shown = [];
  const fixture = (err) => ({
    api: { connect: async () => { throw err; } },
    link: {},
    toast: { show: (message) => shown.push(message) },
  });

  globalThis.__storeFixture = fixture('box is open in another window');
  try {
    const store = new ConnectionStore();
    await store.connect('box', {});
    assert.equal(store.error, undefined, 'this window stays as it was');
    assert.equal(store.phase, 'local');
    assert.equal(shown.length, 1);
    assert.equal(shown[0].title, 'box is open in another window');
  } finally {
    delete globalThis.__storeFixture;
  }

  // Any other failure is the connection's error, not a toast.
  shown.length = 0;
  globalThis.__storeFixture = fixture(new Error('invalid host alias'));
  try {
    const store = new ConnectionStore();
    await store.connect('box', {});
    assert.equal(store.error, 'invalid host alias');
    assert.equal(shown.length, 0);
  } finally {
    delete globalThis.__storeFixture;
  }
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

test('hostInOtherWindow explains a host open in another window', async () => {
  const { hostInOtherWindow } = await import('../src/lib/guidance.ts');
  assert.equal(hostInOtherWindow('box', new Error('probe handshake: exit status 255')), null);

  const raised = hostInOtherWindow('box', new Error('box is open in another window'));
  assert.equal(raised.title, 'box is open in another window');
  assert.match(raised.body, /^That window was brought to the front\./);
  assert.equal(raised.tone, undefined);

  // Wails rejects with the error's text as a string.
  const failed = hostInOtherWindow(
    'box',
    'box is open in another window: could not bring it to the front: connection refused',
  );
  assert.equal(failed.title, 'box is open in another window');
  assert.equal(
    failed.body,
    'Switch to that window to use box. Could not bring it to the front: connection refused.',
  );

  const wsl = hostInOtherWindow('wsl:Ubuntu', new Error('wsl:Ubuntu is open in another window'));
  assert.doesNotMatch(wsl.title, /^wsl:/);
});
