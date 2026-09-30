import type { ConnectionPhase } from '../types';
import { bumpEpoch, isStale, nextLink } from '../link';

/**
 * Which backend the loaded data came from and whether it is still live. It
 * lives apart from the connection store so data stores can gate mutations
 * without importing the store that resets them.
 */
export class DataLink {
  dataHost = $state<string | undefined>(undefined);
  phase = $state<ConnectionPhase>('local');

  get stale(): boolean {
    return isStale({ dataHost: this.dataHost, phase: this.phase });
  }

  /** Why a mutation is refused right now, or null when it may run. */
  get blockedReason(): string | null {
    return this.stale ? `Not connected to ${this.dataHost}. Changes are disabled until it reconnects.` : null;
  }

  /** Seeds the link from the state the backend reports at startup. */
  seed(phase: ConnectionPhase, host: string | undefined): void {
    this.phase = phase;
    this.dataHost = phase === 'local' ? undefined : host;
  }

  /** Applies a connection update; true when the stores must reload. */
  update(phase: ConnectionPhase, host: string | undefined): boolean {
    const next = nextLink({ dataHost: this.dataHost, phase: this.phase }, phase, host);
    this.phase = phase;
    this.dataHost = next.dataHost;
    // Replies to calls made before a reload must not land in the new data.
    if (next.reload) bumpEpoch();
    return next.reload;
  }
}

export const link = new DataLink();
