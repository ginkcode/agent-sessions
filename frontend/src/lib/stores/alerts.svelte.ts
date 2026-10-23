import { api, subscribeNotificationOpen } from '../api';
import type { NotificationStatus } from '../types';
import { setTitleCount } from '../hosts';
import { AlertLog, alertText, isAlertKind, type AlertKind } from '../alerts';
import type { AgentAttention } from '../attention';
import { terminals } from './terminals.svelte';

const STORAGE_KEY = 'agent-sessions:agent-alerts';

/**
 * Tells the user when an agent tab needs them or finished: the window title
 * counts the tabs that need them, and while the window is in the
 * background, a desktop notification names the tab. Clicking it shows the
 * tab.
 */
export class AlertsStore {
  /** Notify when an agent waits for the user. */
  notifyNeeds = $state(true);
  /** Notify when an agent finishes its turn. */
  notifyDone = $state(true);
  /** Whether notifications reach the desktop; null until known. */
  status = $state<NotificationStatus | null>(null);
  private log = new AlertLog();
  private preparing: Promise<NotificationStatus> | null = null;
  private off: (() => void)[] = [];

  init(): void {
    if (this.off.length) return;
    try {
      const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
      if (typeof saved.needs === 'boolean') this.notifyNeeds = saved.needs;
      if (typeof saved.done === 'boolean') this.notifyDone = saved.done;
    } catch {
      // Storage disabled; keep both on.
    }
    this.off.push(
      terminals.onAttention((id, state) => this.changed(id, state)),
      terminals.onViewing((id) => void api.clearNotification(id).catch(() => {})),
      subscribeNotificationOpen((id) => terminals.show(id)),
    );
    void this.refreshStatus();
  }

  destroy(): void {
    for (const off of this.off) off();
    this.off = [];
  }

  /** Asks the backend whether notifications work, without asking the user. */
  async refreshStatus(): Promise<void> {
    try {
      this.status = await api.notificationStatus();
    } catch {
      // Keep what is known.
    }
  }

  setNotifyNeeds(on: boolean): void {
    this.notifyNeeds = on;
    this.save();
    if (on && this.hasAgentTabs()) void this.prepare();
  }

  setNotifyDone(on: boolean): void {
    this.notifyDone = on;
    this.save();
    if (on && this.hasAgentTabs()) void this.prepare();
  }

  private enabled(kind: AlertKind): boolean {
    return kind === 'needs' ? this.notifyNeeds : this.notifyDone;
  }

  private hasAgentTabs(): boolean {
    return terminals.tabs.some((t) => t.kind === 'agent');
  }

  /**
   * Readies notifications the first time an agent tab is followed while
   * one is wanted; where the platform asks for permission, this is when.
   */
  private prepare(): Promise<NotificationStatus> {
    if (!this.preparing || this.status?.available === false) {
      this.preparing = api.prepareNotifications().then(
        (status) => (this.status = status),
        (err) => (this.status = { available: false, reason: `Notifications could not start: ${err}` }),
      );
    }
    return this.preparing;
  }

  private changed(id: string, state: AgentAttention): void {
    setTitleCount(Object.values(terminals.attention).filter((s) => s === 'needs').length);
    if (state === 'exited') {
      this.log.forget(id);
      void api.clearNotification(id).catch(() => {});
      return;
    }
    if (!this.preparing && (this.notifyNeeds || this.notifyDone)) void this.prepare();
    if (!isAlertKind(state) || !this.enabled(state)) return;
    // The tab and its dot show it while the window has focus.
    if (terminals.focused) return;
    if (!this.log.take(id, state, terminals.inputs(id))) return;
    const tab = terminals.tabs.find((t) => t.id === id);
    if (!tab) return;
    const said = state === 'needs' ? terminals.signalText(id) : '';
    const { title, body } = alertText(state, tab.agent ?? tab.session?.agent, tab.label, said);
    void this.prepare().then((status) => {
      if (status.available) return api.notify({ tab: id, title, body }).catch(() => {});
    });
  }

  private save(): void {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ needs: this.notifyNeeds, done: this.notifyDone }));
    } catch {
      // Storage disabled; the choice still applies for this run.
    }
  }
}

export const alerts = new AlertsStore();
