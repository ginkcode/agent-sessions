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

/**
 * Filter sessions by their updated timestamp age in days.
 * An ageDays of 0 means "all / no filter".
 */
export function filterSessionsByAge(
  sessions: SessionMeta[],
  ageDays: number,
  now: Date = new Date()
): SessionMeta[] {
  if (!ageDays || ageDays <= 0) return sessions;
  const cutoffMs = now.getTime() - ageDays * 24 * 60 * 60 * 1000;
  return sessions.filter((s) => {
    const sessionTime = new Date(s.updatedAt || s.createdAt).getTime();
    return !Number.isNaN(sessionTime) && sessionTime <= cutoffMs;
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
