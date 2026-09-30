import { api } from '../api';
import type { AgentID, HandoffPreview, HandoffRequest, SessionMeta } from '../types';
import { copyToClipboard } from '../portable';
import { isStaleReply } from '../link';

export class HandoffStore {
  dialogOpen = $state(false);
  session = $state<SessionMeta | null>(null);
  target = $state<AgentID>('codex');
  budget = $state<number>(80000);
  includeReasoning = $state<boolean>(false);
  redactSecrets = $state<boolean>(false);
  cwd = $state<string>('');

  loading = $state(false);
  error = $state<string | null>(null);
  preview = $state<HandoffPreview | null>(null);

  clipboardError = $state<string | null>(null);
  copiedPrompt = $state(false);
  copiedCommand = $state(false);
  saving = $state(false);
  savedPath = $state<string | null>(null);

  private copyPromptTimer: ReturnType<typeof setTimeout> | null = null;
  private copyCommandTimer: ReturnType<typeof setTimeout> | null = null;
  private requestSeq = 0;

  open(meta: SessionMeta, targetAgent?: AgentID): void {
    this.session = meta;
    this.cwd = meta.cwd || '';
    this.budget = 80000;
    this.includeReasoning = false;
    this.redactSecrets = false;
    this.error = null;
    this.clipboardError = null;
    this.copiedPrompt = false;
    this.copiedCommand = false;
    this.savedPath = null;
    this.dialogOpen = true;

    if (targetAgent) {
      this.target = targetAgent;
    } else if (meta.ref.agent === 'claude-code') {
      this.target = 'codex';
    } else {
      this.target = 'claude-code';
    }

    void this.refreshPreview();
  }

  close(): void {
    this.dialogOpen = false;
    this.session = null;
    this.preview = null;
    this.error = null;
    this.clipboardError = null;
  }

  reset(): void {
    if (this.copyPromptTimer) clearTimeout(this.copyPromptTimer);
    if (this.copyCommandTimer) clearTimeout(this.copyCommandTimer);
    this.close();
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
    if (!this.session) return;
    const seq = ++this.requestSeq;
    this.loading = true;
    this.error = null;

    const req: HandoffRequest = {
      ref: this.session.ref,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets,
      cwd: this.cwd || undefined,
    };

    try {
      const res = await api.buildHandoff(req);
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
        this.loading = false;
      }
    }
  }

  async copyPrompt(): Promise<void> {
    this.clipboardError = null;
    if (!this.preview?.promptMarkdown) return;

    const ok = await copyToClipboard(this.preview.promptMarkdown);
    if (!ok) {
      this.clipboardError = 'Failed to copy prompt to clipboard. Please select and copy manually.';
      return;
    }

    this.copiedPrompt = true;
    if (this.copyPromptTimer) clearTimeout(this.copyPromptTimer);
    this.copyPromptTimer = setTimeout(() => {
      this.copiedPrompt = false;
    }, 2000);
  }

  async copyCommand(): Promise<void> {
    this.clipboardError = null;
    if (!this.session) return;

    const req: HandoffRequest = {
      ref: this.session.ref,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets,
      cwd: this.cwd || undefined,
    };

    try {
      const cmd = await api.handoffCommand(req);
      const ok = await copyToClipboard(cmd);
      if (!ok) {
        this.clipboardError = 'Failed to copy command to clipboard. Please copy manually: ' + cmd;
        return;
      }
      this.copiedCommand = true;
      if (this.copyCommandTimer) clearTimeout(this.copyCommandTimer);
      this.copyCommandTimer = setTimeout(() => {
        this.copiedCommand = false;
      }, 2000);
    } catch (err) {
      if (isStaleReply(err)) return;
      this.clipboardError = err instanceof Error ? err.message : String(err);
    }
  }

  async save(): Promise<void> {
    if (!this.session) return;
    this.saving = true;
    this.error = null;
    this.savedPath = null;

    const req: HandoffRequest = {
      ref: this.session.ref,
      target: this.target,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets,
      cwd: this.cwd || undefined,
    };

    try {
      const path = await api.saveHandoff(req);
      if (path) {
        this.savedPath = path;
      }
    } catch (err) {
      if (isStaleReply(err)) return;
      this.error = err instanceof Error ? err.message : String(err);
    } finally {
      this.saving = false;
    }
  }
}

export const handoff = new HandoffStore();
