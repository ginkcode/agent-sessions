import { api } from '../api';
import type { LaunchInfo, TerminalOpenMode, TerminalSettings } from '../types';
import { errorText } from '../manage';
import { link } from './link.svelte';
import { canOpenExternal, canOpenPanel, canOpenTerminal, openMode } from '../terminal';

/** Whether this machine can open sessions in a terminal, and its shell. */
export class LauncherStore {
  info = $state<LaunchInfo>({ terminal: false, shell: 'posix' });
  /** The terminal chosen in Settings, where it can be chosen. */
  terminal = $state<TerminalSettings | null>(null);
  terminalError = $state<string | null>(null);
  savingTerminal = $state(false);
  savingOpenMode = $state(false);

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

  /** Chooses where agents open: the built-in panel or an external terminal. */
  async setOpenMode(mode: TerminalOpenMode): Promise<void> {
    this.savingOpenMode = true;
    try {
      this.info = await api.setTerminalOpen(mode);
      this.terminalError = null;
    } catch (err) {
      this.terminalError = errorText(err, 'Failed to save where agents open');
    } finally {
      this.savingOpenMode = false;
    }
  }

  /** Where Resume and "Continue in" open agents. */
  get openMode(): TerminalOpenMode {
    return openMode(this.info);
  }

  /**
   * Open in terminal is offered where agents open: the panel for any
   * host, or an external terminal for local data on a supporting platform
   * and a WSL distribution's data where this computer can open it.
   */
  get canOpen(): boolean {
    return canOpenTerminal(this.info, link.dataHost);
  }

  /** Whether an external terminal opens for the shown data. */
  get canOpenExternal(): boolean {
    return canOpenExternal(this.info, link.dataHost);
  }

  /** Whether the built-in panel opens for the shown data. */
  get canOpenPanel(): boolean {
    return canOpenPanel(this.info, link.dataHost);
  }

  /** The shell a copied command is meant for; remote ones are POSIX. */
  get copyShell(): LaunchInfo['shell'] {
    return link.dataHost ? 'posix' : this.info.shell;
  }
}

export const launcher = new LauncherStore();
