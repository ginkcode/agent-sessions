import type { BackendAPI } from './api';
import type { CodeDiagnosticsContext, LiveDocumentBasis } from './codeDiagnostics';
import { codeDiagnosticsError } from './codeDiagnostics';
import type { DisplayedDocument, FileLifecycleObserver } from './stores/files.svelte';

/** Debounce for draft changes (CD-21). */
export const LIVE_DOCUMENT_DEBOUNCE_MS = 300;
/** Contract limit on UTF-8 text per live document. */
export const LIVE_DOCUMENT_MAX_BYTES = 5 * 1024 * 1024;
const MAX_RECOVERIES = 2;

export interface LiveTimers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

/** Attachment surface the bridge needs; satisfied by CodeDiagnosticsStore. */
export interface LiveAttachmentSource {
  readonly liveAttachment: { context: CodeDiagnosticsContext; host: string; directory: string; token: string } | undefined;
  /**
   * Whether an available opened-documents provider declares this path (backend
   * provider metadata). Paths no provider owns are simply not live: they are
   * never opened, so they can never be reported as failed synchronization.
   * Absent: every displayed document is offered (older backends).
   */
  liveSupports?(path: string): boolean;
}

type Att = NonNullable<LiveAttachmentSource['liveAttachment']>;
type LiveApi = Pick<BackendAPI, 'openCodeDiagnosticDocument' | 'changeCodeDiagnosticDocument' | 'saveCodeDiagnosticDocument' | 'closeCodeDiagnosticDocument'>;

export interface LiveDocumentStatus {
  path: string;
  documentId: string;
  failure?: string;
  canReopen: boolean;
}

export interface LiveDocumentDeps {
  api: LiveApi;
  store: LiveAttachmentSource;
  /** The displayed (and draft) text of every tab in the browsed directory. */
  source: () => { dir: string | null; documents: DisplayedDocument[] };
  timers?: LiveTimers;
  /** Fresh opaque document identity per lifecycle. */
  newId?: () => string;
  debounceMs?: number;
}

interface Doc {
  path: string;
  id: string;
  revision?: string;
  /** Latest displayed text held by reference only until sent; never logged or persisted. */
  latest: string;
  /** Text of the most recent version assigned (sent or queued). */
  queuedText: string;
  /** Highest version assigned; strictly increasing within `id`. */
  version: number;
  /** Highest version the backend acknowledged. */
  confirmed: number;
  pending?: { version: number; text: string };
  sendQueued: boolean;
  inflight: boolean;
  timer?: unknown;
  saving: boolean;
  failed?: string;
  recoveries: number;
}

function validText(text: string): string | undefined {
  if (text.length > LIVE_DOCUMENT_MAX_BYTES || (text.length * 3 > LIVE_DOCUMENT_MAX_BYTES && new TextEncoder().encode(text).length > LIVE_DOCUMENT_MAX_BYTES)) return 'limitExceeded';
  // A lone surrogate would be silently replaced by the JSON/Go boundary; NUL is rejected by the host.
  if (text.includes('\0') || /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(text)) return 'invalidArgument';
  return undefined;
}

let counter = 0;
function defaultId(): string {
  const c = (globalThis as { crypto?: Crypto }).crypto;
  if (c?.randomUUID) return c.randomUUID();
  return `doc-${Date.now().toString(36)}-${(++counter).toString(36)}-${Math.random().toString(36).slice(2)}`;
}

/**
 * Bridges committed files-store transitions into versioned live document operations.
 * It holds protocol bookkeeping only: the files store keeps tabs, drafts, revisions
 * and save policy; the diagnostics store keeps attachment/result state. Inert (no
 * calls, no state) unless the diagnostics store reports a live-capable attachment.
 */
export class LiveDocumentBridge implements FileLifecycleObserver {
  private docs = new Map<string, Doc>();
  /** Paths whose rename/delete outcome is unknown: nothing is opened until cleared. */
  private blocked = new Set<string>();
  private token: string | undefined;
  /**
   * The owned attachment is momentarily not live (resync, transient store error,
   * pending revoke) but has not been replaced: the backend still holds every
   * open lifecycle. Nothing is sent and nothing is forgotten; the same token
   * resumes, any other token replaces.
   */
  private paused = false;
  /** The attachment `token` names, kept so a paused bridge can still close its lifecycles. */
  private owned: Att | undefined;
  /**
   * Token abandoned by a connection interruption. Its backend state is unknown and
   * nothing more is sent to it; only a different (reattached) token is adopted.
   */
  private retired: string | undefined;
  private chain: Promise<void> = Promise.resolve();
  private listeners = new Set<() => void>();
  private timers: LiveTimers;
  private newId: () => string;
  private debounce: number;

  private deps: LiveDocumentDeps;
  constructor(deps: LiveDocumentDeps) {
    this.deps = deps;
    this.timers = deps.timers ?? { set: (fn, ms) => setTimeout(fn, ms), clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>) };
    this.newId = deps.newId ?? defaultId;
    this.debounce = deps.debounceMs ?? LIVE_DOCUMENT_DEBOUNCE_MS;
  }

  onChange(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }
  private changed_() { for (const l of [...this.listeners]) { try { l(); } catch { /* observers never affect sync */ } } }

  /**
   * What the displayed text's live results must match. Undefined when the document
   * is not live; `synced` is false whenever a change is pending/in flight, so old
   * ranges are invalidated immediately on typing, before the debounce expires.
   */
  basis(path: string): LiveDocumentBasis | undefined {
    const d = this.docs.get(path);
    if (this.paused || !d || d.failed || d.confirmed < 1 || this.blocked.has(path)) return undefined;
    const synced = !d.pending && !d.inflight && !d.saving && d.timer === undefined && d.confirmed === d.version && d.latest === d.queuedText;
    return { documentId: d.id, version: d.confirmed, synced };
  }
  /** Failed synchronization is surfaced, never hidden behind fresh-looking results. */
  failure(path: string): string | undefined { return this.docs.get(path)?.failed; }
  get tracked(): string[] { return [...this.docs.keys()]; }
  get statuses(): LiveDocumentStatus[] {
    const att = this.deps.store.liveAttachment;
    if (!att || att.token !== this.token || this.paused) return [];
    return [...this.docs.values()].map(d => ({ path: d.path, documentId: d.id, failure: d.failed,
      canReopen: !!d.failed && !d.saving && !this.blocked.has(d.path) && d.recoveries < MAX_RECOVERIES && ['toolFailed', 'resyncRequired', 'invalidArgument', 'notFound', 'busy', 'limitExceeded'].includes(d.failed) }));
  }
  /** Explicit sync recovery, not a provider restart or a guarantee of fresh results. */
  reopen(path: string, documentId: string): void {
    const att = this.deps.store.liveAttachment;
    const d = this.docs.get(path);
    if (!att || att.token !== this.token || !d || d.id !== documentId || !this.statuses.some(s => s.documentId === documentId && s.path === path && s.canReopen)) return;
    this.recover(att, d);
  }

  // -- attachment ownership ------------------------------------------------

  /** Reconciles with the diagnostics store; call on its lifecycle notifications. */
  sync(): void { void this.ensure(); }

  private ensure(): Att | undefined {
    const current = this.deps.store.liveAttachment;
    const att = current && current.token !== this.retired ? current : undefined;
    if (!att) {
      // Same attachment, not live right now: keep lifecycles the backend still holds.
      // Forgetting them here made the next reconcile reopen held paths under fresh
      // ids ("Path already has an open document" -> invalidArgument).
      if (this.token !== undefined && !this.paused) this.pause();
      return undefined;
    }
    if (att.token === this.token) {
      this.owned = att;
      if (this.paused) this.resume_(att);
      return att;
    }
    // A different token is a different backend document namespace (detach,
    // reattach or revoke released the old one): replace, never reuse.
    this.invalidate();
    this.retired = undefined;
    this.token = att.token;
    this.owned = att;
    this.reconcileAll(att);
    return att;
  }

  private pause(): void {
    this.paused = true;
    for (const d of this.docs.values()) this.cancelTimer(d);
    this.changed_();
  }

  private resume_(att: Att): void {
    this.paused = false;
    this.reconcileAll(att);
    // Typing whose debounce was canceled by the pause is published now (a save still
    // in flight keeps holding it back, exactly as before the pause).
    for (const d of [...this.docs.values()]) if (!d.failed && !d.saving && d.latest !== d.queuedText) this.queueChange(att, d);
    this.changed_();
  }

  /** Replacement/revoke/disconnect: cancel everything; the old attachment owns no results. */
  private invalidate(): void {
    for (const d of this.docs.values()) this.cancelTimer(d);
    this.docs.clear();
    this.blocked.clear();
    this.paused = false;
    this.token = undefined;
    this.owned = undefined;
    this.changed_();
  }

  private reconcileAll(att: Att): void {
    const { dir, documents } = this.deps.source();
    if (dir !== att.directory) return;
    const shown = new Set<string>();
    for (const doc of documents) {
      if (!this.supported(doc.path)) continue;
      shown.add(doc.path);
      this.track(att, doc.path, doc.text, doc.revision);
    }
    for (const path of [...this.docs.keys()]) if (!shown.has(path)) this.closePath(att, path);
  }

  private for(dir: string): Att | undefined {
    const att = this.ensure();
    return att && att.directory === dir ? att : undefined;
  }

  /** Closing transitions also apply while paused: the owned attachment still holds the lifecycle. */
  private forClose(dir: string): Att | undefined {
    const att = this.for(dir) ?? (this.paused ? this.owned : undefined);
    return att && att.directory === dir ? att : undefined;
  }

  // -- FileLifecycleObserver ---------------------------------------------------

  diskState(dir: string, path: string, absent: boolean): void {
    const att = this.forClose(dir);
    if (!att) return;
    if (absent) {
      this.blocked.add(path);
      this.closePath(att, path);
    } else this.blocked.delete(path);
    this.changed_();
  }
  opened(dir: string, path: string, text: string, revision?: string): void {
    const att = this.for(dir);
    if (att) this.track(att, path, text, revision);
  }
  restored(dir: string): void {
    const att = this.for(dir);
    if (att) this.reconcileAll(att);
  }
  replaced(dir: string, path: string, text: string, revision?: string): void {
    const att = this.for(dir);
    if (!att) return;
    const d = this.docs.get(path);
    if (!d) { this.track(att, path, text, revision); return; }
    d.revision = revision ?? d.revision;
    d.latest = text;
    this.cancelTimer(d);
    // Committed transitions are published at once, without waiting for the debounce.
    if (d.saving) { this.changed_(); return; }
    this.queueChange(att, d);
  }
  changed(dir: string, path: string, text: string): void {
    const att = this.for(dir);
    if (!att) return;
    const d = this.docs.get(path);
    if (!d) { this.track(att, path, text); return; }
    d.latest = text;
    // Typing invalidates earlier ranges now; the network update is debounced.
    this.cancelTimer(d);
    if (!d.saving) d.timer = this.timers.set(() => {
      d.timer = undefined;
      if (this.token !== att.token || this.docs.get(path) !== d) return;
      this.queueChange(att, d);
    }, this.debounce);
    this.changed_();
  }
  beforeSave(dir: string, path: string, text: string): unknown {
    const att = this.for(dir);
    const d = this.docs.get(path);
    if (!att || !d || d.failed) return undefined;
    d.latest = text;
    this.cancelTimer(d);
    d.saving = true;
    // Flush the exact text being written so didSave describes that version only.
    if (text !== d.queuedText) this.assign(d, text);
    this.sendLater(att, d);
    this.changed_();
    return { id: d.id, version: d.version, token: att.token };
  }
  saved(dir: string, path: string, token: unknown, revision: string): void {
    const t = token as { id: string; version: number; token: string } | undefined;
    const d = this.docs.get(path);
    if (!t || !d || d.id !== t.id || this.token !== t.token) return;
    const att = this.deps.store.liveAttachment;
    if (!att || att.token !== t.token || att.directory !== dir) {
      // Settled while paused: the send window is unknown, so no didSave; typing resumes.
      if (this.paused) { d.saving = false; d.revision = revision; this.changed_(); }
      return;
    }
    d.saving = false;
    d.revision = revision;
    const { context, host } = att;
    this.enqueue(att, async () => {
      if (d.failed || this.docs.get(path) !== d) return;
      try { await this.deps.api.saveCodeDiagnosticDocument(context, d.id, t.version, revision, host); }
      catch (e) { this.fail(att, d, e); }
    });
    this.resume(att, d);
  }
  saveFailed(dir: string, path: string, token: unknown, _uncertain: boolean): void {
    // Failed or unknown outcomes never send didSave; only later typing resumes.
    const t = token as { id: string; token: string } | undefined;
    const d = this.docs.get(path);
    if (!t || !d || d.id !== t.id || this.token !== t.token) return;
    d.saving = false;
    const att = this.deps.store.liveAttachment;
    if (att && att.token === t.token && att.directory === dir) this.resume(att, d);
    else this.changed_();
  }
  removed(dir: string, path: string): void {
    const att = this.forClose(dir);
    if (att) this.closePath(att, path, true);
  }
  renamed(dir: string, from: string, to: string): void {
    const att = this.for(dir);
    if (!att) return;
    const matches = (p: string) => p === from || p.startsWith(from + '/');
    const moved = [...this.docs.values()].filter(d => matches(d.path));
    for (const d of moved) {
      // Close the old URI, then open a fresh lifecycle: old coordinates never carry over.
      this.closeDoc(att, d);
    }
    for (const d of moved) {
      const next = to + d.path.slice(from.length);
      this.track(att, next, d.latest, d.revision);
    }
  }
  uncertain(dir: string, path: string): void {
    const att = this.forClose(dir);
    if (!att) return;
    const matches = (p: string) => p === path || p.startsWith(path + '/');
    for (const d of [...this.docs.values()]) if (matches(d.path)) this.closeDoc(att, d);
    // Unknown outcome: no live claims for these paths until the files store resets.
    for (const doc of this.deps.source().documents) if (matches(doc.path)) this.blocked.add(doc.path);
    this.blocked.add(path);
    this.changed_();
  }
  cleared(): void {
    // While paused the owned attachment still holds these lifecycles: close them on
    // it, or a later reopen of the same path would be rejected as a duplicate.
    const live = this.deps.store.liveAttachment;
    const att = live && live.token === this.token ? live : this.paused ? this.owned : undefined;
    if (att) for (const d of [...this.docs.values()]) this.closeDoc(att, d);
    else for (const d of this.docs.values()) this.cancelTimer(d);
    this.docs.clear();
    this.blocked.clear();
    this.changed_();
  }
  interrupted(): void {
    // Cancel timers and invalidate now. Nothing is sent to the old route (no close,
    // no change); reconnect/host switch reattaches and reopens from the files store.
    // The old token is retired: until the diagnostics store replaces it, a hook must
    // not re-adopt it and reopen paths that route may still hold.
    const token = this.token ?? this.deps.store.liveAttachment?.token;
    this.invalidate();
    this.retired = token;
  }

  // -- document lifecycle ------------------------------------------------------

  /** Opens (or reconciles an already tracked) document with the displayed text. */
  private supported(path: string): boolean {
    const supports = this.deps.store.liveSupports;
    return !supports || supports.call(this.deps.store, path);
  }

  private track(att: Att, path: string, text: string, revision?: string): void {
    if (this.blocked.has(path) || !this.supported(path)) return;
    const existing = this.docs.get(path);
    if (existing) {
      existing.revision = revision ?? existing.revision;
      if (existing.latest !== text) {
        existing.latest = text;
        this.cancelTimer(existing);
        if (!existing.saving) this.queueChange(att, existing);
      }
      return;
    }
    const d: Doc = { path, id: this.newId(), revision, latest: text, queuedText: text, version: 1, confirmed: 0, sendQueued: false, inflight: false, saving: false, recoveries: 0 };
    this.docs.set(path, d);
    this.open(att, d);
  }

  private open(att: Att, d: Doc): void {
    const invalid = validText(d.latest);
    if (invalid) { d.failed = invalid; this.changed_(); return; }
    const { context, host } = att;
    const text = d.latest, revision = d.revision;
    d.queuedText = text;
    this.enqueue(att, async () => {
      if (this.docs.get(d.path) !== d || d.failed) return;
      d.inflight = true;
      try {
        await this.deps.api.openCodeDiagnosticDocument(context, d.path, d.id, 1, text, revision, host);
        if (this.token === att.token && this.docs.get(d.path) === d) d.confirmed = 1;
      } catch (e) { this.fail(att, d, e); }
      finally { d.inflight = false; this.changed_(); }
    });
    this.changed_();
  }

  /** Assigns the next version to `text` (monotonic within this document lifecycle). */
  private assign(d: Doc, text: string): void {
    d.version = d.version + 1;
    d.queuedText = text;
    d.pending = { version: d.version, text };
  }

  private queueChange(att: Att, d: Doc): void {
    if (d.failed) { this.recover(att, d); return; }
    if (d.latest !== d.queuedText) {
      if (d.version >= Number.MAX_SAFE_INTEGER) { this.recover(att, d); return; }
      const invalid = validText(d.latest);
      if (invalid) { d.failed = invalid; this.changed_(); return; }
      this.assign(d, d.latest);
    }
    this.sendLater(att, d);
    this.changed_();
  }

  /** One coalesced send per burst: later typing replaces an unstarted pending update. */
  private sendLater(att: Att, d: Doc): void {
    if (d.sendQueued || !d.pending) return;
    d.sendQueued = true;
    const { context, host } = att;
    this.enqueue(att, async () => {
      d.sendQueued = false;
      const p = d.pending;
      d.pending = undefined;
      if (!p || d.failed || this.docs.get(d.path) !== d) return;
      d.inflight = true;
      try {
        await this.deps.api.changeCodeDiagnosticDocument(context, d.id, p.version, p.text, host);
        if (this.token === att.token && this.docs.get(d.path) === d) d.confirmed = Math.max(d.confirmed, p.version);
      } catch (e) { this.fail(att, d, e); }
      finally { d.inflight = false; this.changed_(); }
    });
  }

  /** After a save settles, release typing that was held back to keep savedVersion exact. */
  private resume(att: Att, d: Doc): void {
    if (d.latest !== d.queuedText) {
      // Typing held back during the save resumes through the normal debounce.
      this.cancelTimer(d);
      d.timer = this.timers.set(() => {
        d.timer = undefined;
        if (this.token !== att.token || this.docs.get(d.path) !== d) return;
        this.queueChange(att, d);
      }, this.debounce);
    }
    this.changed_();
  }

  private closePath(att: Att, path: string, deleteBlock = false): void {
    for (const d of [...this.docs.values()]) {
      if (d.path === path) this.closeDoc(att, d);
    }
    if (deleteBlock) this.changed_();
  }

  private closeDoc(att: Att, d: Doc): void {
    this.cancelTimer(d);
    d.pending = undefined;
    if (this.docs.get(d.path) === d) this.docs.delete(d.path);
    const { context, host } = att;
    // A close is attempted even for an ambiguous open; a failure leaves nothing to retry.
    this.enqueue(att, async () => {
      try { await this.deps.api.closeCodeDiagnosticDocument(context, d.id, host); } catch { /* already closed or stale */ }
    });
    this.changed_();
  }

  private fail(att: Att, d: Doc, error: unknown): void {
    if (this.token !== att.token || this.docs.get(d.path) !== d) return;
    const reported = codeDiagnosticsError(error).code;
    const code = ['unsupported', 'disconnected', 'unauthorized', 'approvalRequired', 'approvalChanged', 'staleContext', 'invalidArgument', 'notFound', 'busy', 'limitExceeded', 'resyncRequired', 'toolFailed', 'canceled'].includes(reported) ? reported : 'toolFailed';
    d.failed = code;
    d.pending = undefined;
    this.changed_();
    // A desynchronized lifecycle is replaced, never patched; context errors wait for reattach.
    if (['resyncRequired', 'invalidArgument', 'notFound', 'busy'].includes(code)) this.recover(att, d);
  }

  /** Bounded: close the broken lifecycle and open a fresh documentId with the latest text. */
  private recover(att: Att, d: Doc): void {
    if (this.deps.store.liveAttachment?.token !== att.token || this.token !== att.token || this.docs.get(d.path) !== d || this.blocked.has(d.path) || d.recoveries >= MAX_RECOVERIES) return;
    if (d.failed && !['toolFailed', 'resyncRequired', 'invalidArgument', 'notFound', 'busy', 'limitExceeded'].includes(d.failed)) return;
    const { path, latest, revision, recoveries } = d;
    this.closeDoc(att, d);
    if (this.token !== att.token) return;
    const fresh: Doc = { path, id: this.newId(), revision, latest, queuedText: latest, version: 1, confirmed: 0, sendQueued: false, inflight: false, saving: false, recoveries: recoveries + 1 };
    this.docs.set(path, fresh);
    this.open(att, fresh);
  }

  private cancelTimer(d: Doc): void {
    if (d.timer !== undefined) { this.timers.clear(d.timer); d.timer = undefined; }
  }

  /** Strict global order; work for a replaced attachment is skipped, not sent. */
  private enqueue(att: Att, op: () => Promise<void>): void {
    this.chain = this.chain.then(() => (this.token === att.token ? op() : undefined)).catch(() => {});
  }
}
