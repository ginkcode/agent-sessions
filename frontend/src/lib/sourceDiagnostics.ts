import type { AttributedCodeDiagnostic } from './codeDiagnostics';
import { fileLevelFinding, problemLocation, problemLocationStatus, rangeOffsets, type DiagnosticDisplayBasis, type LocationKind } from './problemNavigation';

export interface SourceDiagnosticSegment { line: number; start: number; end: number; finding: number }
/** `kind` lets the list tell file-level findings apart from results that are actually stale. */
export interface SourceDiagnosticMessage { finding: AttributedCodeDiagnostic; notice: string; kind?: LocationKind }
export interface SourceDiagnosticProjection { messages: SourceDiagnosticMessage[]; segments: SourceDiagnosticSegment[]; text?: string }

/** Project verified saved or current versioned live ranges. Ends are exclusive; zero-width and empty
 * lines retain a gutter location without inventing source characters. */
export function projectSourceDiagnostics(text: string, findings: AttributedCodeDiagnostic[], saved: DiagnosticDisplayBasis, current = true): SourceDiagnosticProjection {
  const lines = text.split(/\r\n|\n/);
  const segments: SourceDiagnosticSegment[] = [];
  const messages = findings.map((f): SourceDiagnosticMessage => {
    const eligible = current && f.entries.find(e => !problemLocation(e.diagnostic, saved));
    const finding = eligible ? { ...f, diagnostic: eligible.diagnostic } : f;
    const d = finding.diagnostic;
    // A whole-file finding stays file-level whatever the attachment state; it never had a position.
    if (!current && !fileLevelFinding(d)) return { finding, kind: 'unverified', notice: 'Code checks are reconnecting or updating; position not shown until results are current.' };
    let { kind, notice } = problemLocationStatus(d, saved);
    if (!notice && (!d.range || !rangeOffsets(text, d.range))) { kind = 'invalidRange'; notice = 'Position is outside the displayed text; shown in the list only.'; }
    return { finding, notice, kind };
  });
  messages.forEach(({ finding, notice }, findingIndex) => {
    if (notice) return;
    const range = finding.diagnostic.range!;
    for (let line = range.start.line; line <= range.end.line; line++) {
      const start = line === range.start.line ? range.start.character : 0;
      const end = line === range.end.line ? range.end.character : lines[line].length;
      // An exclusive end at column zero belongs only to the preceding line.
      if (line === range.end.line && end === 0 && line !== range.start.line) break;
      segments.push({ line, start, end, finding: findingIndex });
    }
  });
  return { messages, segments, text };
}

/** CSS Highlights paint DOM text ranges without changing syntax tokens, find
 * offsets or native selection. Unsupported WebViews retain gutters/messages. */
export function paintSourceDiagnostics(root: HTMLElement, projection: SourceDiagnosticProjection, prefix: string): () => void {
  const view = root.ownerDocument.defaultView as (Window & { Highlight?: new (...ranges: Range[]) => unknown; CSS?: { highlights?: Map<string, unknown> } }) | null;
  const registry = view?.CSS?.highlights;
  const HighlightClass = view?.Highlight;
  if (!registry || !HighlightClass) return () => {};
  const names: string[] = [];
  const expectedLines = projection.text?.split('\n');
  for (const severity of ['error', 'warning', 'information', 'hint']) {
    const ranges: Range[] = [];
    for (const segment of projection.segments) {
      if (projection.messages[segment.finding].finding.diagnostic.severity !== severity || segment.start === segment.end) continue;
      const el = root.querySelector<HTMLElement>(`[data-source-line="${segment.line + 1}"]`);
      if (!el) continue;
      const expected = expectedLines?.[segment.line];
      if (expected !== undefined && el.textContent !== expected) continue;
      const walker = root.ownerDocument.createTreeWalker(el, 4);
      const nodes: Node[] = [];
      let node: Node | null;
      while ((node = walker.nextNode())) nodes.push(node);
      const point = (offset: number): [Node, number] | undefined => {
        for (const n of nodes) {
          const length = n.textContent?.length ?? 0;
          if (offset <= length) return [n, offset];
          offset -= length;
        }
      };
      const start = point(segment.start), end = point(segment.end);
      if (!start || !end) continue;
      const range = root.ownerDocument.createRange();
      range.setStart(...start); range.setEnd(...end); ranges.push(range);
    }
    const name = `${prefix}-${severity}`;
    names.push(name); registry.set(name, new HighlightClass(...ranges));
  }
  return () => { for (const name of names) registry.delete(name); };
}
