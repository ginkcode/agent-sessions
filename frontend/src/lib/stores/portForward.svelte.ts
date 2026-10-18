import { api } from '../api';
import { connectionStore } from './connection.svelte';
import type { PortForwardRequest, PortForwardState } from '../portForward';

export class PortForwardStore {
  open = $state(false);
  data = $state<PortForwardState>({ host: '', rules: [] });
  loading = $state(false);
  busy = $state(false);
  error = $state('');
  private sequence = 0;

  async refresh(): Promise<void> {
    const sequence = ++this.sequence;
    const host = connectionStore.host;
    try {
      const data = await api.listPortForwards();
      if (sequence === this.sequence && host === connectionStore.host) {
        if (!this.busy) this.data = { ...data, rules: data.rules ?? [] };
        if (!this.busy) this.error = '';
      }
    } catch (err) {
      if (sequence === this.sequence) this.error = String(err instanceof Error ? err.message : err);
    }
  }

  async show(): Promise<void> {
    this.open = true;
    this.loading = true;
    await this.refresh();
    this.loading = false;
  }

  async action(kind: 'start' | 'stop' | 'remove' | 'save', value: string | PortForwardRequest): Promise<boolean> {
    if (this.busy) return false;
    this.busy = true;
    const host = connectionStore.host;
    try {
      const data = kind === 'save' ? await api.savePortForward(value as PortForwardRequest)
        : kind === 'start' ? await api.startPortForward(value as string)
        : kind === 'stop' ? await api.stopPortForward(value as string)
        : await api.removePortForward(value as string);
      if (host !== connectionStore.host) return false;
      this.data = { ...data, rules: data.rules ?? [] };
      this.error = '';
      return true;
    } catch (err) {
      if (host === connectionStore.host) this.error = err instanceof Error ? err.message : String(err);
      return false;
    } finally {
      this.busy = false;
    }
  }
}

export const portForward = new PortForwardStore();
