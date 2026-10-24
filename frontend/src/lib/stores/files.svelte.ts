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
const LISTING_CACHE_LIMIT = 4;
// Oversized replies remain visible, but are not retained across dialog lifetimes.
const LISTING_SNAPSHOT_LIMIT = 500_000; // serialized UTF-16 code units, not heap bytes

function listingRoot(dir: string): string {
  // Normalize separators only for Windows roots; a backslash is a valid POSIX name.
  const root = /^(?:[a-z]:[\\/]|\\\\)/i.test(dir) ? dir.replace(/\\/g, '/') : dir;
  const normalized = root.replace(/\/+$/, '') || '/';
  return /^[a-z]:\/+$/i.test(root) ? normalized + '/' : normalized;
}

function listingKey(host: string | undefined, dir: string): string {
  return JSON.stringify([host ?? null, listingRoot(dir)]);
}

/** Equal, nested or case-variant roots. Over-matching only drops a hint. */
function rootsOverlap(a: string, b: string): boolean {
  const x = listingRoot(a).toLowerCase(), y = listingRoot(b).toLowerCase();
  const within = (p: string, q: string) => p === q || p.startsWith(q.endsWith('/') ? q : q + '/');
  return within(x, y) || within(y, x);
}

function listingSnapshot(listing: FileListing): FileListing {
  // Raw state and the cache share only immutable replacements, never API-owned arrays.
  return Object.freeze({
    ...listing,
    files: Object.freeze([...listing.files]) as unknown as string[],
    ...(listing.directories && { directories: Object.freeze([...listing.directories]) as unknown as string[] }),
    ...(listing.ignored && { ignored: Object.freeze([...listing.ignored]) as unknown as string[] }),
    ...(listing.status && { status: Object.freeze({ ...listing.status }) }),
  });
}

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

/** The text a tab currently displays, for live-diagnostics reconciliation only. */
export interface DisplayedDocument { path: string; text: string; revision?: string }

/**
 * Explicit notifications of *committed* files-store transitions. The observer
 * (the live diagnostics bridge) never owns tabs, drafts, revisions or save policy;
 * failures in it are swallowed and cannot affect editing.
 */
export interface FileLifecycleObserver {
  /** A read committed displayable text to a tab not being edited. */
  diskState?(dir: string, path: string, absent: boolean): void;
  opened(dir: string, path: string, text: string, revision?: string): void;
  /** The displayed text was replaced by committed text (edit toggle, discard/reload). */
  replaced(dir: string, path: string, text: string, revision?: string): void;
  /** The draft changed (already committed to the store). */
  changed(dir: string, path: string, text: string): void;
  /** Called with the exact text about to be saved; the opaque token is returned to saved/saveFailed. */
  beforeSave(dir: string, path: string, text: string): unknown;
  saved(dir: string, path: string, token: unknown, revision: string): void;
  saveFailed(dir: string, path: string, token: unknown, uncertain: boolean): void;
  /** The tab is gone (close, preview replacement, delete, nothing displayable). */
  removed(dir: string, path: string): void;
  /** A confirmed rename already remapped open tabs. */
  renamed(dir: string, from: string, to: string): void;
  /** A rename/delete outcome is unknown for this path (and its descendants). */
  uncertain(dir: string, path: string): void;
  /** Saved tabs of a directory were restored; reconcile all displayed documents. */
  restored(dir: string): void;
  /** The store was cleared/reset/dialog closed: forget everything without sending. */
  cleared(): void;
  /** The connection changed: cancel pending work immediately. */
  interrupted(): void;
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
  /** Only a confirmed leaf absence, never a failed read. */
  diskAbsent?: boolean;
  /**
   * path is an absolute host path outside the project that go to definition
   * opened (a dependency's source): read-only, without Git, blame or
   * diagnostics, and never a live document.
   */
  external?: boolean;
  /** A Git review snapshot, never an editable/live document. */
  gitBasis?: 'head' | 'index' | 'staged';
  gitDeleted?: boolean;
}

export class FilesStore {
  /** Soft-wraps long lines in the viewer; remembered between runs. */
  wrap = $state(readFlag(WRAP_KEY, false));
  /** Shows a changed file as a diff against the last commit. */
  diff = $state(readFlag(DIFF_KEY, false));

  /** The directory being browsed; null while the dialog is closed. */
  dir = $state<string | null>(null);
  listing = $state.raw<FileListing | null>(null);
  /** Retained paths/status are hints until the current list request succeeds. */
  listingStale = $state(false);
  private listings = new Map<string, FileListing>();
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
  creating = $state(false);
  deleting = $state(false);
  /** An unknown deletion must never be followed by an old-path write. */
  deleteUncertain = $state<string | null>(null);
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
  private observer?: FileLifecycleObserver;
  private diagnosticPaths: () => string[] = () => [];
  setDiagnosticPaths(paths: () => string[]): void { this.diagnosticPaths = paths; }
  private extraProbeSeq = 0;
  private invalidations = new WeakMap<FileTab, Promise<void>>();
  private probeReads = new WeakMap<FileTab, number>();
  private invalidatePath: (dir: string, path: string) => Promise<void> = async () => {};
  setPathInvalidator(invalidate: (dir: string, path: string) => Promise<void>): void { this.invalidatePath = invalidate; }
  private readExternal: (path: string) => Promise<FileContent> = async () => { throw new Error('Files outside the project cannot be shown here.'); };
  /** Installs how an external tab's file is read (code navigation's grant). */
  setExternalReader(read: (path: string) => Promise<FileContent>): void { this.readExternal = read; }
  private savedReads = new WeakMap<FileTab, number>();

  /** Installs the single lifecycle observer; returns an unsubscribe function. */
  observe(observer: FileLifecycleObserver | undefined): () => void {
    this.observer = observer;
    return () => { if (this.observer === observer) this.observer = undefined; };
  }

  private notify(call: (o: FileLifecycleObserver) => void): void {
    const o = this.observer;
    if (!o) return;
    try { call(o); } catch { /* Diagnostics must never affect files, drafts or saves. */ }
  }

  /** Displayed text of the browsed directory's tabs (drafts included); never persisted. */
  displayedDocuments(): DisplayedDocument[] {
    const out: DisplayedDocument[] = [];
    for (const tab of this.tabs) {
      if (tab.diskAbsent || tab.external || tab.gitBasis) continue;
      if (tab.edit) out.push({ path: tab.path, text: tab.edit.draft, revision: tab.edit.revision });
      else if (displayable(tab.content)) out.push({ path: tab.path, text: tab.content.text, revision: tab.content.revision });
    }
    return out;
  }

  private shown(dir: string, tab: FileTab): void {
    if (tab.external || tab.gitBasis) return;
    const content = tab.content;
    if (displayable(content)) this.notify(o => o.opened(dir, tab.path, content.text, content.revision));
    else this.notify(o => o.removed(dir, tab.path));
  }

  /** Reconnect retains every editor/cache; only an explicitly resolved host switch may reset. */
  async connectionChanged(host?: string): Promise<void> {
    this.notify(o => o.interrupted());
    this.connectionSeq++;
    this.listSeq++;
    this.loadingList = this.refreshing = false;
    if (this.listing) this.listingStale = true; // Unverified until a list on this connection succeeds.
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
      // An external file is not read again: its grant ends with the attachment.
      for (const tab of this.tabs) if (!tab.external || !tab.content) pending.push(this.read(tab.path));
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
    if (!this.invalidateListings(dir, host)) return;
    this.loadingList = !this.listing;
    this.refreshing = !!this.listing;
    void this.list(this.dir!);
  }

  /**
   * After an app mutation under root (null: anywhere on the host, as for Git) that
   * succeeded or may have happened: drop overlapping retained snapshots, including
   * hidden ones, and supersede an overlapping visible list request that may predate
   * it. Never touches tabs, drafts or saved editor state. Returns whether the
   * visible listing now needs a list request that can be made.
   */
  invalidateListings(root: string | null, host: string | undefined): boolean {
    for (const key of this.listings.keys()) {
      const [h, r] = JSON.parse(key) as [string | null, string];
      if (h === (host ?? null) && (root === null || rootsOverlap(r, root))) this.listings.delete(key);
    }
    const dir = this.dir;
    if (!dir || !this.hasHost || this.dataHost !== host || (root !== null && !rootsOverlap(dir, root))) return false;
    this.listSeq++;
    this.loadingList = this.refreshing = false;
    if (this.listing) this.listingStale = true;
    return !this.mutationError(host);
  }

  /** invalidateListings, then refresh an affected visible listing and its open tabs. */
  async listingsChanged(root: string | null, host: string | undefined): Promise<void> {
    if (this.invalidateListings(root, host)) await this.refresh();
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

  /** Invalidates destructive confirmations across reconnects and host switches. */
  get contextVersion(): string { return `${epoch()}:${this.connectionSeq}:${this.dataHost ?? ''}`; }

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
    if (this.creating || this.renaming || this.deleting) return;
    // Until the host-transition coordinator exists, retain the old host's drafts
    // and refuse to populate that cache with another host's responses.
    if (this.hasHost && this.dataHost !== link.dataHost && !this.reset()) return;
    this.hasHost = true;
    this.dataHost = link.dataHost;
    if (this.dir === dir && this.listing) return;
    this.save();
    this.clear();
    this.dir = dir;
    const key = listingKey(this.dataHost, dir);
    const cached = this.listings.get(key);
    if (cached) {
      this.listings.delete(key);
      this.listings.set(key, cached);
      this.listing = cached;
      this.listingStale = true;
    }
    this.loadingList = !cached;
    this.refreshing = !!cached;
    const saved = this.saved.get(dir);
    if (saved) {
      this.expanded = new Set(saved.expanded);
      this.tabs = saved.tabs;
      this.selected = saved.selected;
      this.notify(o => o.restored(dir));
    }
    // Cached paths never stand in for fresh presence probes or content reads.
    await Promise.all([this.reconcileDisplayed(), this.list(dir)]);
  }

  /** Fetches the file list and the open files again, keeping both shown meanwhile. */
  async refresh(): Promise<void> {
    const dir = this.dir;
    if (!dir || this.loadingList || this.refreshing || this.mutationError(this.dataHost)) return;
    this.refreshing = true;
    await Promise.all([this.reconcileDisplayed(), this.list(dir)]);
  }

  /** Bounded, explicit observations only. No revision can prove rename or recreation. */
  private async probe(dir: string, tab: FileTab): Promise<boolean> {
    const host = this.dataHost, connection = this.connectionSeq, started = epoch();
    const path = tab.path, token = tab.token, edit = tab.edit;
    const revision = edit?.revision, version = edit?.version;
    const seq = (this.probeReads.get(tab) ?? 0) + 1;
    this.probeReads.set(tab, seq);
    const valid = () => this.dir === dir && host === link.dataHost && connection === this.connectionSeq
      && started === epoch() && this.tabs.includes(tab) && tab.path === path && tab.token === token
      && this.probeReads.get(tab) === seq && tab.edit === edit && !edit?.saving
      && edit?.revision === revision && edit?.version === version;
    if (edit?.saving) return false;
    try {
      const result = await api.probeFiles(dir, [path], host ?? '');
      if (!valid()) return false;
      const observation = result.observations.find(o => o.path === path);
      if (observation?.state === 'absent') {
        tab.diskAbsent = true;
        tab.token = ++this.readSeq;
        tab.loading = false;
        this.savedReads.set(tab, (this.savedReads.get(tab) ?? 0) + 1);
        tab.error = 'The file was observed absent on disk. Displayed text has been retained.';
        this.notify(o => o.diskState?.(dir, path, true));
        // Owned backend cancellation/clear must precede any recreation lifecycle.
        const invalidation = this.invalidatePath(dir, path);
        this.invalidations.set(tab, invalidation);
        await invalidation;
        if (this.invalidations.get(tab) === invalidation) this.invalidations.delete(tab);
        return false;
      }
      if (observation?.state === 'present' && tab.diskAbsent) {
        await this.invalidations.get(tab);
        if (!valid()) return false;
        tab.diskAbsent = false;
        tab.error = null;
        this.notify(o => o.diskState?.(dir, path, false));
        if (edit) this.notify(o => o.opened(dir, path, edit.draft, observation.revision));
      }
      return !tab.diskAbsent;
    } catch { return valid() && !tab.diskAbsent; } // Unsupported/unknown is never deletion.
  }

  /** Read-only revision reconciliation. Never writes, settles uncertainty or resets undo. */
  async reconcileDisplayed(): Promise<void> {
    const dir = this.dir, host = this.dataHost, connection = this.connectionSeq, started = epoch();
    if (!dir || this.mutationError(host)) return;
    await Promise.all(this.tabs.map(async tab => {
      const edit = tab.edit;
      if (tab.external || tab.gitBasis) return;
      if (!edit) { await this.read(tab.path); return; }
      if (edit.saving || (typeof api.probeFiles === 'function' && !await this.probe(dir, tab))) return;
      // Typing is allowed during the read. Commit saved context only, not the
      // captured draft; version/draft/baseline/uncertain are editor-owned.
      const read = (this.savedReads.get(tab) ?? 0) + 1;
      this.savedReads.set(tab, read);
      const token = tab.token, version = edit.version, revision = edit.revision;
      try {
        const content = await api.readFile(dir, tab.path);
        if (this.dir !== dir || started !== epoch() || connection !== this.connectionSeq || host !== link.dataHost
          || this.savedReads.get(tab) !== read || !this.tabs.includes(tab) || tab.edit !== edit || edit.saving || edit.version !== version || edit.revision !== revision) return;
        if (content.revision === tab.content?.revision) return;
        tab.content = content;
        if (content.revision !== edit.revision) edit.conflict = { observedRevision: content.revision };
        // A concurrent replacement invalidates the read, but ordinary typing
        // must not lose its newer draft or undo version.
        if (tab.token === token) tab.error = null;
        this.notify(o => o.replaced(dir, tab.path, edit.draft, content.revision));
      } catch (err) {
        if (this.dir === dir && started === epoch() && connection === this.connectionSeq && host === link.dataHost
          && this.savedReads.get(tab) === read && this.tabs.includes(tab) && tab.edit === edit
          && !edit.saving && edit.version === version && edit.revision === revision && !isStaleReply(err)) {
          tab.error = errorText(err, 'Could not reconcile the saved file.');
        }
      }
    }));
    // Retained findings may refer to files without tabs. Use the captured file-host
    // alias, not the diagnostics stable identity, for this read-only API.
    const seq = ++this.extraProbeSeq;
    const paths = [...new Set(this.diagnosticPaths())].filter(path => !this.tabs.some(t => t.path === path));
    for (let i = 0; i < paths.length; i += 32) {
      try {
        const out = await api.probeFiles(dir, paths.slice(i, i + 32), host ?? '');
        if (this.dir !== dir || started !== epoch() || connection !== this.connectionSeq || host !== link.dataHost || seq !== this.extraProbeSeq) return;
        for (const o of out.observations) if (o.state === 'absent' && paths.includes(o.path)) await this.invalidatePath(dir, o.path);
      } catch { /* Unknown never deletes findings. */ }
    }
  }

  private async list(dir: string): Promise<void> {
    const seq = ++this.listSeq;
    const started = epoch();
    const host = this.dataHost;
    const key = listingKey(host, dir);
    if (this.listing) this.listingStale = true;
    const current = () => seq === this.listSeq && started === epoch() && host === this.dataHost
      && host === link.dataHost && this.dir === dir;
    try {
      const listing = await api.listFiles(dir);
      if (!current()) return;
      const snapshot = listingSnapshot(listing);
      this.listings.delete(key);
      if (JSON.stringify(snapshot).length <= LISTING_SNAPSHOT_LIMIT) {
        this.listings.set(key, snapshot);
        while (this.listings.size > LISTING_CACHE_LIMIT) this.listings.delete(this.listings.keys().next().value!);
      }
      this.listing = snapshot;
      this.listingStale = false;
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

  /** Create once, then refresh only the tree; never reload or discard open drafts. */
  createDirectory(path: string, name: string): Promise<void> {
    return this.createEntry(path, name, 'folder');
  }

  /** Creates an empty file like createDirectory, then opens it in a tab of its own. */
  async createFile(path: string, name: string): Promise<void> {
    const dir = this.dir;
    await this.createEntry(path, name, 'file');
    if (this.dir === dir) this.openFile(path ? `${path}/${name}` : name, { keep: true });
  }

  private async createEntry(path: string, name: string, kind: 'folder' | 'file'): Promise<void> {
    const dir = this.dir;
    const host = this.dataHost;
    if (!dir || !name || name === '.' || name === '..' || /[\/\\\0]/.test(name)) throw new Error(`Enter a single ${kind} name.`);
    if ((path && path.split('/').some(part => !part || part === '.' || part === '..')) || /[\\\0]/.test(path)) throw new Error('Choose a project directory.');
    if (this.creating || this.renaming || this.deleting || this.renameUncertain || this.deleteUncertain) {
      throw new Error(this.deleteUncertain || this.renameUncertain || 'A file operation is already in progress.');
    }
    const blocked = this.mutationError(host);
    if (blocked) throw new Error(blocked);
    const started = epoch();
    const connection = this.connectionSeq;
    const current = () => started === epoch() && connection === this.connectionSeq && this.dir === dir && host === link.dataHost;
    this.creating = true;
    try {
      if (kind === 'folder') await api.createDirectory(dir, path, name, host ?? '');
      else await api.createFile(dir, path, name, host ?? '');
      if (!current()) throw new Error(`The connection changed during ${kind} creation.`);
      const destination = path ? `${path}/${name}` : name;
      this.expanded = new Set([...this.expanded, ...parentFolders(destination), ...(kind === 'folder' ? [destination] : [])]);
      this.save();
      this.invalidateListings(dir, host);
      this.refreshing = true;
      await this.list(dir);
    } catch (err) {
      if (!current() || isStaleReply(err) || isDisconnectedError(err)) {
        this.invalidateListings(dir, host);
        throw new Error(kind === 'folder'
          ? 'The folder creation outcome is unknown. Refresh and verify the directory before trying again.'
          : 'The file creation outcome is unknown. Refresh and verify the file before trying again.');
      }
      throw err;
    } finally { this.creating = false; }
  }

  /** Rename in place: retain tab/editor identities, draft text and undo history. */
  async rename(path: string, name: string): Promise<void> {
    const dir = this.dir;
    const host = this.dataHost;
    if (!dir || !path || !name || name === '.' || name === '..' || /[\\/\\\\\0]/.test(name)) throw new Error('Enter a single file or directory name.');
    if (name === path.split('/').pop()) return;
    if (this.creating || this.renaming || this.renameUncertain || this.deleting || this.deleteUncertain) throw new Error(this.deleteUncertain || this.renameUncertain || 'A file operation is already in progress.');
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
      this.invalidateListings(dir, host);
      this.refreshing = true;
      await this.list(dir);
      for (const tab of affected) if (!tab.edit) void this.read(tab.path);
    } catch (err) {
      if (!current() || isStaleReply(err) || isDisconnectedError(err)) {
        this.notify(o => o.uncertain(dir, path));
        this.invalidateListings(dir, host);
        this.renameUncertain = 'The rename outcome is unknown. Verify the paths on disk before reopening this project; saving and renaming are disabled to protect drafts.';
        throw new Error(this.renameUncertain);
      }
      throw err;
    } finally { this.renaming = false; }
  }

  /** Caller confirms permanent deletion before invoking this operation. */
  async delete(path: string): Promise<void> {
    const dir = this.dir;
    const host = this.dataHost;
    if (!dir || !path || path.split('/').some(part => !part || part === '.' || part === '..') || /[\\\\\0]/.test(path)) {
      throw new Error('Choose a non-root file or directory to delete.');
    }
    if (this.creating || this.renaming || this.deleting || this.renameUncertain || this.deleteUncertain) {
      throw new Error(this.deleteUncertain || this.renameUncertain || 'A file operation is already in progress.');
    }
    const blocked = this.mutationError(host);
    if (blocked) throw new Error(blocked);
    const matches = (p: string) => p === path || p.startsWith(path + '/');
    const affected = this.tabs.filter(t => matches(t.path));
    if (affected.some(t => t.edit?.saving || t.edit?.uncertain || this.reconciling.has(t))) {
      throw new Error('Wait for pending saves to finish and resolve unknown save outcomes before deleting.');
    }
    const started = epoch();
    const connection = this.connectionSeq;
    const current = () => started === epoch() && connection === this.connectionSeq && this.dir === dir && host === link.dataHost;
    this.deleting = true;
    this.listSeq++;
    this.loadingList = this.refreshing = false;
    for (const tab of affected) { tab.token = ++this.readSeq; tab.loading = false; }
    try {
      await api.deleteFile(dir, path, host ?? '');
      if (!current()) throw new Error('The connection changed during deletion.');
      const selectedAt = this.tabs.findIndex(t => t.path === this.selected);
      this.tabs = this.tabs.filter(t => !matches(t.path));
      for (const tab of affected) this.notify(o => o.removed(dir, tab.path));
      if (this.selected && matches(this.selected)) {
        this.selected = (this.tabs[Math.min(selectedAt, this.tabs.length - 1)])?.path ?? null;
      }
      this.expanded = new Set([...this.expanded].filter(p => !matches(p)));
      this.save();
      this.invalidateListings(dir, host);
      this.refreshing = true;
      await this.list(dir);
    } catch (err) {
      if (!current() || isStaleReply(err) || isDisconnectedError(err)) {
        this.notify(o => o.uncertain(dir, path));
        this.invalidateListings(dir, host);
        this.deleteUncertain = 'The deletion outcome is unknown. Some contents may already have been removed. Verify the paths on disk before reopening this project; saving, renaming and deleting are disabled to protect drafts.';
        throw new Error(this.deleteUncertain);
      }
      // Recursive deletion is not atomic: retain every draft and tab, and only
      // refresh the tree (never reload the editors) after a partial failure.
      this.invalidateListings(dir, host);
      this.refreshing = true;
      await this.list(dir);
      throw new Error(`${errorText(err, 'Could not delete the item.')} Some contents may already have been removed. Your open tabs and drafts have been kept.`);
    } finally { this.deleting = false; }
  }

  /** Prefix boundaries matter: renaming src must not change src-old. */
  remapPaths(path: string, destination: string): void {
    const remap = (p: string) => p === path || p.startsWith(path + '/') ? destination + p.slice(path.length) : p;
    for (const tab of this.tabs) {
      if (tab.external) continue;
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
    const dir = this.dir;
    if (dir) this.notify(o => o.renamed(dir, path, destination));
  }

  setExpanded(next: Set<string>): void {
    this.expanded = next;
  }

  /**
   * Shows path in a tab: its own if it has one, else the preview tab, or
   * with keep a tab of its own. With read false (a deleted file) nothing is
   * fetched.
   */
  openFile(path: string, opts: { keep?: boolean; read?: boolean; external?: boolean } = {}): void {
    if (!this.dir || this.mutationError(this.dataHost)) return;
    if (!opts.external) this.revealPath(path);
    const keep = opts.keep ?? false;
    const existing = this.tabs.find((t) => t.path === path);
    if (existing) {
      if (keep) existing.preview = false;
      this.selected = path;
      if (existing.gitBasis && opts.read !== false) {
        existing.gitBasis = undefined; existing.gitDeleted = undefined;
        existing.content = null;
        void this.read(path);
      }
      return;
    }
    const tab: FileTab = {
      path,
      preview: !keep,
      content: null,
      loading: false,
      error: null,
      token: 0,
      ...(opts.external ? { external: true } : {}),
    };
    const previewAt = keep ? -1 : this.tabs.findIndex((t) => t.preview && !t.edit?.editing && !tabHasUnsaved(t));
    if (previewAt >= 0) {
      const replaced = this.tabs[previewAt];
      this.tabs[previewAt] = tab;
      const dir = this.dir;
      if (!replaced.external) this.notify(o => o.removed(dir, replaced.path));
    } else {
      const activeAt = this.tabs.findIndex((t) => t.path === this.selected);
      this.tabs.splice(activeAt >= 0 ? activeAt + 1 : this.tabs.length, 0, tab);
    }
    this.selected = path;
    if (opts.read !== false) void this.read(path);
  }

  /**
   * Shows a file outside the project, by its absolute host path, read-only:
   * where go to definition led into a dependency.
   */
  openExternal(path: string, opts: { keep?: boolean } = {}): void {
    const existing = this.tabs.find((t) => t.path === path);
    if (existing && !existing.external) return;
    this.openFile(path, { ...opts, external: true });
  }

  /** Reviews a Git basis in the file's tab; a retained draft is never replaced. */
  openGitDiff(path: string, basis: 'head' | 'index' | 'staged'): boolean {
    const existing = this.tabs.find(t => t.path === path);
    if (existing?.edit) return false;
    this.openFile(path, { keep: true, read: false });
    const tab = this.activeTab;
    if (!tab || tab.path !== path || tab.external) return false;
    if (this.dir && !tab.gitBasis) this.notify(o => o.removed(this.dir!, path));
    tab.gitBasis = basis;
    tab.gitDeleted = false;
    tab.content = null;
    this.diff = true;
    void this.read(path);
    return true;
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
    const tab = this.tabs.find((t) => t.path === path);
    if (tab) {
      if (!tab.external) this.revealPath(path);
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
    const [closed] = this.tabs.splice(at, 1);
    if (this.selected === path) {
      this.selected = (this.tabs[at] ?? this.tabs[at - 1])?.path ?? null;
    }
    const dir = this.dir;
    if (dir && !closed.external) this.notify(o => o.removed(dir, path));
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
      if ((!tab.external && !tab.gitBasis && typeof api.probeFiles === 'function' && !await this.probe(dir, tab)) || !current()) return;
      const review = tab.gitBasis ? await api.readGitDiff(dir, path, tab.gitBasis) : undefined;
      const content = review ? review.content : tab.external ? await this.readExternal(path) : await api.readFile(dir, path);
      const t = current();
      if (t) {
        t.content = content;
        t.gitDeleted = review?.deleted;
        t.error = null;
        this.shown(dir, t);
      }
    } catch (err) {
      const t = current();
      if (t && !isStaleReply(err)) {
        // Unknown read failure retains the last displayed text.
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
    if (!tab || tab.external || tab.gitBasis || this.mutationError(this.dataHost) || !canEditContent(tab.content)) return false;
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
    // The editor shows LF text without the BOM; the viewer showed the raw text.
    const dir = this.dir;
    const shown = tab.content.text;
    if (dir && shown !== baseline) this.notify(o => o.replaced(dir, tab.path, baseline, tab.edit?.revision));
    return true;
  }

  /**
   * Leave edit mode and drop the draft: the viewer shows the saved content.
   * `reread` refreshes it from disk when the disk may differ from the baseline.
   */
  stopEditing(tab: FileTab, reread = false): boolean {
    if (!tab.edit || tab.edit.saving) return false;
    const draft = tab.edit.draft;
    tab.edit = undefined;
    tab.token = ++this.readSeq;
    const dir = this.dir;
    if (dir && this.tabs.includes(tab)) {
      if (displayable(tab.content)) {
        if (tab.content.text !== draft) this.notify(o => o.replaced(dir, tab.path, tab.content!.text!, tab.content!.revision));
      } else this.notify(o => o.removed(dir, tab.path));
    }
    if (reread && this.tabs.includes(tab)) void this.read(tab.path);
    return true;
  }

  updateDraft(tabOrPath: FileTab | string, text: string): boolean {
    const tab = this.target(tabOrPath)?.tab;
    const edit = tab?.edit;
    if (!tab || !edit?.editing) return false;
    tab.token = ++this.readSeq;
    edit.draft = text;
    const dir = this.target(tab)?.dir;
    if (dir) this.notify(o => o.changed(dir, tab.path, text));
    return true;
  }

  /** Snapshot once; expectedRevision authorizes a confirmed overwrite, still using CAS. */
  async saveEdited(tabOrPath: FileTab | string, expectedRevision?: string): Promise<boolean> {
    const target = this.target(tabOrPath);
    if (!target?.tab.edit) return false;
    const { tab, dir, host } = target;
    const edit = tab.edit!;
    if (edit.saving || edit.uncertain || this.renaming || this.renameUncertain || this.deleting || this.deleteUncertain) return false;
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
    // Flush/order the live document change before the write; the token pins its version.
    const token = (() => { let t: unknown; this.notify(o => { t = o.beforeSave(dir, tab.path, snapshot.text); }); return t; })();
    edit.saving = true;
    edit.saveError = null;
    edit.conflict = null;
    try {
      const result = await api.saveFile(dir, { path: tab.path, text: snapshot.text, expectedRevision: snapshot.expectedRevision }, host);
      if (!valid()) return false;
      if (!sameHost()) {
        edit.uncertain = snapshot;
        edit.saveError = 'The connection changed; the save outcome is unknown.';
        this.refreshSavedListing(dir, host); // The write may have landed.
        this.notify(o => o.saveFailed(dir, tab.path, token, true));
        return false;
      }
      if (result.status === 'conflict') {
        this.notify(o => o.saveFailed(dir, tab.path, token, false));
        edit.conflict = { observedRevision: result.observedRevision };
        edit.saveError = 'The file changed on disk.';
        return false;
      }
      tab.content = result.content;
      edit.baseline = snapshot.text;
      edit.revision = result.content.revision!;
      // draft and version deliberately stay untouched (including typing/undo during save).
      this.refreshSavedListing(dir, host);
      this.notify(o => o.saved(dir, tab.path, token, result.content.revision!));
      return true;
    } catch (err) {
      if (!valid()) return false;
      // The disk may hold the submitted bytes (or part of them): keep unsaved
      // protection until a read settles it, even if the draft was undone to
      // the baseline meanwhile.
      const unknown = !sameHost() || isStaleReply(err) || isDisconnectedError(err) || isSaveIncompleteError(err);
      if (unknown) {
        edit.uncertain = snapshot;
        this.refreshSavedListing(dir, host);
      }
      this.notify(o => o.saveFailed(dir, tab.path, token, unknown));
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
    if (edit.saving || this.renaming || this.renameUncertain || this.deleting || this.deleteUncertain) return false;
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
      this.notify(o => o.replaced(dir, tab.path, text, content.revision));
      return true;
    } catch (err) {
      if (valid()) edit.saveError = errorText(err, 'Could not reload the file.');
      return false;
    }
  }

  /**
   * Shows what a replace in files wrote. A clean editor takes the new text
   * as one undoable change (the version stays, so its history does too); one
   * typed in meanwhile keeps its draft and is marked as changed on disk.
   */
  async reloadReplaced(paths: string[]): Promise<void> {
    const dir = this.dir, host = this.dataHost, started = epoch(), connection = this.connectionSeq;
    if (!dir) return;
    this.refreshSavedListing(dir, host); // Drops hidden snapshots even while blocked.
    if (this.mutationError(host)) return;
    const wanted = new Set(paths);
    await Promise.all(this.tabs.filter(t => wanted.has(t.path)).map(async tab => {
      const edit = tab.edit;
      if (!edit) return this.read(tab.path);
      if (edit.saving) return;
      const token = tab.token = ++this.readSeq;
      const valid = () => this.dir === dir && started === epoch() && connection === this.connectionSeq
        && host === link.dataHost && this.tabs.includes(tab) && tab.edit === edit && !edit.saving;
      try {
        const content = await api.readFile(dir, tab.path);
        if (!valid()) return;
        // Typing bumps the token: the draft is kept, and the disk change shown as a conflict.
        if (tab.token !== token || tabHasUnsaved(tab) || !canEditContent(content)) {
          if (content.revision !== edit.revision) edit.conflict = { observedRevision: content.revision };
          return;
        }
        const text = editorText(content);
        serializedEditSize(text, content.edit);
        tab.content = content;
        tab.error = null;
        edit.baseline = edit.draft = text;
        edit.revision = content.revision;
        edit.saveError = null;
        this.notify(o => o.replaced(dir, tab.path, text, content.revision));
      } catch (err) {
        if (valid() && !isStaleReply(err)) tab.error = errorText(err, 'Could not read the replaced file.');
      }
    }));
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
    if (this.creating || this.renaming || this.deleting) return;
    this.save();
    this.clear();
  }

  /** Refuse destructive resets until a coordinator has resolved every draft. */
  reset(): boolean {
    if (this.hasUnsaved || this.creating || this.renaming || this.deleting) return false;
    this.clear();
    this.saved.clear();
    this.listings.clear();
    this.renameUncertain = null;
    this.deleteUncertain = null;
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
    this.notify(o => o.cleared());
    this.listSeq++;
    this.readSeq++;
    this.dir = null;
    this.listing = null;
    this.listingStale = false;
    this.loadingList = false;
    this.refreshing = false;
    this.listError = null;
    this.expanded = new Set();
    this.tabs = [];
    this.selected = null;
  }
}

function displayable(content: FileContent | null): content is FileContent & { text: string } {
  return !!content && typeof content.text === 'string' && !content.binary && !content.tooLarge && !content.image;
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
