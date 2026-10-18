import test from 'node:test';
import assert from 'node:assert/strict';
import { isFullscreenShortcut, toggleFullscreen, preventDesktopEscapeFullscreen } from '../src/lib/fullscreen.ts';
import { terminalKeyAction } from '../src/lib/terminal.ts';

const key = { type: 'keydown', key: 'F', code: 'KeyF', metaKey: true, shiftKey: true, ctrlKey: false, altKey: false };

test('fullscreen shortcuts use Cmd+Shift+F on macOS and F11 elsewhere, including terminals', () => {
  assert.equal(isFullscreenShortcut(key, true), true);
  assert.equal(isFullscreenShortcut({ ...key, shiftKey: false }, true), false);
  assert.equal(isFullscreenShortcut({ ...key, ctrlKey: true }, true), false);
  assert.equal(isFullscreenShortcut(key, false), false);
  const f11 = { ...key, key: 'F11', code: 'F11', metaKey: false, shiftKey: false };
  assert.equal(isFullscreenShortcut(f11, false), true);
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
