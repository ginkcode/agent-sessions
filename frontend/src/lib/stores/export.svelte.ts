import { api } from '../api';
import { isStaleReply } from '../link';
import { link } from './link.svelte';
import type { ExportPreview, ExportProfile, ExportRequest, SessionMeta } from '../types';

// ExportStore drives the export dialog. Complete is the default profile and
// the only one that can carry secrets; share-safe forces redaction on.
export class ExportStore {
  dialogOpen = $state(false);
  session = $state<SessionMeta | null>(null);
  profile = $state<ExportProfile>('complete');
  budget = $state<number>(80000);
  includeReasoning = $state(false);
  redactSecrets = $state(false);

  loading = $state(false);
  exporting = $state(false);
  error = $state<string | null>(null);
  preview = $state<ExportPreview | null>(null);
  savedPath = $state<string | null>(null);

  private requestSeq = 0;

  open(meta: SessionMeta, profile: ExportProfile = 'complete'): void {
    this.session = meta;
    this.profile = profile;
    this.budget = 80000;
    this.includeReasoning = false;
    this.redactSecrets = false;
    this.error = null;
    this.preview = null;
    this.savedPath = null;
    this.dialogOpen = true;
    void this.refreshPreview();
  }

  close(): void {
    this.dialogOpen = false;
    this.session = null;
    this.preview = null;
    this.error = null;
  }

  reset(): void {
    this.savedPath = null;
    this.close();
  }

  async setProfile(profile: ExportProfile): Promise<void> {
    if (this.profile === profile) return;
    this.profile = profile;
    await this.refreshPreview();
  }

  async setBudget(budget: number): Promise<void> {
    if (this.budget === budget) return;
    this.budget = budget;
    await this.refreshPreview();
  }

  async setIncludeReasoning(value: boolean): Promise<void> {
    if (this.includeReasoning === value) return;
    this.includeReasoning = value;
    await this.refreshPreview();
  }

  async setRedactSecrets(value: boolean): Promise<void> {
    if (this.redactSecrets === value) return;
    this.redactSecrets = value;
    await this.refreshPreview();
  }

  // Share-safe bundles are always redacted, whatever the toggle says.
  get redactionForced(): boolean {
    return this.profile === 'share-safe';
  }

  private request(): ExportRequest | null {
    if (!this.session) return null;
    return {
      ref: this.session.ref,
      profile: this.profile,
      budget: this.budget,
      includeReasoning: this.includeReasoning,
      redactSecrets: this.redactSecrets || this.redactionForced,
    };
  }

  async refreshPreview(): Promise<void> {
    const req = this.request();
    if (!req) return;
    const seq = ++this.requestSeq;
    this.loading = true;
    this.error = null;
    try {
      const preview = await api.previewExport(req);
      if (this.requestSeq === seq) this.preview = preview;
    } catch (err) {
      if (isStaleReply(err)) return;
      if (this.requestSeq === seq) {
        this.error = err instanceof Error ? err.message : String(err);
      }
    } finally {
      if (this.requestSeq === seq) this.loading = false;
    }
  }

  async save(): Promise<void> {
    const req = this.request();
    if (!req) return;
    const blocked = link.blockedReason;
    if (blocked) {
      this.error = blocked;
      return;
    }
    this.exporting = true;
    this.error = null;
    this.savedPath = null;
    try {
      const path = await api.exportBundle(req);
      if (path) this.savedPath = path;
    } catch (err) {
      if (isStaleReply(err)) return;
      this.error = err instanceof Error ? err.message : String(err);
    } finally {
      this.exporting = false;
    }
  }
}

export const exporter = new ExportStore();
