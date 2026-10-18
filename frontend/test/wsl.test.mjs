import test from 'node:test';
import assert from 'node:assert/strict';
import { copiedGuidance, connectionBanner, CONNECTION_LOST } from '../src/lib/guidance.ts';
import { MockBackendAPI } from '../src/lib/mock/mockApi.ts';
import { canOpenTerminal } from '../src/lib/terminal.ts';
import { wslDistro } from '../src/lib/hosts.ts';
import { blockedReason, nextLink, resumeButtonTitle } from '../src/lib/link.ts';
import { createAPI } from '../src/lib/api.ts';
import {
  loadRemoteCommandNote,
  renderHandoffDialog,
  renderReconnectBanner,
  renderSessionHeader,
  renderWithFixture,
  unescapeHTML,
} from './helpers.mjs';

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
