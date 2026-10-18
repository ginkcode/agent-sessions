import { api } from '../api';
import type { FileContent, FileListing } from '../types';
import { errorText } from '../manage';
import { isStaleReply } from '../link';
import { parentFolders } from '../files';

/**
 * State of the Go to File dialog: the directory it browses, its file list,
 * and the files open in tabs.
 */
const WRAP_KEY = 'agent-sessions:file-wrap';
const DIFF_KEY = 'agent-sessions:file-diff';

/**
 * A file open in the viewer. A preview tab (one click, as in VS Code) is
 * replaced by the next file picked; a kept one (double click) stays.
 */
export interface FileTab {
  path: string;
  preview: boolean;
  content: FileContent | null;
  loading: boolean;
  error: string | null;
  /** Matches the read in flight, so a late reply for a replaced tab is dropped. */
  token: number;
}

export class FilesStore {
  /** Soft-wraps long lines in the viewer; remembered between runs. */
  wrap = $state(readFlag(WRAP_KEY, false));
  /** Shows a changed file as a diff against the last commit. */
  diff = $state(readFlag(DIFF_KEY, false));

  /** The directory being browsed; null while the dialog is closed. */
  dir = $state<string | null>(null);
  listing = $state<FileListing | null>(null);
  loadingList = $state(false);
  listError = $state<string | null>(null);
  /** A refresh is fetching the list again; the old one stays shown. */
  refreshing = $state(false);

  /** The folders open in the tree. */
  expanded = $state(new Set<string>());
  tabs = $state<FileTab[]>([]);
  /** The path of the tab shown. */
  selected = $state<string | null>(null);

  // What was open in each directory (folders, tabs, the tab shown), kept
  // while the app runs, so Go to File opens there as it was left.
  private saved = new Map<string, { expanded: Set<string>; tabs: FileTab[]; selected: string | null }>();
  private listSeq = 0;
  private readSeq = 0;

  get open(): boolean {
    return this.dir !== null;
  }

  get activeTab(): FileTab | undefined {
    return this.tabs.find((t) => t.path === this.selected);
  }

  get content(): FileContent | null {
    return this.activeTab?.content ?? null;
  }

  get loadingFile(): boolean {
    return this.activeTab?.loading ?? false;
  }

  get fileError(): string | null {
    return this.activeTab?.error ?? null;
  }

  async show(dir: string): Promise<void> {
    if (this.dir === dir && this.listing) return;
    this.save();
    this.clear();
    this.dir = dir;
    const saved = this.saved.get(dir);
    if (saved) {
      this.expanded = new Set(saved.expanded);
      this.tabs = saved.tabs;
      this.selected = saved.selected;
      // The files may have changed since: read them again, keeping what
      // shows until the new text arrives.
      for (const tab of this.tabs) void this.read(tab.path);
    }
    this.loadingList = true;
    await this.list(dir);
  }

  /** Fetches the file list and the open files again, keeping both shown meanwhile. */
  async refresh(): Promise<void> {
    const dir = this.dir;
    if (!dir || this.loadingList || this.refreshing) return;
    this.refreshing = true;
    for (const tab of this.tabs) void this.read(tab.path);
    await this.list(dir);
  }

  private async list(dir: string): Promise<void> {
    const seq = ++this.listSeq;
    try {
      const listing = await api.listFiles(dir);
      if (seq !== this.listSeq) return;
      this.listing = listing;
      this.listError = null;
    } catch (err) {
      if (seq !== this.listSeq || isStaleReply(err)) return;
      this.listError = errorText(err, 'Could not list the files.');
    } finally {
      if (seq === this.listSeq) {
        this.loadingList = false;
        this.refreshing = false;
      }
    }
  }

  setExpanded(next: Set<string>): void {
    this.expanded = next;
  }

  /**
   * Shows path in a tab: its own if it has one, else the preview tab, or
   * with keep a tab of its own. With read false (a deleted file) nothing is
   * fetched.
   */
  openFile(path: string, opts: { keep?: boolean; read?: boolean } = {}): void {
    if (!this.dir) return;
    this.revealPath(path);
    const keep = opts.keep ?? false;
    const existing = this.tabs.find((t) => t.path === path);
    if (existing) {
      if (keep) existing.preview = false;
      this.selected = path;
      return;
    }
    const tab: FileTab = {
      path,
      preview: !keep,
      content: null,
      loading: false,
      error: null,
      token: 0,
    };
    const previewAt = keep ? -1 : this.tabs.findIndex((t) => t.preview);
    if (previewAt >= 0) {
      this.tabs[previewAt] = tab;
    } else {
      const activeAt = this.tabs.findIndex((t) => t.path === this.selected);
      this.tabs.splice(activeAt >= 0 ? activeAt + 1 : this.tabs.length, 0, tab);
    }
    this.selected = path;
    if (opts.read !== false) void this.read(path);
  }

  /** Turns a preview tab into one that stays. */
  keepTab(path: string): void {
    const tab = this.tabs.find((t) => t.path === path);
    if (tab) tab.preview = false;
  }

  private revealPath(path: string): void {
    const missing = parentFolders(path).filter((dir) => !this.expanded.has(dir));
    if (missing.length) this.expanded = new Set([...this.expanded, ...missing]);
  }

  selectTab(path: string): void {
    if (this.tabs.some((t) => t.path === path)) {
      this.revealPath(path);
      this.selected = path;
    }
  }

  /** Closes a tab; closing the one shown shows its neighbour. */
  closeTab(path: string): void {
    const at = this.tabs.findIndex((t) => t.path === path);
    if (at < 0) return;
    this.tabs.splice(at, 1);
    if (this.selected === path) {
      this.selected = (this.tabs[at] ?? this.tabs[at - 1])?.path ?? null;
    }
  }

  private async read(path: string): Promise<void> {
    const dir = this.dir;
    const tab = this.tabs.find((t) => t.path === path);
    if (!dir || !tab) return;
    const token = ++this.readSeq;
    tab.token = token;
    // A file read again keeps its old text on screen until the new one comes.
    tab.loading = !tab.content;
    // The tab may be replaced or closed while the file is read.
    const current = () => this.dir === dir && this.tabs.find((t) => t.path === path && t.token === token);
    try {
      const content = await api.readFile(dir, path);
      const t = current();
      if (t) {
        t.content = content;
        t.error = null;
      }
    } catch (err) {
      const t = current();
      if (t && !isStaleReply(err)) {
        t.content = null;
        t.error = errorText(err, 'Could not read the file.');
      }
    } finally {
      const t = current();
      if (t) t.loading = false;
    }
  }

  toggleWrap(): void {
    this.wrap = !this.wrap;
    saveFlag(WRAP_KEY, this.wrap);
  }

  toggleDiff(): void {
    this.diff = !this.diff;
    saveFlag(DIFF_KEY, this.diff);
  }

  /** Closes the dialog. What was open is kept for next time. */
  close(): void {
    this.save();
    this.clear();
  }

  /** Forgets everything, e.g. when another host is selected. */
  reset(): void {
    this.clear();
    this.saved.clear();
  }

  private save(): void {
    if (!this.dir) return;
    this.saved.set(this.dir, { expanded: this.expanded, tabs: this.tabs, selected: this.selected });
  }

  private clear(): void {
    this.listSeq++;
    this.readSeq++;
    this.dir = null;
    this.listing = null;
    this.loadingList = false;
    this.refreshing = false;
    this.listError = null;
    this.expanded = new Set();
    this.tabs = [];
    this.selected = null;
  }
}

function readFlag(key: string, fallback: boolean): boolean {
  try {
    if (typeof localStorage === 'undefined') return fallback;
    const saved = localStorage.getItem(key);
    return saved === null ? fallback : saved === '1';
  } catch {
    return fallback;
  }
}

function saveFlag(key: string, on: boolean): void {
  try {
    localStorage.setItem(key, on ? '1' : '0');
  } catch {
    // Storage disabled; the choice lasts this run.
  }
}

export const files = new FilesStore();
