import { api } from '../api';
import type {
  AgentID,
  BundleHandoffRequest,
  BundleRestoreRequest,
  BundleSummary,
  HandoffPreview,
  RestorePreview,
  SessionRef,
} from '../types';
import { copyToClipboard } from '../portable';
import { isStaleReply } from '../link';
import { copiedGuidance, openedGuidance } from '../guidance';
import { directoryNodeKey } from '../tree';
import { toast } from './toast.svelte';
import { link } from './link.svelte';
import { launcher } from './launcher.svelte';
import { terminals } from './terminals.svelte';
import { appState } from './appState.svelte';

export class ImporterStore {
  dialogOpen = $state(false);
  opening = $state(false);
  loading = $state(false);
  error = $state<string | null>(null);

  bundle = $state<BundleSummary | null>(null);
  activeTab = $state<'summary' | 'handoff'>('summary');

  /**
   * The directory on the selected host the bundle is imported into. A bundle
   * comes from another device, where the original path rarely exists, so the
   * user chooses it before the bundle is opened.
   */
  cwd = $state<string>('');

  /**
   * Set while the directory chooser is shown. The native folder dialog only
   * browses this computer, so a remote host gets this chooser instead.
   * 'start' opens a bundle next; 'change' replaces the directory.
   */
  chooser = $state<'start' | 'change' | null>(null);

  // Native import into cwd
  restorePreview = $state<RestorePreview | null>(null);
  restoreCopy = $state(false);
  loadingRestore = $state(false);
  restoreError = $state<string | null>(null);
  restoring = $state(false);

  // Handoff state from bundle
  target = $state<AgentID>('codex');
  budget = $state<number>(80000);
  includeReasoning = $state<boolean>(false);
  redactSecrets = $state<boolean>(false);

  loadingPreview = $state(false);
  preview = $state<HandoffPreview | null>(null);
  clipboardError = $state<string | null>(null);
  copiedPrompt = $state(false);
  copiedCommand = $state(false);
  launching = $state(false);
  saving = $state(false);
  savedPath = $state<string | null>(null);

  private copyPromptTimer: ReturnType<typeof setTimeout> | null = null;
  private copyCommandTimer: ReturnType<typeof setTimeout> | null = null;
  private requestSeq = 0;
  private restoreSeq = 0;

  /**
   * Starts an import: with dir (a directory in the sidebar) it goes straight
   * to the bundle picker, otherwise it asks for the directory first.
   */
  async start(dir?: string): Promise<void> {
    if (this.refuseWhileStale()) return;
    if (dir) {
      await this.open(dir);
      return;
    }
    this.chooser = 'start';
  }

  /** Lets the user choose another directory for the open bundle. */
  changeDirectory(): void {
    this.chooser = 'change';
  }

  /** The chooser's answer: a directory, or null when cancelled. */
  async chooseDirectory(dir: string | null): Promise<void> {
    const purpose = this.chooser;
    this.chooser = null;
    if (!dir) return;
    if (purpose === 'start') await this.open(dir);
    else await this.setCWD(dir);
  }

  /**
   * Shows the native folder dialog for the chooser's Browse button. It
   * browses this computer, so it is offered only when no remote host is
   * selected. Returns '' when cancelled.
   */
  async browseDirectory(): Promise<string> {
    try {
      return await api.pickImportDirectory();
    } catch (err) {
      if (isStaleReply(err)) return '';
      throw err;
    }
  }

  /** Picks a bundle file and opens it for import into dir. */
  async open(dir: string): Promise<void> {
    if (this.refuseWhileStale()) return;
    this.opening = true;
    this.error = null;
    try {
      const summary = await api.openBundle();
      if (!summary) {
        // User cancelled the file picker dialog
        return;
      }
      this.initBundle(summary, dir);
    } catch (err) {
      if (isStaleReply(err)) return;
      this.error = err instanceof Error ? err.message : String(err);
      this.dialogOpen = true;
    } finally {
      this.opening = false;
    }
  }

  // A bundle opened now would be read by a host that is not connected.
  private refuseWhileStale(): boolean {
    const reason = link.blockedReason;
    if (!reason) return false;
    this.bundle = null;
    this.error = reason;
    this.dialogOpen = true;
    return true;
  }

  initBundle(summary: BundleSummary, dir: string): void {
    this.bundle = summary;
    this.activeTab = 'summary';
    this.cwd = dir;
    this.budget = 80000;
    this.includeReasoning = false;
    this.redactSecrets = summary.profile === 'share-safe';
    this.error = null;
    this.clipboardError = null;
    this.copiedPrompt = false;
    this.copiedCommand = false;
    this.savedPath = null;
    this.preview = null;
    this.restorePreview = null;
    this.restoreCopy = false;
    this.restoreError = null;

    // Pick target different from bundle source agent
    const srcAgent = summary.source?.agent;
    if (srcAgent === 'claude-code') {
      this.target = 'codex';
    } else {
      this.target = 'claude-code';
    }

    this.dialogOpen = true;
    void this.refreshRestore();
  }

  close(): void {
    this.dialogOpen = false;
    this.chooser = null;
    this.bundle = null;
    this.preview = null;
    this.restorePreview = null;
    this.restoreError = null;
    this.error = null;
    this.clipboardError = null;
    this.savedPath = null;
  }

  reset(): void {
    if (this.copyPromptTimer) clearTimeout(this.copyPromptTimer);
    if (this.copyCommandTimer) clearTimeout(this.copyCommandTimer);
    this.close();
  }

  /** A complete bundle carries the agent's own files, so it can be imported natively. */
  get canRestore(): boolean {
    return Boolean(this.bundle?.restoreAvailable);
  }

  async setRestoreCopy(val: boolean): Promise<void> {
    if (this.restoreCopy === val) return;
    this.restoreCopy = val;
    await this.refreshRestore();
  }

  private restoreRequest(): BundleRestoreRequest | null {
    if (!this.bundle) return null;
    return { bundleId: this.bundle.bundleId, cwd: this.cwd, copy: this.restoreCopy };
  }

  /** Plans the native import into cwd, so the dialog can show what it writes. */
  async refreshRestore(): Promise<void> {
    const req = this.restoreRequest();
    const seq = ++this.restoreSeq;
    this.restorePreview = null;
    this.restoreError = null;
    if (!req || !this.canRestore) return;
    this.loadingRestore = true;
    try {
      const preview = await api.previewBundleRestore(req);
      if (this.restoreSeq === seq) this.restorePreview = preview;
    } catch (err) {
      if (isStaleReply(err)) return;
      if (this.restoreSeq === seq) this.restoreError = err instanceof Error ? err.message : String(err);
    } finally {
      if (this.restoreSeq === seq) this.loadingRestore = false;
    }
  }

  /** True when the previewed import can go ahead. */
  get restoreReady(): boolean {
    const items = this.restorePreview?.items ?? [];
    return items.length > 0 && items.every((it) => !it.blocked);
  }

  /**
   * Writes the session into the agent's store under cwd, then shows it in
   * the session list.
   */
  async importSession(): Promise<void> {
    const req = this.restoreRequest();
    const preview = this.restorePreview;
    if (!req || !preview || !this.restoreReady || this.restoring) return;
    req.copyId = preview.copyId;
    const cwd = this.cwd;
    this.restoring = true;
    this.restoreError = null;
    try {
      const report = await api.restoreBundle(req, preview.token);
      this.close();
      toast.show({
        title: 'Session imported',
        body: `It is in ${cwd} now; resume or continue it like any other session.`,
      });
      await this.reveal(report.createdRefs[0], cwd);
    } catch (err) {
      if (isStaleReply(err)) return;
      // Plan again: the token may have expired or the store changed since.
      const message = err instanceof Error ? err.message : String(err);
      await this.refreshRestore();
      this.restoreError = message;
    } finally {
      this.restoring = false;
    }
  }

  // Reloads the catalog and selects the imported session in its directory.
  private async reveal(ref: SessionRef | undefined, cwd: string): Promise<void> {
    await appState.refresh();
    if (!ref) return;
    // The scanner resolves symlinks, so its cwd may differ from the one typed.
    const scanned = await api.getSessionMeta(ref).then((m) => m.cwd).catch(() => '');
    const key = directoryNodeKey(appState.groups, scanned || cwd, ref.agent);
    if (key) {
      await appState.selectGroup(key, 'push');
      if (appState.selectedGroupKey !== key) return;
      await appState.selectSession(ref);
    } else {
      await appState.selectSession(ref, 'push');
    }
  }

  setTab(tab: 'summary' | 'handoff'): void {
    this.activeTab = tab;
    if (tab === 'handoff' && !this.preview && !this.loadingPreview) {
      void this.refreshPreview();
    }
  }

  get redactionForced(): boolean {
    return this.bundle?.profile === 'share-safe';
  }

  async setTarget(t: AgentID): Promise<void> {
    if (this.target === t) return;
    this.target = t;
    await this.refreshPreview();
  }

  async setBudget(b: number): Promise<void> {
    if (this.budget === b) return;
    this.budget = b;
    await this.refreshPreview();
  }

  async setIncludeReasoning(val: boolean): Promise<void> {
    if (this.includeReasoning === val) return;
    this.includeReasoning = val;
    await this.refreshPreview();
  }

  async setRedactSecrets(val: boolean): Promise<void> {
    if (this.redactSecrets === val) return;
    this.redactSecrets = val;
    await this.refreshPreview();
  }

  async setCWD(val: string): Promise<void> {
    this.cwd = val;
    const jobs = [this.refreshRestore()];
    if (this.preview || this.activeTab === 'handoff') jobs.push(this.refreshPreview());
    await Promise.all(jobs);
  }

  async refreshPreview(): Promise<void> {
    if (!this.bundle) return;
    const seq = ++this.requestSeq;
    this.loadingPreview = true;
    this.error = null;

    const req: BundleHandoffRequest = {
      bundleId: this.bundle.bundleId,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets || this.redactionForced,
      cwd: this.cwd || undefined,
    };

    try {
      const res = await api.buildBundleHandoff(req);
      if (this.requestSeq === seq) {
        this.preview = res;
      }
    } catch (err) {
      if (isStaleReply(err)) return;
      if (this.requestSeq === seq) {
        this.error = err instanceof Error ? err.message : String(err);
      }
    } finally {
      if (this.requestSeq === seq) {
        this.loadingPreview = false;
      }
    }
  }

  async copyPrompt(): Promise<void> {
    await this.copyCurrent('prompt');
  }

  /** Copies the content of the active preview tab. */
  async copyCurrent(tab: 'prompt' | 'full'): Promise<void> {
    this.clipboardError = null;
    const text = tab === 'full' ? this.preview?.fullMarkdown : this.preview?.promptMarkdown;
    if (!text) return;

    const ok = await copyToClipboard(text);
    if (!ok) {
      this.clipboardError = 'Failed to copy to clipboard. Please select and copy manually.';
      return;
    }

    this.copiedPrompt = true;
    toast.show(copiedGuidance(tab === 'full' ? 'doc' : 'prompt', { host: link.dataHost, agent: this.target }));
    if (this.copyPromptTimer) clearTimeout(this.copyPromptTimer);
    this.copyPromptTimer = setTimeout(() => {
      this.copiedPrompt = false;
    }, 2000);
  }

  async copyCommand(): Promise<void> {
    this.clipboardError = null;
    if (!this.bundle) return;

    const req: BundleHandoffRequest = {
      bundleId: this.bundle.bundleId,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets || this.redactionForced,
      cwd: this.cwd || undefined,
    };

    try {
      const cmd = await api.bundleHandoffCommand(req);
      const ok = await copyToClipboard(cmd);
      if (!ok) {
        this.clipboardError = 'Failed to copy command to clipboard. Please copy manually: ' + cmd;
        return;
      }
      this.copiedCommand = true;
      toast.show(copiedGuidance('launch', { host: link.dataHost, agent: this.target, shell: launcher.copyShell }));
      if (this.copyCommandTimer) clearTimeout(this.copyCommandTimer);
      this.copyCommandTimer = setTimeout(() => {
        this.copiedCommand = false;
      }, 2000);
    } catch (err) {
      if (isStaleReply(err)) return;
      this.clipboardError = err instanceof Error ? err.message : String(err);
    }
  }

  /**
   * Writes the handoff files and starts the target agent with them, in the
   * built-in panel or a new terminal as Settings chooses. The dialog closes
   * once the terminal is up.
   */
  async openInTerminal(): Promise<void> {
    this.clipboardError = null;
    if (!this.bundle || this.launching) return;

    const req: BundleHandoffRequest = {
      bundleId: this.bundle.bundleId,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets || this.redactionForced,
      cwd: this.cwd || undefined,
    };

    this.launching = true;
    try {
      if (launcher.openMode === 'panel') {
        if (!(await terminals.runBundleHandoff(req, this.bundle.source.cwd, this.bundle.source.title))) return;
      } else {
        await api.openBundleHandoffInTerminal(req);
        toast.show(openedGuidance('launch', this.target));
      }
      this.close();
    } catch (err) {
      if (isStaleReply(err)) return;
      this.clipboardError = err instanceof Error ? err.message : String(err);
    } finally {
      this.launching = false;
    }
  }

  async saveHandoff(): Promise<void> {
    if (!this.bundle) return;
    this.saving = true;
    this.error = null;
    this.savedPath = null;

    const req: BundleHandoffRequest = {
      bundleId: this.bundle.bundleId,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets || this.redactionForced,
      cwd: this.cwd || undefined,
    };

    try {
      const path = await api.saveBundleHandoff(req);
      if (path) this.savedPath = path;
    } catch (err) {
      if (isStaleReply(err)) return;
      this.error = err instanceof Error ? err.message : String(err);
    } finally {
      this.saving = false;
    }
  }
}

export const importer = new ImporterStore();
