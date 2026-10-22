import { api } from '../api';
import type { ManagedToolRequest, ManagedToolState } from '../managedTools';
import type { LiveDocumentStatus } from '../liveDocuments';
import { aggregateCodeDiagnostics } from '../codeDiagnosticCounts';
import { epoch } from '../link';
import type { BackendAPI } from '../api';
import type { ConnectionState } from '../types';
import type { LiveDocumentBasis, CodeDiagnosticsContext, CodeDiagnosticsProject, CodeDiagnosticsSnapshot, CodeDiagnosticsUpdate, CodeDiagnosticsRun } from '../codeDiagnostics';
import { CODE_DIAGNOSTICS_EVENT, CODE_DIAGNOSTICS_LIMITS as limits, CodeDiagnosticsError, codeDiagnosticsError, sameAttachment, sameContext, encodedBytes, resultSetKey, groupCodeDiagnostics, inlineCodeDiagnostic, liveDocumentEligibility } from '../codeDiagnostics';

type Assembly = { parts: Map<number, CodeDiagnosticsUpdate>; bytes: number; timer: ReturnType<typeof setTimeout> };

/** Owns results only. No editor imports, source text, drafts, tabs or persistence. */
export class CodeDiagnosticsStore {
  project = $state.raw<CodeDiagnosticsProject | undefined>(undefined);
  directory = $state<string | undefined>(undefined);
  snapshot = $state.raw<CodeDiagnosticsSnapshot | undefined>(undefined);
  status = $state<'detached' | 'attaching' | 'ready' | 'unsupported' | 'resyncing' | 'disconnected' | 'error'>('detached');
  error = $state<CodeDiagnosticsError | undefined>(undefined);
  pending = $state<string | undefined>(undefined);
  revoking = $state(false);
  private pendingRuns = $state.raw<CodeDiagnosticsRun[]>([]);
  private context?: CodeDiagnosticsContext;
  managed = $state.raw<ManagedToolState | undefined>(undefined);
  managedPending = $state(false);
  managedNotice = $state('');
  managedDismissed = $state(false);
  private managedTimer?: ReturnType<typeof setTimeout>;
  private managedPolls = 0;
  private clearManaged() {
    clearTimeout(this.managedTimer); this.managedTimer = undefined;
    this.managed = undefined; this.managedPending = false; this.managedNotice = ''; this.managedDismissed = false;
    this.managedPolls = 0;
  }
  /** Only bounded read-only polling is repeated; consent mutations are never replayed. */
  private scheduleManaged() {
    clearTimeout(this.managedTimer);
    if (this.managed?.state !== 'installing') return;
    if (++this.managedPolls > 300) { this.managedNotice = 'Progress polling paused. Refresh status to resume; installation may still be running.'; return; }
    const life = this.lifetime;
    this.managedTimer = setTimeout(() => { if (this.active(life) && this.context) void this.managedAction('status'); }, 2000);
  }
  async managedAction(action: ManagedToolRequest['action']) {
    if (!this.context || !this.active() || this.managedPending) return;
    const life = this.lifetime, c = this.context, host = this.host;
    const current = this.managed;
    const proposal = current?.proposal;
    if (action === 'install' && (current?.state !== 'proposed' || !proposal)) return;
    if (action === 'install' && Date.parse(proposal!.expires) <= Date.now()) {
      this.managed = undefined; this.managedNotice = 'Approval expired. Review a fresh installation proposal.'; return;
    }
    if (action === 'cancel' && (!current?.jobId || current.state !== 'installing')) return;
    if (action === 'propose' && current?.state === 'installing') return;
    this.managedPending = true; this.managedNotice = '';
    clearTimeout(this.managedTimer);
    // Consume locally before sending. Lost acknowledgements require status, never token replay.
    if (action === 'install') this.managed = undefined;
    if (action === 'dismiss') { this.managedDismissed = true; this.managed = undefined; }
    const valid = () => this.active(life) && !!this.context && sameContext(c, this.context!);
    try {
      if (!this.backend.managedTool) throw new Error('Managed setup unavailable on this older backend. Ask the backend operator to provision reviewed Go >=1.27.');
      const out = await this.backend.managedTool({ action, expectedHostId: host,
        ...(action === 'propose' ? { tool: 'gopls' as const } : {}),
        ...(action === 'install' ? { token: proposal!.token } : {}),
        ...(['status', 'cancel'].includes(action) && current?.jobId ? { jobId: current.jobId } : {}) });
      if (!valid()) return;
      if (out.hostId !== host || (out.proposal && (out.state !== 'proposed' || out.proposal.hostId !== host || out.proposal.tool !== 'gopls' || !out.proposal.token || !out.proposal.connectionId || !Number.isFinite(Date.parse(out.proposal.expires))))) throw new Error('Managed setup identity mismatch. Reconnect to the intended host.');
      if (out.state === 'installing' && (!out.jobId || (current?.jobId && action === 'status' && out.jobId !== current.jobId))) throw new Error('Installation job identity mismatch. Refresh status explicitly.');
      this.managed = out;
      this.scheduleManaged();
    } catch (e) {
      if (valid()) { this.managed = undefined; this.managedNotice = `Managed setup unavailable: ${codeDiagnosticsError(e).message}. Refresh status before retrying; no installation request will be replayed.`; }
    } finally { if (valid()) this.managedPending = false; }
  }
  refreshManaged() { this.managedPolls = 0; this.managedDismissed = false; return this.managedAction('status'); }

  get approved() { return this.status === 'ready' && !this.revoking && this.snapshot?.approval.state === 'approved'; }
  get running() { return this.pending === 'run' || this.pendingRuns.length > 0 || this.providers.some(p => ['starting', 'checking'].includes(p.phase)); }
  get canRun() { return this.approved && !this.pending && !this.running && this.providers.some(p => p.availability === 'available' && p.coverage.kind === 'savedProject'); }
  canRetry(providerId: string) { return this.canRun && this.providers.some(p => p.providerId === providerId && p.availability === 'available' && p.coverage.kind === 'savedProject'); }
  /** Visibility discovery is asynchronous; only the latest visible route may attach. */
  async show(dir: string) {
    void this.detach();
    const life = this.lifetime, epoch = this.currentEpoch();
    this.status = 'attaching';
    try {
      const connection = await this.backend.connectionState();
      if (life === this.lifetime && epoch === this.currentEpoch()) await this.attach(dir, connection);
    } catch (e) { if (life === this.lifetime && epoch === this.currentEpoch()) this.fail(e); }
  }
  private lifetime = 0;
  private startedEpoch = 0;
  private off?: () => void;
  private offConnection?: () => void;
  private assemblies = new Map<string, Assembly>();
  private early: CodeDiagnosticsUpdate[] = [];
  private earlyBytes = 0;
  private earlyOverflow = false;
  private recovering = false;
  private attempts = 0;
  private host = '';
  private retiredGenerations = new Set<string>();
  private liveCapable = $state(false);
  /** The attached host advertises live document diagnostics (copy only; never a grant). */
  get liveEnabled(): boolean { return this.liveCapable; }
  private listeners = new Set<() => void>();
  private reconcileFiles: () => Promise<void> = async () => {};
  private liveRevision = $state(0);
  private readLiveBasis: (path: string) => LiveDocumentBasis | undefined = () => undefined;
  /** Bridge-owned protocol identity only; never text or editor state. */
  setLiveBasis(read: (path: string) => LiveDocumentBasis | undefined): void { this.readLiveBasis = read; }
  private readLiveStatus: () => LiveDocumentStatus[] = () => [];
  private reopenLive: (path: string, documentId: string) => void = () => {};
  setLiveRecovery(read: () => LiveDocumentStatus[], reopen: (path: string, documentId: string) => void): void {
    this.readLiveStatus = read; this.reopenLive = reopen;
  }
  get liveDocuments(): LiveDocumentStatus[] {
    this.liveRevision;
    return this.liveAttachment ? this.readLiveStatus() : [];
  }
  reopenDocument(path: string, documentId: string): void {
    if (this.liveDocuments.some(d => d.path === path && d.documentId === documentId && d.canReopen)) this.reopenLive(path, documentId);
  }
  liveChanged(): void {
    this.liveRevision++;
    // A failed lifecycle cannot regain freshness just because a replacement opens.
    // Only a subsequent authoritative publication can replace its retained sets.
    if (this.snapshot && this.liveDocuments.some(d => d.failure)) this.snapshot = { ...this.snapshot, resultSets: this.resultSets };
  }
  liveBasis(path: string): LiveDocumentBasis | undefined {
    this.liveRevision;
    return this.liveAttachment ? this.readLiveBasis(path) : undefined;
  }
  async invalidatePath(dir: string, path: string): Promise<void> {
    const c = this.context, life = this.lifetime;
    if (!c || this.directory !== dir || !this.active(life)) return;
    try {
      if (!this.backend.invalidateCodeDiagnosticsPath) throw new CodeDiagnosticsError('unsupported', 'Diagnostics invalidation unavailable');
      const s = await this.backend.invalidateCodeDiagnosticsPath(c, path, this.host);
      if (this.active(life) && this.context && sameContext(c, this.context)) this.commit(s);
    } catch (e) { if (this.active(life)) this.fail(e); }
  }
  /** Read-only files-store reconciliation; no discovery or language-server startup. */
  setReconciler(reconcile: () => Promise<void>): void { this.reconcileFiles = reconcile; }

  /**
   * Identity of the attachment that may carry live document operations: only while
   * approved, attached to a host that advertises liveCodeDiagnostics. Absent otherwise,
   * which keeps every document bridge inert. `token` changes with any lifetime or
   * generation replacement. The store keeps no text.
   */
  get liveAttachment(): { context: CodeDiagnosticsContext; host: string; directory: string; token: string } | undefined {
    if (!this.liveCapable || !this.approved || !this.context || !this.directory || !this.active()) return undefined;
    return { context: this.context, host: this.host, directory: this.directory, token: `${this.lifetime}:${this.context.generation}` };
  }
  /** Notified (synchronously, errors swallowed) whenever attachment state may have changed. */
  onLifecycle(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => { this.listeners.delete(listener); };
  }
  private emit() { for (const l of [...this.listeners]) { try { l(); } catch { /* observers never affect protocol state */ } } }

  constructor(private backend: BackendAPI = api, private currentEpoch: () => number = epoch) {}

  get resultSets() {
    const failedProviders = new Set(this.providers.filter(p => p.failure || p.availability !== 'available' || ['failed', 'canceled', 'disconnected'].includes(p.phase)).map(p => p.providerId));
    const failedDocuments = new Set(this.liveDocuments.filter(d => d.failure).map(d => d.documentId));
    return (this.snapshot?.resultSets ?? []).map(s => failedProviders.has(s.providerId) || (s.scope.kind === 'document' && failedDocuments.has(s.scope.documentId))
      ? { ...s, diagnostics: s.diagnostics.map(d => ({ ...d, freshness: 'stale' as const })) } : s);
  }
  get providers() { return this.snapshot?.providers ?? []; }
  private grouped = $derived(groupCodeDiagnostics(this.resultSets));
  private aggregated = $derived(aggregateCodeDiagnostics(this.grouped));
  get findings() { return this.grouped; }
  get pathCounts() { return this.aggregated; }
  get partial() { return this.status !== 'ready' || this.providers.length === 0 || this.liveDocuments.some(d => d.failure) || this.providers.some(p => p.truncated || p.failure || p.coverage.partial || p.availability !== 'available' || ['failed', 'canceled', 'disconnected'].includes(p.phase)); }
  get truncated() { return this.providers.some(p => p.truncated); }
  get counts() {
    const { error, warning } = this.pathCounts.total;
    return { error, warning };
  }
  countsUnder(path: string) {
    const counts = path ? this.pathCounts.folders.get(path) ?? this.pathCounts.files.get(path) : this.pathCounts.total;
    return counts ? counts.error + counts.warning : 0;
  }
  inline(path: string, saved: { dirty: boolean; revision?: string }) {
    if (this.status !== 'ready') return [];
    return this.findings.flatMap(f => {
      const eligible = f.entries.find(e => e.diagnostic.path === path && inlineCodeDiagnostic(e.diagnostic, saved));
      return eligible ? [{ ...f, diagnostic: eligible.diagnostic }] : [];
    });
  }
  /**
   * Live document findings drawn over the displayed text: only versioned results
   * of the same document lifecycle whose version equals the confirmed, unchanged
   * version. Everything else stays panel-only (see liveDocumentEligibility).
   */
  liveInline(path: string, doc: LiveDocumentBasis | undefined) {
    if (this.status !== 'ready' || !doc) return [];
    return this.findings.flatMap(f => {
      const eligible = f.entries.find(e => e.diagnostic.path === path && liveDocumentEligibility(e.diagnostic, doc) === 'current');
      return eligible ? [{ ...f, diagnostic: eligible.diagnostic }] : [];
    });
  }
  private active(lifetime = this.lifetime) { return lifetime === this.lifetime && this.startedEpoch === this.currentEpoch(); }
  private clearBuffers() {
    for (const a of this.assemblies.values()) clearTimeout(a.timer);
    this.assemblies.clear();
    this.early = []; this.earlyBytes = 0;
  }
  private stale(disconnected = false) {
    if (!this.snapshot) return;
    this.snapshot = { ...this.snapshot, providers: this.providers.map(p => ({ ...p, freshness: 'stale', ...(disconnected ? { phase: 'disconnected' as const } : {}) })), resultSets: this.resultSets.map(s => ({ ...s, diagnostics: s.diagnostics.map(d => ({ ...d, freshness: 'stale' })) })) };
  }
  /** Hide/replacement invalidates ownership before asynchronous detach. */
  async detach() {
    const c = this.context, host = this.host;
    this.lifetime++; this.context = undefined;
    // Hiding only stops observation; an explicitly approved install remains backend-owned.
    this.clearManaged();
    this.off?.(); this.offConnection?.(); this.off = this.offConnection = undefined;
    this.clearBuffers(); this.recovering = false; this.attempts = 0;
    this.directory = undefined;
    this.project = undefined; this.snapshot = undefined; this.status = 'detached'; this.error = undefined;
    this.pending = undefined; this.revoking = false; this.pendingRuns = [];
    this.liveCapable = false;
    this.emit();
    if (c) { try { await this.backend.detachCodeDiagnostics(c, host); } catch { /* Never retry mutations across a connection change. */ } }
  }
  hide() { return this.detach(); }
  /** Called on any route change/drop; retained findings are explicitly stale. */
  disconnect() {
    this.lifetime++; this.context = undefined;
    // Hiding only stops observation; an explicitly approved install remains backend-owned.
    this.clearManaged();
    this.off?.(); this.offConnection?.(); this.off = this.offConnection = undefined;
    this.clearBuffers(); this.recovering = false;
    this.pending = undefined; this.pendingRuns = []; this.revoking = false;
    this.status = 'disconnected'; this.stale(true);
    this.liveCapable = false;
    this.emit();
  }
  async attach(dir: string, connection: ConnectionState) {
    // Invalidate synchronously, but do not make rendering wait for old detach.
    void this.detach();
    this.startedEpoch = this.currentEpoch();
    const life = this.lifetime;
    if (!connection.capabilities.codeDiagnostics || !connection.hostId || !['local', 'connected'].includes(connection.phase)) {
      this.status = 'unsupported'; return;
    }
    this.liveCapable = !!connection.capabilities.liveCodeDiagnostics;
    this.host = connection.hostId; this.status = 'attaching'; this.earlyOverflow = false; this.retiredGenerations.clear();
    this.off = this.backend.onEvent(CODE_DIAGNOSTICS_EVENT, (event: CodeDiagnosticsUpdate) => {
      if (!this.active(life)) return;
      if (!this.context) {
        const bytes = encodedBytes(event);
        const updateIds = new Set(this.early.map(e => e.updateId));
        updateIds.add(event.updateId);
        if (updateIds.size > limits.updates || bytes > limits.partBytes || this.early.length >= limits.parts * limits.updates || this.earlyBytes + bytes > limits.bufferBytes) { this.earlyOverflow = true; this.early = []; this.earlyBytes = 0; return; }
        if (!this.earlyOverflow) { this.early.push(structuredClone(event)); this.earlyBytes += bytes; }
      } else this.receive(event);
    });
    this.offConnection = this.backend.onEvent('connection:state', (next: ConnectionState) => {
      if (next.generation !== connection.generation || next.phase !== connection.phase || next.host !== connection.host || next.hostId !== this.host) this.disconnect();
    });
    try {
      const attachment = await this.backend.attachCodeDiagnostics(dir, 1, this.host);
      if (!this.active(life)) {
        if (this.startedEpoch === this.currentEpoch()) void this.backend.detachCodeDiagnostics(attachment.context, attachment.project.hostId).catch(() => {});
        return;
      }
      if (attachment.project.hostId !== this.host || !sameContext(attachment.context, attachment.snapshot.context)) throw new CodeDiagnosticsError('staleContext', 'Attachment identity mismatch');
      this.directory = dir;
      this.project = attachment.project; this.context = attachment.context;
      if (!this.commit(attachment.snapshot, true)) throw new CodeDiagnosticsError('resyncRequired', 'Invalid attachment snapshot');
      const early = this.early; this.early = []; this.earlyBytes = 0;
      for (const event of early) this.receive(event);
      if (this.earlyOverflow) this.resync();
      // Optional App contract is discovered by a read-only status call, not a fabricated capability.
      void this.managedAction('status');
    } catch (e) { if (this.active(life)) this.fail(e); }
  }
  private fail(e: unknown) {
    this.error = codeDiagnosticsError(e);
    if (this.error.code === 'disconnected' || this.error.code === 'staleContext') { this.disconnect(); return; }
    this.status = this.error.code === 'unsupported' ? 'unsupported' : 'error'; this.stale();
    this.emit();
  }
  private validSnapshot(s: CodeDiagnosticsSnapshot) {
    return !!s && s.contractVersion === 1 && !!s.context && !!s.approval && Number.isSafeInteger(s.sequence) && s.sequence >= 0 && Array.isArray(s.providers) && s.providers.length <= 32 && Array.isArray(s.resultSets) && s.resultSets.every(r => Array.isArray(r.diagnostics)) && s.resultSets.reduce((n, r) => n + r.diagnostics.length, 0) <= limits.findings && encodedBytes(s) <= limits.snapshotBytes;
  }
  private commit(s: CodeDiagnosticsSnapshot, initial = false, revoke = false): boolean {
    if (!this.context || !this.validSnapshot(s) || !sameAttachment(this.context, s.context)) return false;
    if (!initial && this.snapshot && s.sequence <= this.snapshot.sequence) return false;
    if (!sameContext(this.context, s.context)) {
      if (!revoke || s.approval.state !== 'revoked' || this.retiredGenerations.has(s.context.generation)) return false;
      this.retiredGenerations.add(this.context.generation);
      this.context = s.context; this.clearBuffers(); this.clearManaged();
    }
    // Run acknowledgements never install result sets: sequence-ordered full snapshots
    // are authoritative, including retained old-run sets on failure/cancellation.
    if (s.approval.state === 'revoked') this.revoking = false;
    this.pendingRuns = this.pendingRuns.filter(r => s.approval.state === 'approved' && !s.providers.some(p => p.providerId === r.providerId && p.runId === r.runId));
    this.snapshot = structuredClone(s); this.status = 'ready'; this.error = undefined;
    this.pruneAssemblies(s.sequence);
    this.emit();
    return true;
  }
  private pruneAssemblies(sequence: number) {
    for (const [key, assembly] of this.assemblies) {
      const first = assembly.parts.values().next().value;
      if (first && first.sequence <= sequence) { clearTimeout(assembly.timer); this.assemblies.delete(key); }
    }
  }
  private receive(event: CodeDiagnosticsUpdate) {
    if (!this.context || !event?.context || !sameAttachment(this.context, event.context)) return;
    if (event.sequence <= (this.snapshot?.sequence ?? -1)) return;
    const newGeneration = !sameContext(this.context, event.context);
    if (newGeneration && (event.snapshot?.approval.state !== 'revoked' || this.retiredGenerations.has(event.context.generation))) return;
    if (event.contractVersion !== 1 || !Number.isSafeInteger(event.sequence) || !Number.isSafeInteger(event.partCount) || event.partCount < 1 || event.partCount > limits.parts || !Number.isSafeInteger(event.partIndex) || event.partIndex < 0 || event.partIndex >= event.partCount || !event.updateId || !sameContext(event.context, event.snapshot.context) || event.sequence !== event.snapshot.sequence) { this.resync(); return; }
    const bytes = encodedBytes(event);
    if (bytes > limits.partBytes || (!newGeneration && event.sequence !== (this.snapshot?.sequence ?? -1) + 1)) { this.resync(); return; }
    const key = event.updateId;
    let assembly = this.assemblies.get(key);
    if (!assembly) {
      if (this.assemblies.size >= limits.updates) { this.resync(); return; }
      const life = this.lifetime;
      assembly = { parts: new Map(), bytes: 0, timer: setTimeout(() => { if (this.active(life)) this.resync(); }, limits.timeout) };
      this.assemblies.set(key, assembly);
    }
    const first = assembly.parts.values().next().value;
    const metadata = (e: CodeDiagnosticsUpdate) => JSON.stringify([e.contractVersion, e.context, e.sequence, e.partCount, { ...e.snapshot, resultSets: [] }]);
    if (first && metadata(first) !== metadata(event)) { this.resync(); return; }
    const duplicate = assembly.parts.get(event.partIndex);
    if (duplicate) { if (JSON.stringify(duplicate) !== JSON.stringify(event)) this.resync(); return; }
    const total = [...this.assemblies.values()].reduce((n, a) => n + a.bytes, 0);
    if (total + bytes > limits.bufferBytes) { this.resync(); return; }
    assembly.parts.set(event.partIndex, structuredClone(event)); assembly.bytes += bytes;
    if (assembly.parts.size !== event.partCount) return;
    const sets = new Map<string, CodeDiagnosticsSnapshot['resultSets'][number]>();
    for (let i = 0; i < event.partCount; i++) for (const set of assembly.parts.get(i)!.snapshot.resultSets) {
      const key = resultSetKey(set), previous = sets.get(key);
      if (previous) previous.diagnostics.push(...set.diagnostics);
      else sets.set(key, structuredClone(set));
    }
    clearTimeout(assembly.timer); this.assemblies.delete(key);
    const previous = this.snapshot;
    if (!this.commit({ ...event.snapshot, resultSets: [...sets.values()] }, false, newGeneration)) { this.resync(); return; }
    if (this.watchedRunChanged(previous, this.snapshot)) this.reconcileWatched();
  }
  /** A watching provider published a new run: saved files changed on disk. */
  private watchedRunChanged(before: CodeDiagnosticsSnapshot | undefined, after: CodeDiagnosticsSnapshot | undefined) {
    if (!before || !after || !sameContext(before.context, after.context)) return false;
    return after.providers.some(p => p.coverage.externalChanges === 'watching' && p.runId !== undefined
      && after.resultSets.find(s => s.providerId === p.providerId)?.runId !== before.resultSets.find(s => s.providerId === p.providerId)?.runId);
  }
  private watchReconciling = false;
  private watchReconcileAgain = false;
  /**
   * Coalesced, read-only reconciliation of displayed tabs after an external change.
   * Never saves; drafts and uncertain saves stay editor-owned (files store guards).
   * At most one in flight plus one trailing pass; stops on lifetime change.
   */
  private reconcileWatched() {
    if (this.watchReconciling) { this.watchReconcileAgain = true; return; }
    const life = this.lifetime;
    this.watchReconciling = true;
    void (async () => {
      try {
        do {
          this.watchReconcileAgain = false;
          try { await this.reconcileFiles(); } catch { /* reconciliation never affects diagnostics */ }
        } while (this.watchReconcileAgain && this.active(life));
      } finally { this.watchReconciling = false; this.watchReconcileAgain = false; }
    })();
  }
  private resync() {
    this.clearBuffers(); this.stale();
    if (!this.context) return;
    this.status = 'resyncing';
    this.emit();
    if (this.recovering) return;
    if (this.attempts >= limits.retries) { this.status = 'error'; this.error = new CodeDiagnosticsError('resyncRequired', 'Diagnostics recovery exhausted; retry explicitly'); return; }
    this.recovering = true; this.attempts++;
    const life = this.lifetime, c = this.context;
    void this.backend.getCodeDiagnosticsSnapshot(c, this.host).then(s => {
      if (!this.active(life) || !this.context || !sameContext(c, this.context)) return;
      if (!sameContext(c, s.context) || !this.validSnapshot(s)) throw new CodeDiagnosticsError('resyncRequired', 'Invalid recovery snapshot');
      this.commit(s);
      // An equal authoritative snapshot also resolves uncertainty.
      if (this.snapshot && s.sequence >= this.snapshot.sequence) { this.snapshot = structuredClone(s); this.status = 'ready'; this.error = undefined; }
    }).catch(e => { if (this.active(life)) this.fail(e); }).finally(() => { if (this.active(life)) this.recovering = false; });
  }
  /** Explicit recovery starts a fresh bounded retry budget, never replays mutations. */
  async refresh() {
    const life = this.lifetime;
    await this.reconcileFiles();
    if (!this.active(life)) return;
    this.attempts = 0; this.resync();
  }
  private async action<T>(operation: (c: CodeDiagnosticsContext, host: string) => Promise<T>, apply?: (value: T) => void) {
    if (!this.context || !this.active()) throw new CodeDiagnosticsError('staleContext', 'Attach diagnostics again');
    const life = this.lifetime, c = this.context;
    try {
      const value = await operation(c, this.host);
      if (this.active(life) && this.context && sameContext(c, this.context)) apply?.(value);
      return value;
    } catch (e) { if (this.active(life) && this.context && sameContext(c, this.context)) this.fail(e); throw e; }
  }
  private async request<T>(kind: string, work: () => Promise<T>) {
    if (this.pending && kind !== 'revoke') throw new CodeDiagnosticsError('busy', 'Request already pending');
    const life = this.lifetime;
    this.pending = kind;
    try { return await work(); }
    finally { if (this.active(life) && this.pending === kind) this.pending = undefined; }
  }
  approve(mode: 'session' | 'persistent', fingerprint = this.snapshot?.proposal?.fingerprint ?? '') {
    return this.request('approve', () => this.action((c, h) => this.backend.approveCodeDiagnostics(c, mode, fingerprint, h), s => { this.commit(s); }));
  }
  revoke() {
    this.revoking = true; // Fail closed immediately, even if the reply is lost.
    this.emit();
    return this.request('revoke', () => this.action((c, h) => this.backend.revokeCodeDiagnostics(c, h), s => { if (this.commit(s, false, true)) this.revoking = false; }));
  }
  async run(providerIds?: string[]) {
    if (!this.approved) throw new CodeDiagnosticsError('approvalRequired', 'Approve code checks before running');
    if (this.running) throw new CodeDiagnosticsError('busy', 'Check already active');
    const checkerIds = this.providers.filter(p => p.availability === 'available' && p.coverage.kind === 'savedProject' && (!providerIds || providerIds.includes(p.providerId))).map(p => p.providerId);
    if (!checkerIds.length || providerIds?.some(id => !checkerIds.includes(id))) throw new CodeDiagnosticsError('unsupported', 'No available saved-file checker; live providers cannot be retried with Check Project');
    return this.request('run', async () => {
      const life = this.lifetime;
      await this.reconcileFiles();
      if (!this.active(life)) throw new CodeDiagnosticsError('staleContext', 'Attach diagnostics again');
      if (!this.approved) throw new CodeDiagnosticsError('approvalRequired', 'Approve code checks before running');
      // Only the checker reads disk. Reconciliation does not save drafts, send
      // didSave, or certify unversioned LSP publications.
      return this.action((c, h) => this.backend.runCodeDiagnostics(c, checkerIds, h), reply => {
        this.pendingRuns = reply.runs.filter(r => !this.providers.some(p => p.providerId === r.providerId && p.runId === r.runId));
      });
    });
  }
  retry(providerId: string) { return this.run([providerId]); }
  cancel(runs: CodeDiagnosticsRun[] = [...this.pendingRuns, ...this.providers.flatMap(p => p.runId && ['starting', 'checking'].includes(p.phase) ? [{ providerId: p.providerId, runId: p.runId }] : [])]) {
    return this.request('cancel', () => this.action((c, h) => this.backend.cancelCodeDiagnostics(c, runs, h)));
  }
}

export const codeDiagnostics = new CodeDiagnosticsStore();
