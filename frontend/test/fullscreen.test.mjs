import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { isFullscreenShortcut, toggleFullscreen, preventDesktopEscapeFullscreen, keyHandled } from '../src/lib/fullscreen.ts';
import { terminalKeyAction } from '../src/lib/terminal.ts';

const key = { type: 'keydown', key: 'f', code: 'KeyF', metaKey: true, shiftKey: false, ctrlKey: true, altKey: false };

test('fullscreen shortcuts use Ctrl+Cmd+F on macOS and F11 elsewhere, including terminals', () => {
  assert.equal(isFullscreenShortcut(key, true), true);
  assert.equal(isFullscreenShortcut({ ...key, ctrlKey: false }, true), false);
  assert.equal(isFullscreenShortcut({ ...key, metaKey: false }, true), false);
  assert.equal(isFullscreenShortcut({ ...key, shiftKey: true }, true), false);
  assert.equal(isFullscreenShortcut({ ...key, altKey: true }, true), false);
  assert.equal(isFullscreenShortcut({ ...key, ctrlKey: false, shiftKey: true }, true), false);
  assert.equal(isFullscreenShortcut({ ...key, metaKey: false, shiftKey: true }, true), false);
  assert.equal(isFullscreenShortcut(key, false), false);
  const f11 = { ...key, key: 'F11', code: 'F11', metaKey: false, ctrlKey: false };
  assert.equal(isFullscreenShortcut(f11, false), true);
  for (const modifier of ['ctrlKey', 'metaKey', 'shiftKey', 'altKey']) {
    assert.equal(isFullscreenShortcut({ ...f11, [modifier]: true }, false), false);
  }
  assert.equal(terminalKeyAction(key, true), 'app');
  assert.equal(terminalKeyAction(f11, false), 'app');
});

test('fullscreen prefers native Wails window APIs and toggles both ways', async () => {
  const previous = globalThis.window;
  const calls = [];
  let full = false;
  globalThis.window = { runtime: {
    WindowIsFullscreen: async () => full,
    WindowFullscreen: () => { full = true; calls.push('enter'); },
    WindowUnfullscreen: () => { full = false; calls.push('exit'); },
  } };
  try {
    await toggleFullscreen();
    await toggleFullscreen();
    assert.deepEqual(calls, ['enter', 'exit']);
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});

test('fullscreen falls back to browser APIs and recovers from failures', async () => {
  const oldWindow = globalThis.window;
  const oldDocument = globalThis.document;
  let full = false;
  globalThis.window = {};
  globalThis.document = {
    get fullscreenElement() { return full ? {} : null; },
    documentElement: { requestFullscreen: async () => { full = true; } },
    exitFullscreen: async () => { full = false; },
  };
  try {
    await toggleFullscreen();
    assert.equal(full, true);
    await toggleFullscreen();
    assert.equal(full, false);
    globalThis.document.documentElement.requestFullscreen = async () => { throw new Error('denied'); };
    await assert.rejects(toggleFullscreen(), /denied/);
    await assert.rejects(toggleFullscreen(), /denied/);
  } finally {
    if (oldWindow === undefined) delete globalThis.window;
    else globalThis.window = oldWindow;
    if (oldDocument === undefined) delete globalThis.document;
    else globalThis.document = oldDocument;
  }
});

test('desktop Escape safeguard uses capture so dialogs cannot bypass it by stopping propagation', () => {
  const app = readFileSync(new URL('../src/App.svelte', import.meta.url), 'utf8');
  assert.match(app, /window\.addEventListener\('keydown', preventDesktopEscapeFullscreen, true\)/);
  assert.match(app, /window\.removeEventListener\('keydown', preventDesktopEscapeFullscreen, true\)/);
});

test('native macOS fullscreen does not exit on Escape', () => {
  const gui = readFileSync(new URL('../../cmd/agent-sessions/run_gui.go', import.meta.url), 'utf8');
  assert.match(gui, /DisableEscapeExitsFullscreen:\s*true/);
});

test('desktop Escape cancels WebKit fullscreen exit without swallowing the key', () => {
  const previous = globalThis.window;
  let cancelled = 0;
  const escape = { key: 'Escape', preventDefault: () => cancelled++ };
  try {
    globalThis.window = {};
    preventDesktopEscapeFullscreen(escape);
    assert.equal(cancelled, 0, 'browser keeps its native Escape behavior');
    globalThis.window = { go: { app: { App: {} } } };
    preventDesktopEscapeFullscreen(escape);
    assert.equal(cancelled, 1);
    preventDesktopEscapeFullscreen({ ...escape, key: 'Enter' });
    assert.equal(cancelled, 1);
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});

test('the desktop Escape safeguard does not make Escape look already handled', () => {
  const previous = globalThis.window;
  // A minimal event whose defaultPrevented reflects preventDefault calls.
  const event = (key = 'Escape') => {
    const e = { key, defaultPrevented: false };
    e.preventDefault = function () { this.defaultPrevented = true; };
    return e;
  };
  try {
    globalThis.window = { go: { app: { App: {} } } };
    const fresh = event();
    preventDesktopEscapeFullscreen(fresh);
    assert.equal(fresh.defaultPrevented, true, 'WebKit fullscreen exit is still cancelled');
    assert.equal(keyHandled(fresh), false, 'dialogs must still close on an unused Escape');

    const used = event();
    preventDesktopEscapeFullscreen(used);
    used.preventDefault(); // e.g. a find bar or menu consuming Escape
    assert.equal(keyHandled(used), true);

    globalThis.window = {};
    const browser = event();
    preventDesktopEscapeFullscreen(browser);
    assert.equal(keyHandled(browser), false);
    browser.preventDefault();
    assert.equal(keyHandled(browser), true);
  } finally {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  }
});
