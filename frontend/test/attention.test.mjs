import test from 'node:test';
import assert from 'node:assert/strict';
import {
  AttentionTracker,
  PROVOKED_MS,
  QUIET_MS,
  TerminalSignalScanner,
  agentAttention,
  isUserInput,
  notificationText,
} from '../src/lib/attention.ts';

const facts = (f = {}) => ({ exited: false, workedAt: 0, inputAt: 0, signaled: false, turnAt: 0, ...f });

test('agentAttention applies its rules in order', () => {
  const now = 100_000;
  // Input at 10s, the agent worked until 20s, the session reported at 30s.
  const after = { inputAt: 10_000, workedAt: 20_000, turnAt: 30_000 };
  assert.equal(agentAttention(facts({ exited: true, workedAt: now }), now), 'exited', 'exit wins');
  assert.equal(agentAttention(facts({ ...after, workedAt: now - QUIET_MS + 1, signaled: true, turn: 'done' }), now), 'working');
  assert.equal(agentAttention(facts({ ...after, signaled: true, turn: 'done' }), now), 'needs', 'a bell beats the turn');
  assert.equal(agentAttention(facts({ ...after, turn: 'tool' }), now), 'needs');
  assert.equal(agentAttention(facts({ ...after, turn: 'done' }), now), 'done');
  assert.equal(agentAttention(facts({ ...after, turn: 'working' }), now), 'working', 'quiet but in its turn');
  assert.equal(agentAttention(facts({ ...after }), now), 'idle', 'no turn known');
  assert.equal(agentAttention(facts(), now), 'idle');
});

test('the turn counts only when reported after the input and the work that followed it', () => {
  const now = 100_000;
  const base = { inputAt: 10_000, workedAt: 20_000, turnAt: 30_000, turn: 'done' };
  assert.equal(agentAttention(facts(base), now), 'done');
  assert.equal(agentAttention(facts({ ...base, inputAt: 0 }), now), 'idle', 'never prompted: a resumed session');
  assert.equal(agentAttention(facts({ ...base, inputAt: 35_000 }), now), 'idle', 'input clears done');
  assert.equal(agentAttention(facts({ ...base, workedAt: 5000 }), now), 'idle', 'no work since the input');
  assert.equal(agentAttention(facts({ ...base, turnAt: 0 }), now), 'idle', 'only known from linking');
  assert.equal(agentAttention(facts({ ...base, turn: 'tool', inputAt: 35_000 }), now), 'idle', 'input clears needs');
});

const bytes = (s) => new TextEncoder().encode(s);

test('the scanner tells bells from BEL ending an OSC sequence', () => {
  const s = new TerminalSignalScanner();
  assert.deepEqual(s.scan(bytes('\x07')), { signals: [{ kind: 'bell', text: '' }], drew: false });
  assert.deepEqual(s.scan(bytes('\x1b]0;title\x07')), { signals: [], drew: true }, 'a title is drawing');
  assert.deepEqual(s.scan(bytes('\x1b]2;title\x1b\\')).signals, []);
  assert.deepEqual(s.scan(bytes('\x1bPq#0\x07data\x1b\\')).signals, [], 'BEL inside DCS is data');
  assert.deepEqual(s.scan(bytes('\x1b_\x07\x1b\\')).signals, [], 'and inside APC');
  assert.deepEqual(s.scan(bytes('\x1b[31mred\x1b[0m\x07')), { signals: [{ kind: 'bell', text: '' }], drew: true });
  // CAN cancels a sequence: the BEL after it rings.
  assert.deepEqual(s.scan(bytes('\x1b]0;x\x18\x07')).signals, [{ kind: 'bell', text: '' }]);
});

test('the scanner keeps a sequence split across chunks', () => {
  const s = new TerminalSignalScanner();
  assert.deepEqual(s.scan(bytes('\x1b')).signals, []);
  assert.deepEqual(s.scan(bytes(']9;Claude needs')).signals, []);
  assert.deepEqual(s.scan(bytes(' your permission\x07')), {
    signals: [{ kind: 'notify', text: 'Claude needs your permission' }],
    drew: false,
  });
  // A multi-byte character split between chunks.
  const text = bytes('\x1b]777;notify;Codex;Done — 2 files\x1b\\');
  const cut = text.indexOf(0x94); // inside the dash
  assert.deepEqual(s.scan(text.slice(0, cut)).signals, []);
  assert.deepEqual(s.scan(text.slice(cut)).signals, [{ kind: 'notify', text: 'Codex: Done — 2 files' }]);
});

test('notifications: OSC 9 text and OSC 777 notify, not ConEmu commands', () => {
  assert.equal(notificationText('9;Task finished'), 'Task finished');
  assert.equal(notificationText('9;3 tests failed'), '3 tests failed');
  assert.equal(notificationText('9;4;1;50'), null, 'progress');
  assert.equal(notificationText('9;4'), null);
  assert.equal(notificationText('777;notify;Title;Body; more'), 'Title: Body; more');
  assert.equal(notificationText('777;notify;Title'), 'Title');
  assert.equal(notificationText('777;preexec'), null);
  assert.equal(notificationText('0;title'), null);
});

test('the terminal replying to queries is not the user typing', () => {
  assert.ok(!isUserInput('\x1b[I'), 'focus in');
  assert.ok(!isUserInput('\x1b[O\x1b[12;40R'), 'focus out and cursor position');
  assert.ok(!isUserInput('\x1b[?1;2c\x1b[>0;276;0c'), 'device attributes');
  assert.ok(!isUserInput('\x1b[?2004;1$y\x1b[0n\x1b[?997;1n'), 'modes and status');
  assert.ok(!isUserInput('\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\\x1b]10;rgb:ffff/ffff/ffff\x07'), 'colours');
  assert.ok(isUserInput('a'));
  assert.ok(isUserInput('\r'));
  assert.ok(isUserInput('\x1b[A'), 'an arrow key');
  assert.ok(isUserInput('\x1b[Ix'), 'a reply and a key');
});

function fakeClock() {
  let now = 1_000_000;
  let timers = [];
  return {
    now: () => now,
    setTimeout: (fn, ms) => {
      const timer = { at: now + ms, fn };
      timers.push(timer);
      return timer;
    },
    clearTimeout: (timer) => {
      timers = timers.filter((t) => t !== timer);
    },
    advance(ms) {
      const end = now + ms;
      for (;;) {
        const next = timers.filter((t) => t.at <= end).sort((a, b) => a.at - b.at)[0];
        if (!next) break;
        timers = timers.filter((t) => t !== next);
        now = next.at;
        next.fn();
      }
      now = end;
    },
  };
}

function tracker() {
  const clock = fakeClock();
  const changes = [];
  const t = new AttentionTracker((id, state) => changes.push(`${id}:${state}`), clock);
  return { t, clock, changes };
}

test('the tracker follows a prompt through work to a finished turn', () => {
  const { t, clock, changes } = tracker();
  t.add('a', false);
  clock.advance(1000);
  t.turn('a', 'done', false); // the last turn, known from linking
  assert.equal(t.state('a'), 'idle');

  t.input('a', 'fix it');
  t.output('a', bytes('fix it'), false); // the echo
  assert.equal(t.state('a'), 'idle', 'echo is not work');
  t.input('a', '\r');
  clock.advance(PROVOKED_MS);
  t.output('a', bytes('⠋ Working'), false);
  assert.equal(t.state('a'), 'working');
  clock.advance(QUIET_MS - 1);
  t.output('a', bytes('⠙ Working'), false);
  clock.advance(QUIET_MS - 1);
  assert.equal(t.state('a'), 'working', 'still drawing');
  clock.advance(1);
  assert.equal(t.state('a'), 'idle', 'quiet, nothing reported yet');
  t.turn('a', 'done', true);
  assert.equal(t.state('a'), 'done');
  t.input('a', '\x1b[O'); // focus out: not input
  assert.equal(t.state('a'), 'done');
  t.input('a', 'n');
  assert.equal(t.state('a'), 'idle', 'input clears done');
  assert.deepEqual(changes, ['a:working', 'a:idle', 'a:done', 'a:idle']);
});

test('the tracker: approvals, bells while looked at, handoffs and exits', () => {
  const { t, clock } = tracker();
  t.add('a', false);
  t.input('a', 'go\r');
  clock.advance(PROVOKED_MS);
  t.output('a', bytes('running'), false);
  clock.advance(QUIET_MS);
  t.turn('a', 'tool', true);
  assert.equal(t.state('a'), 'needs', 'waiting on an approval');

  t.add('b', false);
  t.output('b', bytes('\x07'), true);
  assert.equal(t.state('b'), 'idle', 'the user saw it ring');
  t.output('b', bytes('\x1b]9;Approve?\x07'), false);
  assert.equal(t.state('b'), 'needs');
  assert.equal(t.signalText('b'), 'Approve?');
  clock.advance(10);
  t.seen('b');
  assert.equal(t.state('b'), 'idle', 'looked at');
  t.output('b', bytes('\x07'), false);
  assert.equal(t.state('b'), 'needs');
  t.input('b', 'y');
  assert.equal(t.state('b'), 'idle', 'typed in');

  // A handoff starts with its prompt: its first turn finishes on its own.
  t.add('h', true);
  clock.advance(PROVOKED_MS);
  t.output('h', bytes('reading the handoff'), false);
  clock.advance(QUIET_MS);
  t.turn('h', 'done', true);
  assert.equal(t.state('h'), 'done');
  t.exited('h');
  assert.equal(t.state('h'), 'exited');
  t.remove('h');
  assert.equal(t.has('h'), false);
});

test('a resize redraw is not work', () => {
  const { t, clock } = tracker();
  t.add('a', false);
  clock.advance(PROVOKED_MS);
  t.resized('a');
  t.output('a', bytes('redraw'), false);
  assert.equal(t.state('a'), 'idle');
  clock.advance(PROVOKED_MS);
  t.output('a', bytes('more'), false);
  assert.equal(t.state('a'), 'working');
});
