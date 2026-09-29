<script lang="ts">
  import {
    summarizePreview,
    isPermanentDelete,
    formatDeleteResultSummary,
  } from '../../manage';
  import { formatBytes } from '../../format';
  import { manage } from '../../stores/manage.svelte';

  interface Props {
    open: boolean;
    onClose: () => void;
  }

  let { open = false, onClose }: Props = $props();

  let summary = $derived(summarizePreview(manage.preview));
  let permanent = $derived(isPermanentDelete(manage.preview));

  function handleCancel() {
    manage.dismissDialog();
    onClose();
  }

  // The dialog stays open after deleting so per-item results are visible;
  // the user closes it with Done.
  async function handleConfirm() {
    await manage.executeDelete();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) handleCancel();
    }}
    onkeydown={(e) => {
      if (e.key === 'Escape') handleCancel();
    }}
  >
    <div
      class="dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="delete-confirm-title"
    >
      <header class="dialog-header">
        <h2 id="delete-confirm-title">Delete Sessions</h2>
        <button
          type="button"
          class="dialog-close"
          title="Close"
          aria-label="Close dialog"
          onclick={handleCancel}
        >
          ✕
        </button>
      </header>

      {#if manage.previewLoading}
        <div class="dialog-body">
          <p class="loading-note">Preparing delete preview…</p>
        </div>
      {:else if manage.previewError}
        <div class="dialog-body">
          <div class="error-banner" role="alert">
            <strong>Could not prepare delete.</strong>
            <span>{manage.previewError}</span>
          </div>
          <footer class="dialog-footer">
            <button type="button" class="btn" onclick={handleCancel}>
              Close
            </button>
          </footer>
        </div>
      {:else if manage.lastResult}
        <div class="dialog-body">
          <p class="preview-total">
            {formatDeleteResultSummary(manage.lastResult)} ·
            {formatBytes(manage.lastResult.freedBytes)} freed
          </p>
          <ul class="preview-list">
            {#each manage.lastResult.items ?? [] as item (item.ref.agent + ':' + item.ref.id)}
              <li class="preview-item" class:blocked={!item.ok}>
                <div class="preview-row">
                  <span class="preview-title" title={item.title}
                    >{item.title}</span
                  >
                  {#if item.ok}
                    <span class="result-ok">done</span>
                  {:else}
                    <span class="blocked-tag">failed</span>
                  {/if}
                </div>
                {#if item.error}
                  <p class="preview-warning">{item.error}</p>
                {/if}
                {#if item.remaining?.length}
                  <p class="preview-warning">Not removed:</p>
                  <ul class="preview-paths">
                    {#each item.remaining as p}
                      <li title={p}>{p}</li>
                    {/each}
                  </ul>
                {/if}
              </li>
            {/each}
          </ul>
        </div>
        <footer class="dialog-footer">
          <button type="button" class="btn" onclick={handleCancel}>Done</button>
        </footer>
      {:else if manage.preview}
        <div class="dialog-body">
          {#if summary.blockedCount > 0}
            <div class="warn-banner" role="alert">
              <strong>
                {summary.blockedCount}
                {summary.blockedCount === 1 ? 'session is' : 'sessions are'}
                blocked.</strong
              >
              <span>
                {summary.canProceed
                  ? 'Blocked sessions will be skipped; the rest can proceed.'
                  : 'None of the selected sessions can be deleted right now.'}
              </span>
            </div>
          {/if}

          {#if summary.permanentCount > 0}
            <div class="danger-banner" role="alert">
              <strong>
                {summary.permanentCount}
                {summary.permanentCount === 1 ? 'session' : 'sessions'} will be
                permanently deleted.</strong
              >
              <span>
                These files have no Trash copy. They will be forgotten and
                cannot be restored.
              </span>
            </div>
          {:else if summary.reversibleCount > 0}
            <p class="reversible-note">
              Selected sessions move to Trash. You can restore them from
              Trash.
            </p>
          {/if}

          <ul class="preview-list">
            {#each manage.preview.items as item (item.ref.agent + ':' + item.ref.id)}
              <li class="preview-item" class:blocked={item.blocked}>
                <div class="preview-row">
                  <span class="preview-title" title={item.title}
                    >{item.title}</span
                  >
                  <span class="preview-bytes">{formatBytes(item.bytes)}</span>
                </div>
                <div class="preview-action">
                  <span
                    class="action-kind"
                    class:permanent={!item.reversible}
                  >
                    {item.action}
                  </span>
                  {#if item.blocked}
                    <span class="blocked-tag" title={item.blocked}
                      >blocked: {item.blocked}</span
                    >
                  {:else if !item.reversible}
                    <span class="permanent-tag">no Trash copy</span>
                  {/if}
                </div>
                {#if item.paths?.length}
                  <ul class="preview-paths">
                    {#each item.paths as p}
                      <li title={p}>{p}</li>
                    {/each}
                  </ul>
                {/if}
                {#if item.warning}
                  <p class="preview-warning">{item.warning}</p>
                {/if}
              </li>
            {/each}
          </ul>

          <p class="preview-total">
            {summary.total - summary.blockedCount} of {summary.total}
            {summary.total === 1 ? 'session' : 'sessions'} ·
            {formatBytes(manage.preview.totalBytes)} to free
          </p>

          {#if permanent}
            <p class="permanent-note">
              Sessions without a Trash copy are deleted permanently. This
              action cannot be undone.
            </p>
          {:else}
            <p class="soft-confirm-note">
              Confirm to move the selected
              {summary.reversibleCount === 1 ? 'session' : 'sessions'} to
              Trash.
            </p>
          {/if}

          {#if manage.deleteError}
            <div class="error-banner" role="alert">
              <strong>Delete failed.</strong>
              <span>{manage.deleteError}</span>
            </div>
          {/if}
        </div>

        <footer class="dialog-footer">
          <button type="button" class="btn" onclick={handleCancel}>
            Cancel
          </button>
          <button
            type="button"
            class="btn danger-btn"
            disabled={!summary.canProceed || manage.deleting}
            onclick={handleConfirm}
          >
            {manage.deleting
              ? 'Deleting…'
              : permanent
                ? 'Delete Permanently'
                : 'Move to Trash'}
          </button>
        </footer>
      {/if}
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 100;
  }

  .dialog {
    background: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-lg);
    width: min(560px, 92vw);
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
  }

  .dialog-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border-color);
    background: var(--bg-secondary);
    border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  }

  .dialog-header h2 {
    margin: 0;
    font-size: 0.95rem;
    font-weight: 600;
  }

  .dialog-close {
    padding: 2px 8px;
    border-radius: var(--radius-sm);
    font-size: 0.9rem;
  }

  .dialog-close:hover {
    background: var(--hover-bg);
  }

  .dialog-body {
    padding: 16px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .loading-note {
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .error-banner,
  .warn-banner,
  .danger-banner {
    padding: 8px 12px;
    border-radius: var(--radius-sm);
    font-size: 0.8rem;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .error-banner {
    background: color-mix(in srgb, var(--danger) 10%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
    color: var(--danger);
  }

  .warn-banner {
    background: rgba(245, 158, 11, 0.1);
    border: 1px solid rgba(245, 158, 11, 0.4);
    color: #f59e0b;
  }

  .danger-banner {
    background: color-mix(in srgb, var(--danger) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
    color: var(--danger);
  }

  .reversible-note {
    margin: 0;
    padding: 8px 12px;
    border-radius: var(--radius-sm);
    background: rgba(16, 185, 129, 0.08);
    border: 1px solid rgba(16, 185, 129, 0.3);
    color: var(--status-live);
    font-size: 0.8rem;
  }

  .preview-list {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 220px;
    overflow-y: auto;
    border: 1px solid var(--border-color);
    border-radius: var(--radius-sm);
  }

  .preview-item {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border-color);
    font-size: 0.8rem;
  }

  .preview-item:last-child {
    border-bottom: none;
  }

  .preview-item.blocked {
    opacity: 0.6;
  }

  .preview-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .preview-title {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
  }

  .preview-bytes {
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
    font-size: 0.72rem;
  }

  .preview-action {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-top: 3px;
  }

  .action-kind {
    font-size: 0.68rem;
    padding: 1px 6px;
    border-radius: 3px;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
    text-transform: uppercase;
    font-weight: 600;
  }

  .action-kind.permanent {
    background: color-mix(in srgb, var(--danger) 15%, transparent);
    color: var(--danger);
  }

  .result-ok {
    font-size: 0.75rem;
    color: var(--text-secondary);
  }

  .blocked-tag,
  .permanent-tag {
    font-size: 0.68rem;
    color: var(--text-muted);
  }

  .permanent-tag {
    color: var(--danger);
    font-weight: 500;
  }

  .preview-paths {
    list-style: none;
    margin: 4px 0 0 0;
    padding: 0;
  }

  .preview-paths li {
    font-family: var(--font-mono);
    font-size: 0.7rem;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .preview-warning {
    margin: 4px 0 0 0;
    color: #f59e0b;
    font-size: 0.72rem;
  }

  .preview-total {
    margin: 0;
    color: var(--text-muted);
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
  }

  .soft-confirm-note {
    margin: 0;
    color: var(--text-secondary);
    font-size: 0.8rem;
  }

  .permanent-note {
    margin: 0;
    color: var(--danger);
    font-size: 0.8rem;
    font-weight: 500;
  }

  .dialog-footer {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 16px;
    border-top: 1px solid var(--border-color);
    background: var(--bg-secondary);
    border-radius: 0 0 var(--radius-lg) var(--radius-lg);
  }

  .btn {
    padding: 5px 14px;
    border-radius: var(--radius-sm);
    font-size: 0.8rem;
    font-weight: 500;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
    border: 1px solid var(--border-color);
    cursor: pointer;
  }

  .btn:hover:not(:disabled) {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .danger-btn {
    background: var(--danger);
    border-color: var(--danger);
    color: white;
  }

  .danger-btn:hover:not(:disabled) {
    background: color-mix(in srgb, var(--danger) 85%, black);
    border-color: color-mix(in srgb, var(--danger) 85%, black);
    color: white;
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>