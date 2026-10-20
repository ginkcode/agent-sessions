import test from 'node:test';
import assert from 'node:assert/strict';
import { installTerminalColorScheme } from '../src/lib/terminalColorScheme.ts';

function setup(initial = 'dark') {
  const handlers = new Map();
  const replies = [];
  const key = (id) => `${id.prefix ?? ''}${id.intermediates ?? ''}${id.final}`;
  const register = (id, handler) => {
    const k = key(id);
    handlers.set(k, handler);
    return { dispose: () => handlers.delete(k) };
  };
  const terminal = {
    parser: { registerCsiHandler: register, registerEscHandler: register },
    input: (data, userInput) => {
      assert.equal(userInput, false);
      replies.push(data);
    },
  };
  const scheme = installTerminalColorScheme(terminal, initial);
  return { scheme, handlers, replies, send: (key, ...params) => handlers.get(key)(params) };
}

test('explicit queries report the current scheme even without a subscription', () => {
  const { scheme, send, replies } = setup();
  assert.equal(send('?n', 996), true);
  scheme.update('light');
  assert.equal(send('?n', 996), true);
  assert.deepEqual(replies, ['\x1b[?997;1n', '\x1b[?997;2n']);
  assert.equal(send('?n', 6), false);
  assert.equal(send('?n', 996, 6), false);
});

test('notifications require opt-in, fire only on changes, and stop on opt-out', () => {
  const { scheme, send, replies } = setup('light');
  scheme.update('dark');
  assert.deepEqual(replies, []);
  assert.equal(send('?h', 2031), true);
  assert.deepEqual(replies, []);
  scheme.update('light');
  scheme.update('light');
  scheme.update('dark');
  assert.equal(send('?l', 2031), true);
  scheme.update('light');
  assert.deepEqual(replies, ['\x1b[?997;2n', '\x1b[?997;1n']);
});

test('DECRQM advertises support and subscription state', () => {
  const { send, replies } = setup();
  assert.equal(send('?$p', 2031), true);
  send('?h', 2031);
  send('?$p', 2031);
  send('?l', 2031);
  send('?$p', 2031);
  assert.deepEqual(replies, ['\x1b[?2031;2$y', '\x1b[?2031;1$y', '\x1b[?2031;2$y']);
  assert.equal(send('?$p', 2004), false);
});

test('combined mode changes preserve xterm handling of other modes', () => {
  const { scheme, send, replies } = setup();
  assert.equal(send('?h', 2004), false);
  assert.equal(send('?h', 2004, 2031), false);
  scheme.update('light');
  assert.equal(send('?l', 2031, 1004), false);
  scheme.update('dark');
  assert.deepEqual(replies, ['\x1b[?997;2n']);
});

test('terminal resets clear the subscription without swallowing the reset', () => {
  for (const reset of ['c', '!p']) {
    const { scheme, send, replies } = setup();
    send('?h', 2031);
    assert.equal(send(reset), false);
    scheme.update('light');
    assert.deepEqual(replies, []);
    send('?$p', 2031);
    assert.deepEqual(replies, ['\x1b[?2031;2$y']);
  }
});

test('dispose removes handlers and suppresses further notifications', () => {
  const { scheme, send, replies, handlers } = setup();
  send('?h', 2031);
  scheme.dispose();
  scheme.update('light');
  assert.equal(handlers.size, 0);
  assert.deepEqual(replies, []);
});
