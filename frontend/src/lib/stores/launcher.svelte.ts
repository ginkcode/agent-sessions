import { api } from '../api';
import type { LaunchInfo } from '../types';
import { link } from './link.svelte';

/** Whether this machine can open sessions in a terminal, and its shell. */
export class LauncherStore {
  info = $state<LaunchInfo>({ terminal: false, shell: 'posix' });

  async init(): Promise<void> {
    try {
      this.info = await api.launchInfo();
    } catch {
      // Older backends lack the binding: keep Copy only.
    }
  }

  /** Open in terminal is offered for local data on a supporting platform. */
  get canOpen(): boolean {
    return this.info.terminal && !link.dataHost;
  }

  /** The shell a copied command is meant for; remote ones are POSIX. */
  get copyShell(): LaunchInfo['shell'] {
    return link.dataHost ? 'posix' : this.info.shell;
  }
}

export const launcher = new LauncherStore();
