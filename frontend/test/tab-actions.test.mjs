import test from 'node:test';
import assert from 'node:assert/strict';
import { tabsToClose } from '../src/lib/tabActions.ts';

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
