import test from 'node:test';
import assert from 'node:assert/strict';
import { openMode } from '../src/lib/terminal.ts';
import { fixtureStore, loadServerComponent, renderWithFixture } from './helpers.mjs';

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
