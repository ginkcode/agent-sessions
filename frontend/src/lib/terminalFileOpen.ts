import type { ConnectionPhase } from './types';

export interface TerminalFileTarget { dir: string; path: string }

interface LinkSnapshot {
  host?: string;
  dataHost?: string;
  phase: ConnectionPhase;
  stale: boolean;
  locked: boolean;
}

export interface TerminalFileOpenDependencies {
  link(): LinkSnapshot;
  epoch(): number;
  resolve(dir: string, path: string): Promise<TerminalFileTarget>;
  files: {
    dir: string | null;
    listError: string | null;
    show(dir: string): Promise<void>;
    openFile(path: string, opts: { keep: boolean }): void;
  };
  report(message: string): void;
  isStale(error: unknown): boolean;
}

export function terminalFileHostReady(host: string, state: LinkSnapshot): boolean {
  return !state.stale && !state.locked && (state.host ?? '') === host && (state.dataHost ?? '') === host
    && (host ? state.phase === 'connected' : state.phase === 'local');
}

/** One opener per terminal view. Late clicks/replies must not replace a newer destination. */
export function createTerminalFileOpener(deps: TerminalFileOpenDependencies) {
  let sequence = 0;
  return {
    dispose() { sequence++; },
    async open(host: string, path: string, cwd?: string): Promise<void> {
      const request = ++sequence;
      if (!terminalFileHostReady(host, deps.link())) {
        deps.report('Select/connect to this terminal’s host first to open its files.');
        return;
      }
      const started = deps.epoch();
      const current = () => request === sequence && started === deps.epoch() && terminalFileHostReady(host, deps.link());
      try {
        const target = await deps.resolve(cwd ?? '', path);
        if (!current()) return;
        await deps.files.show(target.dir);
        if (!current() || deps.files.dir !== target.dir) return;
        if (deps.files.listError) {
          deps.report(deps.files.listError);
          return;
        }
        deps.files.openFile(target.path, { keep: true });
      } catch (error) {
        if (current() && !deps.isStale(error)) {
          deps.report(error instanceof Error ? error.message : String(error));
        }
      }
    },
  };
}
