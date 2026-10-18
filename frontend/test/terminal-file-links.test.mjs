import test from 'node:test';
import assert from 'node:assert/strict';
import { extractTerminalFileCandidates, installTerminalFileLinks, parseOsc7Cwd } from '../src/lib/terminalFileLinks.ts';

const paths = (text) => extractTerminalFileCandidates(text).map((c) => c.path);

test('file candidates include paths, dotfiles and conventional extensionless filenames, not prose', () => {
  assert.deepEqual(paths('Edit Makefile README LICENSE .gitignore .husky/pre-commit docs/formats.md'),
    ['Makefile', 'README', 'LICENSE', '.gitignore', '.husky/pre-commit', 'docs/formats.md']);
  assert.deepEqual(paths('Please read this ordinary sentence with --verbose --file=foo.ts https://example.com/a.ts mailto:hi@example.com'), []);
  assert.deepEqual(paths('1.2.3 ../ ./ / -foo.ts'), []);
  assert.deepEqual(paths('   ordinary indented prose'), []);
});

test('candidate offsets exclude quotes, punctuation and line/column suffixes', () => {
  const text = 'See (docs/formats.md:12:8), "some folder/a.ts":22 and C:\\work\\main.ts:9';
  const candidates = extractTerminalFileCandidates(text);
  assert.deepEqual(candidates.map((c) => c.path), ['docs/formats.md', 'some folder/a.ts', 'C:\\work\\main.ts']);
  for (const c of candidates) assert.equal(text.slice(c.start, c.end), c.path);
  assert.deepEqual(paths("'a folder/config.json' foo.ts:12:3: README.md."), ['a folder/config.json', 'foo.ts', 'README.md']);
});

test('git status recognizes extensionless paths and spaces, and only rename destinations', () => {
  for (const [text, expected] of [
    ['\tmodified:   scripts/check', 'scripts/check'],
    ['  new file:   plainname', 'plainname'],
    [' M plainname', 'plainname'],
    ['?? a folder/plainname', 'a folder/plainname'],
    ['R  old name -> new name', 'new name'],
    ['  renamed:    "old name" -> "new name"', 'new name'],
    [' M "has -> arrow"', 'has -> arrow'],
    ['UU conflict', 'conflict'],
  ]) {
    const candidates = extractTerminalFileCandidates(text);
    assert.deepEqual(candidates.map((c) => c.path), [expected], text);
    assert.equal(text.slice(candidates[0].start, candidates[0].end), expected);
  }
  assert.deepEqual(paths(' M https://example.com/file'), []);
});

test('candidate work and result counts are bounded', () => {
  assert.deepEqual(paths('a'.repeat(8193)), []);
  assert.equal(paths('a.ts '.repeat(500)).length, 128);
});

test('OSC 7 validates file URIs and decodes POSIX and Windows cwd', () => {
  for (const [uri, expected] of [
    ['file:///home/me/my%20project', '/home/me/my project'],
    ['file://remote-host/home/me', '/home/me'],
    ['file://localhost/', '/'],
    ['file:///C:/Users/me/project', 'C:/Users/me/project'],
    ['file://windows-host/D:/my%20project', 'D:/my project'],
    ['file:///home/%E6%96%87', '/home/文'],
  ]) assert.equal(parseOsc7Cwd(uri), expected, uri);
  for (const uri of [
    '/home/me', 'https://host/home', 'file:relative', 'file:///a?query', 'file:///a#hash',
    'file://user@host/home', 'file://host:44/home', 'file:///a%00b', 'file:///a%0Ab',
    'file:///a b', 'file:///bad%ZZ', 'file:///a\nb', 'file:///' + 'a'.repeat(8192),
  ]) assert.equal(parseOsc7Cwd(uri), undefined, uri);
});

// Only public xterm interfaces are needed. Cells model full-width characters and
// their trailing zero-width cells, including surrogate pairs/combining marks.
function mockTerminal(rows, cols = 80) {
  let osc;
  let provider;
  const disposed = [];
  const lines = rows.map(({ text, wrapped = false, cells }) => {
    const data = cells ?? Array.from(text, (chars) => ({ chars, width: 1 }));
    while (data.length < cols) data.push({ chars: '', width: 1 });
    return {
      isWrapped: wrapped,
      length: data.length,
      getCell(x) {
        const cell = data[x];
        return cell && { getChars: () => cell.chars, getWidth: () => cell.width };
      },
    };
  });
  const terminal = {
    element: { title: 'original title' },
    buffer: { active: { getLine: (y) => lines[y] } },
    parser: { registerOscHandler(id, handler) {
      assert.equal(id, 7);
      osc = handler;
      return { dispose: () => disposed.push('osc') };
    } },
    registerLinkProvider(value) {
      assert.ok(osc, 'OSC tracking must be registered first');
      provider = value;
      return { dispose: () => disposed.push('provider') };
    },
  };
  return {
    terminal, disposed,
    osc: (value) => osc(value),
    links(row = 1) { let result; provider.provideLinks(row, (links) => { result = links; }); return result; },
  };
}
const click = (keys = {}) => ({ metaKey: false, ctrlKey: false, preventDefault() {}, ...keys });

test('provider gates activation, uses live cwd with fallback, and does no work on hover', () => {
  for (const isMac of [true, false]) {
    const mock = mockTerminal([{ text: 'docs/file.md' }]);
    const opened = [];
    const installed = installTerminalFileLinks(mock.terminal, { isMac, cwd: '/fallback', onOpen: (...args) => opened.push(args) });
    const [link] = mock.links();
    assert.equal(link.decorations, undefined, 'retain xterm default underline');
    link.hover();
    assert.equal(mock.terminal.element.title, `${isMac ? '⌘' : 'Ctrl'}+click to open file`);
    assert.deepEqual(opened, []);
    link.activate(click());
    link.activate(click(isMac ? { ctrlKey: true } : { metaKey: true }));
    assert.deepEqual(opened, []);
    const event = click(isMac ? { metaKey: true } : { ctrlKey: true });
    link.activate(event);
    mock.osc('file:///live%20cwd');
    link.activate(event);
    mock.osc('https://bad-cwd');
    link.activate(event);
    assert.deepEqual(opened, [['docs/file.md', '/fallback'], ['docs/file.md', '/live cwd'], ['docs/file.md', '/live cwd']]);
    link.leave();
    assert.equal(mock.terminal.element.title, 'original title');
    link.hover();
    installed.dispose();
    installed.dispose();
    assert.equal(mock.terminal.element.title, 'original title');
    assert.deepEqual(mock.disposed, ['provider', 'osc']);
    link.activate(event);
    assert.equal(opened.length, 3);
    assert.equal(mock.links(), undefined);
  }
});

test('hover disposal restores only its own title and supports terminals opened later', () => {
  const mock = mockTerminal([{ text: 'Makefile README' }]);
  mock.terminal.element = undefined;
  const installed = installTerminalFileLinks(mock.terminal, { isMac: false, onOpen() {} });
  const [one, two] = mock.links();
  mock.terminal.element = { title: 'late title' };
  one.hover();
  two.hover();
  one.leave();
  assert.equal(mock.terminal.element.title, 'Ctrl+click to open file');
  two.dispose();
  assert.equal(mock.terminal.element.title, 'late title');
  one.hover();
  mock.terminal.element.title = 'changed elsewhere';
  installed.dispose();
  assert.equal(mock.terminal.element.title, 'changed elsewhere');
});

test('wrapped lines return the complete path with physical buffer coordinates', () => {
  const mock = mockTerminal([{ text: 'src/long' }, { text: 'name.ts ', wrapped: true }], 8);
  installTerminalFileLinks(mock.terminal, { isMac: false, onOpen() {} });
  for (const row of [1, 2]) {
    const [link] = mock.links(row);
    assert.equal(link.text, 'src/longname.ts');
    assert.deepEqual(link.range, { start: { x: 1, y: 1 }, end: { x: 7, y: 2 } });
  }
});

test('wide, combining and surrogate characters map UTF-16 offsets to cells', () => {
  const mock = mockTerminal([{ cells: [
    { chars: '界', width: 2 }, { chars: '', width: 0 },
    { chars: '😀', width: 2 }, { chars: '', width: 0 },
    { chars: 'é', width: 1 }, { chars: ' ', width: 1 },
    ...Array.from('文.ts', (chars) => ({ chars, width: chars === '文' ? 2 : 1 })).flatMap((c) => c.width === 2 ? [c, { chars: '', width: 0 }] : [c]),
  ] }]);
  installTerminalFileLinks(mock.terminal, { isMac: false, onOpen() {} });
  const [link] = mock.links();
  assert.equal(link.text, '文.ts');
  assert.deepEqual(link.range, { start: { x: 7, y: 1 }, end: { x: 11, y: 1 } });
});

test('wide glyph wrap padding is excluded, and pathological wrapped lines are capped', () => {
  const mock = mockTerminal([
    { text: 'src/' },
    { wrapped: true, cells: [{ chars: '文', width: 2 }, { chars: '', width: 0 }, ...Array.from('.ts', (chars) => ({ chars, width: 1 }))] },
  ], 5);
  installTerminalFileLinks(mock.terminal, { isMac: false, onOpen() {} });
  assert.equal(mock.links()[0].text, 'src/文.ts');
  const huge = mockTerminal(Array.from({ length: 40 }, (_, i) => ({ text: 'a'.repeat(80), wrapped: i > 0 })));
  installTerminalFileLinks(huge.terminal, { isMac: false, onOpen() {} });
  assert.equal(huge.links(20), undefined);
});
