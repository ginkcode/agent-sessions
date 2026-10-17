import { api } from '../api';
import type { FileContent, FileListing } from '../types';
import { errorText } from '../manage';
import { isStaleReply } from '../link';

/**
 * State of the Go to File dialog: the directory it browses, its file list,
 * and the file shown.
 */
const WRAP_KEY = 'agent-sessions:file-wrap';
const DIFF_KEY = 'agent-sessions:file-diff';

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

  selected = $state<string | null>(null);
  content = $state<FileContent | null>(null);
  loadingFile = $state(false);
  fileError = $state<string | null>(null);

  private listSeq = 0;
  private fileSeq = 0;

  get open(): boolean {
    return this.dir !== null;
  }

  async show(dir: string): Promise<void> {
    if (this.dir === dir && this.listing) return;
    this.reset();
    this.dir = dir;
    this.loadingList = true;
    const seq = ++this.listSeq;
    try {
      const listing = await api.listFiles(dir);
      if (seq !== this.listSeq) return;
      this.listing = listing;
    } catch (err) {
      if (seq !== this.listSeq || isStaleReply(err)) return;
      this.listError = errorText(err, 'Could not list the files.');
    } finally {
      if (seq === this.listSeq) this.loadingList = false;
    }
  }

  /** Shows path; with read false (a deleted file) nothing is fetched. */
  async select(path: string, read = true): Promise<void> {
    if (!this.dir) return;
    this.selected = path;
    this.content = null;
    this.fileError = null;
    if (!read) {
      this.fileSeq++;
      this.loadingFile = false;
      return;
    }
    this.loadingFile = true;
    const dir = this.dir;
    const seq = ++this.fileSeq;
    try {
      const content = await api.readFile(dir, path);
      if (seq !== this.fileSeq) return;
      this.content = content;
    } catch (err) {
      if (seq !== this.fileSeq || isStaleReply(err)) return;
      this.fileError = errorText(err, 'Could not read the file.');
    } finally {
      if (seq === this.fileSeq) this.loadingFile = false;
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

  close(): void {
    this.reset();
  }

  /** Forgets everything, e.g. when another host is selected. */
  reset(): void {
    this.listSeq++;
    this.fileSeq++;
    this.dir = null;
    this.listing = null;
    this.loadingList = false;
    this.listError = null;
    this.selected = null;
    this.content = null;
    this.loadingFile = false;
    this.fileError = null;
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
