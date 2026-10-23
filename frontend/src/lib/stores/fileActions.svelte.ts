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

  /** Keep project/host transitions serialized with directory creation. */
  async createDirectory(path: string, name: string): Promise<void> {
    if (this.busy || this.quitting) throw new Error('Another file action is in progress.');
    this.busy = true;
    try { await files.createDirectory(path, name); }
    finally { this.busy = false; }
  }

  async createFile(path: string, name: string): Promise<void> {
    if (this.busy || this.quitting) throw new Error('Another file action is in progress.');
    this.busy = true;
    try { await files.createFile(path, name); }
    finally { this.busy = false; }
  }

  /** Shares the close/host-transition lock; dirty drafts are moved, not saved or discarded. */
  async rename(path: string, name: string): Promise<void> {
    if (this.busy || this.quitting) throw new Error('Another file action is in progress.');
    this.busy = true;
    try { await files.rename(path, name); }
    finally { this.busy = false; }
  }

  /** One irreversible decision; drafts are disposed only after confirmed success. */
  async delete(path: string, directory: boolean): Promise<boolean> {
    if (this.busy || this.quitting) throw new Error('Another file action is in progress.');
    this.busy = true;
    const dir = files.dir;
    const context = files.contextVersion;
    try {
      if (!dir || !path) throw new Error('Choose a non-root file or directory to delete.');
      const affected = files.tabs.filter(t => t.path === path || t.path.startsWith(path + '/'));
      if (affected.some(t => t.edit?.saving || t.edit?.uncertain)) {
        throw new Error('Wait for pending saves to finish and resolve unknown save outcomes before deleting.');
      }
      const dirty = affected.filter(tabHasUnsaved).length;
      const warning = dirty ? `\n${dirty} affected unsaved draft${dirty === 1 ? '' : 's'} will be permanently discarded after successful deletion.` : '';
      const decision = await filePrompt.ask(directory ? 'Permanently delete directory?' : 'Permanently delete file?',
        `Permanently delete ${directory ? 'directory' : 'file'} “${path}”${directory ? ' and all its contents recursively' : ''}? This cannot be undone and does not move anything to the Trash.${warning}`,
        [{ value: 'delete', label: 'Delete permanently', danger: true }]);
      if (decision !== 'delete') return false;
      if (files.dir !== dir || files.contextVersion !== context) throw new Error('The project or connection changed. Delete was cancelled.');
      await files.delete(path);
      return true;
    } finally { this.busy = false; }
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

  /**
   * Discard: confirm when there are changes, drop the draft and return to the
   * viewer. After a conflict or an unknown save outcome the disk may differ
   * from the baseline, so the file is read again.
   */
  async discard(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting || !tab.edit || tab.edit.saving) return false;
    this.busy = true;
    try {
      if (tabHasUnsaved(tab) && await filePrompt.ask('Discard changes?', `Revert all changes to ${tab.path}?`,
        [{ value: 'discard', label: 'Discard changes', danger: true }]) !== 'discard') return false;
      if (tab.edit?.saving) return false;
      return files.stopEditing(tab, Boolean(tab.edit?.uncertain || tab.edit?.conflict));
    } finally { this.busy = false; }
  }

  /** Reload in the error bar: replace the draft with the disk contents, keep editing. */
  async reload(tab: FileTab): Promise<boolean> {
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

  /** Cmd/Ctrl+S: write the draft and keep editing. */
  async saveInPlace(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting) return false;
    const saved = await files.saveEdited(tab);
    if (saved || !tab.edit?.conflict || this.busy || this.quitting) return saved;
    this.busy = true;
    try { return await this.conflict(tab); }
    finally { this.busy = false; }
  }

  /**
   * Save writes the draft and returns to the viewer. A failed save, or typing
   * while the save ran, keeps the editor open so nothing is lost.
   */
  async save(tab: FileTab): Promise<boolean> {
    if (this.busy || this.quitting || !tab.edit || tab.edit.saving) return false;
    let saved = !tabHasUnsaved(tab) || await files.saveEdited(tab);
    if (!saved && tab.edit?.conflict && !this.busy && !this.quitting) {
      this.busy = true;
      try { saved = await this.conflict(tab); }
      finally { this.busy = false; }
    }
    if (!saved || tabHasUnsaved(tab)) return false;
    return files.stopEditing(tab);
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
