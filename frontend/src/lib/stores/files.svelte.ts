import { api } from '../api';
import type { FileContent, FileEditInfo, FileListing } from '../types';
import { errorText } from '../manage';
import { epoch, isDisconnectedError, isSaveIncompleteError, isStaleReply } from '../link';
import { link } from './link.svelte';
import { canEditContent, editorText, serializedEditSize } from '../fileEditing';
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
export interface FileEditState {
  editing: boolean;
  /** LF text, excluding only the original disk BOM. */
  baseline: string;
  revision: string;
  draft: string;
  readonly dirty: boolean;
  saving: boolean;
  saveError: string | null;
  conflict: { observedRevision?: string } | null;
  /** An attempted write whose outcome is not known; never automatically replayed. */
  uncertain: { text: string; expectedRevision: string; format: FileEditInfo } | null;
  /** External editor history caches must rebuild when this changes. */
  version: number;
}

/**
 * A draft that is not known to be on disk. An unresolved conflict counts even
 * when the draft equals the baseline: after a failed save the disk may hold a
 * partial write, and the draft is then the only intact copy.
 */
export function tabHasUnsaved(tab: FileTab): boolean {
  const edit = tab.edit;
  return !!edit && (edit.draft !== edit.baseline || edit.saving || !!edit.uncertain || !!edit.conflict);
}

export interface DraftTarget {
  host: string | undefined;
  dir: string;
  tab: FileTab;
}

export interface FileTab {
  path: string;
  preview: boolean;
  content: FileContent | null;
  loading: boolean;
  error: string | null;
  /** Matches the read in flight, so a late reply for a replaced tab is dropped. */
  token: number;
  edit?: FileEditState;
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
  renaming = $state(false);
  /** Do not write to old paths after a rename with an unknown outcome. */
  renameUncertain = $state<string | null>(null);
  private listSeq = 0;
  private readSeq = 0;
  private editVersion = 0;
  private dataHost: string | undefined;
  private hasHost = false;
  private connectionSeq = 0;
  private reconciling = new Map<FileTab, { seq: number; promise: Promise<void> }>();

  /** Reconnect retains every editor/cache; only an explicitly resolved host switch may reset. */
  async connectionChanged(host?: string): Promise<void> {
    this.connectionSeq++;
    this.listSeq++;
    this.loadingList = this.refreshing = false;
    const tabs = new Set([...this.tabs, ...[...this.saved.values()].flatMap((state) => state.tabs)]);
    for (const tab of tabs) {
      tab.token = ++this.readSeq;
      tab.loading = false;
    }
    if (this.hasHost && this.dataHost !== host && !this.reset()) return;
    this.hasHost = true;
    this.dataHost = host;
    if (this.mutationError(host)) return;
    const pending = this.draftTargets.map((target) => this.reconcile(target));
    if (this.dir) {
      for (const tab of this.tabs) pending.push(this.read(tab.path));
      this.refreshing = true;
      pending.push(this.list(this.dir));
    }
    await Promise.all(pending);
  }

  /** Read-only recovery: acknowledge exactly the submitted disk bytes, never replay a write. */
  private reconcile(target: DraftTarget): Promise<void> {
    const { tab, dir, host } = target;
    const edit = tab.edit;
    const snapshot = edit?.uncertain;
    if (!edit || !snapshot || edit.saving || this.mutationError(host)) return Promise.resolve();
    const seq = this.connectionSeq;
    const existing = this.reconciling.get(tab);
    if (existing?.seq === seq) return existing.promise;
    const started = epoch();
    const valid = () => this.connectionSeq === seq && started === epoch() && !this.mutationError(host)
      && this.target(tab)?.dir === dir && tab.edit === edit && edit.uncertain === snapshot && !edit.saving;
    const promise = (async () => {
      try {
        const content = await api.readFile(dir, tab.path);
        if (!valid()) return;
        // Even unsupported content can identify a conflict, but cannot settle uncertainty.
        if (content.revision) edit.conflict = { observedRevision: content.revision };
        if (!canEditContent(content)) throw new Error('The file is no longer editable.');
        serializedEditSize(editorText(content), content.edit);
        serializedEditSize(snapshot.text, snapshot.format);
        const submitted = (snapshot.format.bom ? '\uFEFF' : '')
          + (snapshot.format.newline === 'crlf' ? snapshot.text.replace(/\n/g, '\r\n') : snapshot.text);
        if (content.text === submitted) {
          tab.content = content;
          edit.baseline = snapshot.text;
          edit.revision = content.revision;
          edit.conflict = null;
          edit.saveError = null;
          // Keep draft and version: later typing and undo history belong to the editor.
          this.refreshSavedListing(dir, host);
        } else {
          edit.conflict = { observedRevision: content.revision };
          edit.saveError = 'The file changed on disk.';
        }
        edit.uncertain = null;
      } catch (err) {
        if (valid()) edit.saveError = errorText(err, 'Could not verify the save outcome.');
      }
    })();
    this.reconciling.set(tab, { seq, promise });
    void promise.finally(() => {
      if (this.reconciling.get(tab)?.promise === promise) this.reconciling.delete(tab);
    });
    return promise;
  }

  /** Saving refreshes only the visible directory list, never another tab's contents. */
  private refreshSavedListing(dir: string, host: string | undefined): void {
    if (this.dir !== dir || this.mutationError(host)) return;
    this.refreshing = true;
    void this.list(dir);
  }

  /** Includes hidden directories; callers can use these targets for a future coordinator. */
  get draftTargets(): DraftTarget[] {
    const targets: DraftTarget[] = [];
    for (const [dir, state] of this.saved) {
      if (dir !== this.dir) for (const tab of state.tabs) {
        if (tab.edit) targets.push({ host: this.dataHost, dir, tab });
      }
    }
    if (this.dir) for (const tab of this.tabs) {
      if (tab.edit) targets.push({ host: this.dataHost, dir: this.dir, tab });
    }
    return targets;
  }

  get hasUnsaved(): boolean {
    return this.draftTargets.some(({ tab }) => tabHasUnsaved(tab));
  }

  private target(tabOrPath: FileTab | string): DraftTarget | undefined {
    if (typeof tabOrPath !== 'string') {
      return this.draftTargets.find(({ tab }) => tab === tabOrPath);
    }
    const tab = this.tabs.find((t) => t.path === tabOrPath);
    return tab && this.dir ? { host: this.dataHost, dir: this.dir, tab } : undefined;
  }

  private mutationError(host: string | undefined): string | null {
    return link.blockedReason || (host !== link.dataHost ? 'This file belongs to another host.' : null);
  }

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
    if (this.renaming) return;
    // Until the host-transition coordinator exists, retain the old host's drafts
    // and refuse to populate that cache with another host's responses.
    if (this.hasHost && this.dataHost !== link.dataHost && !this.reset()) return;
    this.hasHost = true;
    this.dataHost = link.dataHost;
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
    if (!dir || this.loadingList || this.refreshing || this.mutationError(this.dataHost)) return;
    this.refreshing = true;
    for (const tab of this.tabs) void this.read(tab.path);
    await this.list(dir);
  }

  private async list(dir: string): Promise<void> {
    const seq = ++this.listSeq;
    const started = epoch();
    const current = () => seq === this.listSeq && started === epoch() && this.dataHost === link.dataHost;
    try {
      const listing = await api.listFiles(dir);
      if (!current()) return;
      this.listing = listing;
      this.listError = null;
    } catch (err) {
      if (!current() || isStaleReply(err)) return;
      this.listError = errorText(err, 'Could not list the files.');
    } finally {
      if (seq === this.listSeq) {
        this.loadingList = false;
        this.refreshing = false;
      }
    }
  }

  /** Rename in place: retain tab/editor identities, draft text and undo history. */
  async rename(path: string, name: string): Promise<void> {
    const dir = this.dir;
    const host = this.dataHost;
    if (!dir || !path || !name || name === '.' || name === '..' || /[\\/\\\\\0]/.test(name)) throw new Error('Enter a single file or directory name.');
    if (name === path.split('/').pop()) return;
    if (this.renaming || this.renameUncertain) throw new Error(this.renameUncertain || 'A rename is already in progress.');
    const blocked = this.mutationError(host);
    if (blocked) throw new Error(blocked);
    const matches = (p: string) => p === path || p.startsWith(path + '/');
    const affected = this.tabs.filter(t => matches(t.path));
    if (affected.some(t => t.edit?.saving || t.edit?.uncertain) || affected.some(t => this.reconciling.has(t))) {
      throw new Error('Wait for pending saves to finish and resolve unknown save outcomes before renaming.');
    }
    const destination = path.slice(0, path.lastIndexOf('/') + 1) + name;
    if (this.tabs.some(t => !matches(t.path) && (t.path === destination || t.path.startsWith(destination + '/')))) {
      throw new Error('The destination already has open tabs. Close them before renaming.');
    }
    const started = epoch();
    const connection = this.connectionSeq;
    const current = () => started === epoch() && connection === this.connectionSeq && this.dir === dir && host === link.dataHost;
    this.renaming = true;
    // Supersede old-path reads, including explicit draft reloads.
    this.listSeq++;
    this.loadingList = this.refreshing = false;
    for (const tab of affected) { tab.token = ++this.readSeq; tab.loading = false; }
    try {
      await api.renameFile(dir, path, name, host ?? '');
      if (!current()) throw new Error('The connection changed during rename.');
      this.remapPaths(path, destination);
      this.refreshing = true;
      await this.list(dir);
      for (const tab of affected) if (!tab.edit) void this.read(tab.path);
    } catch (err) {
      if (!current() || isStaleReply(err) || isDisconnectedError(err)) {
        this.renameUncertain = 'The rename outcome is unknown. Verify the paths on disk before reopening this project; saving and renaming are disabled to protect drafts.';
        throw new Error(this.renameUncertain);
      }
      throw err;
    } finally { this.renaming = false; }
  }

  /** Prefix boundaries matter: renaming src must not change src-old. */
  remapPaths(path: string, destination: string): void {
    const remap = (p: string) => p === path || p.startsWith(path + '/') ? destination + p.slice(path.length) : p;
    for (const tab of this.tabs) {
      const next = remap(tab.path);
      if (next === tab.path) continue;
      tab.token = ++this.readSeq;
      tab.loading = false;
      tab.path = next;
      if (tab.content) tab.content = { ...tab.content, path: next };
    }
    if (this.selected) this.selected = remap(this.selected);
    this.expanded = new Set([...this.expanded].map(remap));
    this.save();
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
    if (!this.dir || this.mutationError(this.dataHost)) return;
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
    const previewAt = keep ? -1 : this.tabs.findIndex((t) => t.preview && !t.edit?.editing && !tabHasUnsaved(t));
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

  /**
   * Selects the tab at a position counting from 1, when that tab exists;
   * returns whether it did. The digit shortcuts count tabs left to right.
   */
  selectNth(n: number): boolean {
    const tab = this.tabs[n - 1];
    if (!tab) return false;
    this.selectTab(tab.path);
    return true;
  }

  /** Closes a tab; closing the one shown shows its neighbour. */
  closeTab(path: string): boolean {
    const at = this.tabs.findIndex((t) => t.path === path);
    if (at < 0 || tabHasUnsaved(this.tabs[at])) return false;
    this.tabs.splice(at, 1);
    if (this.selected === path) {
      this.selected = (this.tabs[at] ?? this.tabs[at - 1])?.path ?? null;
    }
    return true;
  }

  private async read(path: string): Promise<void> {
    const dir = this.dir;
    const tab = this.tabs.find((t) => t.path === path);
    if (!dir || !tab || tab.edit || this.mutationError(this.dataHost)) return;
    const started = epoch();
    const token = ++this.readSeq;
    tab.token = token;
    // A file read again keeps its old text on screen until the new one comes.
    tab.loading = !tab.content;
    // The tab may be replaced or closed while the file is read.
    const current = () => started === epoch() && this.dataHost === link.dataHost && this.dir === dir
      && this.tabs.find((t) => t === tab && t.token === token && !t.edit);
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

  /** Enter edit mode without rereading, retaining even a clean editor's undo history. */
  startEditing(path: string): boolean {
    const tab = this.tabs.find((t) => t.path === path);
    if (!tab || this.mutationError(this.dataHost) || !canEditContent(tab.content)) return false;
    if (tab.edit) {
      tab.edit.editing = true;
      return true;
    }
    try {
      serializedEditSize(editorText(tab.content), tab.content.edit);
    } catch {
      return false;
    }
    tab.preview = false;
    tab.token = ++this.readSeq; // Invalidate any refresh read already in flight.
    tab.loading = false;
    const baseline = editorText(tab.content);
    tab.edit = {
      editing: true, baseline, revision: tab.content.revision, draft: baseline,
      get dirty() { return this.draft !== this.baseline; },
      saving: false, saveError: null, conflict: null, uncertain: null,
      version: ++this.editVersion,
    };
    return true;
  }

  /**
   * Leave edit mode and drop the draft: the viewer shows the saved content.
   * `reread` refreshes it from disk when the disk may differ from the baseline.
   */
  stopEditing(tab: FileTab, reread = false): boolean {
    if (!tab.edit || tab.edit.saving) return false;
    tab.edit = undefined;
    tab.token = ++this.readSeq;
    if (reread && this.tabs.includes(tab)) void this.read(tab.path);
    return true;
  }

  updateDraft(tabOrPath: FileTab | string, text: string): boolean {
    const tab = this.target(tabOrPath)?.tab;
    const edit = tab?.edit;
    if (!tab || !edit?.editing) return false;
    tab.token = ++this.readSeq;
    edit.draft = text;
    return true;
  }

  /** Snapshot once; expectedRevision authorizes a confirmed overwrite, still using CAS. */
  async saveEdited(tabOrPath: FileTab | string, expectedRevision?: string): Promise<boolean> {
    const target = this.target(tabOrPath);
    if (!target?.tab.edit) return false;
    const { tab, dir, host } = target;
    const edit = tab.edit!;
    if (edit.saving || edit.uncertain || this.renaming || this.renameUncertain) return false;
    const blocked = this.mutationError(host);
    if (blocked) {
      edit.saveError = blocked;
      return false;
    }
    if (!canEditContent(tab.content)) {
      edit.saveError = 'This file cannot be edited.';
      return false;
    }
    try {
      serializedEditSize(edit.draft, tab.content.edit);
    } catch (err) {
      edit.saveError = errorText(err, 'Unsupported edit text.');
      return false;
    }
    tab.token = ++this.readSeq; // Supersede an explicit reload still in flight.
    const snapshot = { text: edit.draft, expectedRevision: expectedRevision ?? edit.revision,
      format: { ...tab.content.edit } };
    const started = epoch();
    const connection = this.connectionSeq;
    const valid = () => this.target(tab)?.dir === dir && tab.edit === edit;
    const sameHost = () => epoch() === started && connection === this.connectionSeq
      && link.dataHost === host && !link.blockedReason;
    edit.saving = true;
    edit.saveError = null;
    edit.conflict = null;
    try {
      const result = await api.saveFile(dir, { path: tab.path, text: snapshot.text, expectedRevision: snapshot.expectedRevision }, host);
      if (!valid()) return false;
      if (!sameHost()) {
        edit.uncertain = snapshot;
        edit.saveError = 'The connection changed; the save outcome is unknown.';
        return false;
      }
      if (result.status === 'conflict') {
        edit.conflict = { observedRevision: result.observedRevision };
        edit.saveError = 'The file changed on disk.';
        return false;
      }
      tab.content = result.content;
      edit.baseline = snapshot.text;
      edit.revision = result.content.revision!;
      // draft and version deliberately stay untouched (including typing/undo during save).
      this.refreshSavedListing(dir, host);
      return true;
    } catch (err) {
      if (!valid()) return false;
      // The disk may hold the submitted bytes (or part of them): keep unsaved
      // protection until a read settles it, even if the draft was undone to
      // the baseline meanwhile.
      if (!sameHost() || isStaleReply(err) || isDisconnectedError(err) || isSaveIncompleteError(err)) edit.uncertain = snapshot;
      edit.saveError = errorText(err, 'Could not save the file.');
      return false;
    } finally {
      if (valid()) {
        edit.saving = false;
        // A stale reply may arrive after the reconnect callback already ran.
        if (edit.uncertain && !this.mutationError(host)) void this.reconcile(target);
      }
    }
  }

  /**
   * Explicit destructive reload: caller must confirm first. Errors, stale replies,
   * or typing during the reload leave the draft/history intact. Also resolves an
   * uncertain save only through an explicit disk read, never a write replay.
   */
  async discardEdited(tabOrPath: FileTab | string): Promise<boolean> {
    const target = this.target(tabOrPath);
    if (!target?.tab.edit) return false;
    const { tab, dir, host } = target;
    const edit = tab.edit!;
    if (edit.saving || this.renaming || this.renameUncertain) return false;
    const blocked = this.mutationError(host);
    if (blocked) {
      edit.saveError = blocked;
      return false;
    }
    const draft = edit.draft;
    const started = epoch();
    const token = tab.token = ++this.readSeq;
    const valid = () => this.target(tab)?.dir === dir && tab.edit === edit && tab.token === token
      && started === epoch() && host === link.dataHost && !link.blockedReason;
    try {
      const content = await api.readFile(dir, tab.path);
      if (!valid() || edit.draft !== draft || edit.saving) return false;
      if (!canEditContent(content)) throw new Error('The file is no longer editable.');
      const text = editorText(content);
      serializedEditSize(text, content.edit);
      tab.content = content;
      edit.baseline = edit.draft = text;
      edit.revision = content.revision;
      edit.uncertain = null;
      edit.conflict = null;
      edit.saveError = null;
      edit.version = ++this.editVersion;
      return true;
    } catch (err) {
      if (valid()) edit.saveError = errorText(err, 'Could not reload the file.');
      return false;
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
    if (this.renaming) return;
    this.save();
    this.clear();
  }

  /** Refuse destructive resets until a coordinator has resolved every draft. */
  reset(): boolean {
    if (this.hasUnsaved || this.renaming) return false;
    this.clear();
    this.saved.clear();
    this.renameUncertain = null;
    this.editVersion++;
    this.hasHost = false;
    this.dataHost = undefined;
    return true;
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
