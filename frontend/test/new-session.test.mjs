import test from 'node:test';
import assert from 'node:assert/strict';
import { defaultNewSessionAgent, newSessionChoices, newSessionMenuItems } from '../src/lib/newSession.ts';
import { isNewSessionShortcut, newSessionShortcutLabel, terminalKeyAction } from '../src/lib/terminal.ts';
import { openedGuidance } from '../src/lib/guidance.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { fixtureStore, loadServerModule, renderSessionHeader, unescapeHTML } from './helpers.mjs';

// The rendered buttons with their attributes.
const buttons = (html) =>
  [...html.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)].map(([, attrs, text]) => ({
    attrs: unescapeHTML(attrs),
    text: text.replace(/<!--[\s\S]*?-->/g, '').replace(/<[^>]+>/g, '').trim(),
  }));
const header = (newSession, props) =>
  renderSessionHeader({ link: { dataHost: undefined }, launcher: { canOpen: true }, newSession: { starting: false, refresh() {}, ...newSession } }, props);

const REPORT = [
  { agent: 'claude-code', name: 'Claude Code', installed: true },
  { agent: 'codex', name: 'Codex', installed: false, hint: 'codex was not found on PATH. Install it with: npm install -g @openai/codex' },
  { agent: 'opencode', name: 'OpenCode', installed: false },
];

test('choices offer every agent until the host reports, then mark the missing ones', () => {
  assert.deepEqual(newSessionChoices(null).map((c) => [c.agent, c.missing]), [
    ['claude-code', undefined],
    ['codex', undefined],
    ['opencode', undefined],
  ]);
  const choices = newSessionChoices(REPORT);
  assert.equal(choices[0].missing, undefined);
  assert.match(choices[1].missing, /npm install -g @openai\/codex/);
  assert.equal(choices[2].missing, 'OpenCode is not installed on this host', 'no hint still says what is wrong');
});

test('the default agent is the last one started when it can start, else the first that can', () => {
  const choices = newSessionChoices(REPORT);
  assert.equal(defaultNewSessionAgent(choices, 'claude-code'), 'claude-code');
  assert.equal(defaultNewSessionAgent(choices, 'codex'), 'claude-code');
  const none = newSessionChoices(REPORT.map((s) => ({ ...s, installed: false })));
  assert.equal(defaultNewSessionAgent(none, 'codex'), 'codex', 'its launch then says what to install');
});

test('menu items start the chosen agent, explain missing agents and directories', () => {
  const started = [];
  const items = newSessionMenuItems(newSessionChoices(REPORT), (agent) => started.push(agent), { separator: true });
  assert.deepEqual(items.map((i) => [i.label, i.disabled, i.separator]), [
    ['New Claude Code Session', false, true],
    ['New Codex Session', true, false],
    ['New OpenCode Session', true, false],
  ]);
  assert.equal(items[0].title, 'Start a new Claude Code session in this directory');
  assert.match(items[1].title, /not found on PATH/);
  items[0].onSelect();
  assert.deepEqual(started, ['claude-code']);

  const gone = newSessionMenuItems(newSessionChoices(null), () => {}, { disabled: 'This directory no longer exists on disk' });
  assert.ok(gone.every((i) => i.disabled && i.title === 'This directory no longer exists on disk' && !i.separator));
});

test('the shortcut is Cmd+Shift+A on macOS and Ctrl+Shift+A elsewhere, and the terminal leaves it alone', () => {
  const key = (over) => ({ type: 'keydown', key: 'A', code: 'KeyA', ctrlKey: false, metaKey: false, altKey: false, shiftKey: true, ...over });
  assert.ok(isNewSessionShortcut(key({ metaKey: true }), true));
  assert.ok(isNewSessionShortcut(key({ ctrlKey: true }), false));
  assert.ok(isNewSessionShortcut(key({ ctrlKey: true, key: 'Ф' }), false), 'layouts without Latin letters use the key position');
  assert.ok(!isNewSessionShortcut(key({ ctrlKey: true }), true));
  assert.ok(!isNewSessionShortcut(key({ metaKey: true, shiftKey: false, key: 'a' }), true), 'Select All stays');
  assert.ok(!isNewSessionShortcut(key({ ctrlKey: true, altKey: true }), false));
  assert.ok(!isNewSessionShortcut(key({ ctrlKey: true, key: 'N', code: 'KeyN' }), false), 'Ctrl+Shift+N is New window');
  assert.equal(terminalKeyAction(key({ ctrlKey: true }), false), 'app');
  assert.equal(terminalKeyAction(key({ metaKey: true }), true), 'app');
  assert.deepEqual([newSessionShortcutLabel(true), newSessionShortcutLabel(false)], ['⌘⇧A', 'Ctrl+Shift+A']);
});

test('the external-terminal toast names the agent', () => {
  assert.match(openedGuidance('new', 'codex').body, /A new Codex session starts in the project directory/);
});

test('the mock reports missing agents and refuses to start them', async () => {
  const mock = new MockBackendAPI();
  const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
  assert.ok((await mock.newSessionAgents()).every((a) => a.installed));
  mock.missingAgents.add('codex');
  const report = await mock.newSessionAgents();
  assert.deepEqual(report.map((a) => a.installed), [true, false, true]);
  assert.match(report[1].hint, /not found on PATH/);
  mock.launch.terminal = true;
  await assert.rejects(mock.openNewSessionInTerminal('codex', meta.cwd), /agent CLI not found/);
});

test('the header offers New for the session agent only where new sessions can start', async () => {
  assert.doesNotMatch(await header({ available: false, choices: [] }), /new-split/);

  const main = buttons(await header({ available: true, choices: newSessionChoices(null) })).find((b) => b.text === 'New');
  assert.ok(main, 'the New button is shown');
  assert.doesNotMatch(main.attrs, /disabled/);
  assert.match(main.attrs, /Start a new Codex session in C:\\work\\proj/);

  // The session's agent is missing: New explains, ▾ still offers the others.
  const missing = buttons(await header({ available: true, choices: newSessionChoices(REPORT) }));
  const blocked = missing.find((b) => b.text === 'New');
  assert.match(blocked.attrs, /disabled/);
  assert.match(blocked.attrs, /npm install -g @openai\/codex/);
  assert.doesNotMatch(missing.find((b) => /New session with another agent/.test(b.attrs)).attrs, /disabled/);

  const gone = buttons(await header(
    { available: true, choices: newSessionChoices(null) },
    { meta: { ref: { agent: 'codex', id: 's1' }, title: 't', cwd: '/gone', cwdMissing: true, counts: { user: 1, assistant: 1 }, tokens: { input: 0, output: 0 } } },
  ));
  const split = gone.filter((b) => b.text === 'New' || /New session with another agent/.test(b.attrs));
  assert.equal(split.length, 2);
  assert.ok(split.every((b) => /disabled/.test(b.attrs)));
});

const STORE_URL = new URL('../src/lib/stores/newSession.svelte.ts', import.meta.url);

async function newSessionStore(fixture) {
  const lib = (rel) => JSON.stringify(new URL(rel, STORE_URL).href);
  const store = (name, file) => [`import { ${name} } from './${file}.svelte';`, fixtureStore(name, '__storeFixture')];
  const module = await loadServerModule(STORE_URL, [
    ["import { api } from '../api';", fixtureStore('api', '__storeFixture')],
    ["'../link'", lib('../link.ts')],
    ["'../guidance'", lib('../guidance.ts')],
    ["'../newSession'", lib('../newSession.ts')],
    store('connectionStore', 'connection'),
    store('launcher', 'launcher'),
    store('link', 'link'),
    store('terminals', 'terminals'),
    store('toast', 'toast'),
  ]);
  globalThis.__storeFixture = fixture;
  return new module.NewSessionStore();
}

function storeFixture(over = {}) {
  const calls = [];
  const toasts = [];
  return {
    calls,
    toasts,
    api: {
      newSessionAgents: async () => { calls.push(['agents']); return REPORT; },
      openNewSessionInTerminal: async (agent, dir) => { calls.push(['external', agent, dir]); },
    },
    connectionStore: { capabilities: { newSession: true } },
    launcher: { canOpen: true, openMode: 'panel', canOpenPanel: true },
    link: { dataHost: undefined },
    terminals: { runNewSession: async (agent, dir) => { calls.push(['panel', agent, dir]); return true; } },
    toast: { show: (t) => toasts.push(t) },
    ...over,
  };
}

test('the store opens new sessions where agents open, and remembers the agent', async () => {
  const fx = storeFixture();
  const s = await newSessionStore(fx);
  try {
    assert.equal(s.where, 'panel');
    assert.ok(await s.start('codex', '/p'));
    assert.equal(s.lastAgent, 'codex');
    fx.launcher = { canOpen: true, openMode: 'external', canOpenPanel: true };
    assert.ok(await s.start('opencode', '/p'));
    assert.deepEqual(fx.calls, [['panel', 'codex', '/p'], ['external', 'opencode', '/p']]);
    assert.match(fx.toasts[0].body, /new OpenCode session/);

    // Only the panel works for this host: new sessions go there.
    fx.launcher = { canOpen: false, openMode: 'external', canOpenPanel: true };
    assert.equal(s.where, 'panel');
    fx.launcher = { canOpen: false, openMode: 'external', canOpenPanel: false };
    assert.equal(s.where, null);
    assert.equal(await s.start('codex', '/p'), false);

    // A server from before new sessions offers none.
    fx.launcher = { canOpen: true, openMode: 'panel', canOpenPanel: true };
    fx.connectionStore = { capabilities: {} };
    assert.equal(s.available, false);
  } finally {
    delete globalThis.__storeFixture;
  }
});

test('the store keeps each host report, defaults to an installed agent and toasts failures', async () => {
  const fx = storeFixture();
  const s = await newSessionStore(fx);
  try {
    s.lastAgent = 'codex';
    assert.equal(s.defaultAgent, 'codex', 'before a report every agent is offered');
    await s.refresh();
    assert.equal(s.defaultAgent, 'claude-code');
    fx.link = { dataHost: 'dev-box' };
    assert.equal(s.choices.every((c) => !c.missing), true, 'another host has its own report');
    fx.api.newSessionAgents = async () => { throw new Error('offline'); };
    await s.refresh();
    fx.link = { dataHost: undefined };
    assert.match(s.choices[1].missing, /not found on PATH/, 'a failed refresh keeps what was known');

    fx.terminals = { runNewSession: async () => { throw new Error('agent CLI not found: codex'); } };
    assert.equal(await s.start('codex', '/p'), false);
    assert.equal(fx.toasts.at(-1).tone, 'error');
    assert.equal(s.lastAgent, 'codex');
    assert.equal(s.starting, false);
  } finally {
    delete globalThis.__storeFixture;
  }
});
