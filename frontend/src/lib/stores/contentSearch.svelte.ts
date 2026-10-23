import { api } from '../api';
import type { ReplaceFile, SearchFile, SearchMatch, SearchQuery, SearchResult } from '../types';
import { errorText } from '../manage';
import { isStaleReply } from '../link';
import { link } from './link.svelte';
import { files, tabHasUnsaved } from './files.svelte';
import { connectionStore } from './connection.svelte';
import { filePrompt } from './filePrompt.svelte';
import { toast } from './toast.svelte';
import { readOnlyText } from '../fileEditing';
import {
  matchOrder,
  overlayDrafts,
  searchLimit,
  searchRegExp,
  stepMatch,
  type MatchRef,
  type SearchDraft,
} from '../contentSearch';
import { replaceInText, replaceSummary, replacementTemplate, type ReplaceOutcome } from '../searchReplace';

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
  /** The replacement, as typed. */
  replace: string;
  /** The replace field is shown. */
  replaceOpen: boolean;
}

const emptyOptions = (): SearchOptions => ({
  text: '', regex: false, matchCase: false, wholeWord: false, include: '', exclude: '', filtersOpen: false,
  replace: '', replaceOpen: false,
});

/** Options that change what is shown, not what is searched. */
const VIEW_OPTIONS: ReadonlySet<keyof SearchOptions> = new Set(['filtersOpen', 'replace', 'replaceOpen']);

export const REPLACE_UNSUPPORTED = "This host's agent-sessions server cannot replace in files. Update agent-sessions on the host, then reconnect.";

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
  /** Matches and files dismissed from the results; see dismissKey. */
  dismissed = $state(new Set<string>());
  /** A replace is running. */
  replacing = $state(false);

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

  /** The results less what was dismissed. */
  get result(): SearchResult | null {
    const result = this.overlaid.result, gone = this.dismissed;
    if (!result || gone.size === 0) return result;
    const files = result.files.flatMap(f => {
      if (gone.has(dismissKey(f.path))) return [];
      const matches = f.matches.filter(m => !gone.has(dismissKey(f.path, m)));
      return matches.length ? [matches.length === f.matches.length ? f : { ...f, matches }] : [];
    });
    return { ...result, files, matches: files.reduce((n, f) => n + f.matches.length, 0) };
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
      .some(k => !VIEW_OPTIONS.has(k) && patch[k] !== before[k]);
    if (searchChanged) this.schedule(SEARCH_DELAY_MS);
  }

  toggle(option: 'regex' | 'matchCase' | 'wholeWord' | 'filtersOpen' | 'replaceOpen'): void {
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

  /**
   * Takes a match, or a whole file, out of the results without changing it,
   * so a replace leaves it alone. A new search text brings it back.
   */
  dismiss(path: string, ref?: MatchRef): void {
    const m = ref ? this.match(ref) : undefined;
    if (ref && !m) return;
    const active = this.active, before = this.order;
    const gone = (r: MatchRef) => r.path === path && (!ref || r.index === ref.index);
    const at = active ? before.findIndex(r => r.path === active.path && r.index === active.index) : -1;
    this.dismissed = new Set(this.dismissed).add(dismissKey(path, m));
    if (!active) return;
    if (at >= 0) {
      // The keyboard stays on its match, or goes to the one taking its place.
      const after = this.order;
      const place = at - before.slice(0, at).filter(gone).length;
      this.active = after[Math.min(place, after.length - 1)] ?? null;
    } else if (active.path === path) {
      // A match in a folded file, reached with F4.
      this.active = gone(active) ? null : { path, index: active.index - (active.index > ref!.index ? 1 : 0) };
    }
  }

  /**
   * Why a replace cannot run now, or null. With path, also what keeps that
   * file from being changed.
   */
  replaceBlocked(path?: string): string | null {
    if (connectionStore.capabilities.replaceFiles !== true) return REPLACE_UNSUPPORTED;
    const blocked = link.blockedReason;
    if (blocked) return blocked;
    if (this.replacing) return 'A replace is running.';
    if (!this.upToDate || !this.result) return 'Wait for the search to finish.';
    if (files.renaming || files.deleting || files.creating) return 'Wait for the file change to finish.';
    if (files.renameUncertain || files.deleteUncertain) return 'A rename or delete did not finish. Refresh the file list first.';
    if (path === undefined) return null;
    const tab = files.tabs.find(t => t.path === path);
    if (!tab) return null;
    if (tab.edit && tabHasUnsaved(tab)) {
      if (tab.edit.saving) return 'Wait for the save to finish.';
      return this.result.files.find(f => f.path === path)?.draft ? null
        : 'Unsaved draft: the browser cannot search it with this pattern. Save it first.';
    }
    const content = tab.content;
    if (content?.edit?.eligible === false) return readOnlyText(content.edit.reason);
    return null;
  }

  /** Replaces one match. */
  replaceMatch(ref: MatchRef): Promise<void> {
    const file = this.result?.files.find(f => f.path === ref.path);
    const m = file?.matches[ref.index];
    return file && m ? this.replace([{ ...file, matches: [m] }], false) : Promise.resolve();
  }

  /** Replaces every match listed in one file. */
  replaceFile(path: string): Promise<void> {
    const file = this.result?.files.find(f => f.path === path);
    return file ? this.replace([file], false) : Promise.resolve();
  }

  /** Replaces every match listed, after asking once. */
  replaceAll(): Promise<void> {
    return this.replace(this.result?.files ?? [], true);
  }

  /**
   * Replaces the listed matches of targets: in the draft for a file with
   * unsaved changes (one undoable change, still unsaved), on disk for the
   * rest. Then the open tabs show the new text, a toast says what happened
   * and the search runs again.
   */
  private async replace(targets: SearchFile[], confirm: boolean): Promise<void> {
    targets = targets.filter(f => f.matches.length > 0);
    const query = this.searched, dir = files.dir, host = link.dataHost, project = this.project;
    if (targets.length === 0 || !query || !dir || this.replaceBlocked()) return;
    const typed = this.options.replace;
    if (confirm) {
      const n = targets.reduce((sum, f) => sum + f.matches.length, 0);
      const more = this.result?.truncated || targets.some(f => f.truncated)
        ? ' Only the matches listed are replaced; search again for the rest.' : '';
      const answer = await filePrompt.ask(
        'Replace in files',
        `Replace ${plural(n, 'match', 'matches')} in ${plural(targets.length, 'file', 'files')} ${typed ? `with “${typed}”` : 'with nothing'}?${more}`,
        [{ value: 'replace', label: 'Replace', danger: true }],
      );
      if (answer !== 'replace') return;
      // The project, host or search may have changed while asking.
      if (files.dir !== dir || link.dataHost !== host || this.project !== project
        || !sameSearch(this.searched, query) || this.options.replace !== typed || this.replaceBlocked()) return;
    }
    const replacement = replacementTemplate(typed, !!query.regex);
    const outcomes = new Map<string, ReplaceOutcome>();
    const disk: ReplaceFile[] = [];
    let re: RegExp | null = null;
    try { re = searchRegExp(query); } catch { /* Drafts are then skipped below. */ }
    this.replacing = true;
    try {
      for (const f of targets) {
        const matches = f.matches.map(m => ({ line: m.line, start: m.start }));
        const tab = files.tabs.find(t => t.path === f.path);
        if (!tab?.edit || !tabHasUnsaved(tab)) {
          disk.push({ path: f.path, expectedRevision: f.revision, matches });
          continue;
        }
        if (!f.draft || !re || tab.edit.saving) {
          outcomes.set(f.path, { path: f.path, status: 'skipped', reason: 'draft_unsearched' });
          continue;
        }
        const out = replaceInText(tab.edit.draft, re, replacement, !!query.regex, matches);
        if (!out.ok) outcomes.set(f.path, { path: f.path, status: 'skipped', reason: 'stale_match' });
        else if (files.updateDraft(tab, out.text)) outcomes.set(f.path, { path: f.path, status: 'draft', replaced: out.replaced });
        else outcomes.set(f.path, { path: f.path, status: 'failed', message: 'the draft could not be changed' });
      }
      if (disk.length > 0) {
        try {
          const res = await api.replaceInFiles(dir, { query, replacement, files: disk }, host);
          for (const r of res.files) outcomes.set(r.path, { path: r.path, status: r.status, replaced: r.replaced, reason: r.reason, message: r.message });
          if (files.dir === dir && link.dataHost === host) {
            await files.reloadReplaced(res.files.filter(r => r.status === 'replaced').map(r => r.path));
          }
        } catch (e) {
          // The host may have written some files before the error.
          const message = errorText(e, 'Replace failed.');
          toast.show({ title: 'Replace may not have finished', body: `${message} Files may have changed; the search runs again to show them as they are.`, tone: 'error' });
          return;
        }
      }
      toast.show(replaceSummary(targets.map(f => outcomes.get(f.path) ?? { path: f.path, status: 'failed', message: 'no answer from the host' })));
    } finally {
      this.replacing = false;
      if (this.project === project && files.dir === dir) this.searchNow();
    }
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
    this.dismissed = new Set();
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
        this.dismissed = new Set();
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

/**
 * The key a dismissed file, or a match in it, is kept under. A match is
 * known by its line and column, so it stays dismissed when the search runs
 * again after a replace elsewhere.
 */
function dismissKey(path: string, m?: SearchMatch): string {
  return m ? `m\0${path}\0${m.line}\0${m.start}` : `f\0${path}`;
}

const plural = (n: number, one: string, many: string) => `${n.toLocaleString()} ${n === 1 ? one : many}`;

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
