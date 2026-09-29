import { api, subscribeIndexProgress } from '../api';
import type {
  FTSProgress,
  MessageJump,
  SearchFilter,
  SearchHit,
} from '../types';
import { errorText } from '../manage';
import { LatestRequestGate } from '../search';

export const SEARCH_DEBOUNCE_MS = 150;

export class SearchState {
  open = $state(false);
  query = $state('');
  filter = $state<SearchFilter>({ limit: 30 });
  results = $state<SearchHit[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  progress = $state<FTSProgress | null>(null);
  activeIndex = $state(-1);
  jump = $state<MessageJump | null>(null);

  private gate = new LatestRequestGate(SEARCH_DEBOUNCE_MS);
  private jumpID = 0;
  private unsubscribeProgress: (() => void) | null = null;

  init(): void {
    if (this.unsubscribeProgress) return;
    this.unsubscribeProgress = subscribeIndexProgress((value) => {
      this.progress = value;
    });
    // Seed status before the first event; a later event always wins.
    void api.indexProgress().then(
      (value) => {
        if (!this.progress) this.progress = value;
      },
      () => {}
    );
  }

  destroy(): void {
    this.gate.cancel();
    this.unsubscribeProgress?.();
    this.unsubscribeProgress = null;
  }

  show(): void {
    this.init();
    this.open = true;
  }

  close(): void {
    this.open = false;
  }

  setFilter(filter: SearchFilter): void {
    this.filter = { ...filter };
    if (this.query.trim()) this.schedule();
  }

  setQuery(query: string): void {
    this.query = query;
    this.schedule();
  }

  moveActive(delta: number): void {
    if (!this.results.length) {
      this.activeIndex = -1;
      return;
    }
    const from = this.activeIndex < 0 ? (delta > 0 ? -1 : 0) : this.activeIndex;
    this.activeIndex = (from + delta + this.results.length) % this.results.length;
  }

  activeHit(): SearchHit | null {
    return this.activeIndex >= 0 ? this.results[this.activeIndex] || null : null;
  }

  requestJump(hit: SearchHit, ready?: Promise<unknown>): void {
    this.jump = {
      id: ++this.jumpID,
      ref: { ...hit.ref },
      messageIndex: hit.messageIndex,
      kind: hit.kind,
      query: this.query,
      ready,
    };
  }

  async runNow(): Promise<void> {
    const generation = this.gate.begin();
    const query = this.query.trim();
    if (!query) {
      this.clearResults();
      return;
    }

    this.loading = true;
    this.error = null;
    try {
      const results = await api.search(query, this.filter);
      if (!this.gate.isCurrent(generation)) return;
      this.results = results || [];
      this.activeIndex = this.results.length ? 0 : -1;
    } catch (err) {
      if (!this.gate.isCurrent(generation)) return;
      this.results = [];
      this.activeIndex = -1;
      this.error = errorText(err, 'Search failed');
    } finally {
      if (this.gate.isCurrent(generation)) this.loading = false;
    }
  }

  private schedule(): void {
    if (!this.query.trim()) {
      this.gate.cancel();
      this.clearResults();
      return;
    }
    this.loading = true;
    this.gate.schedule(() => void this.runNow());
  }

  private clearResults(): void {
    this.results = [];
    this.loading = false;
    this.error = null;
    this.activeIndex = -1;
  }
}

export const search = new SearchState();
