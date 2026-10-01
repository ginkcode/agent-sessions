import type { ToastMessage } from '../guidance';

const SHOW_MS = 8000;

// One toast at a time: a new one replaces the current. The timer pauses
// while the pointer is over the toast so it can be read.
export class ToastStore {
  current = $state<(ToastMessage & { id: number }) | null>(null);

  private seq = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;

  show(message: ToastMessage): void {
    this.current = { ...message, id: ++this.seq };
    this.resume();
  }

  dismiss(): void {
    this.pause();
    this.current = null;
  }

  pause(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }

  resume(): void {
    this.pause();
    if (this.current) this.timer = setTimeout(() => this.dismiss(), SHOW_MS);
  }
}

export const toast = new ToastStore();
