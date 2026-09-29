/**
 * Reports whether text in a wrapping monospace box spans at least minLines
 * visual lines, given at most maxCols characters fit on one line. It never
 * overcounts: tabs, wide glyphs and a narrower box only add lines, so a true
 * result is safe to rely on.
 */
export function reachesLineCount(text: string, minLines: number, maxCols: number): boolean {
  let lines = 0;
  let start = 0;
  // A trailing newline does not start a visible line, hence `<`.
  while (start < text.length) {
    let end = text.indexOf('\n', start);
    if (end < 0) end = text.length;
    lines += Math.max(1, Math.ceil((end - start) / maxCols));
    if (lines >= minLines) return true;
    start = end + 1;
  }
  return false;
}
