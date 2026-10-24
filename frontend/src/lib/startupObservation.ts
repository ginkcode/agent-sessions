// Opt-in, in-memory only. One frontend-local monotonic origin, never compared
// with backend durations. No host, path, filter, session or error content.
export type StartupPhase = 'groups_counts' | 'sessions' | 'usable_data';
export class StartupObservation {
  private readonly origin: number;
  private readonly seen = new Set<StartupPhase>();
  private readonly samples: { phase: StartupPhase; elapsedMs: number }[] = [];
  private readonly now: () => number;
  constructor(now: () => number = () => performance.now()) {
    this.now = now;
    this.origin = now();
  }
  // A successful empty response is not usable session data or discovery completion.
  data(phase: 'groups_counts' | 'sessions', nonempty: boolean): void {
    this.mark(phase);
    if (phase === 'sessions' && nonempty && this.seen.has('groups_counts')) this.mark('usable_data');
  }
  mark(phase: StartupPhase): void {
    if (this.seen.has(phase)) return;
    this.seen.add(phase);
    this.samples.push({ phase, elapsedMs: this.now() - this.origin });
  }
  snapshot(): { phase: StartupPhase; elapsedMs: number }[] {
    return this.samples.map((sample) => ({ ...sample }));
  }
}
