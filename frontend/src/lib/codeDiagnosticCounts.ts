import type { AttributedCodeDiagnostic } from './codeDiagnostics';

export interface CodeDiagnosticCounts {
  error: number;
  warning: number;
  stale: boolean;
  uncertain: boolean;
}
export const emptyCodeDiagnosticCounts = (): CodeDiagnosticCounts => ({ error: 0, warning: 0, stale: false, uncertain: false });

/** Input is the canonical exact-equivalence grouping, never a filtered panel or listing. */
export function aggregateCodeDiagnostics(findings: readonly AttributedCodeDiagnostic[]) {
  const files = new Map<string, CodeDiagnosticCounts>();
  const folders = new Map<string, CodeDiagnosticCounts>();
  const total = emptyCodeDiagnosticCounts();
  const add = (to: CodeDiagnosticCounts, from: CodeDiagnosticCounts) => {
    to.error += from.error; to.warning += from.warning;
    to.stale ||= from.stale; to.uncertain ||= from.uncertain;
  };
  for (const finding of findings) {
    const { path, severity } = finding.diagnostic;
    if (severity !== 'error' && severity !== 'warning') continue;
    const counts = files.get(path) ?? emptyCodeDiagnosticCounts();
    counts[severity]++;
    counts.stale ||= finding.entries.some(e => e.diagnostic.freshness === 'stale');
    counts.uncertain ||= finding.entries.some(e => e.diagnostic.freshness === 'uncertain');
    files.set(path, counts);
  }
  // Each file contributes once to each component ancestor, never child folder totals.
  for (const [path, counts] of files) {
    add(total, counts);
    const parts = path.split('/');
    parts.pop();
    while (parts.length) {
      const folder = parts.join('/');
      const aggregate = folders.get(folder) ?? emptyCodeDiagnosticCounts();
      add(aggregate, counts); folders.set(folder, aggregate);
      parts.pop();
    }
  }
  return { files, folders, total };
}

export function codeDiagnosticCountsLabel(counts: CodeDiagnosticCounts, context = '') {
  return `${counts.error} errors, ${counts.warning} warnings${counts.stale ? ' · stale retained findings' : ''}${counts.uncertain ? ' · freshness uncertain' : ''}${context ? ` · ${context}` : ''}`;
}
