import test from 'node:test';
import assert from 'node:assert/strict';
import { formatTokens, formatCost, formatBytes } from '../src/lib/format.ts';
import { formatRelativeTime, formatAbsoluteTime } from '../src/lib/date.ts';
import { highlightCode, detectLanguage } from '../src/lib/highlight.ts';
import { renderMarkdown } from '../src/lib/markdown.ts';

test('formatTokens formats numbers into compact string representations', () => {
  assert.equal(formatTokens(0), '0');
  assert.equal(formatTokens(950), '950');
  assert.equal(formatTokens(1000), '1k');
  assert.equal(formatTokens(1500), '1.5k');
  assert.equal(formatTokens(128400), '128.4k');
  assert.equal(formatTokens(1000000), '1M');
  assert.equal(formatTokens(2500000), '2.5M');
});

test('formatCost formats USD cost accurately', () => {
  assert.equal(formatCost(0), '$0.00');
  assert.equal(formatCost(0.423), '$0.42');
  assert.equal(formatCost(0.004), '$0.004');
  assert.equal(formatCost(12.5), '$12.50');
});

test('formatBytes formats byte sizes into readable units', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(1024), '1.0 KB');
  assert.equal(formatBytes(1048576), '1.0 MB');
});

test('date formatting provides accurate relative and absolute strings', () => {
  const now = new Date();
  const fiveMinAgo = new Date(now.getTime() - 5 * 60 * 1000).toISOString();
  assert.equal(formatRelativeTime(fiveMinAgo), '5m');

  const twoHoursAgo = new Date(now.getTime() - 2 * 3600 * 1000).toISOString();
  assert.equal(formatRelativeTime(twoHoursAgo), '2h');

  const abs = formatAbsoluteTime('2026-09-28T14:30:00Z');
  assert.ok(abs.includes('2026'));
  assert.ok(abs.includes('UTC'));
});

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
