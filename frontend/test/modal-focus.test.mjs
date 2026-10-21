import test from 'node:test';
import assert from 'node:assert/strict';
import { modalFocus } from '../src/lib/modalFocus.ts';

const nextTask = () => new Promise((resolve) => setTimeout(resolve, 0));

function fixture(tabindex = null) {
  const body = {};
  const doc = { body, activeElement: body };
  const listeners = new Map();
  let focused = 0;
  const child = {};
  const node = {
    ownerDocument: doc, isConnected: true,
    getAttribute: () => tabindex,
    setAttribute: (_, value) => { tabindex = value; },
    removeAttribute: () => { tabindex = null; },
    contains: (target) => target === node || target === child,
    focus: () => { focused++; doc.activeElement = node; },
    addEventListener: (name, fn) => listeners.set(name, fn),
    removeEventListener: (name) => listeners.delete(name),
  };
  return { node, doc, child, listeners, focused: () => focused, tabindex: () => tabindex };
}

test('modal focuses its container initially, but leaves input autofocus alone', async () => {
  const f = fixture();
  const action = modalFocus(f.node);
  await Promise.resolve();
  assert.equal(f.doc.activeElement, f.node);
  assert.equal(f.tabindex(), '-1');
  action.destroy();
  assert.equal(f.tabindex(), null);

  const input = fixture();
  modalFocus(input.node);
  input.doc.activeElement = input.child;
  await Promise.resolve();
  assert.equal(input.focused(), 0);
});

test('clicking the editor lets the browser finish focusing the textarea', async () => {
  const f = fixture();
  const action = modalFocus(f.node);
  await Promise.resolve();
  // During a pointer/focus transition WebKit can temporarily report body.
  for (const event of ['focusout', 'pointerup']) {
    f.doc.activeElement = f.doc.body;
    f.listeners.get(event)();
    await Promise.resolve();
    assert.equal(f.doc.activeElement, f.doc.body, 'do not interrupt the default focus action');
    f.doc.activeElement = f.child;
    await nextTask();
    assert.equal(f.doc.activeElement, f.child, 'the textarea keeps focus for editing');
  }
  action.destroy();
});

test('clicking modal chrome restores body focus without blocking selection or other inputs', async () => {
  const f = fixture('-1');
  const action = modalFocus(f.node);
  await Promise.resolve();
  for (const event of ['focusout', 'pointerup']) {
    f.doc.activeElement = f.doc.body;
    f.listeners.get(event)();
    await nextTask();
    assert.equal(f.doc.activeElement, f.node);
  }
  const otherDialogInput = {};
  f.doc.activeElement = otherDialogInput;
  f.listeners.get('focusout')();
  await Promise.resolve();
  assert.equal(f.doc.activeElement, otherDialogInput, 'do not steal focus from a stacked dialog');
  f.doc.activeElement = f.doc.body;
  f.listeners.get('focusout')();
  action.destroy();
  await Promise.resolve();
  assert.equal(f.doc.activeElement, f.doc.body, 'no queued refocus after close');
  assert.equal(f.tabindex(), '-1', 'preserve an existing tabindex');
  assert.equal(f.listeners.size, 0);
});
