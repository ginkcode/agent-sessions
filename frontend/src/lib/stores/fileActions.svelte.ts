import { api } from '../api';
import { files, tabHasUnsaved, type DraftTarget, type FileTab } from './files.svelte';
import { filePrompt } from './filePrompt.svelte';

/** Shared destructive-action coordinator; freezes input while resolving drafts. */
export class FileActions {
  busy = $state(false);
  quitting = $state(false);

  private async resolve(targets: DraftTarget[], title: string): Promise<boolean> {
    const dirty = targets.filter(({ tab }) => tabHasUnsaved(tab));
    if (!dirty.length) return true;
    if (dirty.some(({ tab }) => tab.edit?.saving)) return false;
    const decision = await filePrompt.ask(title,
      `Unsaved changes in:\n${dirty.map(({ dir, tab }) => `${dir}/${tab.path}`).join('\n')}`,
      [{ value: 'discard', label: 'Discard changes', danger: true }, { value: 'save', label: 'Save changes' }]);
    if (decision === 'discard') {
      // This decision authorizes disposal, not a disk reload (which may fail
      // while offline). Only callers removing these targets may use it.
      for (const { tab } of dirty) {
        if (tab.edit) {
          tab.edit.draft = tab.edit.baseline;
          tab.edit.uncertain = null;
          tab.edit.conflict = null;
          tab.edit.version++;
        }
      }
      return true;
    }
    if (decision !== 'save') return false;
    for (const { tab } of dirty) {
      let saved = await files.saveEdited(tab);
      if (!saved && tab.edit?.conflict) saved = await this.conflict(tab);
      if (!saved || tabHasUnsaved(tab)) return false;
    }
    return true;
  }

  async closeTabs(paths: string[]): Promise<boolean> {
    if (this.busy || this.quitting) return false;
    this.busy = true;
    try {
      const tabs = files.tabs.filter(t => paths.includes(t.path));
      const targets = files.draftTargets.filter(t => tabs.includes(t.tab));
      if (!await this.resolve(targets, 'Close files?')) return false;
      for (const tab of tabs) files.closeTab(tab.path);
      return true;
    } finally { this.busy = false; }
  }

  async done(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting) return false;
    this.busy = true;
    try {
      const targets = files.draftTargets.filter(t => t.tab === tab);
      if (!await this.resolve(targets, 'Finish editing?')) return false;
      // Drop edit state after explicit disposal; viewer uses the saved baseline.
      tab.edit = undefined;
      return true;
    } finally { this.busy = false; }
  }

  async discard(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting || tab.edit?.saving) return false;
    this.busy = true;
    try {
      if (tabHasUnsaved(tab) && await filePrompt.ask('Discard changes?', `Reload ${tab.path} from disk?`,
        [{ value: 'discard', label: 'Discard and reload', danger: true }]) !== 'discard') return false;
      return await files.discardEdited(tab);
    } finally { this.busy = false; }
  }

  async changeHost(action: () => Promise<void>): Promise<boolean> {
    if (this.busy || this.quitting) return false;
    this.busy = true;
    try {
      if (!await this.resolve(files.draftTargets, 'Switch host?')) return false;
      // Keep input frozen until routing has changed, not only until the user
      // answers: otherwise a keystroke during connect could create a new draft.
      await action();
      return true;
    } finally { this.busy = false; }
  }

  async save(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting) return false;
    const saved = await files.saveEdited(tab);
    if (saved || !tab.edit?.conflict || this.busy || this.quitting) return saved;
    this.busy = true;
    try { return await this.conflict(tab); }
    finally { this.busy = false; }
  }

  /** Reopens the conflict decision without another doomed CAS attempt. */
  async resolveConflict(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting || !tab.edit?.conflict || tab.edit.saving) return false;
    this.busy = true;
    try { return await this.conflict(tab); }
    finally { this.busy = false; }
  }

  private async conflict(tab: FileTab): Promise<boolean> {
    const observed = tab.edit?.conflict?.observedRevision;
    if (!observed) return false;
    const choice = await filePrompt.ask('File changed on disk',
      `${tab.path} changed since you opened it. Your draft is still here.`,
      [{ value: 'reload', label: 'Reload from disk', danger: true }, { value: 'overwrite', label: 'Overwrite…', danger: true }]);
    if (choice === 'reload') {
      if (await filePrompt.ask('Discard your draft?', `Replace your draft of ${tab.path} with the disk contents?`,
        [{ value: 'reload', label: 'Discard and reload', danger: true }]) === 'reload') await files.discardEdited(tab);
      return false;
    }
    if (choice !== 'overwrite') return false;
    if (await filePrompt.ask('Overwrite disk contents?', `Save your draft over the changes in ${tab.path}? Another disk change will cause another conflict.`,
      [{ value: 'overwrite', label: 'Overwrite', danger: true }]) !== 'overwrite') return false;
    // Never bypass CAS. A second conflict stays visible for a fresh decision.
    return await files.saveEdited(tab, observed);
  }

  async nativeClose(id: number): Promise<void> {
    if (this.busy || this.quitting) { await api.completeFileClose(id, false); return; }
    this.busy = true;
    try {
      const allow = await this.resolve(files.draftTargets, 'Close window?');
      this.quitting = allow;
      await api.completeFileClose(id, allow);
    } catch {
      this.quitting = false;
      await api.completeFileClose(id, false).catch(() => {});
    } finally { this.busy = false; }
  }
}

export const fileActions = new FileActions();
