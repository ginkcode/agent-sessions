import test from 'node:test';
import assert from 'node:assert/strict';
import { findCount, findInLines, firstMatchFrom } from '../src/lib/fileFind.ts';

test('findInLines finds every match in order, case-insensitive by default', () => {
  const { matches, capped } = findInLines(['Foo foo', 'bar', 'xFOOx'], 'foo', false);
  assert.deepEqual(matches, [
    { row: 0, start: 0, end: 3 },
    { row: 0, start: 4, end: 7 },
    { row: 2, start: 1, end: 4 },
  ]);
  assert.equal(capped, false);
});

test('findInLines matches case when asked', () => {
  assert.deepEqual(findInLines(['Foo foo'], 'foo', true).matches, [{ row: 0, start: 4, end: 7 }]);
});

test('findInLines takes the query as plain text', () => {
  assert.deepEqual(findInLines(['a.b axb (x)'], 'a.b', false).matches, [{ row: 0, start: 0, end: 3 }]);
  assert.deepEqual(findInLines(['a.b axb (x)'], '(x)', false).matches, [{ row: 0, start: 8, end: 11 }]);
  assert.deepEqual(findInLines(['$1 [a] \\d'], '\\d', false).matches, [{ row: 0, start: 7, end: 9 }]);
});

test('findInLines does not overlap matches, and finds nothing for an empty query', () => {
  assert.deepEqual(findInLines(['aaaa'], 'aa', false).matches.map((m) => m.start), [0, 2]);
  assert.deepEqual(findInLines(['abc'], '', false), { matches: [], capped: false });
});

test('findInLines stops at the limit and says so', () => {
  const result = findInLines(['a a a', 'a a'], 'a', false, 3);
  assert.equal(result.matches.length, 3);
  assert.equal(result.capped, true);
  assert.equal(findInLines(['a a a'], 'a', false, 3).capped, false);
});

test('firstMatchFrom finds the first match at or after a position', () => {
  const { matches } = findInLines(['x', 'ab ab', '', 'ab'], 'ab', false);
  assert.equal(firstMatchFrom(matches, 0), 0);
  assert.equal(firstMatchFrom(matches, 1), 0);
  assert.equal(firstMatchFrom(matches, 1, 1), 1);
  assert.equal(firstMatchFrom(matches, 2), 2);
  assert.equal(firstMatchFrom(matches, 4), 3);
});

test('findCount reads as editors show it', () => {
  assert.equal(findCount({ matches: [], capped: false }, -1), 'No results');
  const two = findInLines(['a a'], 'a', false);
  assert.equal(findCount(two, 1), '2 of 2');
  assert.equal(findCount(findInLines(['a'.repeat(20)], 'a', false, 12), 0), '1 of 12+');
});
