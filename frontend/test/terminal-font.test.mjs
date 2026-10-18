import test from 'node:test';
import assert from 'node:assert/strict';
import { isTerminalFont, terminalFontFamily, SYSTEM_MONO_FONTS } from '../src/lib/terminalFont.ts';
import { loadServerModule } from './helpers.mjs';

const storeURL = new URL('../src/lib/stores/preferences.svelte.ts', import.meta.url);

test('terminal font quotes installed family names and retains a monospace fallback', () => {
  assert.equal(terminalFontFamily('', 'app-default'), 'app-default');
  assert.equal(terminalFontFamily('FiraCode Nerd Font Mono', 'app-default'), `"FiraCode Nerd Font Mono", ${SYSTEM_MONO_FONTS}`);
  assert.equal(terminalFontFamily('Font "quoted"', 'app-default'), `"Font \\"quoted\\"", ${SYSTEM_MONO_FONTS}`);
  assert.equal(isTerminalFont('bad\nfont'), false);
  assert.equal(isTerminalFont('x'.repeat(201)), false);
});

test('terminal font preference persists locally and survives unavailable storage', async () => {
  const { PreferencesStore } = await loadServerModule(storeURL, [
    ["'../terminalFont'", JSON.stringify(new URL('../src/lib/terminalFont.ts', import.meta.url).href)],
    ["'../transcript'", JSON.stringify(new URL('../src/lib/transcript.ts', import.meta.url).href)],
    ["'../date'", JSON.stringify(new URL('../src/lib/date.ts', import.meta.url).href)],
  ]);
  const previous = globalThis.localStorage;
  const values = new Map();
  globalThis.localStorage = { getItem: k => values.get(k) ?? null, setItem: (k, v) => values.set(k, v) };
  try {
    const prefs = new PreferencesStore();
    prefs.setTerminalFont('FiraCode Nerd Font Mono');
    const restored = new PreferencesStore();
    restored.init();
    assert.equal(restored.terminalFont, 'FiraCode Nerd Font Mono');
    restored.setTerminalFont('bad\nfont');
    assert.equal(restored.terminalFont, 'FiraCode Nerd Font Mono');
    globalThis.localStorage.setItem = () => { throw new Error('disabled'); };
    restored.setTerminalFont('Menlo');
    assert.equal(restored.terminalFont, 'Menlo');
  } finally { globalThis.localStorage = previous; }
});
