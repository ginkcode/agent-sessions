import { api } from '../api';
import type { SearchMatch, SearchQuery, SearchResult } from '../types';
import { errorText } from '../manage';
import { isStaleReply } from '../link';
import { link } from './link.svelte';
import { files, tabHasUnsaved } from './files.svelte';
import {
  matchOrder,
  overlayDrafts,
  searchLimit,
  stepMatch,
  type MatchRef,
  type SearchDraft,
} from '../contentSearch';

/** What the left pane of Go to File shows. */
export type PickerMode = 'files' | 'search';

/** The Search pane's text and options; kept per project while the app runs. */
export interface SearchOptions {
  text: string;
  regex: boolean;
  matchCase: boolean;
  wholeWord: boolean;
  include: string;
  exclude: string;
  /** The files to include and exclude fields are shown. */
  filtersOpen: boolean;
}

const emptyOptions = (): SearchOptions => ({
  text: '', regex: false, matchCase: false, wholeWord: false, include: '', exclude: '', filtersOpen: false,
});

/** Typing pauses this long before a search starts. */
export const SEARCH_DELAY_MS = 250;
/** The backend cancels the previous search under this ID when a new one starts. */
const SEARCH_ID = 'go-to-file-search';

/**
 * The Search pane of Go to File: a search of the project's file contents on
 * the host, with unsaved drafts searched in the browser in their place.
 */
export class ContentSearchStore {
  mode = $state<PickerMode>('files');
  options = $state<SearchOptions>(emptyOptions());
  /** The host's answer for the last search, before drafts are laid over it. */
  hostResult = $state.raw<SearchResult | null>(null);
  /** The query hostResult answers. */
  searched = $state.raw<SearchQuery | null>(null);
  searching = $state(false);
  error = $state<string | null>(null);
  /** Files whose matches are folded away. */
  collapsed = $state(new Set<string>());
  /** The match the keyboard is on. */
  active = $state.raw<MatchRef | null>(null);
  /** Bumped to ask the pane to focus its field. */
  focusRequest = $state(0);

  private saved = new Map<string, SearchOptions>();
  private project: string | null = null;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private seq = 0;
  /** open() asked for the Search pane, which the dialog opening keeps. */
  private requested = false;

  /** The drafts of the open project that differ from disk. */
  get drafts(): SearchDraft[] {
    return files.tabs
      .filter(t => t.edit && tabHasUnsaved(t))
      .map(t => ({ path: t.path, text: t.edit!.draft }));
  }

  /** The results shown: the host's, with each drafted file searched as drafted. */
  get overlaid() {
    const result = this.hostResult, query = this.searched;
    if (!result || !query) return { result, unsearched: [] as string[] };
    return overlayDrafts(result, this.drafts, query);
  }

  get result(): SearchResult | null {
    return this.overlaid.result;
  }

  get limit(): number {
    return searchLimit(this.searched ?? { pattern: '' });
  }

  /** The matches the arrow keys move through: those of expanded files. */
  get order(): MatchRef[] {
    return matchOrder(this.result, this.collapsed);
  }

  /** The results answer the text and options as they are now. */
  get upToDate(): boolean {
    return !this.searching && sameSearch(this.searched, queryOf(this.options));
  }

  /** The text field is empty: nothing to search. */
  get idle(): boolean {
    return this.options.text === '';
  }

  /** Shows the Search pane for the open project, optionally searching for text. */
  open(text?: string): void {
    this.follow();
    this.requested = true;
    this.mode = 'search';
    if (text && text !== this.options.text) {
      this.options = { ...this.options, text };
      this.remember();
    }
    // Files may have changed since the last search.
    if (this.options.text) this.schedule(0);
    this.focusRequest++;
  }

  /**
   * Go to File just opened: in Search when open() asked for it, else in
   * Files, as Cmd+P and Cmd+Shift+F do in editors.
   */
  dialogOpened(): PickerMode {
    if (!this.requested) this.mode = 'files';
    this.requested = false;
    return this.mode;
  }

  /** Go to File closed: nothing is searched while it is hidden. */
  dialogClosed(): void {
    this.requested = false;
    this.cancel();
  }

  setMode(mode: PickerMode): void {
    this.follow();
    this.mode = mode;
    if (mode === 'search') {
      if (this.options.text && !this.searching) this.schedule(0);
      this.focusRequest++;
    }
  }

  /** Changes the text or options; a search follows once typing pauses. */
  set(patch: Partial<SearchOptions>): void {
    this.follow();
    const before = this.options;
    this.options = { ...before, ...patch };
    this.remember();
    const searchChanged = (Object.keys(patch) as (keyof SearchOptions)[])
      .some(k => k !== 'filtersOpen' && patch[k] !== before[k]);
    if (searchChanged) this.schedule(SEARCH_DELAY_MS);
  }

  toggle(option: 'regex' | 'matchCase' | 'wholeWord' | 'filtersOpen'): void {
    this.set({ [option]: !this.options[option] });
  }

  /** Searches now, as after Enter or Refresh. */
  searchNow(): void {
    this.follow();
    this.schedule(0);
  }

  toggleCollapsed(path: string): void {
    const next = new Set(this.collapsed);
    if (next.has(path)) next.delete(path);
    else next.add(path);
    this.collapsed = next;
    if (this.active?.path === path && next.has(path)) this.active = null;
  }

  /** Moves the keyboard to the next (or previous) visible match. */
  move(direction: 1 | -1): MatchRef | null {
    this.active = stepMatch(this.order, this.active, direction);
    return this.active;
  }

  /**
   * The next (or previous) match in every file, collapsed or not, for F4;
   * it becomes the active one.
   */
  step(direction: 1 | -1): MatchRef | null {
    this.active = stepMatch(matchOrder(this.result), this.active, direction);
    return this.active;
  }

  match(ref: MatchRef | null): SearchMatch | undefined {
    if (!ref) return undefined;
    return this.result?.files.find(f => f.path === ref.path)?.matches[ref.index];
  }

  /** Stops what is running and forgets the results, as after a host switch. */
  reset(): void {
    this.cancel();
    this.requested = false;
    this.mode = 'files';
    this.saved.clear();
    this.project = null;
    this.options = emptyOptions();
    this.clearResults();
  }

  /** Forgets the pending and running search; the dialog closed. */
  cancel(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    if (this.searching) void api.cancelSearch(SEARCH_ID).catch(() => {});
    this.seq++;
    this.searching = false;
  }

  private clearResults(): void {
    this.hostResult = null;
    this.searched = null;
    this.error = null;
    this.active = null;
    this.collapsed = new Set();
  }

  /** Switches to the open project's saved options when the project changed. */
  private follow(): void {
    const key = files.dir === null ? null : JSON.stringify([link.dataHost ?? '', files.dir]);
    if (key === this.project) return;
    this.cancel();
    this.project = key;
    this.options = key ? { ...(this.saved.get(key) ?? emptyOptions()) } : emptyOptions();
    this.clearResults();
  }

  private remember(): void {
    if (this.project) this.saved.set(this.project, { ...this.options });
  }

  private schedule(delay: number): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.timer = null;
      void this.run();
    }, delay);
  }

  private async run(): Promise<void> {
    const dir = files.dir;
    const o = this.options;
    const seq = ++this.seq;
    if (!dir || o.text === '') {
      if (this.searching) void api.cancelSearch(SEARCH_ID).catch(() => {});
      this.searching = false;
      this.clearResults();
      return;
    }
    const query = queryOf(o);
    this.searching = true;
    try {
      // Under the same ID, the backend stops the search this one replaces.
      const result = await api.searchFiles(dir, query, SEARCH_ID);
      if (seq !== this.seq) return;
      if (!sameSearch(this.searched, query)) {
        this.active = null;
        this.collapsed = new Set();
      }
      this.hostResult = result;
      this.searched = query;
      this.error = null;
    } catch (e) {
      if (seq !== this.seq || isStaleReply(e)) return;
      this.hostResult = null;
      this.searched = null;
      this.active = null;
      this.error = errorText(e, 'Search failed.');
    } finally {
      if (seq === this.seq) this.searching = false;
    }
  }
}

function queryOf(o: SearchOptions): SearchQuery {
  return {
    pattern: o.text, regex: o.regex, matchCase: o.matchCase, wholeWord: o.wholeWord,
    include: o.include, exclude: o.exclude,
  };
}

function sameSearch(a: SearchQuery | null, b: SearchQuery): boolean {
  return !!a && a.pattern === b.pattern && a.regex === b.regex && a.matchCase === b.matchCase
    && a.wholeWord === b.wholeWord && a.include === b.include && a.exclude === b.exclude;
}

export const contentSearch = new ContentSearchStore();
