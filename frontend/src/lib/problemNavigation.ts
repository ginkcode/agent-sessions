import type { AttributedCodeDiagnostic, CodeDiagnostic, CodeDiagnosticRange } from './codeDiagnostics';
import { diagnosticKey, liveDocumentEligibility, type LiveDocumentBasis } from './codeDiagnostics';

export interface DiagnosticDisplayBasis { dirty: boolean; revision?: string; live?: LiveDocumentBasis }

export const problemSeverities = ['error', 'warning', 'information', 'hint'] as const;
const compare = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
/** Path, severity, start/end, message, code: independent of event arrival order. */
export function orderedProblems(findings: AttributedCodeDiagnostic[], enabled: readonly string[]) {
  return findings.filter(f => enabled.includes(f.diagnostic.severity)).slice().sort((a, b) => {
    const x = a.diagnostic, y = b.diagnostic;
    return compare(x.path, y.path) || problemSeverities.indexOf(x.severity) - problemSeverities.indexOf(y.severity)
      || (x.range?.start.line ?? -1) - (y.range?.start.line ?? -1)
      || (x.range?.start.character ?? -1) - (y.range?.start.character ?? -1)
      || (x.range?.end.line ?? -1) - (y.range?.end.line ?? -1)
      || (x.range?.end.character ?? -1) - (y.range?.end.character ?? -1)
      || compare(x.message, y.message) || compare(x.code ?? '', y.code ?? '');
  });
}
export function stepProblem(findings: AttributedCodeDiagnostic[], selected: string | undefined, direction: number) {
  if (!findings.length) return undefined;
  const at = findings.findIndex(f => diagnosticKey(f.diagnostic) === selected);
  return findings[at < 0 ? (direction > 0 ? 0 : findings.length - 1) : (at + direction + findings.length) % findings.length];
}
export function problemKey(event: Pick<KeyboardEvent, 'key' | 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'preventDefault'>, step: (direction: number) => void): boolean {
  if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return false;
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return false;
  event.preventDefault(); step(event.key === 'ArrowDown' ? 1 : -1); return true;
}
export function problemPath(path: string): boolean {
  return !!path && !/[\\\0:\r\n]/.test(path) && !path.split('/').some(p => !p || p === '.' || p === '..');
}
/** JS strings, DOM text offsets and CodeMirror offsets all count UTF-16 units.
 * CRLF is one line break; never silently clamp an invalid saved-file range. */
export function rangeOffsets(text: string, range: CodeDiagnosticRange): { anchor: number; head: number } | undefined {
  if (!range?.start || !range?.end) return undefined;
  const lines = text.split(/\r\n|\n/);
  const offset = (p: { line: number; character: number }) => {
    if (!Number.isSafeInteger(p.line) || !Number.isSafeInteger(p.character) || p.line < 0 || p.line >= lines.length || p.character < 0 || p.character > lines[p.line].length) return undefined;
    // Reject a boundary inside a surrogate pair.
    const line = lines[p.line];
    if (p.character > 0 && /[\uD800-\uDBFF]/.test(line[p.character - 1]) && /[\uDC00-\uDFFF]/.test(line[p.character] ?? '')) return undefined;
    let start = 0;
    const breaks = /\r\n|\n/g;
    for (let n = 0; n < p.line; n++) { const match = breaks.exec(text)!; start = match.index + match[0].length; }
    return start + p.character;
  };
  const anchor = offset(range.start), head = offset(range.end);
  return anchor === undefined || head === undefined || head < anchor ? undefined : { anchor, head };
}
/**
 * Why a finding's position cannot be drawn over the displayed text.
 * `fileLevel` is about the whole file (no range, or a build-constraint exclusion
 * reported at 1:1): it is never called stale just because it has no position.
 * Only `staleDocument`/`staleSaved` mean the result predates the displayed text.
 */
export type LocationKind = 'shown' | 'fileLevel' | 'invalidRange' | 'draft' | 'staleDocument' | 'staleSaved' | 'unverified';
export interface ProblemLocationStatus { kind: LocationKind; notice: string }

const buildExclusion = /build constraints? exclude|excluded by build (tags|constraints)|no packages found for open file/i;
/** Whole-file findings: no range, or a zero-width 1:1 build-tag exclusion. Wording only, never freshness. */
export function fileLevelFinding(d: CodeDiagnostic): boolean {
  if (!d.range) return true;
  const { start, end } = d.range;
  return start?.line === 0 && start.character === 0 && end?.line === 0 && end.character === 0 && buildExclusion.test(d.message);
}

export function problemLocationStatus(d: CodeDiagnostic, saved: DiagnosticDisplayBasis): ProblemLocationStatus {
  if (fileLevelFinding(d)) return { kind: 'fileLevel', notice: buildExclusion.test(d.message) ? 'File-level finding: build constraints exclude this file.' : 'File-level finding (no line position).' };
  if (d.locationIssue) return { kind: 'invalidRange', notice: 'The checker reported an invalid position; shown in the list only.' };
  if (d.basis.kind === 'document') {
    if (!saved.live) return { kind: 'staleDocument', notice: 'From a live session that is no longer open for this tab; position not shown.' };
    const eligibility = liveDocumentEligibility(d, saved.live);
    if (eligibility === 'current') return { kind: 'shown', notice: '' };
    if (eligibility === 'hidden') return { kind: 'staleDocument', notice: 'From an older version of this file; waiting for a newer result.' };
    if (d.freshness === 'stale') return { kind: 'staleDocument', notice: 'Stale: live sync stopped after this result; position not shown.' };
    return { kind: 'unverified', notice: 'Waiting for the language server to confirm the current text; position not shown yet.' };
  }
  if (saved.dirty) return { kind: 'draft', notice: 'Position is from the saved file and may not match your unsaved draft.' };
  if (d.freshness === 'stale') return { kind: 'staleSaved', notice: 'Stale: the file or checker changed since this result; position not shown.' };
  if (d.freshness !== 'current' || !saved.revision || d.basis.revision !== saved.revision) return { kind: 'unverified', notice: 'Position unverified: the saved file may have changed since this result.' };
  return { kind: 'shown', notice: '' };
}
export function problemLocation(d: CodeDiagnostic, saved: DiagnosticDisplayBasis): string {
  return problemLocationStatus(d, saved).notice;
}

/** Separate from the line-only blame selection. Highlighted spans are text-walked,
 * so syntax markup and gutter numbers never contribute to UTF-16 columns. */
export function revealSourceRange(root: HTMLElement, range: CodeDiagnosticRange): boolean {
  const doc = root.ownerDocument;
  function point(line: number, column: number): [Node, number] | undefined {
    const el = root.querySelector<HTMLElement>(`[data-source-line="${line + 1}"]`);
    if (!el) return undefined;
    const walker = doc.createTreeWalker(el, 4 /* SHOW_TEXT */);
    let node: Node | null;
    while ((node = walker.nextNode())) {
      const length = node.textContent?.length ?? 0;
      if (column <= length) return [node, column];
      column -= length;
    }
    return column === 0 ? [el, el.childNodes.length] : undefined;
  }
  const start = point(range.start.line, range.start.character), end = point(range.end.line, range.end.character);
  if (!start || !end) return false;
  const selection = doc.getSelection();
  const selected = doc.createRange(); selected.setStart(...start); selected.setEnd(...end);
  selection?.removeAllRanges(); selection?.addRange(selected);
  root.querySelector<HTMLElement>(`[data-source-line="${range.start.line + 1}"]`)?.scrollIntoView({ block: 'center' });
  return true;
}

export interface ProblemNavigationIdentity { dir: string | null; host: string | undefined; version: string; context: string }
export interface ProblemNavigationRequest {
  id: number; identity: ProblemNavigationIdentity; tab: object; path: string; diagnostic: CodeDiagnostic;
}
export function matchesProblemNavigation(request: ProblemNavigationRequest, latest: ProblemNavigationRequest | null, identity: ProblemNavigationIdentity, tab: object | undefined, path: string | null): boolean {
  return request === latest && request.tab === tab && request.path === path
    && request.identity.dir === identity.dir && request.identity.host === identity.host
    && request.identity.version === identity.version && request.identity.context === identity.context;
}
