import test from 'node:test';
import assert from 'node:assert/strict';
import { searchRegExp } from '../src/lib/contentSearch.ts';
import {
  expandReplacement,
  previewReplacement,
  replaceInText,
  replaceSummary,
  replacementTemplate,
  stickyRegExp,
  textChanges,
} from '../src/lib/searchReplace.ts';

const match = (re, text) => text.match(re);

test('expansion follows Go Regexp.Expand: numbers, names, braces, $$ and stray dollars', () => {
  const m = match(/(?<word>f)(o+)/u, 'foo');
  assert.equal(expandReplacement('<$1|$2|${2}x|$word|${word}>', m), '<f|oo|oox|f|f>');
  assert.equal(expandReplacement('$1x', m), '', 'a name is the longest run: $1x is the group named 1x');
  assert.equal(expandReplacement('$$1 costs $', m), '$1 costs $');
  assert.equal(expandReplacement('${2', m), '${2', 'an unclosed brace stays as typed');
  assert.equal(expandReplacement('$9|$nope|$01', m), '||', 'missing groups are empty; 01 is a name');
  assert.equal(expandReplacement('$-', m), '$-');
});

test('the typed replacement: escapes apply only to regular expressions', () => {
  assert.equal(replacementTemplate('a\\nb\\t\\\\c\\d', true), 'a\nb\t\\c\\d');
  assert.equal(replacementTemplate('a\\nb', false), 'a\\nb');
});

test('replacing in text keeps CRLF, skips empty matches and replaces only the chosen matches', () => {
  const re = searchRegExp({ pattern: 'foo' });
  assert.deepEqual(replaceInText('foo foo\r\nfoo', re, 'x', false), { text: 'x x\r\nx', replaced: 3, ok: true });
  assert.deepEqual(replaceInText('foo foo\r\nfoo', re, 'x', false, [{ line: 1, start: 4 }, { line: 2, start: 0 }]), { text: 'foo x\r\nx', replaced: 2, ok: true });
  assert.deepEqual(replaceInText('foo', re, 'x', false, [{ line: 1, start: 1 }]), { text: 'foo', replaced: 0, ok: false }, 'a match not there leaves the text alone');
  assert.equal(replaceInText('abc', searchRegExp({ pattern: 'x*', regex: true }), '-', true).text, 'abc');
  assert.equal(replaceInText('key=1', searchRegExp({ pattern: '(\\w+)=(\\d)', regex: true }), '$2=$1', true).text, '1=key');
  assert.equal(replaceInText('a$b', searchRegExp({ pattern: 'b' }), '$1', false).text, 'a$$1', 'plain text replacements take $ as it is');
});

test('the preview finds its match again, or shows the template as it is', () => {
  const query = { pattern: '(o+)', regex: true };
  const sticky = stickyRegExp(searchRegExp(query));
  const m = { line: 1, start: 1, end: 3, preview: 'foo', previewStart: 1, previewEnd: 3 };
  assert.equal(previewReplacement(m, sticky, '<$1>', true), '<oo>');
  assert.equal(previewReplacement({ ...m, previewEnd: 2 }, sticky, '<$1>', true), '<$1>');
  assert.equal(previewReplacement(m, null, '<$1>', true), '<$1>');
  assert.equal(previewReplacement(m, sticky, '<$1>', false), '<$1>');
});

test('text changes are per line when the line count holds, and never split a surrogate pair', () => {
  assert.deepEqual(textChanges('a foo\nb\nc foo', 'a x\nb\nc x'), [{ from: 2, to: 5, insert: 'x' }, { from: 10, to: 13, insert: 'x' }]);
  assert.deepEqual(textChanges('ab', 'a\nb'), [{ from: 1, to: 1, insert: '\n' }]);
  assert.deepEqual(textChanges('same', 'same'), []);
  // 😀 and 😁 share their high surrogate; the change must take both halves.
  assert.deepEqual(textChanges('x😀', 'x😁'), [{ from: 1, to: 3, insert: '😁' }]);
  assert.deepEqual(textChanges('😀x', '😁x'), [{ from: 0, to: 2, insert: '😁' }]);
  for (const [a, b] of [['x😀y', 'x😁y'], ['a\n😀', 'ab\n😁'], ['', '😀']]) {
    let out = a;
    for (const c of [...textChanges(a, b)].reverse()) out = out.slice(0, c.from) + c.insert + out.slice(c.to);
    assert.equal(out, b);
  }
});

test('the summary counts what changed and names every file left as it was', () => {
  const ok = replaceSummary([{ path: 'a', status: 'replaced', replaced: 2 }, { path: 'b', status: 'draft', replaced: 1 }]);
  assert.equal(ok.title, 'Replaced 3 matches in 2 files');
  assert.match(ok.body, /Changed in unsaved draft, not yet saved: b\./);
  assert.equal(ok.tone, 'info');
  const mixed = replaceSummary([
    { path: 'a', status: 'replaced', replaced: 1 },
    { path: 'p1', status: 'skipped', reason: 'protected' },
    { path: 'p2', status: 'skipped', reason: 'protected' },
    { path: 'd', status: 'skipped', reason: 'draft_unsearched' },
    { path: 'c', status: 'conflict' },
    { path: 'f', status: 'failed', message: 'permission denied' },
  ]);
  assert.equal(mixed.title, 'Replaced 1 match in 1 file');
  assert.match(mixed.body, /Skipped \(protected\): p1, p2\./);
  assert.match(mixed.body, /Skipped \(unsaved draft the browser cannot search with this pattern\): d\./);
  assert.match(mixed.body, /left as they are: c\./);
  assert.match(mixed.body, /Failed: f \(permission denied\)\./);
  assert.equal(mixed.tone, 'error');
  const many = replaceSummary(Array.from({ length: 7 }, (_, i) => ({ path: `f${i}`, status: 'conflict' })));
  assert.equal(many.title, 'Nothing replaced');
  assert.match(many.body, /f0, f1, f2, f3, f4 and 2 more/);
});
