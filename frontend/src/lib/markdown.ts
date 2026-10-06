/**
 * Safe, zero-dependency Markdown parser and sanitizer for Agent Sessions.
 * Neutralizes raw HTML tags and XSS vectors while rendering standard GFM syntax.
 */

import { highlightCode } from './highlight.js';

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function sanitizeHref(url: string): string {
  const trimmed = url.trim();
  const lower = trimmed.toLowerCase();
  if (lower.startsWith('http://') || lower.startsWith('https://') || lower.startsWith('mailto:')) {
    return escapeHtml(trimmed);
  }
  return '#';
}

// Emphasis must hug its text: "2 * 3 * 4" stays literal. `*` may emphasize
// inside a word, but `_` only at word boundaries (as in GFM), so identifiers
// like API_KEY_NAME and snake_case_fn render as written.
const star = (n: number) => new RegExp(`\\*{${n}}(?!\\s)(.*?\\S)\\*{${n}}`, 'g');
const underscore = (n: number) =>
  new RegExp(`(^|[^\\p{L}\\p{N}_])_{${n}}(?!\\s)(.*?\\S)_{${n}}(?![\\p{L}\\p{N}_])`, 'gu');

function renderEmphasis(text: string): string {
  let out = text;

  // Bold + Italic: ***text*** or ___text___
  out = out.replace(star(3), '<strong><em>$1</em></strong>');
  out = out.replace(underscore(3), '$1<strong><em>$2</em></strong>');

  // Bold: **text** or __text__
  out = out.replace(star(2), '<strong>$1</strong>');
  out = out.replace(underscore(2), '$1<strong>$2</strong>');

  // Italic: *text* or _text_
  out = out.replace(star(1), '<em>$1</em>');
  out = out.replace(underscore(1), '$1<em>$2</em>');

  // Strikethrough: ~~text~~
  out = out.replace(/~~(.*?)~~/g, '<del>$1</del>');

  return out;
}

function renderInline(text: string): string {
  // First escape raw HTML
  let out = escapeHtml(text);

  // Code spans and links are rendered first and set aside, so emphasis never
  // rewrites code or URLs. Placeholders use NUL, which escaped text lacks.
  const stashed: string[] = [];
  const stash = (html: string) => `\u0000${stashed.push(html) - 1}\u0000`;

  // Inline code: `code`
  out = out.replace(/`([^`]+)`/g, (_match, code) => stash(`<code class="inline-code">${code}</code>`));

  // Links: [label](url)
  out = out.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_match, label, url) => {
    const safeUrl = sanitizeHref(url);
    return stash(`<a href="${safeUrl}" target="_blank" rel="noopener noreferrer">${renderEmphasis(label)}</a>`);
  });

  out = renderEmphasis(out);

  return out.replace(/\u0000(\d+)\u0000/g, (_match, idx) => stashed[Number(idx)]);
}

const TABLE_SEPARATOR = /^\s*\|?\s*[-:]+[-| :]*\|?\s*$/;

// A table starts at any line with a pipe that sits on a separator line.
function isTableStart(lines: string[], i: number): boolean {
  return lines[i].includes('|') && i + 1 < lines.length && TABLE_SEPARATOR.test(lines[i + 1]);
}

// Splits a table row into trimmed cells. Pipes inside a code span or written
// as \| belong to the cell; \| renders as a plain |.
function splitTableRow(line: string): string[] {
  let row = line.trim();
  if (row.startsWith('|')) row = row.slice(1);
  if (row.endsWith('|') && !row.endsWith('\\|')) row = row.slice(0, -1);

  const cells: string[] = [];
  let cell = '';
  let i = 0;
  while (i < row.length) {
    const ch = row[i];
    if (ch === '\\' && row[i + 1] === '|') {
      cell += '|';
      i += 2;
    } else if (ch === '`' && row.indexOf('`', i + 1) !== -1) {
      // Code span, matching renderInline: up to the next backtick.
      const end = row.indexOf('`', i + 1);
      cell += row.slice(i, end + 1).replace(/\\\|/g, '|');
      i = end + 1;
    } else if (ch === '|') {
      cells.push(cell.trim());
      cell = '';
      i++;
    } else {
      cell += ch;
      i++;
    }
  }
  cells.push(cell.trim());
  return cells;
}

export function renderMarkdown(source: string): string {
  if (!source) return '';

  const lines = source.split(/\r?\n/);
  const out: string[] = [];
  let i = 0;
  const len = lines.length;

  while (i < len) {
    const line = lines[i];

    // Fenced code blocks: ```lang ... ```
    if (line.trimStart().startsWith('```')) {
      const match = line.trimStart().match(/^```([a-zA-Z0-9_\-+]*)/);
      const lang = match ? match[1] : '';
      const codeLines: string[] = [];
      i++;
      while (i < len && !lines[i].trimStart().startsWith('```')) {
        codeLines.push(lines[i]);
        i++;
      }
      i++; // Skip closing ```
      const rawCode = codeLines.join('\n');
      const highlighted = highlightCode(rawCode, lang);
      const displayLang = lang || 'text';

      out.push(
        `<div class="code-block-container" data-lang="${escapeHtml(displayLang)}">` +
          `<div class="code-block-header">` +
            `<span class="code-lang-tag">${escapeHtml(displayLang)}</span>` +
            `<button type="button" class="copy-code-btn" data-code="${escapeHtml(rawCode)}">Copy</button>` +
          `</div>` +
          `<pre><code class="hljs language-${escapeHtml(displayLang)}">${highlighted}</code></pre>` +
        `</div>`
      );
      continue;
    }

    // Horizontal Rule: ---, ***, ___
    if (/^(\*{3,}|-{3,}|_{3,})\s*$/.test(line.trim())) {
      out.push('<hr class="markdown-divider" />');
      i++;
      continue;
    }

    // Headings: # h1 .. ###### h6
    const headingMatch = line.match(/^(#{1,6})\s+(.*)$/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      const content = renderInline(headingMatch[2]);
      out.push(`<h${level}>${content}</h${level}>`);
      i++;
      continue;
    }

    // Blockquote: > text
    if (line.startsWith('>')) {
      const quoteLines: string[] = [];
      while (i < len && lines[i].startsWith('>')) {
        quoteLines.push(lines[i].replace(/^>\s?/, ''));
        i++;
      }
      const quoteContent = renderMarkdown(quoteLines.join('\n'));
      out.push(`<blockquote>${quoteContent}</blockquote>`);
      continue;
    }

    // Unordered List: * or -
    if (/^\s*[*+-]\s+/.test(line)) {
      const listItems: string[] = [];
      while (i < len && /^\s*[*+-]\s+/.test(lines[i])) {
        const itemText = lines[i].replace(/^\s*[*+-]\s+/, '');
        listItems.push(`<li>${renderInline(itemText)}</li>`);
        i++;
      }
      out.push(`<ul>${listItems.join('')}</ul>`);
      continue;
    }

    // Ordered List: 1. 2.
    if (/^\s*\d+\.\s+/.test(line)) {
      const listItems: string[] = [];
      while (i < len && /^\s*\d+\.\s+/.test(lines[i])) {
        const itemText = lines[i].replace(/^\s*\d+\.\s+/, '');
        listItems.push(`<li>${renderInline(itemText)}</li>`);
        i++;
      }
      out.push(`<ol>${listItems.join('')}</ol>`);
      continue;
    }

    // Tables: lines with |
    if (isTableStart(lines, i)) {
      const headerCells = splitTableRow(line);
      i += 2; // Skip header and separator

      const rows: string[][] = [];
      while (i < len && lines[i].includes('|') && lines[i].trim().length > 0) {
        rows.push(splitTableRow(lines[i]));
        i++;
      }

      let tableHtml = '<div class="table-container"><table><thead><tr>';
      for (const h of headerCells) {
        tableHtml += `<th>${renderInline(h)}</th>`;
      }
      tableHtml += '</tr></thead><tbody>';
      for (const row of rows) {
        tableHtml += '<tr>';
        for (let colIdx = 0; colIdx < headerCells.length; colIdx++) {
          const val = row[colIdx] || '';
          tableHtml += `<td>${renderInline(val)}</td>`;
        }
        tableHtml += '</tr>';
      }
      tableHtml += '</tbody></table></div>';
      out.push(tableHtml);
      continue;
    }

    // Blank lines
    if (!line.trim()) {
      i++;
      continue;
    }

    // Regular paragraph
    const pLines: string[] = [];
    while (
      i < len &&
      lines[i].trim() &&
      !lines[i].trimStart().startsWith('```') &&
      !lines[i].startsWith('#') &&
      !lines[i].startsWith('>') &&
      !/^\s*[*+-]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
      !/^(\*{3,}|-{3,}|_{3,})\s*$/.test(lines[i].trim()) &&
      !isTableStart(lines, i)
    ) {
      pLines.push(lines[i]);
      i++;
    }

    if (pLines.length > 0) {
      const pText = pLines.map((l) => renderInline(l)).join('<br />');
      out.push(`<p>${pText}</p>`);
    }
  }

  return out.join('\n');
}
