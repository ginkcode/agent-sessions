import test from 'node:test';
import assert from 'node:assert/strict';
import { highlightCode, detectLanguage } from '../src/lib/highlight.ts';
import { renderMarkdown } from '../src/lib/markdown.ts';
import { reachesLineCount } from '../src/lib/layout.ts';
import { renameSvgId } from '../src/lib/mermaid.ts';

test('detectLanguage detects formats from content', () => {
  assert.equal(detectLanguage('{"key": "value"}'), 'json');
  assert.equal(detectLanguage('diff --git a/foo b/foo'), 'diff');
  assert.equal(detectLanguage('package main\nfunc main() {}'), 'go');
  assert.equal(detectLanguage('import { foo } from \'bar\''), 'typescript');
  assert.equal(detectLanguage('def hello():\n  pass'), 'python');
});

test('highlightCode highlights JSON, diff, and Go safely', () => {
  const json = highlightCode('{"hello": 123}', 'json');
  assert.ok(json.includes('hljs-attr'));
  assert.ok(json.includes('hljs-number'));

  const diff = highlightCode('+ added\n- deleted\n@@ -1,3 +1,3 @@', 'diff');
  assert.ok(diff.includes('hljs-addition'));
  assert.ok(diff.includes('hljs-deletion'));
  assert.ok(diff.includes('hljs-meta'));

  const go = highlightCode('func Run() error { return nil }', 'go');
  assert.ok(go.includes('hljs-keyword'));
  assert.ok(go.includes('hljs-title'));
});

test('highlightCode generic scanner escapes markup and keeps token boundaries', () => {
  const ts = highlightCode('if (a <= 0x1F && s === "<b>") { x = 1.5e3; } // it\'s & done', 'typescript');
  assert.equal(
    ts,
    '<span class="hljs-keyword">if</span> (a &lt;= <span class="hljs-number">0x1F</span> &amp;&amp; s === ' +
      '<span class="hljs-string">&quot;&lt;b&gt;&quot;</span>) { x = <span class="hljs-number">1.5e3</span>; } ' +
      '<span class="hljs-comment">// it&#39;s &amp; done</span>'
  );
  assert.ok(!/<(?!\/?span)/.test(highlightCode('<script>alert(1)</script>', 'go')));
});

test('reachesLineCount counts wrapped visual lines without overcounting', () => {
  assert.equal(reachesLineCount('', 24, 160), false);
  assert.equal(reachesLineCount('x\n'.repeat(24), 24, 160), true);
  assert.equal(reachesLineCount('x\n'.repeat(23), 24, 160), false);
  // A trailing newline adds no visible line; an empty line in between does.
  assert.equal(reachesLineCount('x\n'.repeat(23) + '\n', 24, 160), true);
  assert.equal(reachesLineCount('\n'.repeat(24), 24, 160), true);
  assert.equal(reachesLineCount('\n'.repeat(23), 24, 160), false);
  // Long lines wrap at maxCols.
  assert.equal(reachesLineCount('y'.repeat(23 * 160 + 1), 24, 160), true);
  assert.equal(reachesLineCount('y'.repeat(23 * 160), 24, 160), false);
  assert.equal(reachesLineCount(('z'.repeat(161) + '\n').repeat(12), 24, 160), true);
  assert.equal(reachesLineCount('short', 24, 160), false);
});

test('renderMarkdown converts markdown and strictly neutralizes XSS vectors', () => {
  // Test GFM structures
  const md = renderMarkdown('# Heading 1\n\nThis is **bold** and *italic* and `code`.\n\n- item 1\n- item 2\n\n```go\nfunc Main() {}\n```');
  assert.ok(md.includes('<h1>Heading 1</h1>'));
  assert.ok(md.includes('<strong>bold</strong>'));
  assert.ok(md.includes('<em>italic</em>'));
  assert.ok(md.includes('<code class="inline-code">code</code>'));
  assert.ok(md.includes('<ul><li>item 1</li><li>item 2</li></ul>'));
  assert.ok(md.includes('class="code-block-container"'));

  // Test XSS neutralization: <script> must be escaped and cannot execute
  const xss1 = renderMarkdown('<script>alert("pwned")</script>');
  assert.ok(!xss1.includes('<script>'));
  assert.ok(xss1.includes('&lt;script&gt;'));

  // Test XSS neutralization: <img onerror=...> must be escaped
  const xss2 = renderMarkdown('<img src="x" onerror="alert(1)">');
  assert.ok(!xss2.includes('<img'));
  assert.ok(xss2.includes('&lt;img'));

  // Test XSS neutralization: javascript: href in links must be sanitized to #
  const xss3 = renderMarkdown('[click me](javascript:alert(1))');
  assert.ok(!xss3.includes('javascript:'));
  assert.ok(xss3.includes('href="#"'));

  // Test valid http links
  const validLink = renderMarkdown('[Claude](https://claude.ai)');
  assert.ok(validLink.includes('href="https://claude.ai"'));
  assert.ok(validLink.includes('target="_blank"'));
});

test('renderMarkdown leaves mermaid blocks for the diagram renderer', () => {
  const md = renderMarkdown('```Mermaid\ngraph TD\n  A["<img src=x onerror=alert(1)>"] --> B\n```');
  assert.ok(md.includes('class="code-block-container mermaid-block"'));
  assert.ok(md.includes('<div class="mermaid-diagram" hidden></div>'));
  assert.ok(md.includes('<pre class="mermaid-source">'));
  assert.ok(md.includes('class="mermaid-toggle-btn" hidden'));
  assert.ok(md.includes('class="mermaid-expand-btn"'));
  // The source is shown and copied as text; nothing in it becomes markup.
  assert.ok(!md.includes('<img'));
  assert.ok(md.includes('data-code="graph TD\n  A[&quot;&lt;img src=x onerror=alert(1)&gt;&quot;] --&gt; B"'));

  // An empty block has nothing to draw and stays a plain code block.
  const empty = renderMarkdown('```mermaid\n\n```');
  assert.ok(!empty.includes('mermaid-block'));
  assert.ok(empty.includes('data-lang="mermaid"'));
});

test('renderMarkdown keeps lines that only look like headings as text', () => {
  // These once matched no block and were never read, which hung the page.
  assert.equal(renderMarkdown('###'), '<p>###</p>');
  assert.equal(renderMarkdown('#tag'), '<p>#tag</p>');
  assert.equal(renderMarkdown('####### seven'), '<p>####### seven</p>');
  assert.equal(renderMarkdown('Intro\n###\n## Next'), '<p>Intro</p>\n<p>###</p>\n<h2>Next</h2>');
});

test('renderMarkdown leaves intraword underscores, code and URLs literal', () => {
  const cases = [
    ['Set API_KEY_NAME first', '<p>Set API_KEY_NAME first</p>'],
    ['call snake_case_fn()', '<p>call snake_case_fn()</p>'],
    ['Grüße_und_Tschüss', '<p>Grüße_und_Tschüss</p>'],
    ['Use `API_KEY_NAME` and `a*b*c`', '<p>Use <code class="inline-code">API_KEY_NAME</code> and <code class="inline-code">a*b*c</code></p>'],
    ['2 * 3 * 4', '<p>2 * 3 * 4</p>'],
    ['_it_ (_it_) _foo_bar_', '<p><em>it</em> (<em>it</em>) <em>foo_bar</em></p>'],
    ['__b__ ___bi___ un*frigging*believable', '<p><strong>b</strong> <strong><em>bi</em></strong> un<em>frigging</em>believable</p>'],
  ];
  for (const [input, want] of cases) {
    assert.equal(renderMarkdown(input), want, input);
  }

  const link = renderMarkdown('[**docs**](https://x.dev/a_b_c)');
  assert.ok(link.includes('href="https://x.dev/a_b_c"'), link);
  assert.ok(link.includes('<strong>docs</strong></a>'), link);
});

test('renderMarkdown renders tables, including one right after a line of text', () => {
  const md = renderMarkdown('**Verification**\n| Check | Result |\n|---|---|\n| tests | `ok` |\n| build | pass |');
  assert.equal(
    md,
    '<p><strong>Verification</strong></p>\n' +
      '<div class="table-container"><table><thead><tr><th>Check</th><th>Result</th></tr></thead><tbody>' +
      '<tr><td>tests</td><td><code class="inline-code">ok</code></td></tr>' +
      '<tr><td>build</td><td>pass</td></tr></tbody></table></div>'
  );

  const first = renderMarkdown('| A | B |\n| :-- | --: |\n| 1 | 2 |');
  assert.ok(first.startsWith('<div class="table-container"><table>'), first);
  assert.ok(first.includes('<th>A</th><th>B</th>'), first);
  assert.ok(first.includes('<td>1</td><td>2</td>'), first);

  const code = renderMarkdown('| a | b |\n|---|---|\n| a | `x|y` |');
  assert.ok(code.includes('<tr><td>a</td><td><code class="inline-code">x|y</code></td></tr>'), code);

  const escaped = renderMarkdown('| a \\| b | c |\n|---|---|\n| 1 | 2 |');
  assert.ok(escaped.includes('<th>a | b</th><th>c</th></tr>'), escaped);
  assert.ok(escaped.includes('<tr><td>1</td><td>2</td></tr>'), escaped);
});

test('renderMarkdown keeps table cells safe and leaves non-tables alone', () => {
  const xss = renderMarkdown('| a | b |\n|---|---|\n| <script>alert(1)</script> | [x](javascript:alert(1)) |');
  assert.ok(!xss.includes('<script>'), xss);
  assert.ok(xss.includes('&lt;script&gt;'), xss);
  assert.ok(!xss.includes('javascript:'), xss);
  assert.ok(xss.includes('href="#"'), xss);

  assert.equal(renderMarkdown('a | b'), '<p>a | b</p>');
  assert.equal(renderMarkdown('text\na | b\nmore'), '<p>text<br />a | b<br />more</p>');

  const fence = renderMarkdown('```\n| a | b |\n|---|---|\n```');
  assert.ok(fence.includes('class="code-block-container"'), fence);
  assert.ok(!fence.includes('<table>'), fence);
});

test('renameSvgId renames a diagram id and its references only', () => {
  const svg =
    '<svg id="mermaid-diagram-3" width="100%"><style>#mermaid-diagram-3 .node{fill:red}</style>' +
    '<path marker-end="url(#mermaid-diagram-3_flowchart-pointEnd)"/><g id="mermaid-diagram-30"/></svg>';
  assert.equal(
    renameSvgId(svg, 'viewer'),
    '<svg id="viewer" width="100%"><style>#viewer .node{fill:red}</style>' +
      '<path marker-end="url(#viewer_flowchart-pointEnd)"/><g id="mermaid-diagram-30"/></svg>'
  );
  assert.equal(renameSvgId('<svg width="1"></svg>', 'viewer'), '<svg width="1"></svg>');
});

test('handleCopyCodeClick copies code from delegated click and ignores other targets', async () => {
  const { handleCopyCodeClick } = await import('../src/lib/copycode.ts');

  const written = [];
  const origClipboard = globalThis.navigator?.clipboard;
  Object.defineProperty(globalThis, 'navigator', {
    value: { clipboard: { writeText: (t) => { written.push(t); return Promise.resolve(); } } },
    configurable: true,
  });

  try {
    function makeBtn(code) {
      const classes = new Set(['copy-code-btn']);
      const btn = {
        textContent: 'Copy',
        getAttribute: (k) => (k === 'data-code' ? code : null),
        closest: (selector) => (selector === '.copy-code-btn' ? btn : null),
        classList: {
          contains: (c) => classes.has(c),
          add: (c) => classes.add(c),
          remove: (c) => classes.delete(c),
        },
      };
      return btn;
    }

    const btn = makeBtn('const x = 1;');

    // Click on the button itself
    handleCopyCodeClick({ target: btn, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, ['const x = 1;']);

    // Click on a child inside the button (delegation via closest)
    written.length = 0;
    const child = {
      closest: (sel) => (sel === '.copy-code-btn' ? btn : null),
    };
    handleCopyCodeClick({ target: child, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, ['const x = 1;']);

    // Click elsewhere (closest returns null) must not copy
    written.length = 0;
    const outside = { closest: () => null };
    handleCopyCodeClick({ target: outside, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, []);

    // Button without data-code must not copy
    written.length = 0;
    const noData = makeBtn(null);
    handleCopyCodeClick({ target: noData, stopPropagation: () => {} });
    await new Promise((r) => setTimeout(r, 0));
    assert.deepEqual(written, []);
  } finally {
    if (origClipboard !== undefined) {
      Object.defineProperty(globalThis, 'navigator', { value: origClipboard, configurable: true });
    }
  }
});
