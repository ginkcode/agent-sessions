import type { AgentID, CatalogChanged, SessionMeta, SessionRef } from './types';
import { isFullRefresh } from './catalog';
import { refKey } from './manage';

/**
 * How much earlier than its tab a session may say it was created: the tab's
 * start and the session's creation are stamped by different clocks, and
 * agents round their times.
 */
export const LINK_ALLOWANCE_MS = 5000;

/** A tab that started a fresh session and waits to learn which one. */
export interface PendingLink {
  id: string;
  agent: AgentID;
  dir: string;
  /** When the tab started, in Unix milliseconds. */
  startedAt: number;
}

/** A directory as written, without trailing separators (a root keeps its own). */
export function sameDirectory(a: string, b: string): boolean {
  const trim = (p: string) => p.replace(/(?<=[^/\\:])[/\\]+$/, '');
  return Boolean(a) && trim(a) === trim(b);
}

/**
 * Pairs tabs with the sessions they started: each tab, in start order, takes
 * the earliest session of its agent in its directory created after it
 * started (less the allowance), that no tab took before. Subagent sessions
 * are not started by tabs. taken holds the refKeys of sessions already linked.
 */
export function matchNewSessions(
  pending: PendingLink[],
  sessions: SessionMeta[],
  taken: Set<string>,
): Map<string, SessionMeta> {
  const out = new Map<string, SessionMeta>();
  const used = new Set(taken);
  const created = (s: SessionMeta) => Date.parse(s.createdAt);
  const candidates = sessions
    .filter((s) => !s.parentId && Number.isFinite(created(s)) && !used.has(refKey(s.ref)))
    .sort((a, b) => created(a) - created(b));
  for (const tab of [...pending].sort((a, b) => a.startedAt - b.startedAt)) {
    const match = candidates.find(
      (s) =>
        s.ref.agent === tab.agent &&
        !used.has(refKey(s.ref)) &&
        sameDirectory(s.cwd, tab.dir) &&
        created(s) >= tab.startedAt - LINK_ALLOWANCE_MS,
    );
    if (!match) continue;
    used.add(refKey(match.ref));
    out.set(tab.id, match);
  }
  return out;
}

/**
 * Whether a catalog change may hold a session a pending tab started: a
 * rebuilt catalog, or a changed session of one of their agents.
 */
export function mayHoldNewSession(event: CatalogChanged, agents: Set<AgentID>): boolean {
  if (isFullRefresh(event)) return true;
  return (event.changed ?? []).some((r) => agents.has(r.agent));
}

/** The linked refs a catalog change touches: changed (renamed) and removed. */
export function touchedLinks(event: CatalogChanged, linked: SessionRef[]): { changed: SessionRef[]; removed: SessionRef[] } {
  const keys = (refs: SessionRef[] | null) => new Set((refs ?? []).map(refKey));
  const changed = keys(event.changed);
  const removed = keys(event.removed);
  return {
    changed: linked.filter((r) => changed.has(refKey(r)) || isFullRefresh(event)),
    removed: linked.filter((r) => removed.has(refKey(r))),
  };
}
