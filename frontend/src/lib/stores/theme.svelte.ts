export type ThemeMode = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'agent-sessions:theme';

export class ThemeStore {
  mode = $state<ThemeMode>('system');
  resolved = $state<'light' | 'dark'>('dark');

  private mediaQuery: MediaQueryList | null = null;
  private listener: ((e: MediaQueryListEvent) => void) | null = null;

  init(): void {
    if (typeof window === 'undefined') return;

    const saved = localStorage.getItem(STORAGE_KEY) as ThemeMode | null;
    if (saved && (saved === 'system' || saved === 'light' || saved === 'dark')) {
      this.mode = saved;
    }

    this.mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    this.listener = (e: MediaQueryListEvent) => {
      if (this.mode === 'system') {
        this.resolved = e.matches ? 'dark' : 'light';
        this.applyDOM();
      }
    };
    this.mediaQuery.addEventListener('change', this.listener);

    this.updateResolved();
    this.applyDOM();
  }

  setMode(mode: ThemeMode): void {
    this.mode = mode;
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(STORAGE_KEY, mode);
    }
    this.updateResolved();
    this.applyDOM();
  }

  toggle(): void {
    if (this.resolved === 'dark') {
      this.setMode('light');
    } else {
      this.setMode('dark');
    }
  }

  private updateResolved(): void {
    if (this.mode === 'system') {
      const prefersDark = this.mediaQuery ? this.mediaQuery.matches : false;
      this.resolved = prefersDark ? 'dark' : 'light';
    } else {
      this.resolved = this.mode;
    }
  }

  private applyDOM(): void {
    if (typeof document !== 'undefined') {
      document.documentElement.setAttribute('data-theme', this.resolved);
    }
  }

  destroy(): void {
    if (this.mediaQuery && this.listener) {
      this.mediaQuery.removeEventListener('change', this.listener);
    }
  }
}

export const theme = new ThemeStore();
