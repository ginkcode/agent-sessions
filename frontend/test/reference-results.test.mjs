import test from 'node:test';
import assert from 'node:assert/strict';
import { referenceResults, isReferenceMatch } from '../src/lib/referenceResults.ts';

const location = (path, start, end = start) => ({ path, range: { start, end } });

test('reference groups preserve multiline ranges and UTF-16 columns with bounded previews', () => {
  const target = location('b.ts', { line: 0, character: 3 }, { line: 1, character: 2 });
  const result = referenceResults([target, location('a.ts', { line: 0, character: 500 })], new Map([
    ['b.ts', { text: '😀 symbol\nend', draft: true }],
    ['a.ts', { text: 'x'.repeat(2000), revision: 'r' }],
  ]));
  assert.deepEqual(result.files.map(f => f.path), ['a.ts', 'b.ts']);
  assert.equal(result.files[0].matches[0].preview.length, 300);
  const match = result.files[1].matches[0];
  assert.equal(isReferenceMatch(match), true);
  assert.equal(match.location, target);
  assert.equal(match.preview.slice(match.previewStart, match.previewEnd), 'symbol');
  assert.equal(result.files[1].draft, true);
  assert.equal(result.matches, 2);
});

test('missing or changed lines are explicitly unavailable rather than invented source', () => {
  const result = referenceResults([location('gone.ts', { line: 8, character: 3 })], new Map([['gone.ts', { text: 'one line' }]]));
  const match = result.files[0].matches[0];
  assert.equal(match.preview, '');
  assert.match(match.unavailable, /Preview unavailable/);
  assert.equal(match.line, 9);
});
