import { api, subscribeConnectionState, subscribeDiscoveryStatus } from '../api';
import type { ConnectionState, DiscoveryStatus } from '../types';
import {
  discoveryOutcome,
  isNewerDiscovery,
  type DiscoveryAvailability,
  type DiscoveryOutcome,
} from '../discovery';

interface RouteID {
  host?: string;
  generation?: number;
}

/**
 * Startup discovery status of the serving route (Local, or one connection
 * generation of a remote host). Held status is dropped whenever the route or
 * connection phase changes: an automatic reconnect keeps the generation but
 * restarts the remote engine, whose lifetimes begin at 1 again.
 */
export class DiscoveryState {
  status = $state<DiscoveryStatus | null>(null);
  availability = $state<DiscoveryAvailability>('pending');

  private route: ConnectionState | null = null;
  // Bumped per route; replies for an older route are dropped.
  private epoch = 0;
  private connectionEvents = 0;
  private unsubscribers: (() => void)[] = [];
  private completed: () => void = () => {};

  get outcome(): DiscoveryOutcome {
    return discoveryOutcome(this.availability, this.status);
  }

  /**
   * Subscribes before any snapshot request, so an event published while the
   * snapshot is in flight is never missed and the newer of the two wins.
   * `onComplete` runs when this route's current lifetime reaches a terminal
   * state, including one first seen in a snapshot.
   */
  init(onComplete: () => void = () => {}): void {
    if (this.unsubscribers.length) return;
    this.completed = onComplete;
    this.unsubscribers = [
      subscribeDiscoveryStatus((event) => this.accept(event, event)),
      subscribeConnectionState((state) => {
        this.connectionEvents++;
        this.connectionChanged(state);
      }),
    ];
    const seen = this.connectionEvents;
    void api.connectionState().then(
      (state) => {
        // A connection event that arrived meanwhile is newer.
        if (this.unsubscribers.length && seen === this.connectionEvents && state) {
          this.connectionChanged(state);
        }
      },
      () => {
        if (this.unsubscribers.length && seen === this.connectionEvents && !this.route) {
          this.availability = 'unavailable';
        }
      }
    );
  }

  destroy(): void {
    for (const off of this.unsubscribers) off();
    this.unsubscribers = [];
    this.epoch++;
    this.connectionEvents++;
    this.route = null;
    this.status = null;
    this.availability = 'pending';
  }

  /** Applies a connection state; a changed route or phase refetches. */
  connectionChanged(state: ConnectionState): void {
    const old = this.route;
    if (old && old.phase === state.phase && old.host === state.host && old.generation === state.generation) {
      return;
    }
    this.route = { ...state };
    const epoch = ++this.epoch;
    this.status = null;
    if (!serving(state)) {
      // Nothing serves this host right now: no spinner, no invented result.
      this.availability = 'unavailable';
      return;
    }
    this.availability = 'pending';
    void this.fetch(epoch);
  }

  private async fetch(epoch: number): Promise<void> {
    try {
      const reply = await api.discoveryStatus();
      if (epoch !== this.epoch) return;
      if (!this.matches(reply)) {
        // Answered by another route; the connection event that follows
        // refetches. Until then, do not spin.
        if (!this.status) this.availability = 'unavailable';
        return;
      }
      if (reply.available && reply.status) this.accept(reply.status, reply);
      else if (!this.status) this.availability = 'unavailable';
    } catch {
      // An event already received outranks a failed snapshot.
      if (epoch === this.epoch && !this.status) this.availability = 'unavailable';
    }
  }

  private matches(id: RouteID): boolean {
    const route = this.route;
    if (!route || !serving(route)) return false;
    const local = route.phase === 'local';
    return (id.host ?? '') === (local ? '' : route.host ?? '') &&
      (id.generation ?? 0) === (local ? 0 : route.generation);
  }

  private accept(next: DiscoveryStatus, id: RouteID): void {
    if (!this.matches(id)) return;
    const held = this.status;
    if (!isNewerDiscovery(next, held)) return;
    this.status = copyStatus(next);
    this.availability = 'available';
    const finished = !held || held.lifetime !== next.lifetime || !held.complete;
    if (next.complete && finished) this.completed();
  }
}

function serving(state: ConnectionState): boolean {
  return state.phase === 'local' || (state.phase === 'connected' && Boolean(state.host));
}

// Events carry route identity besides the snapshot; keep only the snapshot.
function copyStatus(s: DiscoveryStatus): DiscoveryStatus {
  return {
    lifetime: s.lifetime,
    revision: s.revision,
    state: s.state,
    catalogUsable: s.catalogUsable,
    complete: s.complete,
    cacheMode: s.cacheMode,
    configuredProviders: s.configuredProviders,
    detectedProviders: s.detectedProviders,
    failedProviders: s.failedProviders,
    detectionComplete: s.detectionComplete,
  };
}

export const discovery = new DiscoveryState();
