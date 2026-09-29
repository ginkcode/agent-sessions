<script lang="ts">
  import { exporter } from '../../stores/export.svelte';
  import { BUDGET_PRESETS } from '../../portable';
  import { formatBytes, formatTokens } from '../../format';
  import type { ExportProfile, RedactionCounts } from '../../types';

  const PROFILES: { id: ExportProfile; title: string; detail: string }[] = [
    {
      id: 'complete',
      title: 'Complete',
      detail: 'Restorable. Includes the original session records.',
    },
    {
      id: 'share-safe',
      title: 'Share-safe',
      detail: 'Redacted handoff only. Secrets are removed and nothing native is included.',
    },
  ];

  function redactionTotal(counts: RedactionCounts | undefined): number {
    if (!counts) return 0;
    return (counts.pem ?? 0) + (counts.token ?? 0) + (counts.jwt ?? 0) + (counts.assignment ?? 0) + (counts.home ?? 0);
  }

  function handleClose() {
    exporter.close();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && exporter.dialogOpen) handleClose();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if exporter.dialogOpen && exporter.session}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) handleClose();
    }}
  >
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="export-dialog-title">
      <header class="dialog-header">
        <div class="header-title-group">
          <h2 id="export-dialog-title">Export Session</h2>
          <span class="source-badge">
            From <span class="agent-name">{exporter.session.ref.agent}</span>
          </span>
        </div>
        <button type="button" class="dialog-close" title="Close dialog" aria-label="Close dialog" onclick={handleClose}>
          ✕
        </button>
      </header>

      <div class="dialog-body">
        <section class="section-group">
          <span class="group-label">Profile</span>
          <div class="profile-cards">
            {#each PROFILES as card (card.id)}
              <button
                type="button"
                class="profile-card"
                class:selected={exporter.profile === card.id}
                aria-pressed={exporter.profile === card.id}
                onclick={() => exporter.setProfile(card.id)}
              >
                <span class="profile-title">{card.title}</span>
                <span class="profile-detail">{card.detail}</span>
              </button>
            {/each}
          </div>
        </section>

        {#if exporter.profile === 'complete'}
          <div class="warning-banner" role="note">
            {exporter.preview?.warning ||
              "A complete bundle contains the session's original records and can include secrets such as tokens, keys, and passwords."}
          </div>
        {/if}

        <div class="controls-row">
          <div class="control-col">
            <span class="group-label">Handoff Budget</span>
            <div class="budget-buttons">
              {#each BUDGET_PRESETS as preset (preset.id)}
                <button
                  type="button"
                  class="budget-btn"
                  class:selected={exporter.budget === preset.tokens}
                  title={preset.description}
                  onclick={() => exporter.setBudget(preset.tokens)}
                >
                  {preset.label}
                </button>
              {/each}
            </div>
          </div>

          <div class="control-col">
            <span class="group-label">Options</span>
            <div class="toggles-group">
              <label class="checkbox-label">
                <input
                  type="checkbox"
                  checked={exporter.includeReasoning}
                  onchange={(e) => exporter.setIncludeReasoning(e.currentTarget.checked)}
                />
                <span>Include reasoning</span>
              </label>
              <label class="checkbox-label" class:forced={exporter.redactionForced}>
                <input
                  type="checkbox"
                  checked={exporter.redactSecrets || exporter.redactionForced}
                  disabled={exporter.redactionForced}
                  onchange={(e) => exporter.setRedactSecrets(e.currentTarget.checked)}
                />
                <span>Redact secrets{exporter.redactionForced ? ' (required)' : ''}</span>
              </label>
            </div>
          </div>
        </div>

        {#if exporter.error}
          <div class="error-banner" role="alert">{exporter.error}</div>
        {/if}

        {#if exporter.savedPath}
          <div class="success-banner" role="status">
            ✓ Bundle saved to: <code>{exporter.savedPath}</code>
          </div>
        {/if}

        {#if exporter.preview}
          {@const total = redactionTotal(exporter.preview.redaction)}
          <div class="report-summary">
            <div class="stat-pill">
              <span class="stat-label">Sessions:</span>
              <strong class="stat-value">{exporter.preview.sessions}</strong>
            </div>
            <div class="stat-pill">
              <span class="stat-label">Size:</span>
              <strong class="stat-value">
                {exporter.preview.nativeFiles > 0
                  ? `${exporter.preview.nativeFiles} files, ${formatBytes(exporter.preview.nativeBytes)}`
                  : 'handoff only'}
              </strong>
            </div>
            <div class="stat-pill">
              <span class="stat-label">Handoff:</span>
              <strong class="stat-value">{formatTokens(exporter.preview.handoffTokens)} tokens</strong>
            </div>
            {#if total > 0}
              <div class="stat-pill redact-pill">
                <span>🔒 {total} redacted</span>
              </div>
            {/if}
            {#if exporter.preview.fidelity?.overflow?.length}
              <div class="stat-pill warn-pill" title={exporter.preview.fidelity.overflow.join('\n')}>
                <span>⚠️ {exporter.preview.fidelity.overflow.length} outputs over the 64 MiB cap</span>
              </div>
            {/if}
          </div>
        {:else if exporter.loading}
          <div class="loading-state"><p>Estimating export…</p></div>
        {/if}
      </div>

      <footer class="dialog-footer">
        <button type="button" class="btn secondary-btn" onclick={handleClose}>Cancel</button>
        <button
          type="button"
          class="btn primary-btn"
          disabled={exporter.exporting || !exporter.preview}
          onclick={() => exporter.save()}
        >
          {exporter.exporting ? 'Exporting…' : 'Export…'}
        </button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    z-index: 1000;
    display: flex;
    align-items: center;
    justify-content: center;
    background: rgba(0, 0, 0, 0.55);
    backdrop-filter: blur(2px);
  }

  .dialog {
    width: 640px;
    max-width: calc(100vw - 32px);
    max-height: calc(100vh - 48px);
    display: flex;
    flex-direction: column;
    background: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-lg);
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.35);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-color);
    background: var(--bg-secondary);
  }

  .header-title-group {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .header-title-group h2 {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
    color: var(--text-primary);
  }

  .source-badge {
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .agent-name {
    font-weight: 500;
    color: var(--text-secondary);
  }

  .dialog-close {
    border: none;
    background: transparent;
    color: var(--text-muted);
    font-size: 1rem;
    cursor: pointer;
    padding: 4px;
    border-radius: var(--radius-sm);
  }

  .dialog-close:hover {
    color: var(--text-primary);
    background: var(--bg-tertiary);
  }

  .dialog-body {
    flex: 1;
    overflow-y: auto;
    padding: 16px 18px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  .section-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .group-label {
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-secondary);
  }

  .profile-cards {
    display: flex;
    gap: 8px;
  }

  .profile-card {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
    padding: 10px 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border-color);
    background: var(--bg-secondary);
    color: var(--text-secondary);
    text-align: left;
    cursor: pointer;
  }

  .profile-card:hover {
    border-color: var(--text-muted);
  }

  .profile-card.selected {
    background: var(--bg-tertiary);
    border-color: var(--accent);
    box-shadow: 0 0 0 1px var(--accent);
  }

  .profile-title {
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--text-primary);
  }

  .profile-detail {
    font-size: 0.75rem;
    line-height: 1.4;
  }

  .warning-banner {
    padding: 8px 12px;
    border-radius: var(--radius-sm);
    background: rgba(245, 158, 11, 0.15);
    border: 1px solid rgba(245, 158, 11, 0.4);
    color: #f59e0b;
    font-size: 0.8rem;
    line-height: 1.4;
  }

  .controls-row {
    display: flex;
    gap: 16px;
  }

  .control-col {
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .budget-buttons {
    display: flex;
    gap: 4px;
  }

  .budget-btn {
    flex: 1;
    padding: 6px 8px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-color);
    background: var(--bg-secondary);
    color: var(--text-secondary);
    font-size: 0.75rem;
    cursor: pointer;
  }

  .budget-btn:hover {
    border-color: var(--text-muted);
    color: var(--text-primary);
  }

  .budget-btn.selected {
    background: var(--bg-tertiary);
    border-color: var(--accent);
    color: var(--text-primary);
    font-weight: 600;
  }

  .toggles-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .checkbox-label {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 0.8rem;
    color: var(--text-secondary);
    cursor: pointer;
    user-select: none;
  }

  .checkbox-label.forced {
    cursor: default;
  }

  .error-banner {
    padding: 8px 12px;
    border-radius: var(--radius-sm);
    background: rgba(239, 68, 68, 0.15);
    border: 1px solid var(--danger);
    color: var(--danger);
    font-size: 0.8rem;
  }

  .success-banner {
    padding: 8px 12px;
    border-radius: var(--radius-sm);
    background: rgba(16, 185, 129, 0.15);
    border: 1px solid var(--success, #10b981);
    color: var(--success, #10b981);
    font-size: 0.8rem;
    word-break: break-all;
  }

  .report-summary {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }

  .stat-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 999px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    font-size: 0.75rem;
    color: var(--text-secondary);
  }

  .stat-value {
    color: var(--text-primary);
  }

  .warn-pill {
    background: rgba(245, 158, 11, 0.15);
    border-color: rgba(245, 158, 11, 0.4);
    color: #f59e0b;
  }

  .redact-pill {
    background: rgba(139, 92, 246, 0.15);
    border-color: rgba(139, 92, 246, 0.4);
    color: #a78bfa;
  }

  .loading-state {
    padding: 12px;
    text-align: center;
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .dialog-footer {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 18px;
    border-top: 1px solid var(--border-color);
    background: var(--bg-secondary);
  }

  .btn {
    height: 34px;
    padding: 0 16px;
    border-radius: var(--radius-sm);
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
  }

  .primary-btn {
    background: var(--accent);
    color: white;
    border: 1px solid var(--accent);
  }

  .primary-btn:hover:not(:disabled) {
    background: color-mix(in srgb, var(--accent) 85%, black);
  }

  .secondary-btn {
    background: var(--bg-primary);
    color: var(--text-secondary);
    border: 1px solid var(--border-color);
  }

  .secondary-btn:hover:not(:disabled) {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
