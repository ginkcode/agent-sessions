<script lang="ts">
  import { importer } from '../../stores/importer.svelte';
  import { BUDGET_PRESETS, ALL_AGENTS } from '../../portable';
  import { formatBytes, formatTokens } from '../../format';
  import AgentIcon from './AgentIcon.svelte';

  let previewTab = $state<'prompt' | 'full'>('prompt');

  function handleClose() {
    importer.close();
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && importer.dialogOpen) {
      handleClose();
    }
  }

  let totalRedactions = $derived.by(() => {
    if (!importer.preview?.report?.redactionCounts) return 0;
    const rc = importer.preview.report.redactionCounts;
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

{#if importer.dialogOpen}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) handleClose();
    }}
  >
    <div
      class="dialog import-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="import-dialog-title"
    >
      <header class="dialog-header">
        <div class="header-title-group">
          <h2 id="import-dialog-title">Session Bundle</h2>
          {#if importer.bundle}
            <span class="profile-badge" class:share-safe={importer.bundle.profile === 'share-safe'}>
              {importer.bundle.profile === 'share-safe' ? 'Share-safe' : 'Complete'}
            </span>
          {/if}
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

      {#if importer.bundle}
        <!-- Tab selector -->
        <nav class="dialog-tabs" aria-label="Bundle actions">
          <button
            type="button"
            class="dialog-tab-btn"
            class:active={importer.activeTab === 'summary'}
            onclick={() => importer.setTab('summary')}
          >
            Bundle Overview
          </button>
          <button
            type="button"
            class="dialog-tab-btn"
            class:active={importer.activeTab === 'handoff'}
            onclick={() => importer.setTab('handoff')}
          >
            Continue in Agent…
          </button>
        </nav>
      {/if}

      <div class="dialog-body">
        {#if importer.error}
          <div class="error-banner" role="alert">
            {importer.error}
          </div>
        {/if}

        {#if importer.bundle}
          {#if importer.activeTab === 'summary'}
            <!-- Bundle Overview Tab -->
            <section class="section-group">
              <div class="bundle-hero">
                <div class="hero-left">
                  <div class="agent-pill">
                    <AgentIcon agent={importer.bundle.source.agent} size={18} />
                    <span class="agent-name">{importer.bundle.source.agent}</span>
                    {#if importer.bundle.source.agentVersion}
                      <span class="agent-ver">v{importer.bundle.source.agentVersion}</span>
                    {/if}
                  </div>
                  <h3 class="hero-title">{importer.bundle.source.title || 'Untitled Session'}</h3>
                  <div class="hero-path" title={importer.bundle.path}>
                    <span class="path-label">File:</span> <code>{importer.bundle.path}</code>
                  </div>
                </div>
                <div class="hero-right">
                  <span class="verified-badge" title="Archive verified with SHA-256 checksums">
                    ✓ Verified Archive
                  </span>
                </div>
              </div>
            </section>

            <!-- Metadata Grid -->
            <section class="section-group meta-grid">
              <div class="meta-item">
                <span class="meta-label">Original Directory</span>
                <span class="meta-val" title={importer.bundle.source.cwd}>
                  {importer.bundle.source.cwd || '—'}
                </span>
              </div>
              <div class="meta-item">
                <span class="meta-label">Git Branch</span>
                <span class="meta-val">{importer.bundle.source.gitBranch || '—'}</span>
              </div>
              <div class="meta-item">
                <span class="meta-label">Exported</span>
                <span class="meta-val">{new Date(importer.bundle.createdAt).toLocaleString()}</span>
              </div>
              <div class="meta-item">
                <span class="meta-label">Bundle Format</span>
                <span class="meta-val">{importer.bundle.format} v{importer.bundle.version}</span>
              </div>
            </section>

            <!-- Content Stats -->
            <section class="section-group">
              <span class="group-label">Bundle Contents</span>
              <div class="stats-row">
                <div class="stat-card">
                  <span class="stat-num">{importer.bundle.sessionsCount}</span>
                  <span class="stat-text">Sessions</span>
                </div>
                <div class="stat-card">
                  <span class="stat-num">{importer.bundle.nativeFilesCount}</span>
                  <span class="stat-text">
                    Native Files ({formatBytes(importer.bundle.nativeBytes)})
                  </span>
                </div>
                <div class="stat-card">
                  <span class="stat-num">{importer.bundle.redactionCounts}</span>
                  <span class="stat-text">Scrubbed Secrets</span>
                </div>
                <div class="stat-card">
                  <span class="stat-num">≈{formatTokens(importer.bundle.handoffTokens)}</span>
                  <span class="stat-text">Universal Tokens</span>
                </div>
              </div>
            </section>

            <!-- Sessions List -->
            {#if importer.bundle.sessions.length > 0}
              <section class="section-group">
                <span class="group-label">Included Sessions</span>
                <div class="sessions-list">
                  {#each importer.bundle.sessions as s}
                    <div class="session-item" class:is-child={Boolean(s.parentId)}>
                      <div class="session-item-left">
                        <AgentIcon agent={s.ref.agent} size={14} />
                        <span class="session-name">
                          {s.title || s.ref.id}
                        </span>
                        {#if s.parentId}
                          <span class="child-tag">subagent</span>
                        {/if}
                      </div>
                      <div class="session-item-right">
                        {#if s.nativeFiles > 0}
                          <span class="native-tag">
                            {s.nativeFiles} native files ({formatBytes(s.nativeBytes)})
                          </span>
                        {:else}
                          <span class="safe-tag">universal only</span>
                        {/if}
                      </div>
                    </div>
                  {/each}
                </div>
              </section>
            {/if}

            <!-- Restore and Continuation Actions Banner -->
            <section class="section-group action-cards">
              {#if importer.bundle.restoreAvailable}
                <div class="action-card restore-card">
                  <div class="card-info">
                    <h4>Restore to {importer.bundle.source.agent}</h4>
                    <p>
                      This bundle contains the native session files and can be restored directly into
                      your local {importer.bundle.source.agent} environment.
                    </p>
                  </div>
                  <div class="card-status">
                    <span class="status-ready">Ready for restore</span>
                  </div>
                </div>
              {:else}
                <div class="action-card info-card">
                  <div class="card-info">
                    <h4>Share-Safe Bundle (No Native Files)</h4>
                    <p>
                      This bundle was exported in share-safe mode. All proprietary files were stripped
                      and secrets redacted. You can resume this session seamlessly in any supported
                      coding agent using the cross-agent handoff.
                    </p>
                  </div>
                  <button
                    type="button"
                    class="btn primary-btn"
                    onclick={() => importer.setTab('handoff')}
                  >
                    Continue in Agent…
                  </button>
                </div>
              {/if}
            </section>
          {:else}
            <!-- Handoff / Continue In Tab -->
            <!-- Target Selector -->
            <section class="section-group">
              <span class="group-label">Target Agent</span>
              <div class="agent-buttons">
                {#each ALL_AGENTS as agent (agent.id)}
                  <button
                    type="button"
                    class="agent-select-btn"
                    class:selected={importer.target === agent.id}
                    onclick={() => importer.setTarget(agent.id)}
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
                <span class="group-label">Token Budget</span>
                <div class="budget-buttons">
                  {#each BUDGET_PRESETS as preset (preset.id)}
                    <button
                      type="button"
                      class="budget-btn"
                      class:selected={importer.budget === preset.tokens}
                      title={preset.description}
                      onclick={() => importer.setBudget(preset.tokens)}
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
                      checked={importer.includeReasoning}
                      onchange={(e) => importer.setIncludeReasoning(e.currentTarget.checked)}
                    />
                    <span>Include reasoning</span>
                  </label>
                  <label class="checkbox-label" class:forced={importer.redactionForced}>
                    <input
                      type="checkbox"
                      checked={importer.redactSecrets || importer.redactionForced}
                      disabled={importer.redactionForced}
                      onchange={(e) => importer.setRedactSecrets(e.currentTarget.checked)}
                    />
                    <span>Redact secrets{importer.redactionForced ? ' (required)' : ''}</span>
                  </label>
                </div>
              </div>
            </div>

            <!-- Working Directory Override -->
            <div class="cwd-row">
              <label class="group-label" for="target-cwd-input">Target Working Directory</label>
              <input
                id="target-cwd-input"
                type="text"
                class="text-input"
                placeholder={importer.bundle.source.cwd || '/path/to/project'}
                value={importer.cwd}
                oninput={(e) => importer.setCWD(e.currentTarget.value)}
              />
            </div>

            {#if importer.clipboardError}
              <div class="clipboard-error-banner" role="alert">
                {importer.clipboardError}
              </div>
            {/if}

            {#if importer.savedPath}
              <div class="success-banner" role="status">
                ✓ Handoff document saved to: <code>{importer.savedPath}</code>
              </div>
            {/if}

            <!-- Handoff Preview -->
            {#if importer.preview}
              <div class="report-summary">
                <div class="stat-pill">
                  <span class="stat-label">Estimated Tokens:</span>
                  <strong class="stat-value">{formatTokens(importer.preview.report.estimatedTokens)}</strong>
                </div>

                {#if importer.preview.report.trimmed}
                  <div class="stat-pill warn-pill">
                    <span>⚠️ Trimmed to fit budget</span>
                  </div>
                {/if}

                {#if totalRedactions > 0}
                  <div class="stat-pill redact-pill">
                    <span>🔒 {totalRedactions} secrets redacted</span>
                  </div>
                {/if}

                {#if importer.preview.promptFile}
                  <div class="stat-pill notice-pill" title={importer.preview.promptFile}>
                    <span>📄 Delivered via handoff file</span>
                  </div>
                {/if}
              </div>

              {#if importer.preview.report.droppedItems && importer.preview.report.droppedItems.length > 0}
                <details class="dropped-details">
                  <summary>Pruned from context ({importer.preview.report.droppedItems.length} items)</summary>
                  <ul class="dropped-list">
                    {#each importer.preview.report.droppedItems as item}
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
                    onclick={() => importer.copyCommand()}
                  >
                    {importer.copiedCommand ? '✓ Copied' : 'Copy Command'}
                  </button>
                </div>
                <pre class="command-code"><code>{importer.preview.command}</code></pre>
              </div>

              <!-- Document Preview -->
              <div class="preview-container">
                <div class="preview-tabs">
                  <button
                    type="button"
                    class="tab-btn"
                    class:active={previewTab === 'prompt'}
                    onclick={() => (previewTab = 'prompt')}
                  >
                    Prompt Preview ({importer.preview.promptBytes} B)
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
                    onclick={() => importer.copyPrompt()}
                  >
                    {importer.copiedPrompt ? '✓ Copied' : 'Copy Prompt'}
                  </button>
                </div>
                <div class="preview-content">
                  <pre><code>{previewTab === 'prompt' ? importer.preview.promptMarkdown : importer.preview.fullMarkdown}</code></pre>
                </div>
              </div>
            {:else if importer.loadingPreview}
              <div class="loading-state">
                <p>Generating handoff preview…</p>
              </div>
            {/if}
          {/if}
        {/if}
      </div>

      <footer class="dialog-footer">
        {#if importer.bundle && importer.activeTab === 'handoff'}
          <button
            type="button"
            class="btn save-btn"
            disabled={importer.saving || !importer.preview}
            onclick={() => importer.saveHandoff()}
          >
            {importer.saving ? 'Saving…' : 'Save As…'}
          </button>
          <button
            type="button"
            class="btn primary-btn"
            disabled={!importer.preview}
            onclick={() => importer.copyCommand()}
          >
            {importer.copiedCommand ? '✓ Copied Command' : 'Copy Launch Command'}
          </button>
        {:else if importer.bundle && importer.activeTab === 'summary'}
          <button
            type="button"
            class="btn"
            onclick={() => importer.open()}
          >
            Open Another Bundle…
          </button>
          <button
            type="button"
            class="btn primary-btn"
            onclick={() => importer.setTab('handoff')}
          >
            Continue in Agent…
          </button>
        {/if}
        <button type="button" class="btn" onclick={handleClose}>
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
    background-color: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    padding: 16px;
  }

  .dialog {
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.35);
    width: 100%;
    max-width: 720px;
    max-height: 90vh;
    display: flex;
    flex-direction: column;
    color: var(--text-primary);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .header-title-group {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .header-title-group h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }

  .profile-badge {
    font-size: 0.72rem;
    padding: 2px 8px;
    border-radius: 10px;
    background: rgba(34, 197, 94, 0.15);
    color: #22c55e;
    border: 1px solid rgba(34, 197, 94, 0.3);
    font-weight: 500;
  }

  .profile-badge.share-safe {
    background: rgba(168, 85, 247, 0.15);
    color: #a855f7;
    border-color: rgba(168, 85, 247, 0.3);
  }

  .dialog-close {
    background: transparent;
    border: none;
    color: var(--text-secondary);
    font-size: 1.1rem;
    cursor: pointer;
    padding: 4px 8px;
    border-radius: 4px;
    line-height: 1;
  }

  .dialog-close:hover {
    color: var(--text-primary);
    background-color: var(--hover-bg);
  }

  .dialog-tabs {
    display: flex;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
    padding: 0 16px;
    gap: 8px;
  }

  .dialog-tab-btn {
    background: none;
    border: none;
    padding: 8px 14px;
    font-size: 0.85rem;
    color: var(--text-secondary);
    cursor: pointer;
    border-bottom: 2px solid transparent;
    font-weight: 500;
    transition: color 0.15s, border-color 0.15s;
  }

  .dialog-tab-btn:hover {
    color: var(--text-primary);
  }

  .dialog-tab-btn.active {
    color: var(--accent-color, #3b82f6);
    border-bottom-color: var(--accent-color, #3b82f6);
  }

  .dialog-body {
    padding: 16px 18px;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 16px;
    flex: 1;
  }

  .section-group {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .group-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .bundle-hero {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 14px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    gap: 12px;
  }

  .hero-left {
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-width: 0;
  }

  .agent-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 0.82rem;
    color: var(--text-primary);
    font-weight: 600;
  }

  .agent-ver {
    font-size: 0.72rem;
    color: var(--text-secondary);
    font-weight: normal;
  }

  .hero-title {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
    color: var(--text-primary);
    word-break: break-word;
  }

  .hero-path {
    font-size: 0.75rem;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .hero-path code {
    color: var(--text-primary);
    font-size: 0.72rem;
  }

  .verified-badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px;
    background: rgba(34, 197, 94, 0.15);
    color: #22c55e;
    border: 1px solid rgba(34, 197, 94, 0.3);
    border-radius: 4px;
    font-size: 0.74rem;
    font-weight: 600;
    white-space: nowrap;
  }

  .meta-grid {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 10px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 12px 14px;
  }

  .meta-item {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .meta-label {
    font-size: 0.7rem;
    color: var(--text-secondary);
    text-transform: uppercase;
  }

  .meta-val {
    font-size: 0.82rem;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .stats-row {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 8px;
  }

  .stat-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 10px 8px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    text-align: center;
  }

  .stat-num {
    font-size: 1.05rem;
    font-weight: 700;
    color: var(--text-primary);
  }

  .stat-text {
    font-size: 0.7rem;
    color: var(--text-secondary);
    margin-top: 2px;
  }

  .sessions-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
    max-height: 140px;
    overflow-y: auto;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background-color: var(--bg-secondary);
    padding: 6px;
  }

  .session-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 10px;
    border-radius: 4px;
    font-size: 0.8rem;
    background-color: var(--bg-primary);
  }

  .session-item.is-child {
    margin-left: 16px;
    border-left: 2px solid var(--border-color);
  }

  .session-item-left {
    display: flex;
    align-items: center;
    gap: 6px;
    overflow: hidden;
  }

  .session-name {
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .child-tag {
    font-size: 0.65rem;
    padding: 1px 5px;
    border-radius: 4px;
    background: var(--hover-bg);
    color: var(--text-secondary);
  }

  .native-tag {
    font-size: 0.7rem;
    color: var(--text-secondary);
  }

  .safe-tag {
    font-size: 0.7rem;
    color: #a855f7;
  }

  .action-cards {
    margin-top: 4px;
  }

  .action-card {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 14px;
    border-radius: 6px;
    border: 1px solid var(--border-color);
    gap: 12px;
  }

  .restore-card {
    background-color: rgba(34, 197, 94, 0.05);
    border-color: rgba(34, 197, 94, 0.25);
  }

  .info-card {
    background-color: rgba(168, 85, 247, 0.05);
    border-color: rgba(168, 85, 247, 0.25);
  }

  .card-info h4 {
    margin: 0 0 4px 0;
    font-size: 0.88rem;
    font-weight: 600;
  }

  .card-info p {
    margin: 0;
    font-size: 0.76rem;
    color: var(--text-secondary);
    line-height: 1.35;
  }

  .status-ready {
    font-size: 0.75rem;
    font-weight: 600;
    color: #22c55e;
    background: rgba(34, 197, 94, 0.15);
    padding: 3px 8px;
    border-radius: 4px;
    white-space: nowrap;
  }

  /* Handoff Tab styling */
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
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    color: var(--text-primary);
    cursor: pointer;
    font-size: 0.85rem;
    font-weight: 500;
    transition: all 0.15s;
  }

  .agent-select-btn:hover {
    border-color: var(--accent-color, #3b82f6);
    background-color: var(--hover-bg);
  }

  .agent-select-btn.selected {
    border-color: var(--accent-color, #3b82f6);
    background-color: rgba(59, 130, 246, 0.1);
    color: var(--accent-color, #3b82f6);
    font-weight: 600;
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
    padding: 6px 4px;
    font-size: 0.75rem;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
    font-weight: 500;
  }

  .budget-btn:hover {
    color: var(--text-primary);
    background-color: var(--hover-bg);
  }

  .budget-btn.selected {
    background-color: var(--accent-color, #3b82f6);
    color: #fff;
    border-color: var(--accent-color, #3b82f6);
  }

  .toggles-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin-top: 2px;
  }

  .checkbox-label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.8rem;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .checkbox-label.forced {
    opacity: 0.85;
    cursor: default;
  }

  .cwd-row {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .text-input {
    width: 100%;
    box-sizing: border-box;
    padding: 7px 10px;
    font-size: 0.82rem;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
    font-family: inherit;
  }

  .text-input:focus {
    outline: none;
    border-color: var(--accent-color, #3b82f6);
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
    gap: 4px;
    padding: 3px 8px;
    border-radius: 4px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    font-size: 0.75rem;
  }

  .stat-label {
    color: var(--text-secondary);
  }

  .stat-value {
    color: var(--text-primary);
  }

  .warn-pill {
    background: rgba(234, 179, 8, 0.15);
    border-color: rgba(234, 179, 8, 0.3);
    color: #eab308;
  }

  .redact-pill {
    background: rgba(168, 85, 247, 0.15);
    border-color: rgba(168, 85, 247, 0.3);
    color: #a855f7;
  }

  .notice-pill {
    background: rgba(59, 130, 246, 0.15);
    border-color: rgba(59, 130, 246, 0.3);
    color: #3b82f6;
  }

  .dropped-details {
    font-size: 0.75rem;
    color: var(--text-secondary);
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    padding: 6px 10px;
  }

  .dropped-details summary {
    cursor: pointer;
    font-weight: 500;
  }

  .dropped-list {
    margin: 6px 0 0 16px;
    padding: 0;
  }

  .command-box-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .command-box-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .command-box-header .label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-secondary);
    text-transform: uppercase;
  }

  .mini-btn {
    font-size: 0.72rem;
    padding: 2px 8px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
    cursor: pointer;
  }

  .mini-btn:hover {
    background-color: var(--hover-bg);
  }

  .command-code {
    margin: 0;
    padding: 8px 10px;
    background-color: #121214;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: #e5e7eb;
    font-family: monospace;
    font-size: 0.78rem;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-all;
  }

  .preview-container {
    display: flex;
    flex-direction: column;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    overflow: hidden;
  }

  .preview-tabs {
    display: flex;
    align-items: center;
    background-color: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
    padding: 0 8px;
    gap: 6px;
  }

  .tab-btn {
    background: none;
    border: none;
    padding: 6px 10px;
    font-size: 0.75rem;
    color: var(--text-secondary);
    cursor: pointer;
    border-bottom: 2px solid transparent;
  }

  .tab-btn.active {
    color: var(--accent-color, #3b82f6);
    border-bottom-color: var(--accent-color, #3b82f6);
    font-weight: 600;
  }

  .tab-copy-btn {
    margin-left: auto;
  }

  .preview-content {
    background-color: #121214;
    max-height: 180px;
    overflow-y: auto;
    padding: 10px;
  }

  .preview-content pre {
    margin: 0;
  }

  .preview-content code {
    color: #d1d5db;
    font-family: monospace;
    font-size: 0.75rem;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .loading-state {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    color: var(--text-secondary);
    font-size: 0.85rem;
  }

  .error-banner {
    padding: 8px 12px;
    background-color: rgba(239, 68, 68, 0.15);
    border: 1px solid rgba(239, 68, 68, 0.3);
    color: #ef4444;
    border-radius: 4px;
    font-size: 0.8rem;
  }

  .clipboard-error-banner {
    padding: 8px 12px;
    background-color: rgba(245, 158, 11, 0.15);
    border: 1px solid rgba(245, 158, 11, 0.3);
    color: #f59e0b;
    border-radius: 4px;
    font-size: 0.8rem;
  }

  .success-banner {
    padding: 8px 12px;
    background-color: rgba(34, 197, 94, 0.15);
    border: 1px solid rgba(34, 197, 94, 0.3);
    color: #22c55e;
    border-radius: 4px;
    font-size: 0.8rem;
  }

  .dialog-footer {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 8px;
    padding: 12px 18px;
    border-top: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .btn {
    height: 34px;
    padding: 0 16px;
    font-size: 0.82rem;
    font-weight: 500;
    border-radius: 4px;
    border: 1px solid var(--border-color);
    background-color: var(--bg-primary);
    color: var(--text-primary);
    cursor: pointer;
    transition: all 0.15s;
  }

  .btn:hover:not(:disabled) {
    background-color: var(--hover-bg);
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .primary-btn {
    background-color: var(--accent-color, #3b82f6);
    border-color: var(--accent-color, #3b82f6);
    color: #fff;
  }

  .primary-btn:hover:not(:disabled) {
    background-color: #2563eb;
    border-color: #2563eb;
  }

  .save-btn {
    margin-right: auto;
  }
</style>
