import { api } from '../api';
import type {
  DeletePreview,
  DeleteResult,
  HandoffCacheInfo,
  ManageSettings,
  SessionMeta,
  SessionRef,
} from '../types';
import {
  refKey,
  refsEqual,
  nextSelectionAfterDelete,
  errorText,
  type AgeFilter,
} from '../manage';
import { appState } from './appState.svelte';
import { link } from './link.svelte';
import { launcher } from './launcher.svelte';
import { isDisconnectedError, isStaleReply } from '../link';

export class ManageStore {
  settings = $state<ManageSettings>({
    enabled: false,
    allowPermanentDelete: false,
  });

  loadingSettings = $state(false);
  settingsError = $state<string | null>(null);

  // Bulk selection: keys are refKey(ref)
  selectedRefKeys = $state<Set<string>>(new Set());
  ageFilter = $state<AgeFilter>('any');

  // Delete flow state
  preview = $state<DeletePreview | null>(null);
  previewLoading = $state(false);
  previewError = $state<string | null>(null);

  deleting = $state(false);
  deleteError = $state<string | null>(null);
  // The link dropped while a delete was in flight: it may or may not have
  // run. Never retried; the lists reload when the host reconnects.
  deleteOutcomeUnknown = $state(false);
  lastResult = $state<DeleteResult | null>(null);

  confirmDialogOpen = $state(false);
  settingsDialogOpen = $state(false);
  firstEnableWarningVisible = $state(false);

  // Handoff files written by "Continue in"
  handoffCache = $state<HandoffCacheInfo | null>(null);
  handoffCacheBusy = $state(false);
  handoffCacheError = $state<string | null>(null);

  async init(): Promise<void> {
    this.loadingSettings = true;
    try {
      this.settings = await api.getSettings();
      this.settingsError = null;
    } catch (err) {
      if (isStaleReply(err)) return;
      this.settingsError = errorText(err, 'Failed to load manage settings');
    } finally {
      this.loadingSettings = false;
    }
  }

  openSettings(): void {
    this.settingsDialogOpen = true;
    this.firstEnableWarningVisible = false;
    void this.loadHandoffCache();
    void launcher.refresh();
  }

  async loadHandoffCache(): Promise<void> {
    this.handoffCacheError = null;
    try {
      this.handoffCache = await api.handoffCache();
    } catch (err) {
      if (isStaleReply(err)) return;
      this.handoffCacheError = errorText(err, 'Failed to read handoff files');
    }
  }

  async clearHandoffCache(): Promise<void> {
    if (link.blockedReason) {
      this.handoffCacheError = link.blockedReason;
      return;
    }
    this.handoffCacheBusy = true;
    this.handoffCacheError = null;
    try {
      this.handoffCache = await api.clearHandoffCache();
    } catch (err) {
      if (isStaleReply(err)) return;
      this.handoffCacheError = errorText(err, 'Failed to delete handoff files');
      await this.loadHandoffCache();
    } finally {
      this.handoffCacheBusy = false;
    }
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
    if (link.blockedReason) {
      this.settingsError = link.blockedReason;
      return;
    }
    this.loadingSettings = true;
    this.settingsError = null;
    try {
      this.settings = await api.setManageEnabled(enabled);
      if (!enabled) {
        this.clearSelection();
      }
    } catch (err) {
      if (isStaleReply(err)) return;
      this.settingsError = errorText(err, 'Failed to update manage enabled setting');
    } finally {
      this.loadingSettings = false;
    }
  }

  async setAllowPermanentDelete(allow: boolean): Promise<void> {
    if (link.blockedReason) {
      this.settingsError = link.blockedReason;
      return;
    }
    this.loadingSettings = true;
    this.settingsError = null;
    try {
      this.settings = await api.setAllowPermanentDelete(allow);
    } catch (err) {
      if (isStaleReply(err)) return;
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

  setAgeFilter(filter: AgeFilter): void {
    this.ageFilter = filter;
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
    if (link.blockedReason) {
      this.deleteError = link.blockedReason;
      return;
    }

    this.previewLoading = true;
    this.previewError = null;
    this.preview = null;
    this.lastResult = null;
    this.deleteError = null;
    this.deleteOutcomeUnknown = false;
    this.confirmDialogOpen = true;

    try {
      const preview = await api.previewDelete(refs);
      this.preview = preview;
    } catch (err) {
      if (isStaleReply(err)) return;
      this.previewError = errorText(err, 'Failed to preview delete operation');
    } finally {
      this.previewLoading = false;
    }
  }

  async executeDelete(): Promise<boolean> {
    if (!this.preview) return false;
    if (link.blockedReason) {
      this.deleteError = link.blockedReason;
      return false;
    }

    this.deleting = true;
    this.deleteError = null;
    this.deleteOutcomeUnknown = false;

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

      // Refresh the tree; loadGroups clears a stale group key and reloads
      // the session list itself when the deleted session was the last in
      // the group.  Otherwise the catalog:changed event that follows
      // (~100ms later) reloads the list through applyCatalogChange.
      await appState.reload(true);

      return result.failed === 0;
    } catch (err) {
      if (isStaleReply(err)) return false;
      if (isDisconnectedError(err)) {
        // The request may have reached the host before the link dropped.
        // The token is spent either way, so the preview cannot be reused.
        this.deleteOutcomeUnknown = true;
        this.preview = null;
        this.deleteError =
          'The connection dropped during the delete, so its outcome is unknown. ' +
          'Nothing will be retried; the sessions reload when the host reconnects.';
        return false;
      }
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
    this.deleteOutcomeUnknown = false;
    this.lastResult = null;
  }

  reset(): void {
    this.selectedRefKeys = new Set();
    this.preview = null;
    this.previewError = null;
    this.deleteError = null;
    this.deleteOutcomeUnknown = false;
    this.lastResult = null;
    this.confirmDialogOpen = false;
    this.settingsDialogOpen = false;
    this.firstEnableWarningVisible = false;
    this.handoffCache = null;
    this.handoffCacheError = null;
    void this.init();
  }
}

export const manage = new ManageStore();
