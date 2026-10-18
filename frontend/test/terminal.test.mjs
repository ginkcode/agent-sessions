import test from 'node:test';
import assert from 'node:assert/strict';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
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
import { StaleReplyError } from '../src/lib/link.ts';
import { ALL_AGENTS } from '../src/lib/portable.ts';
import { fixtureStore, loadServerModule } from './helpers.mjs';

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

test('TerminalsStore bulk closing pauses at each running agent and Cancel stops the queue', async () => {
  await withTerminals(async ({ store, mock }) => {
    const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
    const first = await store.runResume(meta.ref, meta.cwd);
    const second = await store.runResume(meta.ref, meta.cwd);
    const shell = await store.newShell('/work');
    store.closeMany([first.id, second.id, shell.id]);
    assert.equal(store.confirming, first.id);
    assert.equal(store.tabs.length, 3);
    store.confirmClose();
    assert.equal(store.confirming, second.id);
    assert.deepEqual(store.tabs.map(t => t.id), [second.id, shell.id]);
    store.cancelClose();
    store.confirmClose();
    assert.equal(store.tabs.length, 2, 'Cancel also drops remaining bulk targets');
    store.closeMany([second.id, shell.id]);
    store.confirmClose();
    assert.equal(store.tabs.length, 0);
    assert.equal(store.confirming, null);
  });
});

test('TerminalsStore tracks cwd independently for active and inactive tabs', async () => {
  await withTerminals(async ({ store }) => {
    const first = await store.newShell('/a');
    const second = await store.newShell('/b');
    store.setCwd(first.id, '/a/nested');
    store.setCwd(second.id, '/other project');
    assert.equal(store.tabs.find(t => t.id === first.id).cwd, '/a/nested');
    assert.equal(store.tabs.find(t => t.id === second.id).cwd, '/other project');
    assert.equal(store.tabs.find(t => t.id === first.id).dir, '/a');
    assert.equal(store.active, second.id);
    store.setCwd('missing-tab', '/ignored');
    assert.equal(store.tabs.length, 2);
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
