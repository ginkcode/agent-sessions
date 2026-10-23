import test from 'node:test';
import assert from 'node:assert/strict';
import { createAPI } from '../src/lib/api.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';

test('new session API passes the agent and directory to native bindings', async () => {
  const previous = globalThis.window;
  const calls = [];
  const tab = { id: 't1', label: 'Codex', host: '', kind: 'agent', dir: '/p', agent: 'codex', startedAt: 1 };
  const App = {
    PanelNewSession: async (...args) => { calls.push(['PanelNewSession', ...args]); return tab; },
    OpenNewSessionInTerminal: async (...args) => { calls.push(['OpenNewSessionInTerminal', ...args]); },
  };
  globalThis.window = { go: { app: { App } } };
  try {
    const backend = createAPI();
    assert.deepEqual(await backend.panelNewSession('codex', '/p', { cols: 80, rows: 24 }), tab);
    await backend.openNewSessionInTerminal('opencode', '/p');
    assert.deepEqual(calls, [
      ['PanelNewSession', 'codex', '/p', { cols: 80, rows: 24 }],
      ['OpenNewSessionInTerminal', 'opencode', '/p'],
    ]);
    App.PanelNewSession = async () => { throw 'unknown directory: /gone'; };
    await assert.rejects(backend.panelNewSession('codex', '/gone', { cols: 80, rows: 24 }), /unknown directory/);
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});

test('the mock starts new sessions only in known projects, and names the tab', async () => {
  const mock = new MockBackendAPI();
  const [meta] = await mock.listSessions('', {}, { field: 'updatedAt', dir: 'desc' });
  const before = Date.now();
  const tab = await mock.panelNewSession('codex', meta.cwd, { cols: 80, rows: 24 });
  assert.deepEqual([tab.label, tab.kind, tab.agent, tab.dir, tab.host], ['Codex', 'agent', 'codex', meta.cwd, '']);
  assert.ok(tab.startedAt >= before);
  await assert.rejects(mock.panelNewSession('codex', '/nowhere', { cols: 80, rows: 24 }), /unknown directory/);
  await assert.rejects(mock.panelNewSession('gemini', meta.cwd, { cols: 80, rows: 24 }), /unknown provider/);

  mock.launch.terminal = true;
  await mock.openNewSessionInTerminal('claude-code', meta.cwd);
  assert.equal(mock.openedTerminals.at(-1), `cd '${meta.cwd}' && claude`);

  await mock.connect('dev-box');
  assert.equal((await mock.panelNewSession('opencode', meta.cwd, { cols: 80, rows: 24 })).host, 'dev-box');
  await assert.rejects(mock.openNewSessionInTerminal('opencode', meta.cwd), /local and WSL sessions only/);
});
