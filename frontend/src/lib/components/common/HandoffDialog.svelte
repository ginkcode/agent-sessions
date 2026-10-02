<script lang="ts">
  import { handoff } from '../../stores/handoff.svelte';
  import { BUDGET_PRESETS, ALL_AGENTS } from '../../portable';
  import { formatTokens } from '../../format';
  import AgentIcon from './AgentIcon.svelte';
  import RemoteCommandNote from './RemoteCommandNote.svelte';
  import MarkdownDoc from './MarkdownDoc.svelte';
  import { link } from '../../stores/link.svelte';

  let previewTab = $state<'prompt' | 'full'>('prompt');

  function handleClose() {
    handoff.close();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      handleClose();
    }
  }

  let totalRedactions = $derived.by(() => {
    if (!handoff.preview?.report?.redactionCounts) return 0;
    const rc = handoff.preview.report.redactionCounts;
    return (
      (rc.emails || 0) +
      (rc.ipv4 || 0) +
      (rc.apiKeys || 0) +
      (rc.sshKeys || 0) +
      (rc.tokens || 0) +
      (rc.passwords || 0)
    );
  });
</script>

<svelte:window onkeydown={handleKeydown} />

{#if handoff.dialogOpen && handoff.session}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) handleClose();
    }}
  >
    <div
      class="dialog handoff-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="handoff-dialog-title"
    >
      <header class="dialog-header">
        <div class="header-title-group">
          <h2 id="handoff-dialog-title">Continue in Another Agent</h2>
          <span class="source-badge">
            From <span class="agent-name">{handoff.session.ref.agent}</span>
          </span>
        </div>
        <button
          type="button"
          class="dialog-close"
          title="Close dialog"
          aria-label="Close dialog"
          onclick={handleClose}
        >
          ✕
        </button>
      </header>

      <div class="dialog-body">
        <!-- Target Agent Selection -->
        <section class="section-group">
          <span class="group-label">Target Agent</span>
          <div class="agent-buttons" id="target-selector">
            {#each ALL_AGENTS as agent (agent.id)}
              <button
                type="button"
                class="agent-select-btn"
                class:selected={handoff.target === agent.id}
                onclick={() => handoff.setTarget(agent.id)}
              >
                <AgentIcon agent={agent.id} size={16} />
                <span>{agent.label}</span>
              </button>
            {/each}
          </div>
        </section>

        <!-- Budget & Options -->
        <div class="controls-row">
          <div class="control-col">
            <span class="group-label">Budget</span>
            <div class="budget-buttons" id="budget-selector">
              {#each BUDGET_PRESETS as preset (preset.id)}
                <button
                  type="button"
                  class="budget-btn"
                  class:selected={handoff.budget === preset.tokens}
                  title={preset.description}
                  onclick={() => handoff.setBudget(preset.tokens)}
                >
                  {preset.label}
                </button>
              {/each}
            </div>
          </div>

          <div class="control-col options-col">
            <span class="group-label">Options</span>
            <div class="toggles-group">
              <label class="checkbox-label">
                <input
                  type="checkbox"
                  checked={handoff.includeReasoning}
                  onchange={(e) => handoff.setIncludeReasoning(e.currentTarget.checked)}
                />
                <span>Include reasoning</span>
              </label>
              <label class="checkbox-label">
                <input
                  type="checkbox"
                  checked={handoff.redactSecrets}
                  onchange={(e) => handoff.setRedactSecrets(e.currentTarget.checked)}
                />
                <span>Redact secrets</span>
              </label>
            </div>
          </div>
        </div>

        <!-- CWD Field -->
        <div class="cwd-row">
          <label class="group-label" for="cwd-input">Working Directory</label>
          <input
            id="cwd-input"
            type="text"
            class="text-input"
            placeholder={handoff.session.cwd}
            value={handoff.cwd}
            oninput={(e) => handoff.setCWD(e.currentTarget.value)}
          />
        </div>

        {#if handoff.error}
          <div class="error-banner" role="alert">
            {handoff.error}
          </div>
        {/if}

        {#if handoff.clipboardError}
          <div class="clipboard-error-banner" role="alert">
            {handoff.clipboardError}
          </div>
        {/if}

        {#if handoff.savedPath}
          <div class="success-banner" role="status">
            ✓ Handoff document saved to: <code>{handoff.savedPath}</code>
          </div>
        {/if}

        <!-- Report Summary -->
        {#if handoff.preview}
          <div class="report-summary">
            <div class="stat-pill">
              <span class="stat-label">Estimated Tokens:</span>
              <strong class="stat-value">{formatTokens(handoff.preview.report.estimatedTokens)}</strong>
            </div>

            {#if handoff.preview.report.trimmed}
              <div class="stat-pill warn-pill">
                <span>⚠️ Trimmed to fit budget</span>
              </div>
            {/if}

            {#if totalRedactions > 0}
              <div class="stat-pill redact-pill">
                <span>🔒 {totalRedactions} secrets redacted</span>
              </div>
            {/if}

            {#if handoff.preview.promptFile}
              <div class="stat-pill notice-pill" title={handoff.preview.promptFile}>
                <span>📄 Delivered via handoff file</span>
              </div>
            {/if}
          </div>

          {#if handoff.preview.report.droppedItems && handoff.preview.report.droppedItems.length > 0}
            <details class="dropped-details">
              <summary>Pruned from context ({handoff.preview.report.droppedItems.length} items)</summary>
              <ul class="dropped-list">
                {#each handoff.preview.report.droppedItems as item}
                  <li>{item}</li>
                {/each}
              </ul>
            </details>
          {/if}

          <!-- Command Preview -->
          <div class="command-box-group">
            <div class="command-box-header">
              <span class="label">Launch Command</span>
              <button
                type="button"
                class="copy-btn mini-btn"
                onclick={() => handoff.copyCommand()}
              >
                {handoff.copiedCommand ? '✓ Copied' : 'Copy Command'}
              </button>
            </div>
            <pre class="command-code"><code>{handoff.preview.command}</code></pre>
            {#if link.dataHost}
              <RemoteCommandNote host={link.dataHost} />
            {/if}
          </div>

          <!-- Document Preview Tab -->
          <div class="preview-container">
            <div class="preview-tabs">
              <button
                type="button"
                class="tab-btn"
                class:active={previewTab === 'prompt'}
                onclick={() => (previewTab = 'prompt')}
              >
                Prompt Preview ({handoff.preview.promptBytes} B)
              </button>
              <button
                type="button"
                class="tab-btn"
                class:active={previewTab === 'full'}
                onclick={() => (previewTab = 'full')}
              >
                Full Markdown Doc
              </button>
              <button
                type="button"
                class="copy-btn mini-btn tab-copy-btn"
                onclick={() => handoff.copyCurrent(previewTab)}
              >
                {handoff.copiedPrompt ? '✓ Copied' : previewTab === 'full' ? 'Copy Doc' : 'Copy Prompt'}
              </button>
            </div>
            {#if previewTab === 'prompt'}
              <!-- The prompt is the exact text that gets pasted, so show it raw. -->
              <div class="preview-content preview-raw">
                <pre><code>{handoff.preview.promptMarkdown}</code></pre>
              </div>
            {:else}
              <!-- The full doc is a document, so render it as one. -->
              <div class="preview-content preview-rendered">
                <MarkdownDoc source={handoff.preview.fullMarkdown} />
              </div>
            {/if}
          </div>
        {:else if handoff.loading}
          <div class="loading-state">
            <p>Generating handoff preview…</p>
          </div>
        {/if}
      </div>

      <footer class="dialog-footer">
        <button
          type="button"
          class="btn save-btn"
          disabled={handoff.saving || !handoff.preview}
          onclick={() => handoff.save()}
        >
          {handoff.saving ? 'Saving…' : 'Save As…'}
        </button>
        <button
          type="button"
          class="btn primary-btn"
          onclick={() => handoff.copyCommand()}
        >
          {handoff.copiedCommand ? '✓ Copied' : 'Copy Launch Command'}
        </button>
        <button
          type="button"
          class="btn secondary-btn"
          onclick={handleClose}
        >
          Close
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
    width: 680px;
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

  .agent-buttons {
    display: flex;
    gap: 8px;
  }

  .agent-select-btn {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 8px 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border-color);
    background: var(--bg-secondary);
    color: var(--text-secondary);
    font-size: 0.85rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .agent-select-btn:hover {
    border-color: var(--text-muted);
    color: var(--text-primary);
  }

  .agent-select-btn.selected {
    background: var(--bg-tertiary);
    border-color: var(--accent);
    color: var(--text-primary);
    box-shadow: 0 0 0 1px var(--accent);
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
    gap: 12px;
    align-items: center;
    height: 32px;
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

  .cwd-row {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .text-input {
    width: 100%;
    box-sizing: border-box;
    padding: 6px 10px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-color);
    background: var(--bg-secondary);
    color: var(--text-primary);
    font-size: 0.8rem;
    font-family: var(--font-mono, monospace);
  }

  .text-input:focus {
    outline: none;
    border-color: var(--accent);
  }

  .error-banner,
  .clipboard-error-banner {
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

  .notice-pill {
    background: rgba(59, 130, 246, 0.15);
    border-color: rgba(59, 130, 246, 0.4);
    color: #60a5fa;
  }

  .dropped-details {
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .dropped-details summary {
    cursor: pointer;
    user-select: none;
  }

  .dropped-list {
    margin: 6px 0 0 16px;
    padding: 0;
    max-height: 80px;
    overflow-y: auto;
  }

  .command-box-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-sm);
    padding: 8px 12px;
  }

  .command-box-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .command-box-header .label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-secondary);
  }

  .command-code {
    margin: 0;
    font-family: var(--font-mono, monospace);
    font-size: 0.75rem;
    color: var(--text-primary);
    white-space: pre-wrap;
    word-break: break-all;
    max-height: 70px;
    overflow-y: auto;
  }

  .preview-container {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border-color);
    border-radius: var(--radius-sm);
    overflow: hidden;
  }

  .preview-tabs {
    display: flex;
    align-items: center;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
    padding: 0 8px;
  }

  .tab-btn {
    border: none;
    background: transparent;
    padding: 6px 12px;
    font-size: 0.75rem;
    color: var(--text-secondary);
    cursor: pointer;
    border-bottom: 2px solid transparent;
  }

  .tab-btn.active {
    color: var(--text-primary);
    font-weight: 600;
    border-bottom-color: var(--accent);
  }

  .tab-copy-btn {
    margin-left: auto;
  }

  .mini-btn {
    padding: 3px 8px;
    font-size: 0.7rem;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-color);
    background: var(--bg-primary);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .mini-btn:hover {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .preview-content {
    background: var(--bg-primary);
    padding: 10px;
    max-height: 180px;
    overflow-y: auto;
  }

  .preview-content pre {
    margin: 0;
    font-family: var(--font-mono, monospace);
    font-size: 0.75rem;
    line-height: 1.4;
    white-space: pre-wrap;
    word-break: break-word;
    color: var(--text-secondary);
  }

  .preview-rendered {
    max-height: 260px;
  }

  .loading-state {
    padding: 24px;
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
    transition: all 0.15s ease;
  }

  .primary-btn {
    background: var(--accent);
    color: white;
    border: 1px solid var(--accent);
  }

  .primary-btn:hover:not(:disabled) {
    background: color-mix(in srgb, var(--accent) 85%, black);
  }

  .save-btn,
  .secondary-btn {
    background: var(--bg-primary);
    color: var(--text-secondary);
    border: 1px solid var(--border-color);
  }

  .save-btn:hover:not(:disabled),
  .secondary-btn:hover:not(:disabled) {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
</style>
