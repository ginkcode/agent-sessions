import type {
  DeletePreview,
  DeleteResult,
  SessionMeta,
  SessionRef,
} from './types';

/**
 * Returns a stable unique string for a SessionRef.
 */
export function refKey(ref: SessionRef): string {
  return `${ref.agent}:${ref.id}`;
}

/**
 * Extracts a displayable message from a rejected binding call. Wails rejects
 * with the Go error string, not an Error object.
 */
export function errorText(err: unknown, fallback: string): string {
  if (typeof err === 'string' && err) return err;
  if (err instanceof Error && err.message) return err.message;
  return fallback;
}

/**
 * Checks equality of two session refs.
 */
export function refsEqual(
  a: SessionRef | null | undefined,
  b: SessionRef | null | undefined
): boolean {
  if (!a || !b) return a === b;
  return a.agent === b.agent && a.id === b.id;
}

/**
 * Returns the next session to select after deleting one or more sessions.
 * Preserves the previous list index position if possible; falls back to the
 * first remaining session, or null if the list becomes empty.
 */
export function nextSelectionAfterDelete(
  currentSessions: SessionMeta[],
  deletedRefs: SessionRef[],
  currentSelected: SessionRef | null
): SessionRef | null {
  if (!currentSessions.length) return null;
  const deletedSet = new Set(deletedRefs.map(refKey));

  // If the current selection was not deleted, keep it
  if (currentSelected && !deletedSet.has(refKey(currentSelected))) {
    return currentSelected;
  }

  // Find index of currently selected session in the pre-delete list
  const selectedIndex = currentSelected
    ? currentSessions.findIndex((s) => refsEqual(s.ref, currentSelected))
    : -1;

  const remaining = currentSessions.filter((s) => !deletedSet.has(refKey(s.ref)));
  if (!remaining.length) return null;

  if (selectedIndex < 0) {
    return remaining[0].ref;
  }

  // Try to pick the session that slid into selectedIndex, or the previous one
  const targetIndex = Math.min(selectedIndex, remaining.length - 1);
  return remaining[targetIndex]?.ref ?? remaining[0].ref;
}

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;

export type AgeFilter = 'any' | 'within-1h' | 'within-1d' | 'within-7d' | 'within-30d' | 'older-30d';

/**
 * Age filter choices for the session list, in milliseconds. "within" keeps
 * sessions updated in that window; "older" keeps those updated before it.
 */
export const AGE_FILTERS: readonly { id: AgeFilter; label: string; within?: number; older?: number }[] = [
  { id: 'any', label: 'Any age' },
  { id: 'within-1h', label: 'In 1 hour', within: HOUR_MS },
  { id: 'within-1d', label: 'In 1 day', within: DAY_MS },
  { id: 'within-7d', label: 'In 7 days', within: 7 * DAY_MS },
  { id: 'within-30d', label: 'In 30 days', within: 30 * DAY_MS },
  { id: 'older-30d', label: 'Older than 30 days', older: 30 * DAY_MS },
];

export function isAgeFilter(value: string): value is AgeFilter {
  return AGE_FILTERS.some((f) => f.id === value);
}

/**
 * Filter sessions by the age of their updated timestamp. "any" (or an
 * unknown id) returns every session.
 */
export function filterSessionsByAge(
  sessions: SessionMeta[],
  filter: AgeFilter,
  now: Date = new Date()
): SessionMeta[] {
  const rule = AGE_FILTERS.find((f) => f.id === filter);
  if (!rule || (rule.within === undefined && rule.older === undefined)) return sessions;
  const nowMs = now.getTime();
  return sessions.filter((s) => {
    const sessionTime = new Date(s.updatedAt || s.createdAt).getTime();
    if (Number.isNaN(sessionTime)) return false;
    const age = nowMs - sessionTime;
    return rule.within !== undefined ? age <= rule.within : age > rule.older!;
  });
}

/**
 * Summarizes the items in a delete preview for UI warnings.
 */
export function summarizePreview(preview: DeletePreview | null): {
  total: number;
  blockedCount: number;
  reversibleCount: number;
  permanentCount: number;
  canProceed: boolean;
  warnings: string[];
} {
  if (!preview || !preview.items.length) {
    return {
      total: 0,
      blockedCount: 0,
      reversibleCount: 0,
      permanentCount: 0,
      canProceed: false,
      warnings: [],
    };
  }

  let blockedCount = 0;
  let reversibleCount = 0;
  let permanentCount = 0;
  const warnings: string[] = [];

  // Blocked items are skipped, so only actionable ones count toward the
  // trash/permanent split and warnings.
  for (const item of preview.items) {
    if (item.blocked) {
      blockedCount++;
      continue;
    }
    if (item.reversible) {
      reversibleCount++;
    } else {
      permanentCount++;
    }
    if (item.warning && !warnings.includes(item.warning)) {
      warnings.push(item.warning);
    }
  }

  const canProceed = blockedCount < preview.items.length;

  return {
    total: preview.items.length,
    blockedCount,
    reversibleCount,
    permanentCount,
    canProceed,
    warnings,
  };
}

/**
 * True when any actionable item in the preview is deleted permanently (no
 * Trash copy), so the dialog warns that the action cannot be undone.
 */
export function isPermanentDelete(preview: DeletePreview | null): boolean {
  return Boolean(preview?.items.some((item) => !item.reversible && !item.blocked));
}

/**
 * Computes human-readable feedback from a DeleteResult.
 */
export function formatDeleteResultSummary(result: DeleteResult): string {
  const parts: string[] = [];
  if (result.deleted > 0) {
    parts.push(
      `${result.deleted} ${result.deleted === 1 ? 'session' : 'sessions'} removed`
    );
  }
  if (result.failed > 0) {
    parts.push(`${result.failed} failed`);
  }
  if (result.forgotten && result.forgotten.length > 0) {
    parts.push(`${result.forgotten.length} forgotten`);
  }
  return parts.join(', ') || 'No sessions modified';
}
