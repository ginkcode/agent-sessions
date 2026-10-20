import test from 'node:test';
import assert from 'node:assert/strict';
import { selectFileLine, includesFileLine } from '../src/lib/fileSelection.ts';

test('line selection extends from the anchor in either direction and resets on a plain click', () => {
  let selection = selectFileLine(undefined, 5, false);
  assert.deepEqual(selection, { anchor: 5, head: 5 });
  selection = selectFileLine(selection, 2, true);
  assert.deepEqual(selection, { anchor: 5, head: 2 });
  assert.equal(includesFileLine(selection, 2), true);
  assert.equal(includesFileLine(selection, 4), true);
  assert.equal(includesFileLine(selection, 5), true);
  assert.equal(includesFileLine(selection, 1), false);
  assert.equal(includesFileLine(selection, 6), false);
  assert.equal(includesFileLine(undefined, 2), false);
  assert.deepEqual(selectFileLine(selection, 9, false), { anchor: 9, head: 9 });
});
