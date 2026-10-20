import test from 'node:test';
import assert from 'node:assert/strict';
import { previewText } from '../src/lib/previewFind.ts';
import { findInLines, textNodesRange } from '../src/lib/fileFind.ts';

test('preview find groups inline text and skips controls and hidden content', (t) => {
  const paragraph = {};
  const heading = {};
  function node(text, element, { excluded = false, visible = true } = {}) {
    return { nodeValue: text, parentElement: {
      closest: (selector) => selector.startsWith('button') ? (excluded ? {} : null) : element,
      getClientRects: () => visible ? [{}] : [],
    } };
  }
  const nodes = [
    node('Hello ', paragraph), node('world', paragraph),
    node('Copy', null, { excluded: true }),
    node('hidden source', null, { visible: false }),
    node('Heading', heading),
  ];
  let i = 0;
  const originals = { document: globalThis.document, NodeFilter: globalThis.NodeFilter, getComputedStyle: globalThis.getComputedStyle };
  t.after(() => Object.assign(globalThis, originals));
  globalThis.NodeFilter = { SHOW_TEXT: 4 };
  globalThis.getComputedStyle = () => ({ visibility: 'visible' });
  globalThis.document = { createTreeWalker: () => ({ nextNode: () => nodes[i++] ?? null }) };
  const groups = previewText({});
  assert.deepEqual(groups.map((group) => group.text), ['Hello world', 'Heading']);
  assert.deepEqual(groups[0].nodes, nodes.slice(0, 2));
  assert.deepEqual(findInLines(groups.map((group) => group.text), 'lo wor', false).matches,
    [{ row: 0, start: 3, end: 9 }]);
});

test('preview highlight ranges cross inline formatting with exact node offsets', (t) => {
  const nodes = [{ nodeValue: 'Hello ' }, { nodeValue: 'world' }];
  const range = { setStart(node, offset) { this.start = [node, offset]; }, setEnd(node, offset) { this.end = [node, offset]; } };
  const original = globalThis.document;
  t.after(() => { globalThis.document = original; });
  globalThis.document = { createRange: () => range };
  assert.equal(textNodesRange(nodes, 3, 9), range);
  assert.deepEqual(range.start, [nodes[0], 3]);
  assert.deepEqual(range.end, [nodes[1], 3]);
  assert.equal(textNodesRange(nodes, 20, 25), null);
});
