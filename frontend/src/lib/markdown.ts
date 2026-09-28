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

function renderInline(text: string): string {
  // First escape raw HTML
  let out = escapeHtml(text);

  // Inline code: `code`
  out = out.replace(/`([^`]+)`/g, '<code class="inline-code">$1</code>');

  // Bold + Italic: ***text*** or ___text___
  out = out.replace(/(\*\*\*|___)(.*?)\1/g, '<strong><em>$2</em></strong>');

  // Bold: **text** or __text__
  out = out.replace(/(\*\*|__)(.*?)\1/g, '<strong>$2</strong>');

  // Italic: *text* or _text_
  out = out.replace(/(\*|_)(.*?)\1/g, '<em>$2</em>');

  // Strikethrough: ~~text~~
  out = out.replace(/~~(.*?)~~/g, '<del>$1</del>');

  // Links: [label](url)
  out = out.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_match, label, url) => {
    const safeUrl = sanitizeHref(url);
    return `<a href="${safeUrl}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  });

  return out;
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
    if (line.includes('|') && i + 1 < len && /^\s*\|?\s*[-:]+[-| :]*\|?\s*$/.test(lines[i + 1])) {
      const headerCells = line
        .split('|')
        .map((c) => c.trim())
        .filter((c, idx, arr) => (idx > 0 && idx < arr.length - 1) || c.length > 0);
      i += 2; // Skip header and separator

      const rows: string[][] = [];
      while (i < len && lines[i].includes('|') && lines[i].trim().length > 0) {
        const cells = lines[i]
          .split('|')
          .map((c) => c.trim())
          .filter((c, idx, arr) => (idx > 0 && idx < arr.length - 1) || c.length > 0);
        rows.push(cells);
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
      !/^(\*{3,}|-{3,}|_{3,})\s*$/.test(lines[i].trim())
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
