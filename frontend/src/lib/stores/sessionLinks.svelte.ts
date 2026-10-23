import { api, subscribeCatalogChanged } from '../api';
import type { AgentID, CatalogChanged, SessionMeta } from '../types';
import { refKey } from '../manage';
import { matchNewSessions, mayHoldNewSession, touchedLinks, type PendingLink } from '../sessionLinks';
import { link } from './link.svelte';
import { terminals } from './terminals.svelte';

/**
 * Links agent tabs to the sessions they run. A resumed session is known from
 * the start; a fresh one is found when the catalog reports it: the first
 * session of the tab's agent in its directory created after it started.
 * Only the shown host's catalog is seen, so its tabs alone link; others wait
 * until their host is shown again.
 */
export class SessionLinksStore {
  private unsubscribe: (() => void) | null = null;
  // Catalog changes apply one at a time, each seeing the links before it.
  private queue: Promise<void> = Promise.resolve();

  init(): void {
    if (this.unsubscribe) return;
    this.unsubscribe = subscribeCatalogChanged((event) => {
      this.queue = this.queue.then(() => this.apply(event)).catch(() => {});
    });
  }

  destroy(): void {
    this.unsubscribe?.();
    this.unsubscribe = null;
  }

  /** Applies one catalog change to the shown host's tabs. */
  async apply(event: CatalogChanged): Promise<void> {
    const host = link.dataHost ?? '';
    const tabs = terminals.tabs.filter((t) => t.host === host && t.status === 'running');
    const linked = tabs.flatMap((t) => (t.session ? [t.session] : []));
    const { changed, removed } = touchedLinks(event, linked);
    for (const ref of removed) terminals.unlinkSession(ref);
    await Promise.all(
      changed.map(async (ref) => {
        try {
          const meta = await api.getSessionMeta(ref);
          if ((link.dataHost ?? '') === host) terminals.sessionReported(ref, meta);
        } catch {
          // Gone or unreachable: a removal arrives as its own change.
        }
      }),
    );

    const pending: PendingLink[] = tabs.flatMap((t) =>
      t.linkPending && t.agent && t.dir && t.startedAt ? [{ id: t.id, agent: t.agent, dir: t.dir, startedAt: t.startedAt }] : [],
    );
    if (pending.length === 0 || !mayHoldNewSession(event, new Set(pending.map((p) => p.agent)))) return;
    await this.linkPending(host, pending);
  }

  /** Looks for the sessions pending tabs started, one query per agent and directory. */
  private async linkPending(host: string, pending: PendingLink[]): Promise<void> {
    const queries = new Map<string, { agent: AgentID; dir: string }>();
    for (const p of pending) queries.set(`${p.agent}\n${p.dir}`, { agent: p.agent, dir: p.dir });
    let sessions: SessionMeta[] = [];
    try {
      const lists = await Promise.all(
        [...queries.values()].map((q) =>
          api.listSessions('', { agent: q.agent, path: q.dir }, { field: 'createdAt', desc: false }),
        ),
      );
      sessions = lists.flat();
    } catch {
      // The next change tries again.
      return;
    }
    if ((link.dataHost ?? '') !== host) return;
    // Sessions another tab of this host runs are taken, resumed ones too.
    const taken = new Set(
      terminals.tabs.flatMap((t) => (t.host === host && t.session ? [refKey(t.session)] : [])),
    );
    const live = new Set(terminals.tabs.filter((t) => t.linkPending).map((t) => t.id));
    const matches = matchNewSessions(
      pending.filter((p) => live.has(p.id)),
      sessions,
      taken,
    );
    for (const [id, meta] of matches) terminals.linkSession(id, meta.ref, meta.title, meta.turn);
  }
}

export const sessionLinks = new SessionLinksStore();
