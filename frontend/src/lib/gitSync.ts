import type { GitSyncErrorCode } from './types';

/** Coded fetch/pull/push failure; message keeps Git's bounded output after the first line. */
export class GitSyncError extends Error {
  code: GitSyncErrorCode;
  constructor(code: GitSyncErrorCode, message: string) { super(message); this.code = code; }
}

const CODE = /^(authRequired|nonFastForward|localChanges|detached|noUpstream|noRemote|ambiguousRemote|pushRemote|noCommits|inProgress|timeout|canceled|unsupported|failed): /;

/** Wails serializes Go errors as "<code>: <message>" strings; recover the code. */
export function gitSyncError(error: unknown): GitSyncError {
  if (error instanceof GitSyncError) return error;
  const message = typeof error === 'string' ? error : (error as { message?: string })?.message ?? 'Git sync failed';
  const match = CODE.exec(message);
  return match ? new GitSyncError(match[1] as GitSyncErrorCode, message.slice(match[0].length)) : new GitSyncError('failed', message);
}
