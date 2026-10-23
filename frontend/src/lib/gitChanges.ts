import type { GitChange, RepoStatus } from './types';

export type ChangeSection = 'conflicts' | 'staged' | 'unstaged';

/** A file with changes in both places appears in both sections. */
export function changeGroups(status: RepoStatus | null): Record<ChangeSection, GitChange[]> {
  const groups: Record<ChangeSection, GitChange[]> = { conflicts: [], staged: [], unstaged: [] };
  for (const entry of status?.entries ?? []) {
    if (entry.conflict) { groups.conflicts.push(entry); continue; }
    if (entry.index) groups.staged.push(entry);
    if (entry.worktree) groups.unstaged.push(entry);
  }
  return groups;
}

export function branchLabel(status: RepoStatus | null): string {
  if (!status?.ok) return 'No repository';
  if (status.detached) return `Detached at ${status.head}`;
  return status.branch || 'Unborn branch';
}

export function isChangesShortcut(event: Pick<KeyboardEvent, 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>, mac: boolean): boolean {
  return event.code === 'KeyG' && event.shiftKey && !event.altKey && (mac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey);
}
