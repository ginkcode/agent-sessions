import type { ConnectionPhase } from './types';

// Bumped each time the stores reset for a new backend.
let currentEpoch = 0;

export function epoch(): number {
  return currentEpoch;
}

export function bumpEpoch(): void {
  currentEpoch++;
}

/**
 * Rejects a reply to a call made before the stores last reset for a new
 * backend. The stores already hold the new backend's data, so the reply
 * must not be applied.
 */
export class StaleReplyError extends Error {
  constructor() {
    super('The connection changed before this call finished.');
    this.name = 'StaleReplyError';
  }
}

export function isStaleReply(err: unknown): boolean {
  return err instanceof StaleReplyError;
}

/** Matches rpc.ErrDisconnected as it crosses the Wails bridge. */
export function isDisconnectedError(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err ?? '');
  return msg.includes('disconnected from remote host');
}

/**
 * Wraps a backend so a reply is rejected with StaleReplyError when `epoch()`
 * moved while the call was in flight. Methods in `passthrough` (connection
 * control, events) are never guarded.
 */
export function guardEpoch<T extends object>(
  backend: T,
  epoch: () => number,
  passthrough: ReadonlySet<string>
): T {
  return new Proxy(backend, {
    get(target, prop, receiver) {
      const value = Reflect.get(target, prop, receiver);
      if (typeof value !== 'function' || typeof prop !== 'string' || passthrough.has(prop)) {
        return value;
      }
      return (...args: unknown[]) => {
        const started = epoch();
        const out = value.apply(target, args);
        if (!(out instanceof Promise)) return out;
        return out.then(
          (v) => {
            if (epoch() !== started) throw new StaleReplyError();
            return v;
          },
          (e) => {
            if (epoch() !== started) throw new StaleReplyError();
            throw e;
          }
        );
      };
    },
  });
}

export interface LinkState {
  /** Host whose data the stores hold; undefined is Local. */
  dataHost: string | undefined;
  phase: ConnectionPhase;
}

/**
 * Decides what a connection update means for the loaded data. Stores reload
 * only when a host (re)connects or the app returns to Local. While a first
 * connect from Local runs, Local keeps serving; while a host is dropped or
 * the user switches to another host, the last host's data stays up as stale.
 */
export function nextLink(
  prev: LinkState,
  phase: ConnectionPhase,
  host: string | undefined
): { dataHost: string | undefined; reload: boolean } {
  if (phase === 'connected' && host) {
    const reload = prev.phase !== 'connected' || prev.dataHost !== host;
    return { dataHost: host, reload };
  }
  if (phase === 'local') {
    // Local kept serving through a connect that never came up.
    return { dataHost: undefined, reload: prev.dataHost !== undefined };
  }
  return { dataHost: prev.dataHost, reload: false };
}

/** The loaded data belongs to a host that is not serving it right now. */
export function isStale(link: LinkState): boolean {
  return link.dataHost !== undefined && link.phase !== 'connected';
}
