import { api } from '../api';
import type { AgentID, BundleHandoffRequest, BundleSummary, HandoffPreview } from '../types';
import { copyToClipboard } from '../portable';
import { isStaleReply } from '../link';
import { copiedGuidance, openedGuidance } from '../guidance';
import { toast } from './toast.svelte';
import { link } from './link.svelte';
import { launcher } from './launcher.svelte';

export class ImporterStore {
  dialogOpen = $state(false);
  opening = $state(false);
  loading = $state(false);
  error = $state<string | null>(null);

  bundle = $state<BundleSummary | null>(null);
  activeTab = $state<'summary' | 'handoff'>('summary');

  // Handoff state from bundle
  target = $state<AgentID>('codex');
  budget = $state<number>(80000);
  includeReasoning = $state<boolean>(false);
  redactSecrets = $state<boolean>(false);
  cwd = $state<string>('');

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

  async open(): Promise<void> {
    if (this.refuseWhileStale()) return;
    this.opening = true;
    this.error = null;
    try {
      const summary = await api.openBundle();
      if (!summary) {
        // User cancelled the file picker dialog
        return;
      }
      this.initBundle(summary);
    } catch (err) {
      if (isStaleReply(err)) return;
      this.error = err instanceof Error ? err.message : String(err);
      this.dialogOpen = true;
    } finally {
      this.opening = false;
    }
  }

  async openPath(path: string): Promise<void> {
    if (this.refuseWhileStale()) return;
    this.opening = true;
    this.error = null;
    try {
      const summary = await api.openBundlePath(path);
      this.initBundle(summary);
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

  initBundle(summary: BundleSummary): void {
    this.bundle = summary;
    this.activeTab = 'summary';
    this.cwd = summary.source?.cwd || '';
    this.budget = 80000;
    this.includeReasoning = false;
    this.redactSecrets = summary.profile === 'share-safe';
    this.error = null;
    this.clipboardError = null;
    this.copiedPrompt = false;
    this.copiedCommand = false;
    this.savedPath = null;
    this.preview = null;

    // Pick target different from bundle source agent
    const srcAgent = summary.source?.agent;
    if (srcAgent === 'claude-code') {
      this.target = 'codex';
    } else {
      this.target = 'claude-code';
    }

    this.dialogOpen = true;
  }

  close(): void {
    this.dialogOpen = false;
    this.bundle = null;
    this.preview = null;
    this.error = null;
    this.clipboardError = null;
    this.savedPath = null;
  }

  reset(): void {
    if (this.copyPromptTimer) clearTimeout(this.copyPromptTimer);
    if (this.copyCommandTimer) clearTimeout(this.copyCommandTimer);
    this.close();
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
    await this.refreshPreview();
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
   * Writes the handoff files and opens a terminal that starts the target
   * agent with them. The dialog closes once the terminal is up.
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
      await api.openBundleHandoffInTerminal(req);
      toast.show(openedGuidance('launch', this.target));
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
