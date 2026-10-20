/**
 * Full syntax highlighting for the file viewer, by highlight.js. It is
 * loaded on first use so the transcript, which has its own light
 * highlighter, does not pay for it.
 */

import type { HLJSApi } from 'highlight.js';

let loading: Promise<HLJSApi> | null = null;

export function loadFileHighlighter(): Promise<HLJSApi> {
  loading ??= import('highlight.js/lib/common').then((m) => m.default as HLJSApi);
  return loading;
}

/**
 * Returns code as escaped HTML with highlight.js token classes. language is
 * a highlight.js name; '' or an unknown one gives escaped plain text.
 */
export async function highlightFile(code: string, language: string): Promise<string> {
  const hljs = await loadFileHighlighter();
  const lang = language && hljs.getLanguage(language) ? language : 'plaintext';
  return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value;
}

const TAG = /<span class="([^"]*)">|<\/span>/g;

/**
 * Splits highlighted HTML into one string per line. A span that runs over a
 * line break (a block comment, a multi-line string) is closed at the end of
 * each line and opened again on the next, so every line is complete HTML on
 * its own. Keep the empty line after a final newline, just as the editor does.
 */
export function splitHighlightedLines(html: string): string[] {
  const lines: string[] = [];
  const open: string[] = [];
  let line = '';
  let last = 0;
  const text = (chunk: string) => {
    const parts = chunk.split('\n');
    line += parts[0];
    for (let i = 1; i < parts.length; i++) {
      lines.push(line + '</span>'.repeat(open.length));
      line = open.map((c) => `<span class="${c}">`).join('') + parts[i];
    }
  };
  for (const m of html.matchAll(TAG)) {
    text(html.slice(last, m.index));
    if (m[1] !== undefined) open.push(m[1]);
    else open.pop();
    line += m[0];
    last = m.index + m[0].length;
  }
  text(html.slice(last));
  lines.push(line);
  return lines;
}

/** Logical document lines, including the empty line after a final newline. */
export function splitLines(text: string): string[] {
  return text.split('\n');
}
