import { api } from '../api';
import type {
  DeletePreview,
  DeleteResult,
  ManageSettings,
  SessionMeta,
  SessionRef,
} from '../types';
import {
  refKey,
  refsEqual,
  nextSelectionAfterDelete,
  errorText,
} from '../manage';
import { appState } from './appState.svelte';

export class ManageStore {
  settings = $state<ManageSettings>({
    enabled: false,
    allowPermanentDelete: false,
  });

  loadingSettings = $state(false);
  settingsError = $state<string | null>(null);

  // Bulk selection: keys are refKey(ref)
  selectedRefKeys = $state<Set<string>>(new Set());
  ageFilterDays = $state<number>(0);

  // Delete flow state
  preview = $state<DeletePreview | null>(null);
  previewLoading = $state(false);
  previewError = $state<string | null>(null);

  deleting = $state(false);
  deleteError = $state<string | null>(null);
  lastResult = $state<DeleteResult | null>(null);

  confirmDialogOpen = $state(false);
  settingsDialogOpen = $state(false);
  firstEnableWarningVisible = $state(false);

  async init(): Promise<void> {
    this.loadingSettings = true;
    try {
      this.settings = await api.getSettings();
      this.settingsError = null;
    } catch (err) {
      this.settingsError = errorText(err, 'Failed to load manage settings');
    } finally {
      this.loadingSettings = false;
    }
  }

  openSettings(): void {
    this.settingsDialogOpen = true;
    this.firstEnableWarningVisible = false;
  }

  closeSettings(): void {
    this.settingsDialogOpen = false;
    this.firstEnableWarningVisible = false;
    this.settingsError = null;
  }

  async toggleManageEnabled(): Promise<void> {
    const next = !this.settings.enabled;
    // Show first-enable warning if turning ON
    if (next && !this.firstEnableWarningVisible && !this.settings.enabled) {
      this.firstEnableWarningVisible = true;
      return;
    }
    await this.setManageEnabled(next);
    this.firstEnableWarningVisible = false;
  }

  async setManageEnabled(enabled: boolean): Promise<void> {
    this.loadingSettings = true;
    this.settingsError = null;
    try {
      this.settings = await api.setManageEnabled(enabled);
      if (!enabled) {
        this.clearSelection();
      }
    } catch (err) {
      this.settingsError = errorText(err, 'Failed to update manage enabled setting');
    } finally {
      this.loadingSettings = false;
    }
  }

  async setAllowPermanentDelete(allow: boolean): Promise<void> {
    this.loadingSettings = true;
    this.settingsError = null;
    try {
      this.settings = await api.setAllowPermanentDelete(allow);
    } catch (err) {
      this.settingsError =
        errorText(err, 'Failed to update permanent delete setting');
    } finally {
      this.loadingSettings = false;
    }
  }

  // Selection management
  toggleRefSelected(ref: SessionRef): void {
    const key = refKey(ref);
    const next = new Set(this.selectedRefKeys);
    if (next.has(key)) {
      next.delete(key);
    } else {
      next.add(key);
    }
    this.selectedRefKeys = next;
  }

  isRefSelected(ref: SessionRef): boolean {
    return this.selectedRefKeys.has(refKey(ref));
  }

  selectAll(sessions: SessionMeta[]): void {
    const next = new Set<string>();
    for (const s of sessions) {
      next.add(refKey(s.ref));
    }
    this.selectedRefKeys = next;
  }

  clearSelection(): void {
    this.selectedRefKeys = new Set();
  }

  setAgeFilter(days: number): void {
    this.ageFilterDays = days;
  }

  // Get selected SessionRef objects matching currently filtered sessions
  getSelectedRefs(availableSessions: SessionMeta[]): SessionRef[] {
    const refs: SessionRef[] = [];
    for (const s of availableSessions) {
      if (this.selectedRefKeys.has(refKey(s.ref))) {
        refs.push(s.ref);
      }
    }
    return refs;
  }

  // Delete flow
  async requestDelete(refs: SessionRef[]): Promise<void> {
    if (!this.settings.enabled) {
      this.deleteError = 'Session management is disabled in Settings.';
      return;
    }
    if (!refs.length) return;

    this.previewLoading = true;
    this.previewError = null;
    this.preview = null;
    this.lastResult = null;
    this.deleteError = null;
    this.confirmDialogOpen = true;

    try {
      const preview = await api.previewDelete(refs);
      this.preview = preview;
    } catch (err) {
      this.previewError = errorText(err, 'Failed to preview delete operation');
    } finally {
      this.previewLoading = false;
    }
  }

  async executeDelete(): Promise<boolean> {
    if (!this.preview) return false;

    this.deleting = true;
    this.deleteError = null;

    // The backend token covers only the unblocked items; sending a blocked
    // ref would fail the MAC check.
    const refsToDelete = this.preview.items
      .filter((i) => !i.blocked)
      .map((i) => i.ref);
    const currentSessions = [...appState.sessions];
    const currentSelected = appState.selectedSessionRef;

    try {
      const result = await api.deleteSessions(refsToDelete, this.preview.token);
      this.lastResult = result;

      // Forgotten includes descendants of deleted parents, so a selected
      // child session is cleared along with its parent.
      const successfulRefs = result.forgotten ?? [];
      const nextSelection = new Set(this.selectedRefKeys);
      for (const ref of successfulRefs) {
        nextSelection.delete(refKey(ref));
      }
      this.selectedRefKeys = nextSelection;

      const nextRef = nextSelectionAfterDelete(
        currentSessions,
        successfulRefs,
        currentSelected
      );

      // Select next session or clear
      await appState.selectSession(nextRef);

      // Refresh catalog groups and sessions
      await appState.loadGroups();
      await appState.loadSessions();

      return result.failed === 0;
    } catch (err) {
      this.deleteError = errorText(err, 'Failed to delete sessions');
      return false;
    } finally {
      this.deleting = false;
    }
  }

  dismissDialog(): void {
    this.confirmDialogOpen = false;
    this.preview = null;
    this.previewError = null;
    this.deleteError = null;
    this.lastResult = null;
  }
}

export const manage = new ManageStore();
