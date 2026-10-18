import test from 'node:test';
import assert from 'node:assert/strict';
import { NavHistory, navigationButton, navigationShortcut } from '../src/lib/navHistory.ts';

const navAt = (id, groupKey = null) => ({ ref: id ? { agent: 'claude', id } : null, groupKey, groupMode: 'dir-agent' });

const navId = (entry) => entry?.ref?.id ?? null;

test('NavHistory goes back and forward and drops the places ahead on a new move', () => {
  const h = new NavHistory();
  assert.equal(h.canGoBack, false);
  h.replace(navAt('a')); // the session selected on load
  h.push(navAt('b'));
  h.push(navAt('b')); // the same place again is not a new entry
  h.push(navAt('c'));
  assert.equal(navId(h.back()), 'b');
  assert.equal(navId(h.back()), 'a');
  assert.equal(h.back(), null);
  assert.equal(navId(h.forward()), 'b');
  h.push(navAt('d'));
  assert.equal(h.canGoForward, false);
  assert.equal(navId(h.back()), 'b');
  assert.equal(navId(h.back()), 'a');

  const groups = new NavHistory();
  groups.push(navAt('a'));
  groups.push(navAt('a', 'g1')); // picking a group is a place of its own
  groups.replace(navAt('x', 'g1')); // ...and its first session updates it
  assert.deepEqual(groups.back(), navAt('a'));
  assert.deepEqual(groups.forward(), navAt('x', 'g1'));
});

test('NavHistory records a run of arrow-key steps as one place', () => {
  const h = new NavHistory();
  h.push(navAt('a'));
  h.step(navAt('b'));
  h.step(navAt('c'));
  h.step(navAt('d'));
  assert.equal(navId(h.back()), 'a');
  assert.equal(navId(h.forward()), 'd');
  h.push(navAt('e'));
  h.step(navAt('f'));
  assert.equal(navId(h.back()), 'e');
});

test('NavHistory caps its length', () => {
  const h = new NavHistory(3);
  for (const id of ['a', 'b', 'c', 'd']) h.push(navAt(id));
  assert.equal(navId(h.back()), 'c');
  assert.equal(navId(h.back()), 'b');
  assert.equal(h.back(), null);
});

test('NavHistory forgets deleted sessions and their subagents', () => {
  const h = new NavHistory();
  for (const id of ['a', 'b', 'b/agent-1', 'a', 'c']) h.push(navAt(id));
  h.back(); // at the second "a"
  h.remove([{ agent: 'claude', id: 'b' }]);
  // b and its subagent are gone and the two a's merge into one place.
  assert.equal(h.canGoBack, false);
  assert.equal(navId(h.forward()), 'c');
  h.remove([{ agent: 'codex', id: 'a' }]); // another agent's session
  assert.equal(navId(h.back()), 'a');

  // Deleting the session shown: the neighbour selected next takes its place
  // between the remaining ones.
  const shown = new NavHistory();
  for (const id of ['a', 'b', 'c']) shown.push(navAt(id));
  shown.back(); // at b
  shown.remove([{ agent: 'claude', id: 'b' }]);
  assert.equal(shown.canGoBack, true);
  shown.replace(navAt('n'));
  assert.equal(navId(shown.back()), 'a');
  assert.equal(navId(shown.forward()), 'n');
  assert.equal(navId(shown.forward()), 'c');

  // Deleting every session leaves nothing to go to.
  const all = new NavHistory();
  all.push(navAt('a'));
  all.push(navAt('b'));
  all.remove([{ agent: 'claude', id: 'a' }, { agent: 'claude', id: 'b' }]);
  assert.equal(all.canGoBack || all.canGoForward, false);
  assert.equal(all.back(), null);
  all.replace(navAt(null));
  assert.equal(all.canGoBack || all.canGoForward, false);

  // Deleting the shown first place, then going forward without a new one.
  const first = new NavHistory();
  first.push(navAt('a'));
  first.push(navAt('b'));
  first.back();
  first.remove([{ agent: 'claude', id: 'a' }]);
  assert.equal(first.canGoBack, false);
  assert.equal(navId(first.forward()), 'b');
});

test('the drop guard keeps drags from other apps from navigating the app', async () => {
  const { setupDropGuard } = await import('../src/lib/navigation.ts');
  const target = new EventTarget();
  setupDropGuard(target);
  const drag = (type) => {
    const e = new Event(type, { cancelable: true });
    e.dataTransfer = { dropEffect: 'none' };
    target.dispatchEvent(e);
    return e;
  };
  const over = drag('dragover');
  assert.deepEqual([over.defaultPrevented, over.dataTransfer.dropEffect], [true, 'copy']);
  assert.equal(drag('drop').defaultPrevented, true);

  // Dragging selected text inside the app keeps its default.
  target.dispatchEvent(new Event('dragstart'));
  assert.equal(drag('dragover').defaultPrevented, false);
  assert.equal(drag('drop').defaultPrevented, false);
  target.dispatchEvent(new Event('dragend'));
  assert.equal(drag('drop').defaultPrevented, true);
});

test('navigation shortcuts and mouse side buttons', () => {
  const key = (key, mods = {}) => ({ key, altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, ...mods });
  assert.equal(navigationShortcut(key('BrowserBack'), false), 'back');
  assert.equal(navigationShortcut(key('BrowserForward'), true), 'forward');
  assert.equal(navigationShortcut(key('ArrowLeft', { altKey: true }), false), 'back');
  assert.equal(navigationShortcut(key('ArrowRight', { altKey: true }), false), 'forward');
  assert.equal(navigationShortcut(key('ArrowLeft', { altKey: true, shiftKey: true }), false), null);
  assert.equal(navigationShortcut(key('ArrowLeft', { altKey: true }), true), null);
  assert.equal(navigationShortcut(key('ArrowLeft'), false), null);
  assert.equal(navigationShortcut(key('[', { metaKey: true }), true), 'back');
  assert.equal(navigationShortcut(key(']', { metaKey: true }), true), 'forward');
  assert.equal(navigationShortcut(key('[', { ctrlKey: true }), false), null);
  assert.equal(navigationButton(3), 'back');
  assert.equal(navigationButton(4), 'forward');
  assert.equal(navigationButton(0), null);
  assert.equal(navigationButton(2), null);
});
