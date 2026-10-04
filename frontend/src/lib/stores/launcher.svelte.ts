import { api } from '../api';
import type { LaunchInfo, TerminalSettings } from '../types';
import { errorText } from '../manage';
import { link } from './link.svelte';
import { canOpenTerminal } from '../terminal';

/** Whether this machine can open sessions in a terminal, and its shell. */
export class LauncherStore {
  info = $state<LaunchInfo>({ terminal: false, shell: 'posix' });
  /** The terminal chosen in Settings, where it can be chosen. */
  terminal = $state<TerminalSettings | null>(null);
  terminalError = $state<string | null>(null);
  savingTerminal = $state(false);

  async init(): Promise<void> {
    try {
      this.info = await api.launchInfo();
    } catch {
      // Older backends lack the binding: keep Copy only.
    }
  }

  /** Re-scans the installed terminals, as Settings opens: apps come and go. */
  async refresh(): Promise<void> {
    await this.init();
    if (!this.info.chooseTerminal) {
      this.terminal = null;
      return;
    }
    try {
      this.terminal = await api.terminalSettings();
      this.terminalError = null;
    } catch (err) {
      this.terminalError = errorText(err, 'Failed to find terminal apps');
    }
  }

  /** Chooses the terminal by id; '' is Automatic. */
  async choose(id: string): Promise<void> {
    this.savingTerminal = true;
    try {
      this.terminal = await api.setTerminal(id);
      this.terminalError = null;
      await this.init();
    } catch (err) {
      this.terminalError = errorText(err, 'Failed to save the terminal');
    } finally {
      this.savingTerminal = false;
    }
  }

  /**
   * Open in terminal is offered for local data on a supporting platform,
   * and for a WSL distribution's data where this computer can open it.
   */
  get canOpen(): boolean {
    return canOpenTerminal(this.info, link.dataHost);
  }

  /** The shell a copied command is meant for; remote ones are POSIX. */
  get copyShell(): LaunchInfo['shell'] {
    return link.dataHost ? 'posix' : this.info.shell;
  }
}

export const launcher = new LauncherStore();
