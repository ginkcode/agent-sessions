import type { GroupNode, GroupNodeKind, SessionRef } from './types';
import { refsEqual } from './manage';

// Threshold: groups with at least this many children start collapsed.
export const DEFAULT_COLLAPSE_THRESHOLD = 8;

/**
 * Returns the node's kind, inferring it from fields when the backend did not
 * send one (older cache or mock data) so the UI never renders blank.
 */
export function nodeKind(node: GroupNode): GroupNodeKind {
  if (node.kind) return node.kind;
  if (node.agent && !node.cwd) return 'agent';
  if (node.cwd) return 'directory';
  return 'session';
}

/**
 * Computes the set of keys that should start collapsed: every session with
 * subagent children (topics stay hidden until the user opens them), plus
 * every 'directory' or 'agent' group whose child count meets the threshold.
 */
export function defaultCollapsedKeys(
  groups: GroupNode[],
  threshold = DEFAULT_COLLAPSE_THRESHOLD
): Set<string> {
  const out = new Set<string>();
  const walk = (nodes: GroupNode[]) => {
    for (const node of nodes) {
      const children = node.children ?? [];
      const min = nodeKind(node) === 'session' ? 1 : threshold;
      if (children.length > 0 && children.length >= min) {
        out.add(node.key);
      }
      walk(children);
    }
  };
  walk(groups);
  return out;
}

/**
 * True when the node is collapsed. A key in `manual` records a user toggle
 * and flips that node's default state.
 */
export function isCollapsed(
  key: string,
  manual: Set<string>,
  defaults: Set<string>
): boolean {
  return manual.has(key) !== defaults.has(key);
}

/** Returns the subset of `keys` that still exist somewhere in the tree. */
export function pruneKeys(keys: Set<string>, groups: GroupNode[]): Set<string> {
  const live = new Set<string>();
  const walk = (nodes: GroupNode[]) => {
    for (const node of nodes) {
      if (keys.has(node.key)) live.add(node.key);
      if (node.children) walk(node.children);
    }
  };
  walk(groups);
  return live;
}

/**
 * Picks the session to select after a tree node is clicked: a session node
 * selects itself when visible, otherwise the first visible session in the
 * list. Returns null when the list is empty.
 */
export function sessionToSelect(
  node: GroupNode,
  visible: { ref: SessionRef }[]
): SessionRef | null {
  const own = nodeKind(node) === 'session' ? node.sessions?.[0] : undefined;
  if (own && visible.some((s) => refsEqual(s.ref, own))) return own;
  return visible[0]?.ref ?? null;
}

/**
 * Hashes a remote host alias deterministically to an 8-character hex string using FNV-1a.
 */
export function hashHost(host: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < host.length; i++) {
    hash ^= host.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}

/**
 * Returns the storage namespace identifier for a target host.
 * Empty, null, or 'local' identifies the local machine.
 */
export function targetKey(host?: string): string {
  if (!host || host === 'local') {
    return 'local';
  }
  return hashHost(host);
}

export const BASE_COLLAPSED_STORAGE_KEY = 'agent-sessions:tree-collapsed';

/**
 * Returns the namespaced storage key for the given host.
 * Migrates legacy un-namespaced 'agent-sessions:tree-collapsed' to
 * 'agent-sessions:tree-collapsed:local' when visiting local.
 */
export function storageKeyForCollapsed(host?: string): string {
  const target = targetKey(host);
  const key = `${BASE_COLLAPSED_STORAGE_KEY}:${target}`;
  if (target === 'local' && typeof localStorage !== 'undefined') {
    try {
      const existing = localStorage.getItem(key);
      if (existing === null) {
        const legacy = localStorage.getItem(BASE_COLLAPSED_STORAGE_KEY);
        if (legacy !== null) {
          localStorage.setItem(key, legacy);
          localStorage.removeItem(BASE_COLLAPSED_STORAGE_KEY);
        }
      }
    } catch {
      // Storage access disabled or restricted
    }
  }
  return key;
}

export function loadCollapsedKeys(host?: string): Set<string> {
  if (typeof localStorage === 'undefined') return new Set();
  try {
    const key = storageKeyForCollapsed(host);
    const parsed = JSON.parse(localStorage.getItem(key) || '[]');
    if (Array.isArray(parsed)) {
      return new Set(parsed.filter((k): k is string => typeof k === 'string'));
    }
  } catch {
    // Ignore JSON parse errors and start from defaults
  }
  return new Set();
}

export function saveCollapsedKeys(keys: Set<string>, host?: string): void {
  if (typeof localStorage === 'undefined') return;
  try {
    const key = storageKeyForCollapsed(host);
    localStorage.setItem(key, JSON.stringify([...keys]));
  } catch {
    // Storage quota exceeded or disabled
  }
}

