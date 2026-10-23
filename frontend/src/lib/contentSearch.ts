/**
 * Helpers for the Search pane: searching text in the browser the way the
 * host does (for unsaved drafts and the browser mock), merging those results
 * with the host's, and the order the keyboard moves through them.
 */
import type { CodeDiagnosticRange } from './codeDiagnostics';
import type { SearchFile, SearchMatch, SearchQuery, SearchResult } from './types';

/** The host's caps, mirrored so a draft overlay cannot grow past them. */
export const DEFAULT_SEARCH_MATCHES = 5000;
export const MAX_SEARCH_MATCHES = 20000;
export const MAX_FILE_MATCHES = 1000;
const MAX_PREVIEW = 240;
const PREVIEW_LEAD = 60;

export class SearchPatternError extends Error {}

/** The limit a query asks for, within the host's bounds. */
export function searchLimit(q: SearchQuery): number {
  return Math.min(q.maxMatches && q.maxMatches > 0 ? q.maxMatches : DEFAULT_SEARCH_MATCHES, MAX_SEARCH_MATCHES);
}

/**
 * The JavaScript expression for q. The host uses RE2; the syntax people
 * type (classes, groups, alternation, \b, \d, \w) means the same in both.
 * Throws SearchPatternError when JavaScript cannot read the pattern.
 */
export function searchRegExp(q: SearchQuery): RegExp {
  let source = q.regex ? q.pattern : q.pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  if (q.wholeWord) source = `\\b(?:${source})\\b`;
  const flags = q.matchCase ? 'g' : 'gi';
  try {
    return new RegExp(source, `${flags}u`);
  } catch {
    try {
      // Without u, escapes such as \- outside a class are allowed, as in RE2.
      return new RegExp(source, flags);
    } catch (e) {
      throw new SearchPatternError(`invalid search pattern: ${(e as Error).message}`);
    }
  }
}

interface Glob { re: RegExp; anywhere: boolean }

/**
 * Reads comma-separated globs as the host does: * and ? stay within one
 * path component, ** crosses them, {a,b} is either and [!x] is a negated
 * class. A glob without a slash matches any path component; one with a
 * slash is anchored at the root and covers the folder it names.
 */
export function parseSearchGlobs(list: string | undefined): Glob[] {
  const parts: string[] = [];
  let depth = 0, start = 0;
  const text = list ?? '';
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '{') depth++;
    else if (text[i] === '}') depth = Math.max(0, depth - 1);
    else if (text[i] === ',' && depth === 0) { parts.push(text.slice(start, i)); start = i + 1; }
  }
  parts.push(text.slice(start));
  return parts
    .map(p => p.trim().replace(/^\.\//, '').replace(/\/$/, ''))
    .filter(Boolean)
    .map(p => ({ re: new RegExp(`^${globSource(p.replace(/^\//, ''))}$`), anywhere: !p.includes('/') }));
}

function globSource(p: string): string {
  let out = '';
  let braces = 0;
  for (let i = 0; i < p.length; i++) {
    const c = p[i];
    if (c === '*' && p[i + 1] === '*') {
      i++;
      if (p[i + 1] === '/') { i++; out += '(?:.*/)?'; } else out += '.*';
    } else if (c === '*') out += '[^/]*';
    else if (c === '?') out += '[^/]';
    else if (c === '{') { braces++; out += '(?:'; }
    else if (c === '}' && braces > 0) { braces--; out += ')'; }
    else if (c === ',' && braces > 0) out += '|';
    else if (c === '[' && p.indexOf(']', i + 1) > i) {
      const end = p.indexOf(']', i + 1);
      const cls = p.slice(i + 1, end);
      out += `[${cls.startsWith('!') ? `^${cls.slice(1)}` : cls}]`.replace(/\\/g, '\\\\');
      i = end;
    } else out += c.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&');
  }
  return out + ')'.repeat(braces);
}

/** Whether a slash-separated project path matches one of globs. */
export function matchSearchGlobs(globs: Glob[], path: string): boolean {
  const parts = path.split('/');
  return globs.some(g => g.anywhere
    ? parts.some(part => g.re.test(part))
    : parts.some((_, i) => g.re.test(parts.slice(0, i + 1).join('/'))));
}

/** Whether q's include and exclude globs let path be searched. */
export function searchesPath(q: SearchQuery, path: string): boolean {
  const include = parseSearchGlobs(q.include);
  return (include.length === 0 || matchSearchGlobs(include, path)) && !matchSearchGlobs(parseSearchGlobs(q.exclude), path);
}

/** Searches one file's text line by line, as the host does. */
export function searchText(path: string, text: string, re: RegExp, revision = ''): SearchFile {
  const file: SearchFile = { path, revision, matches: [] };
  const lines = text.split('\n');
  for (let i = 0; i < lines.length && !file.truncated; i++) {
    const line = lines[i].endsWith('\r') ? lines[i].slice(0, -1) : lines[i];
    re.lastIndex = 0;
    for (const m of line.matchAll(re)) {
      if (m[0] === '') continue;
      if (file.matches.length === MAX_FILE_MATCHES) { file.truncated = true; break; }
      file.matches.push(lineMatch(i + 1, line, m.index ?? 0, (m.index ?? 0) + m[0].length));
    }
  }
  return file;
}

/** The match of line[start, end): a trimmed preview, cut around the match when long. */
function lineMatch(lineNumber: number, line: string, start: number, end: number): SearchMatch {
  let from = Math.min(line.length - line.trimStart().length, start);
  let to = line.length;
  if (to - from > MAX_PREVIEW) {
    if (start - from > PREVIEW_LEAD) from = start - PREVIEW_LEAD;
    to = Math.max(from + MAX_PREVIEW, end);
  }
  // Never cut a surrogate pair in half.
  if (from > 0 && isLowSurrogate(line.charCodeAt(from))) from--;
  if (to < line.length && isLowSurrogate(line.charCodeAt(to))) to++;
  return {
    line: lineNumber, start, end,
    preview: line.slice(from, to), previewStart: start - from, previewEnd: end - from,
  };
}

const isLowSurrogate = (c: number) => c >= 0xdc00 && c <= 0xdfff;

/** An unsaved draft, searched in place of the file on disk. */
export interface SearchDraft { path: string; text: string }

export interface OverlaidResult {
  result: SearchResult;
  /** Drafts the browser could not search: JavaScript rejected the pattern. */
  unsearched: string[];
}

/**
 * Replaces the host's matches for each drafted file with the draft's, so
 * results describe what the editor shows. Drafts outside the include and
 * exclude globs are left as the host found them (absent).
 */
export function overlayDrafts(result: SearchResult, drafts: SearchDraft[], q: SearchQuery): OverlaidResult {
  const wanted = drafts.filter(d => searchesPath(q, d.path));
  if (wanted.length === 0) return { result, unsearched: [] };
  let re: RegExp;
  try {
    re = searchRegExp(q);
  } catch {
    return { result, unsearched: wanted.map(d => d.path) };
  }
  const byPath = new Map(result.files.map(f => [f.path, f]));
  for (const d of wanted) {
    const file = searchText(d.path, d.text, re, byPath.get(d.path)?.revision ?? '');
    if (file.matches.length) byPath.set(d.path, { ...file, draft: true });
    else byPath.delete(d.path);
  }
  const files = [...byPath.values()].sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  const matches = files.reduce((n, f) => n + f.matches.length, 0);
  return { result: { ...result, files, matches }, unsearched: [] };
}

/** "N results in M files", with the limit when a cap cut the search short. */
export function searchSummary(result: SearchResult, limit: number): string {
  const n = result.matches, m = result.files.length;
  const text = `${n.toLocaleString()} ${n === 1 ? 'result' : 'results'} in ${m.toLocaleString()} ${m === 1 ? 'file' : 'files'}`;
  return result.truncated ? `${text} (limited to ${limit.toLocaleString()})` : text;
}

/** One match's place in the results: the file and the match's index in it. */
export interface MatchRef { path: string; index: number }

export const matchKey = (ref: MatchRef) => `${ref.index}:${ref.path}`;

/** The matches in display order, leaving out the collapsed files. */
export function matchOrder(result: SearchResult | null, collapsed: ReadonlySet<string> = new Set()): MatchRef[] {
  const out: MatchRef[] = [];
  for (const f of result?.files ?? []) {
    if (collapsed.has(f.path)) continue;
    f.matches.forEach((_, index) => out.push({ path: f.path, index }));
  }
  return out;
}

/** The match after (or before) current in order, wrapping around. */
export function stepMatch(order: MatchRef[], current: MatchRef | null, direction: 1 | -1): MatchRef | null {
  if (order.length === 0) return null;
  const at = current ? order.findIndex(r => r.path === current.path && r.index === current.index) : -1;
  if (at < 0) return direction > 0 ? order[0] : order[order.length - 1];
  return order[(at + direction + order.length) % order.length];
}

/** The editor range of a match: 0-based lines, UTF-16 columns. */
export function matchRange(m: SearchMatch): CodeDiagnosticRange {
  return { start: { line: m.line - 1, character: m.start }, end: { line: m.line - 1, character: m.end } };
}

/** The matched text, as the preview holds it. */
export function matchedText(m: SearchMatch): string {
  return m.preview.slice(m.previewStart, m.previewEnd);
}

/** Longer selections are not a search someone means to start. */
const MAX_SEED = 200;

/** The selected text as the initial search: one line, not too long. */
export function searchSeed(selection: string): string | undefined {
  return selection && selection.length <= MAX_SEED && !/[\r\n]/.test(selection) ? selection : undefined;
}

/** Cmd+Shift+F on macOS, Ctrl+Shift+F elsewhere. */
export function isSearchShortcut(e: KeyboardEvent, isMac: boolean): boolean {
  return (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && e.shiftKey && !e.altKey && e.code === 'KeyF';
}
