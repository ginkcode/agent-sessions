import type { CatalogChanged, GroupNode, SessionMeta, SessionRef } from './types';
import { refKey } from './manage';

/** A rebuilt catalog: groupsDirty with no refs means every view reloads. */
export function isFullRefresh(event: CatalogChanged): boolean {
  return event.groupsDirty && !event.changed?.length && !event.removed?.length;
}

/** True when `target` is non-null and listed in `refs` (by agent + id). */
export function hasRef(
  refs: SessionRef[] | null | undefined,
  target: SessionRef | null | undefined
): boolean {
  if (!target || !refs) return false;
  return refs.some((r) => r.agent === target.agent && r.id === target.id);
}

/** Finds the node with `key` anywhere in the tree. */
export function findGroup(groups: GroupNode[], key: string): GroupNode | null {
  for (const node of groups) {
    if (node.key === key) return node;
    const found = node.children ? findGroup(node.children, key) : null;
    if (found) return found;
  }
  return null;
}

/**
 * Collects the session refs a list for `groupKey` would contain, according
 * to `groups`. A null key means the whole tree (the "all sessions" list).
 */
export function groupSessionKeys(groups: GroupNode[], groupKey: string | null): Set<string> {
  const out = new Set<string>();
  const nodes = groupKey ? [findGroup(groups, groupKey)].filter((n): n is GroupNode => !!n) : groups;
  const walk = (ns: GroupNode[]) => {
    for (const node of ns) {
      for (const ref of node.sessions ?? []) out.add(refKey(ref));
      if (node.children) walk(node.children);
    }
  };
  walk(nodes);
  return out;
}

/**
 * Reports whether the visible session list must reload. A ref matters when
 * it is already listed (updated or removed) or when the fresh tree places it
 * in the selected group (a new or moved session).
 */
export function affectsSessionList(
  event: CatalogChanged,
  currentSessions: SessionMeta[],
  groups: GroupNode[],
  groupKey: string | null
): boolean {
  if (isFullRefresh(event)) return true;
  const refs = [...(event.changed ?? []), ...(event.removed ?? [])];
  if (!refs.length) return false;
  const relevant = groupSessionKeys(groups, groupKey);
  for (const s of currentSessions) relevant.add(refKey(s.ref));
  return refs.some((r) => relevant.has(refKey(r)));
}

/**
 * Hands out increasing request ids so a response can tell whether a newer
 * request superseded it and must be dropped.
 */
export class RequestSequence {
  private current = 0;

  next(): number {
    return ++this.current;
  }

  isCurrent(id: number): boolean {
    return id === this.current;
  }
}
