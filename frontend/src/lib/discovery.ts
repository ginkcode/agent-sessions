// Startup discovery presentation. Catalog rows, request flags and FTS
// progress are not discovery readiness; only the engine status is.
import type { DiscoveryStatus } from './types';

export type DiscoveryAvailability = 'pending' | 'available' | 'unavailable';

/** Where discovery stands for the serving route, as far as the UI knows. */
export type DiscoveryOutcome =
  // No status yet for this route: the snapshot or first event is in flight.
  | 'waiting'
  // An attempt is restoring or reconciling; rows may still change.
  | 'reconciling'
  | 'settled'
  | 'degraded'
  | 'failed'
  // Older host, backend without status, failed snapshot or offline route:
  // presentation stays as it was before discovery status existed.
  | 'unavailable';

/** What an empty tree or list says. */
export type EmptyCatalogState =
  | 'no-matches'
  | 'legacy'
  | 'discovering'
  | 'no-agents'
  | 'no-sessions'
  | 'degraded'
  | 'failed';

export interface CatalogNotice {
  tone: 'info' | 'warning';
  text: string;
  title: string;
}

/** Within one route, (lifetime, revision) orders snapshots and events. */
export function isNewerDiscovery(next: DiscoveryStatus, held: DiscoveryStatus | null): boolean {
  if (!held) return true;
  return next.lifetime > held.lifetime || (next.lifetime === held.lifetime && next.revision > held.revision);
}

export function discoveryOutcome(
  availability: DiscoveryAvailability,
  status: DiscoveryStatus | null
): DiscoveryOutcome {
  if (status) {
    if (!status.complete) return 'reconciling';
    if (status.state === 'settled') return 'settled';
    return status.state === 'failed' ? 'failed' : 'degraded';
  }
  return availability === 'unavailable' ? 'unavailable' : 'waiting';
}

/** "No Agents Detected" needs actual, complete, failure-free detection. */
export function noAgentsDetected(status: DiscoveryStatus | null): boolean {
  return Boolean(
    status?.complete &&
      status.detectionComplete &&
      status.detectedProviders === 0 &&
      status.failedProviders === 0
  );
}

/**
 * The state of a catalog view that shows no rows. `narrowed` means a filter,
 * search or group selection hid rows; `reloading` means a catalog reload
 * queued by completed discovery has not applied yet.
 */
export function emptyCatalogState(
  outcome: DiscoveryOutcome,
  status: DiscoveryStatus | null,
  narrowed: boolean,
  reloading = false
): EmptyCatalogState {
  if (outcome === 'unavailable') return narrowed ? 'no-matches' : 'legacy';
  if (narrowed && status?.catalogUsable && !reloading) return 'no-matches';
  if (outcome === 'waiting' || outcome === 'reconciling' || reloading) return 'discovering';
  if (outcome === 'failed' || outcome === 'degraded') return outcome;
  if (narrowed) return 'no-matches';
  return noAgentsDetected(status) ? 'no-agents' : 'no-sessions';
}

function providers(n: number): string {
  return `${n} ${n === 1 ? 'agent provider' : 'agent providers'}`;
}

/** A short line beside rows that are shown while discovery is not clean. */
export function catalogNotice(outcome: DiscoveryOutcome, status: DiscoveryStatus | null): CatalogNotice | null {
  if (outcome === 'waiting' || outcome === 'reconciling') {
    return {
      tone: 'info',
      text: 'Updating…',
      title: 'Checking agent session history; sessions may still be added or removed.',
    };
  }
  if (outcome === 'degraded') {
    const failed = status?.failedProviders ?? 0;
    return {
      tone: 'warning',
      text: 'Discovery incomplete',
      title: failed > 0
        ? `${providers(failed)} could not be read; shown sessions may be incomplete.`
        : 'Not every agent provider could be checked; shown sessions may be incomplete.',
    };
  }
  if (outcome === 'failed') {
    return {
      tone: 'warning',
      text: 'Discovery failed',
      title: 'Agent session history could not be read; shown sessions may be outdated or incomplete.',
    };
  }
  return null;
}

/** Title and description for a failed or degraded empty view. */
export function discoveryProblem(state: 'failed' | 'degraded', status: DiscoveryStatus | null): { title: string; description: string } {
  if (state === 'failed') {
    return {
      title: 'Session discovery failed',
      description: 'Agent session history could not be read, so no sessions can be shown.',
    };
  }
  const failed = status?.failedProviders ?? 0;
  return {
    title: 'Session discovery incomplete',
    description: failed > 0
      ? `No sessions were found, but ${providers(failed)} could not be read.`
      : 'No sessions were found, but not every agent provider could be checked.',
  };
}
