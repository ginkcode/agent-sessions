import type { AttributedCodeDiagnostic, CodeDiagnosticsProvider } from './codeDiagnostics';

/** The store fields the compact status reads. Every field may be absent in fixtures. */
export interface CodeCheckSummaryInput {
  status?: 'detached' | 'attaching' | 'ready' | 'unsupported' | 'resyncing' | 'disconnected' | 'error';
  revoking?: boolean;
  running?: boolean;
  partial?: boolean;
  truncated?: boolean;
  snapshot?: { approval?: { state?: string }; proposal?: { fingerprint?: string } };
  providers?: CodeDiagnosticsProvider[];
  findings?: AttributedCodeDiagnostic[];
}

/** 'ok': approved checks are running and report nothing. */
export type CodeCheckTone = 'error' | 'warning' | 'info' | 'attention' | 'ok' | 'muted';

export interface CodeCheckSummary {
  /** Short visible label for the bottom status button. */
  text: string;
  /** Tooltip: scope and coverage caveats. Never host identities. */
  title: string;
  tone: CodeCheckTone;
  /** Whether project approval or setup needs the user's attention. */
  needsSettings: boolean;
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

/** Counts every reported severity. */
export function problemCounts(findings: readonly AttributedCodeDiagnostic[] = []) {
  let error = 0, warning = 0, other = 0;
  for (const f of findings) {
    if (f.diagnostic.severity === 'error') error++;
    else if (f.diagnostic.severity === 'warning') warning++;
    else other++;
  }
  return { error, warning, other, total: findings.length };
}

/**
 * Clicking the status toggles the details. When there is nothing to list and
 * approval or setup needs attention, opening also reveals the settings, which
 * still require their own explicit button presses. Collapsing hides both.
 */
export function toggleDisclosure(state: { expanded: boolean; settingsOpen: boolean }, summary: Pick<CodeCheckSummary, 'needsSettings'>, findings: number) {
  if (state.expanded) return { expanded: false, settingsOpen: false };
  return { expanded: true, settingsOpen: state.settingsOpen || (!findings && summary.needsSettings) };
}

/**
 * Reported findings of any severity per file, and the folders above them. Feeds
 * the red file names in the tree and search; independent of listing/expansion.
 */
export function issueCounts(findings: readonly AttributedCodeDiagnostic[] = []) {
  const files = new Map<string, number>();
  const folders = new Set<string>();
  for (const f of findings) {
    const path = f.diagnostic.path;
    files.set(path, (files.get(path) ?? 0) + 1);
  }
  for (const path of files.keys()) {
    const parts = path.split('/');
    for (parts.pop(); parts.length; parts.pop()) folders.add(parts.join('/'));
  }
  return { files, folders };
}

/**
 * One concise status line. It never claims a clean project when checks are off,
 * unavailable, awaiting approval or disconnected: "No problems reported" only
 * appears for an approved, ready attachment with an available checker.
 */
export function codeCheckSummary(c: CodeCheckSummaryInput): CodeCheckSummary {
  const counts = problemCounts(c.findings);
  const parts = [
    counts.error ? plural(counts.error, 'error') : '',
    counts.warning ? plural(counts.warning, 'warning') : '',
    counts.other ? `${counts.other} info` : '',
  ].filter(Boolean);
  const problems = parts.join(' · ');
  const has = counts.total > 0;
  const tone: CodeCheckTone = counts.error ? 'error' : counts.warning ? 'warning' : counts.other ? 'info' : 'muted';
  const notes = ['Reported findings for this project only; not a complete-coverage guarantee.'];
  if (has) notes.push('Counts include files outside the current listing and search.');
  if (c.partial) notes.push('Partial coverage.');
  if (c.truncated) notes.push('Results were truncated.');
  const done = (text: string, extra: Partial<CodeCheckSummary> = {}): CodeCheckSummary =>
    ({ text, tone, needsSettings: false, ...extra, title: [...notes, ...(extra.title ? [extra.title] : []), 'Click for details.'].join(' ') });

  switch (c.status) {
    case undefined:
    case 'detached': return done(has ? problems : 'Code checks off', { tone: has ? tone : 'muted' });
    case 'attaching': return done('Code checks starting…', { tone: 'muted' });
    case 'unsupported': return done(has ? `${problems} · stale` : 'Code checks unavailable', { tone: has ? tone : 'muted', title: 'Code checks are not supported on this connection.' });
    case 'error': return done(has ? `${problems} · status uncertain` : 'Code checks unavailable', { tone: has ? tone : 'muted', needsSettings: true, title: 'Status is uncertain; retained results are not a successful check.' });
    case 'disconnected': return done(has ? `${problems} · stale` : 'Code checks disconnected', { tone: has ? tone : 'muted', title: 'Disconnected; retained results are stale.' });
    case 'resyncing': return done(has ? `${problems} · updating…` : 'Code checks updating…', { tone: has ? tone : 'muted', title: 'Reconciling; retained results are stale.' });
  }
  if (c.revoking) return done('Code checks turning off…', { tone: 'muted' });
  if (c.snapshot?.approval?.state !== 'approved' && !c.snapshot?.proposal?.fingerprint && (c.providers ?? []).length) {
    return done(has ? `${problems} · no checker found` : 'Code checks: no checker found', { tone: has ? tone : 'muted', needsSettings: !has, title: 'No supported checker was found on this host; see the details.' });
  }
  if (c.snapshot?.approval?.state !== 'approved') {
    return done(has ? `${problems} · approval needed` : 'Code checks: approval needed', { tone: has ? tone : 'attention', needsSettings: true, title: 'Nothing runs until you approve this project in the details.' });
  }
  if (c.running) return done(has ? `Checking… · ${problems}` : 'Checking…');
  if (!(c.providers ?? []).some(p => p.availability === 'available')) {
    return done(has ? problems : 'No checker available', { tone: has ? tone : 'muted', needsSettings: !has });
  }
  if (has) return done(c.partial ? `${problems} · partial` : problems);
  return done(c.partial ? 'No problems reported (partial)' : 'No problems reported', { tone: 'ok' });
}
