/**
 * Find in the open file: plain text matches over the lines of the code
 * view, as editors find. The view paints and scrolls to them.
 */

/** A match: its row in the code view and its span within the row's text. */
export interface FindMatch {
  row: number;
  start: number;
  end: number;
}

export interface FindResult {
  matches: FindMatch[];
  /** More matches than the limit: the rest are left uncounted. */
  capped: boolean;
}

/** A common letter in a large file matches more often than anyone steps through. */
export const FIND_LIMIT = 10_000;

const NONE: FindResult = { matches: [], capped: false };

/**
 * The matches of `query` in each line, in order, without overlaps. Ignoring
 * case leaves the positions those of the line as written.
 */
export function findInLines(lines: string[], query: string, matchCase: boolean, limit = FIND_LIMIT): FindResult {
  if (!query) return NONE;
  const pattern = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), matchCase ? 'g' : 'gi');
  const matches: FindMatch[] = [];
  for (let row = 0; row < lines.length; row++) {
    for (const m of lines[row].matchAll(pattern)) {
      if (matches.length === limit) return { matches, capped: true };
      matches.push({ row, start: m.index, end: m.index + m[0].length });
    }
  }
  return { matches, capped: false };
}

/** The index of the first match at or after a row and column, or matches.length. */
export function firstMatchFrom(matches: FindMatch[], row: number, column = 0): number {
  let lo = 0;
  let hi = matches.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    const m = matches[mid];
    if (m.row < row || (m.row === row && m.start < column)) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

/**
 * The range of characters start to end of an element's text, across the
 * spans highlighting splits it into, or null past its end.
 */
export function textRange(el: Element, start: number, end: number): Range | null {
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  let pos = 0;
  let started = false;
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const length = node.nodeValue?.length ?? 0;
    if (!started && start < pos + length) {
      range.setStart(node, start - pos);
      started = true;
    }
    if (started && end <= pos + length) {
      range.setEnd(node, end - pos);
      return range;
    }
    pos += length;
  }
  return null;
}

/** "3 of 17", "No results", or "1 of 10,000+" past the limit. */
export function findCount(result: FindResult, current: number): string {
  const total = result.matches.length;
  if (total === 0) return 'No results';
  return `${current + 1} of ${total.toLocaleString('en-US')}${result.capped ? '+' : ''}`;
}
