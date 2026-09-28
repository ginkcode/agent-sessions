import type { GroupNode, GroupNodeKind } from './types';

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
