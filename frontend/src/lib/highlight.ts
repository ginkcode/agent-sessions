/**
 * Syntax highlighting for common programming languages and formats.
 * Produces safe HTML with standard highlight.js class names.
 * Completely zero-dependency and XSS-safe.
 */

const HTML_ESCAPES: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

function escapeHtml(text: string): string {
  return text.replace(/[&<>"']/g, (ch) => HTML_ESCAPES[ch]);
}

const LANGUAGE_ALIASES: Record<string, string> = {
  js: 'javascript',
  ts: 'typescript',
  py: 'python',
  sh: 'bash',
  shell: 'bash',
  zsh: 'bash',
  golang: 'go',
  yml: 'yaml',
  patch: 'diff',
  md: 'markdown',
  htm: 'html',
};

const KEYWORDS_GO = new Set([
  'break', 'case', 'chan', 'const', 'continue', 'default', 'defer', 'else',
  'fallthrough', 'for', 'func', 'go', 'goto', 'if', 'import', 'interface',
  'map', 'package', 'range', 'return', 'select', 'struct', 'switch', 'type',
  'var', 'nil', 'true', 'false', 'iota', 'string', 'int', 'int64', 'bool',
  'byte', 'rune', 'error', 'any', 'uint', 'uint64', 'float64'
]);

const KEYWORDS_JS = new Set([
  'async', 'await', 'break', 'case', 'catch', 'class', 'const', 'continue',
  'debugger', 'default', 'delete', 'do', 'else', 'export', 'extends',
  'finally', 'for', 'function', 'if', 'import', 'in', 'instanceof', 'new',
  'return', 'super', 'switch', 'this', 'throw', 'try', 'typeof', 'var',
  'void', 'while', 'with', 'yield', 'let', 'static', 'enum', 'interface',
  'type', 'implements', 'public', 'private', 'protected', 'null', 'undefined',
  'true', 'false', 'from', 'as'
]);

const KEYWORDS_PY = new Set([
  'and', 'as', 'assert', 'async', 'await', 'break', 'class', 'continue',
  'def', 'del', 'elif', 'else', 'except', 'finally', 'for', 'from',
  'global', 'if', 'import', 'in', 'is', 'lambda', 'nonlocal', 'not',
  'or', 'pass', 'raise', 'return', 'try', 'while', 'with', 'yield',
  'True', 'False', 'None'
]);

const KEYWORDS_BASH = new Set([
  'if', 'then', 'else', 'elif', 'fi', 'case', 'esac', 'for', 'while',
  'until', 'do', 'done', 'in', 'function', 'select', 'time', 'echo',
  'exit', 'cd', 'export', 'local', 'source', 'set', 'unset'
]);

const KEYWORDS_SQL = new Set([
  'select', 'from', 'where', 'insert', 'into', 'update', 'delete', 'join',
  'inner', 'left', 'right', 'outer', 'on', 'group', 'by', 'order', 'having',
  'limit', 'offset', 'as', 'and', 'or', 'not', 'null', 'is', 'in', 'like',
  'create', 'table', 'drop', 'index', 'view', 'primary', 'key', 'references'
]);

export function detectLanguage(code: string): string {
  const trimmed = code.trim();
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      JSON.parse(trimmed);
      return 'json';
    } catch {}
  }
  if (trimmed.startsWith('diff --git') || trimmed.startsWith('--- a/') || trimmed.startsWith('+++ b/')) {
    return 'diff';
  }
  if (trimmed.includes('func ') && (trimmed.includes('package ') || trimmed.includes(':= '))) {
    return 'go';
  }
  if (trimmed.includes('import ') && (trimmed.includes('from \'') || trimmed.includes('export '))) {
    return 'typescript';
  }
  if (trimmed.includes('def ') && trimmed.includes(':')) {
    return 'python';
  }
  return 'plaintext';
}

function highlightJson(code: string): string {
  return code.replace(
    /("(?:\\u[\da-fA-F]{4}|\\[^u]|[^\\"])*")(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|[&<>]/g,
    (match, str, colon, lit) => {
      if (str) {
        const escapedStr = escapeHtml(str);
        if (colon) {
          return `<span class="hljs-attr">${escapedStr}</span>${colon}`;
        }
        return `<span class="hljs-string">${escapedStr}</span>`;
      }
      if (lit) {
        return `<span class="hljs-literal">${escapeHtml(match)}</span>`;
      }
      if (/^-?\d/.test(match)) {
        return `<span class="hljs-number">${escapeHtml(match)}</span>`;
      }
      return escapeHtml(match);
    }
  );
}

function highlightDiff(code: string): string {
  const lines = code.split('\n');
  const highlighted = lines.map((line) => {
    const escaped = escapeHtml(line);
    if (line.startsWith('+') && !line.startsWith('+++')) {
      return `<span class="hljs-addition">${escaped}</span>`;
    }
    if (line.startsWith('-') && !line.startsWith('---')) {
      return `<span class="hljs-deletion">${escaped}</span>`;
    }
    if (line.startsWith('@@')) {
      return `<span class="hljs-meta">${escaped}</span>`;
    }
    if (line.startsWith('diff --git') || line.startsWith('index ')) {
      return `<span class="hljs-keyword">${escaped}</span>`;
    }
    return escaped;
  });
  return highlighted.join('\n');
}

const NUMBER_BOUNDARY = /[\s,([\]{}:;=+\-*/%<>&|^~!]/;
const NUMBER_CHAR = /[\d.xXa-fA-F_]/;

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

function isWordStart(ch: string): boolean {
  return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch === '_' || ch === '$';
}

function isWordChar(ch: string): boolean {
  return isWordStart(ch) || isDigit(ch);
}

// Scans one line at a time. Tokens are sliced out of the line rather than
// built char by char, and punctuation is escaped through a lookup, since
// this runs over every code block in a transcript page.
function highlightGenericCode(code: string, keywords: Set<string>, commentPrefix: string): string {
  const lines = code.split('\n');
  const result: string[] = [];

  for (const line of lines) {
    let i = 0;
    let out = '';
    const len = line.length;

    while (i < len) {
      // Check single-line comment
      if (line.startsWith(commentPrefix, i)) {
        out += `<span class="hljs-comment">${escapeHtml(line.slice(i))}</span>`;
        break;
      }

      const start = i;
      const ch = line[i];

      // Check strings (single or double quoted or backticks)
      if (ch === '"' || ch === '\'' || ch === '`') {
        i++;
        while (i < len) {
          const c = line[i];
          if (c === '\\' && i + 1 < len) {
            i++;
          } else if (c === ch) {
            i++;
            break;
          }
          i++;
        }
        out += `<span class="hljs-string">${escapeHtml(line.slice(start, i))}</span>`;
        continue;
      }

      // Check numbers
      if (isDigit(ch) && (i === 0 || NUMBER_BOUNDARY.test(line[i - 1]))) {
        while (i < len && NUMBER_CHAR.test(line[i])) i++;
        out += `<span class="hljs-number">${escapeHtml(line.slice(start, i))}</span>`;
        continue;
      }

      // Check words / identifiers / keywords
      if (isWordStart(ch)) {
        while (i < len && isWordChar(line[i])) i++;
        const word = line.slice(start, i);
        if (keywords.has(word) || keywords.has(word.toLowerCase())) {
          out += `<span class="hljs-keyword">${word}</span>`;
        } else if (i < len && line[i] === '(') {
          out += `<span class="hljs-title hljs-function">${word}</span>`;
        } else {
          out += word;
        }
        continue;
      }

      // Operators and punctuation
      out += HTML_ESCAPES[ch] ?? ch;
      i++;
    }
    result.push(out);
  }

  return result.join('\n');
}

export function highlightCode(code: string, language?: string): string {
  if (!code) return '';

  let lang = (language || '').toLowerCase().trim();
  if (LANGUAGE_ALIASES[lang]) {
    lang = LANGUAGE_ALIASES[lang];
  }

  if (!lang) {
    lang = detectLanguage(code);
  }

  switch (lang) {
    case 'json':
      return highlightJson(code);
    case 'diff':
      return highlightDiff(code);
    case 'go':
      return highlightGenericCode(code, KEYWORDS_GO, '//');
    case 'typescript':
    case 'javascript':
      return highlightGenericCode(code, KEYWORDS_JS, '//');
    case 'python':
      return highlightGenericCode(code, KEYWORDS_PY, '#');
    case 'bash':
      return highlightGenericCode(code, KEYWORDS_BASH, '#');
    case 'sql':
      return highlightGenericCode(code, KEYWORDS_SQL, '--');
    default:
      return escapeHtml(code);
  }
}
