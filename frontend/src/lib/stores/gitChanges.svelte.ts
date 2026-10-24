import { api } from '../api';
import type { GitCommitRequest, RepoStatus } from '../types';
import { errorText } from '../manage';
import { changeGroups } from '../gitChanges';
import { files, tabHasUnsaved } from './files.svelte';
import { link } from './link.svelte';
import { connectionStore } from './connection.svelte';
import { filePrompt } from './filePrompt.svelte';
import { codeDiagnostics } from './codeDiagnostics.svelte';
import type { GitBranch } from '../types';

/** The Changes pane follows the selected project and host, never cached routes. */
export class GitChangesStore {
  status = $state.raw<RepoStatus | null>(null);
  loading = $state(false);
  busy = $state(false);
  error = $state<string | null>(null);
  message = $state('');
  branches = $state.raw<GitBranch[]>([]);
  branchesTruncated = $state(false);
  branchesLoading = $state(false);
  collapsed = $state(new Set<string>());
  syncOperation = $state<'fetch' | 'pull' | 'push' | null>(null);
  syncMessage = $state('');
  private syncController: AbortController | null = null;
  private identity = '';
  private sequence = 0;

  get groups() { return changeGroups(this.status); }
  get draftPaths(): string[] { return files.tabs.filter(tabHasUnsaved).map(t => t.path); }
  get writeBlocker(): string | null {
    if (link.blockedReason) return link.blockedReason;
    if (!connectionStore.capabilities.gitWrite) return 'This host does not support Git write actions. Update the server and reconnect.';
    if (this.busy) return 'Wait for the current Git action to finish.';
    if (!this.status?.ok) return this.status?.reason ?? 'Wait for repository status.';
    return null;
  }
  private key() { return JSON.stringify([files.dir, link.dataHost, files.contextVersion]); }

  async refresh(): Promise<void> {
    const identity = this.key();
    if (identity !== this.identity) {
      this.identity = identity;
      this.status = null; this.message = ''; this.error = null; this.collapsed = new Set();
      this.branches = []; this.branchesTruncated = false; this.syncMessage = '';
    }
    const sequence = ++this.sequence;
    if (!files.dir || !connectionStore.capabilities.gitChanges || link.blockedReason) { this.loading = false; return; }
    this.loading = true;
    try {
      const status = await api.gitStatus(files.dir);
      if (sequence === this.sequence && identity === this.key()) { this.status = status; this.error = null; }
    } catch (e) {
      if (sequence === this.sequence && identity === this.key()) this.error = errorText(e, 'Could not read repository status.');
    } finally { if (sequence === this.sequence) this.loading = false; }
  }

  async loadBranches(): Promise<void> {
    const dir = files.dir, identity = this.key();
    if (!dir || this.branchesLoading || !connectionStore.capabilities.gitChanges) return;
    this.branchesLoading = true;
    try {
      const result = await api.gitBranches(dir);
      if (identity === this.key()) { this.branches = result.branches; this.branchesTruncated = result.truncated; }
    } catch (e) { if (identity === this.key()) this.error = errorText(e, 'Could not list branches.'); }
    finally { this.branchesLoading = false; }
  }

  async switchBranch(name: string, create = false, remote = false): Promise<boolean> {
    if (this.draftPaths.length) { this.error = 'Save or close unsaved drafts before switching branches.'; return false; }
    const identity = this.key();
    const paths = files.tabs.filter(t => !t.external).map(t => t.path);
    const ok = await this.write((dir, host) => api.gitSwitch(dir, { name, create, remote, draftPaths: this.draftPaths }, host), paths);
    if (ok && files.dir && identity === this.key()) { await codeDiagnostics.hide(); if (identity === this.key() && files.dir) await codeDiagnostics.show(files.dir); await this.loadBranches(); }
    return ok;
  }

  get syncBlocker(): string | null {
    if (!connectionStore.capabilities.gitSync) return 'This host does not support Git sync. Update the server and reconnect.';
    return this.writeBlocker;
  }

  cancelSync(): void { this.syncController?.abort(); }

  async sync(op: 'fetch' | 'pull' | 'push'): Promise<boolean> {
    const dir = files.dir, host = link.dataHost, identity = this.key();
    if (!dir || this.syncBlocker) return false;
    if (op !== 'fetch' && this.status?.detached) { this.error = 'Switch to a branch before pulling or pushing.'; return false; }
    if (op === 'pull' && this.draftPaths.length) { this.error = 'Save or close unsaved drafts before pulling.'; return false; }
    // Publishing creates a remote branch, so it asks; a push to the upstream does not.
    if (op === 'push' && !this.status?.upstream) {
      const answer = await filePrompt.ask('Publish branch', `Publish ${this.status?.branch} to the remote and track it as its upstream?`, [{ value: 'push', label: 'Publish' }]);
      if (answer !== 'push' || identity !== this.key() || this.syncBlocker) return false;
    }
    const controller = new AbortController();
    this.syncController = controller; this.syncOperation = op;
    this.busy = true; this.error = null; this.syncMessage = '';
    let ok = false;
    try {
      const result = await api.gitSync(dir, op, host, controller.signal);
      ok = true;
      if (identity === this.key()) { this.status = result.status; this.syncMessage = result.summary; }
    } catch (e) {
      if (identity !== this.key()) { /* another project now */ }
      else if ((e as { code?: string } | null)?.code === 'canceled') this.syncMessage = `Canceled ${op}. Anything Git already finished is kept.`;
      else this.error = errorText(e, 'Git sync failed.');
    } finally {
      // Cancellation or a failed pull can still have fetched refs or changed disk,
      // including under hidden or overlapping roots of this host.
      const relist = op === 'pull' && files.invalidateListings(null, host);
      if (identity === this.key()) {
        const failure = this.error;
        try {
          if (op === 'pull') {
            await files.reloadReplaced(files.tabs.filter(t => !t.external).map(t => t.path));
            if (identity === this.key()) await files.refresh();
            if (identity === this.key()) await codeDiagnostics.hide();
            if (identity === this.key() && files.dir) await codeDiagnostics.show(files.dir);
          }
          if (identity === this.key()) await this.refresh();
        } catch (e) { if (identity === this.key()) this.error = errorText(e, 'Could not refresh after Git sync.'); }
        if (failure && identity === this.key()) this.error = failure;
      } else if (relist) void files.refresh();
      this.syncController = null; this.syncOperation = null; this.busy = false;
    }
    return ok;
  }

  toggle(section: string): void {
    const next = new Set(this.collapsed);
    if (next.has(section)) next.delete(section); else next.add(section);
    this.collapsed = next;
  }

  private async write(work: (dir: string, host: string | undefined) => Promise<void>, reload: string[] = []): Promise<boolean> {
    const dir = files.dir, host = link.dataHost, identity = this.key();
    if (!dir || this.writeBlocker) return false;
    this.busy = true; this.error = null;
    let ok = false, relist = false;
    try {
      // Before any reload: every retained listing of this host may show older Git
      // status, even after a failure (hooks may stage or rewrite files).
      try { await work(dir, host); } finally { relist = files.invalidateListings(null, host); }
      ok = true;
      if (identity === this.key() && reload.length) await files.reloadReplaced(reload);
    } catch (e) { if (identity === this.key()) this.error = errorText(e, 'Git action failed.'); }
    finally {
      this.busy = false;
      // Failed hooks may still stage files; failed discards may be partial.
      if (identity === this.key()) {
        const failure = this.error;
        await files.refresh();
        await this.refresh();
        if (failure) this.error = failure;
      } else if (relist) void files.refresh();
    }
    return ok;
  }

  stage(paths: string[]): Promise<boolean> { return this.write((dir, host) => api.gitStage(dir, paths, host)); }
  unstage(paths: string[]): Promise<boolean> { return this.write((dir, host) => api.gitUnstage(dir, paths, host)); }

  async discard(paths: string[]): Promise<boolean> {
    const chosen = paths.length ? paths : this.groups.unstaged.map(e => e.path);
    if (!chosen.length || this.writeBlocker) return false;
    if (chosen.some(path => this.draftPaths.includes(path))) { this.error = 'Save or close the unsaved drafts before discarding their changes.'; return false; }
    const identity = this.key();
    const answer = await filePrompt.ask('Discard changes', `Permanently discard changes in ${chosen.join(', ')}? Untracked files will be deleted. This cannot be undone.`, [{ value: 'discard', label: 'Discard', danger: true }]);
    if (answer !== 'discard' || identity !== this.key()) return false;
    return this.write((dir, host) => api.gitDiscard(dir, { paths: chosen, draftPaths: this.draftPaths }, host), chosen);
  }

  async commit(amend = false, stageAll = this.groups.staged.length === 0): Promise<boolean> {
    if (this.groups.conflicts.length) { this.error = 'Resolve and stage merge conflicts before committing.'; return false; }
    if (!this.message.trim()) { this.error = 'Enter a commit message.'; return false; }
    if (stageAll && this.draftPaths.length) { this.error = 'Save or close unsaved drafts before committing all. Git commits saved files only.'; return false; }
    const message = this.message, identity = this.key();
    const request: GitCommitRequest = { message, amend, stageAll };
    const ok = await this.write((dir, host) => api.gitCommit(dir, request, host));
    if (ok && identity === this.key() && this.message === message) this.message = '';
    return ok;
  }
}

export const gitChanges = new GitChangesStore();
