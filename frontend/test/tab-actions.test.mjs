import test from 'node:test';
import assert from 'node:assert/strict';
import { tabShortcut, tabsToClose } from '../src/lib/tabActions.ts';

test('tab close actions use the clicked tab and preserve tab order', () => {
  const ids = ['a', 'b', 'c', 'd'];
  assert.deepEqual(tabsToClose(ids, 'b', 'close'), ['b']);
  assert.deepEqual(tabsToClose(ids, 'b', 'others'), ['a', 'c', 'd']);
  assert.deepEqual(tabsToClose(ids, 'b', 'left'), ['a']);
  assert.deepEqual(tabsToClose(ids, 'b', 'right'), ['c', 'd']);
  assert.deepEqual(tabsToClose(ids, 'a', 'left'), []);
  assert.deepEqual(tabsToClose(ids, 'd', 'right'), []);
  assert.deepEqual(tabsToClose(['a'], 'a', 'others'), []);
  assert.deepEqual(tabsToClose(ids, 'missing', 'others'), []);
  assert.deepEqual(ids, ['a', 'b', 'c', 'd']);
});

test('tab shortcut reads Cmd+1–9 on macOS and Ctrl+1–9 elsewhere', () => {
  const key = (code, mods) => ({ code, ...mods });
  for (const mods of [{ altKey: false, ctrlKey: false, metaKey: true, shiftKey: false }, { altKey: false, ctrlKey: true, metaKey: false, shiftKey: false }]) {
    const mac = mods.metaKey;
    assert.equal(tabShortcut(key('Digit3', mods), mac), 3);
    assert.equal(tabShortcut(key('Numpad7', mods), mac), 7);
  }
  // The wrong modifier chord for the platform never matches.
  assert.equal(tabShortcut(key('Digit1', { ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }), true), null);
  assert.equal(tabShortcut(key('Digit1', { ctrlKey: false, metaKey: true, altKey: false, shiftKey: false }), false), null);
  // Modifiers that must not ride along, digits out of range and other keys.
  assert.equal(tabShortcut(key('Digit1', { ctrlKey: true, metaKey: false, altKey: true, shiftKey: false }), false), null);
  assert.equal(tabShortcut(key('Digit1', { ctrlKey: true, metaKey: false, altKey: false, shiftKey: true }), false), null);
  assert.equal(tabShortcut(key('Digit0', { ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }), false), null);
  assert.equal(tabShortcut(key('KeyA', { ctrlKey: true, metaKey: false, altKey: false, shiftKey: false }), false), null);
});
