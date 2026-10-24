import type { CodeDiagnosticPosition, CodeDiagnosticRange, CodeHoverText, CodeNavigationLocation } from './codeDiagnostics';
import { renderMarkdown } from './markdown';
import { navigationShortcut, type NavDirection } from './navHistory';

/**
 * Code navigation in the file viewer: where the pointer is in the source,
 * the word it is over, the way back, and how an answer reads.
 */

/** A place in a file: a project path, or an absolute host path outside it. */
export interface SourceLocation { path: string; external?: boolean; range: CodeDiagnosticRange }

/** A point in the viewport, where a popup about a symbol opens. */
export interface ViewportPoint { x: number; y: number }

/** What the source view reports about words, for code navigation. */
export interface SourceSymbols {
  /** Words underline under the link modifier only when navigation can be asked. */
  linked: boolean;
  /** Cmd/Ctrl+click on a word; position is the word's start. */
  definition(position: CodeDiagnosticPosition, at: ViewportPoint): void;
  /** The pointer rested on a word. */
  hover(position: CodeDiagnosticPosition, at: ViewportPoint): void;
  /** The pointer left the word, the view scrolled or a button went down. */
  hoverEnd(): void;
  /** A context menu on a word. */
  menu(position: CodeDiagnosticPosition, word: string, at: ViewportPoint): void;
}

export const HOVER_DELAY = 400;

/** The word (identifier) around a UTF-16 column, or undefined off a word. */
export function wordRange(line: string, character: number): { start: number; end: number } | undefined {
  const word = /[\p{L}\p{N}_$]/u;
  const at = (i: number) => i >= 0 && i < line.length && word.test(line[i]);
  // The character under the pointer, or the one before it at a word's end.
  const i = at(character) ? character : at(character - 1) ? character - 1 : -1;
  if (i < 0) return undefined;
  let start = i, end = i + 1;
  while (at(start - 1)) start--;
  while (at(end)) end++;
  // A number is a literal, with nothing to go to.
  if (/^\p{N}+$/u.test(line.slice(start, end))) return undefined;
  return { start, end };
}

/** The DOM point under viewport coordinates, in WebKit and Chromium alike. */
export function caretPoint(doc: Document, x: number, y: number): { node: Node; offset: number } | undefined {
  const d = doc as Document & {
    caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null;
    caretRangeFromPoint?: (x: number, y: number) => Range | null;
  };
  if (typeof d.caretPositionFromPoint === 'function') {
    const p = d.caretPositionFromPoint(x, y);
    return p ? { node: p.offsetNode, offset: p.offset } : undefined;
  }
  if (typeof d.caretRangeFromPoint === 'function') {
    const r = d.caretRangeFromPoint(x, y);
    return r ? { node: r.startContainer, offset: r.startOffset } : undefined;
  }
  return undefined;
}

/**
 * The source position of a DOM point inside root: the line from the nearest
 * `[data-source-line]`, the UTF-16 column from the text before the point.
 * Syntax markup adds no characters, so it never shifts the column. Removed
 * diff lines and gutter numbers have no position.
 */
export function sourcePosition(root: HTMLElement, node: Node, offset: number): CodeDiagnosticPosition | undefined {
  const el = (node.nodeType === 1 ? node as Element : node.parentElement)?.closest<HTMLElement>('[data-source-line]');
  if (!el || !root.contains(el)) return undefined;
  const line = Number(el.dataset.sourceLine) - 1;
  if (!Number.isInteger(line) || line < 0) return undefined;
  const range = root.ownerDocument.createRange();
  range.setStart(el, 0);
  try { range.setEnd(node, offset); } catch { return undefined; }
  return { line, character: range.toString().length };
}

/** The DOM range of columns start..end on a source line, for painting. */
export function sourceRange(root: HTMLElement, line: number, start: number, end: number): Range | undefined {
  const el = root.querySelector<HTMLElement>(`[data-source-line="${line + 1}"]`);
  if (!el) return undefined;
  const walker = root.ownerDocument.createTreeWalker(el, 4 /* SHOW_TEXT */);
  const points: ([Node, number] | undefined)[] = [undefined, undefined];
  let seen = 0, node: Node | null;
  while ((node = walker.nextNode()) && !points[1]) {
    const length = node.textContent?.length ?? 0;
    if (!points[0] && start <= seen + length) points[0] = [node, start - seen];
    if (!points[1] && end <= seen + length) points[1] = [node, end - seen];
    seen += length;
  }
  if (!points[0] || !points[1]) return undefined;
  const range = root.ownerDocument.createRange();
  range.setStart(...points[0]); range.setEnd(...points[1]);
  return range;
}

/** Cmd on macOS, Ctrl elsewhere: the modifier that turns words into links. */
export function linkModifier(event: Pick<MouseEvent | KeyboardEvent, 'metaKey' | 'ctrlKey' | 'altKey'>, mac: boolean): boolean {
  return (mac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey) && !event.altKey;
}

/** F12 and Shift+F12, as in VS Code. */
export function navigationKey(event: Pick<KeyboardEvent, 'key' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>): 'definition' | 'references' | undefined {
  if (event.key !== 'F12' || event.metaKey || event.ctrlKey || event.altKey) return undefined;
  return event.shiftKey ? 'references' : 'definition';
}

/**
 * Back and forward through code navigation: the app's keys (Cmd+[ / Cmd+],
 * Alt+Left/Right) and, as in VS Code on macOS, Ctrl+- / Ctrl+Shift+-. While
 * typing, the app's keys belong to the field (Cmd+[ outdents in the editor),
 * so only Ctrl+- and the keyboard's Back/Forward keys go.
 */
export function historyKey(event: Pick<KeyboardEvent, 'key' | 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey' | 'target'>, mac: boolean): NavDirection | null {
  if (mac && event.ctrlKey && !event.metaKey && !event.altKey && event.code === 'Minus') return event.shiftKey ? 'forward' : 'back';
  const t = event.target as { tagName?: string; isContentEditable?: boolean } | null;
  const typing = Boolean(t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName ?? '')));
  if (typing && !event.key.startsWith('Browser')) return null;
  return navigationShortcut(event, mac);
}

/** How a location reads in a list: path, then 1-based line and column. */
export function locationText(location: CodeNavigationLocation, label: (path: string) => string = p => p): string {
  const { line, character } = location.range.start;
  return `${location.external ? label(location.path) : location.path}:${line + 1}:${character + 1}`;
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// Language servers may pass through Python-Markdown documentation directives.
// Adapt only their indented bodies, never literal examples or plaintext hovers.
function hoverMarkdown(source: string): string {
  const lines = source.split(/\r?\n/);
  const out: string[] = [];
  let fenced = false;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.trimStart().startsWith('```')) fenced = !fenced;
    const header = !fenced && line.match(/^!!! ([a-z][\w-]*)(?: "([^"\n]+)")?\s*$/i);
    if (!header) {
      out.push(line);
      continue;
    }
    // Some servers encode the four-space indent as nonbreaking spaces.
    const indent = /^(?: {4}|\t|(?:&nbsp;){4}| {4})/;
    const body: string[] = [];
    let next = i + 1;
    while (next < lines.length) {
      if (indent.test(lines[next])) {
        body.push(lines[next].replace(indent, ''));
        next++;
      } else if (!lines[next].trim()) {
        body.push('');
        next++;
      } else break;
    }
    if (!body.some((l) => l.trim())) {
      out.push(line);
      continue;
    }
    const title = (header[2] ?? header[1]).replace(/[\\`*_[\]<>]/g, '\\$&');
    out.push('', `> **${title}**`, '>', ...body.map((l) => `> ${l}`), '');
    i = next - 1;
  }
  return out.join('\n');
}

/** The hover text as HTML: Markdown through the transcript renderer, plain text escaped. */
export function hoverHtml(hover: CodeHoverText): string {
  return hover.kind === 'markdown' ? renderMarkdown(hoverMarkdown(hover.value)) : `<pre class="hover-plain">${escapeHtml(hover.value)}</pre>`;
}

export const SOURCE_HISTORY_LIMIT = 50;

/**
 * Back and forward through the places navigation went, for one project.
 * `visit` records where the user left from; going back records where they
 * are, so forward returns there.
 */
export class SourceHistory {
  private back: SourceLocation[] = [];
  private forward: SourceLocation[] = [];
  private readonly limit: number;

  constructor(limit = SOURCE_HISTORY_LIMIT) { this.limit = limit; }

  get canGoBack(): boolean { return this.back.length > 0; }
  get canGoForward(): boolean { return this.forward.length > 0; }

  /** Leaving from for somewhere else: forward is dropped. */
  visit(from: SourceLocation): void {
    const last = this.back[this.back.length - 1];
    if (!last || !sameLocation(last, from)) this.back.push(from);
    if (this.back.length > this.limit) this.back.shift();
    this.forward = [];
  }

  goBack(current: SourceLocation | undefined): SourceLocation | undefined {
    return this.step(this.back, this.forward, current);
  }

  goForward(current: SourceLocation | undefined): SourceLocation | undefined {
    return this.step(this.forward, this.back, current);
  }

  /** A path closed for good (deleted) or renamed: its places go too. */
  forget(keep: (location: SourceLocation) => boolean): void {
    this.back = this.back.filter(keep);
    this.forward = this.forward.filter(keep);
  }

  clear(): void { this.back = []; this.forward = []; }

  private step(from: SourceLocation[], to: SourceLocation[], current: SourceLocation | undefined): SourceLocation | undefined {
    const next = from.pop();
    if (next && current) {
      to.push(current);
      if (to.length > this.limit) to.shift();
    }
    return next;
  }
}

function sameLocation(a: SourceLocation, b: SourceLocation): boolean {
  return a.path === b.path && Boolean(a.external) === Boolean(b.external)
    && a.range.start.line === b.range.start.line && a.range.start.character === b.range.start.character;
}
