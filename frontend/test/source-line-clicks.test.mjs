import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { compile } from 'svelte/compiler';

const url = new URL('../src/lib/components/common/go-to-file/SourceView.svelte', import.meta.url);
const source = readFileSync(url, 'utf8');
const action = source.slice(source.indexOf('  function lineClicks('), source.indexOf('  const MARK_LABELS'));
const js = ts.transpileModule(action, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;

test('source text clicks select lines without blocking native drag selection', () => {
  const selected = [];
  const create = new Function('onselectline', `${js}; return lineClicks;`)((...args) => selected.push(args));
  const listeners = new Map();
  const line = { dataset: { sourceLine: '7' } };
  const OriginalElement = globalThis.Element;
  globalThis.Element = class { closest() { return line; } };
  let collapsed = true;
  const node = {
    ownerDocument: { getSelection: () => ({ isCollapsed: collapsed }) },
    contains: () => true,
    addEventListener: (name, handler) => listeners.set(name, handler),
    removeEventListener: name => listeners.delete(name),
  };
  try {
    const mounted = create(node);
    const event = { button: 0, clientX: 10, clientY: 20, target: new Element(), shiftKey: false };
    listeners.get('pointerdown')(event);
    listeners.get('click')(event);
    listeners.get('click')({ ...event, shiftKey: true });
    listeners.get('click')({ ...event, clientX: 30 });
    collapsed = false;
    listeners.get('click')(event);
    assert.deepEqual(selected, [[7, false], [7, true]]);
    mounted.destroy();
    assert.equal(listeners.size, 0);
  } finally { globalThis.Element = OriginalElement; }
});

test('blank wrapped lines have height without generated escape text', () => {
  const css = compile(source, { generate: 'client', filename: url.pathname }).css.code;
  assert.doesNotMatch(css, /00a0|(?:^|[;{\s])content\s*:/);
  assert.match(css, /min-height:\s*1\.5em/);
});
