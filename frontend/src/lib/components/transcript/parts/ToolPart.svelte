<script lang="ts">
  import type { ToolCall, SessionRef } from '../../../types';
  import { highlightCode } from '../../../highlight';
  import { api } from '../../../api';
  import SubagentLink from './SubagentLink.svelte';

  interface Props {
    tool: ToolCall;
    sessionRef: SessionRef;
  }

  let { tool, sessionRef }: Props = $props();

  let isFetchingBlob = $state(false);
  let fullOutput = $state<string | null>(null);
  let blobError = $state<string | null>(null);
  let copiedInput = $state(false);
  let copiedOutput = $state(false);

  let formattedInput = $derived.by(() => {
    if (!tool.input) return '';
    if (typeof tool.input === 'string') return tool.input;
    try {
      return JSON.stringify(tool.input, null, 2);
    } catch {
      return String(tool.input);
    }
  });

  let highlightedInput = $derived(highlightCode(formattedInput, 'json'));

  let displayOutput = $derived(fullOutput !== null ? fullOutput : (tool.output || ''));

  async function handleLoadFullOutput() {
    if (!tool.outputRef || isFetchingBlob) return;
    isFetchingBlob = true;
    blobError = null;
    try {
      const res = await api.getBlob(sessionRef, tool.outputRef);
      fullOutput = res.data;
    } catch (err: any) {
      blobError = err?.message || 'Failed to fetch complete tool output';
    } finally {
      isFetchingBlob = false;
    }
  }

  async function copyText(text: string, type: 'input' | 'output') {
    if (typeof navigator !== 'undefined' && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      if (type === 'input') {
        copiedInput = true;
        setTimeout(() => (copiedInput = false), 1500);
      } else {
        copiedOutput = true;
        setTimeout(() => (copiedOutput = false), 1500);
      }
    }
  }
</script>

<div class="tool-card" class:status-error={tool.status === 'error'}>
  <div class="tool-header">
    <div class="tool-id-group">
      <span class="tool-icon">🔧</span>
      <span class="tool-name">{tool.name}</span>
      <span class="tool-call-id">{tool.id}</span>
    </div>

    <div class="tool-status-badge status-{tool.status}">
      {#if tool.status === 'completed'}
        <span class="status-indicator">✓</span> completed
      {:else if tool.status === 'error'}
        <span class="status-indicator">✗</span> error
      {:else if tool.status === 'pending'}
        <span class="status-indicator">⏳</span> running
      {:else}
        <span class="status-indicator">?</span> {tool.status}
      {/if}
    </div>
  </div>

  {#if formattedInput}
    <details class="tool-section input-section">
      <summary class="section-summary">
        <span>Parameters</span>
        <button
          type="button"
          class="mini-copy-btn"
          onclick={(e) => {
            e.stopPropagation();
            copyText(formattedInput, 'input');
          }}
        >
          {copiedInput ? '✓ Copied' : 'Copy'}
        </button>
      </summary>
      <div class="code-wrapper">
        <pre><code class="hljs language-json">{@html highlightedInput}</code></pre>
      </div>
    </details>
  {/if}

  {#if displayOutput || tool.outputTruncated}
    <div class="tool-section output-section">
      <div class="output-header">
        <span class="output-label">Result:</span>
        {#if displayOutput}
          <button
            type="button"
            class="mini-copy-btn"
            onclick={() => copyText(displayOutput, 'output')}
          >
            {copiedOutput ? '✓ Copied' : 'Copy'}
          </button>
        {/if}
      </div>

      {#if displayOutput}
        <pre class="output-content"><code>{displayOutput}</code></pre>
      {/if}

      {#if tool.outputTruncated && fullOutput === null}
        <div class="truncated-banner">
          <span>Output truncated for display (exceeds 64 KiB)</span>
          <button
            type="button"
            class="load-blob-btn"
            disabled={isFetchingBlob}
            onclick={handleLoadFullOutput}
          >
            {isFetchingBlob ? 'Loading full output…' : 'Load full output'}
          </button>
        </div>
      {/if}

      {#if blobError}
        <div class="blob-error-msg">⚠️ {blobError}</div>
      {/if}
    </div>
  {/if}

  {#if tool.child}
    <div class="tool-child-section">
      <SubagentLink child={tool.child} />
    </div>
  {/if}
</div>

<style>
  .tool-card {
    margin: 8px 0;
    border-radius: 6px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    overflow: hidden;
  }

  .tool-card.status-error {
    border-color: rgba(239, 68, 68, 0.4);
  }

  .tool-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 10px;
    background-color: var(--bg-tertiary);
    border-bottom: 1px solid var(--border-color);
  }

  .tool-id-group {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .tool-icon {
    font-size: 0.85rem;
  }

  .tool-name {
    font-size: 0.8rem;
    font-weight: 600;
    font-family: var(--font-mono);
    color: var(--text-primary);
  }

  .tool-call-id {
    font-size: 0.7rem;
    font-family: var(--font-mono);
    color: var(--text-muted);
  }

  .tool-status-badge {
    font-size: 0.68rem;
    font-weight: 600;
    padding: 1px 6px;
    border-radius: 4px;
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }

  .tool-status-badge.status-completed {
    background: rgba(16, 185, 129, 0.15);
    color: var(--status-live);
  }

  .tool-status-badge.status-error {
    background: rgba(239, 68, 68, 0.15);
    color: #ef4444;
  }

  .tool-status-badge.status-pending {
    background: rgba(245, 158, 11, 0.15);
    color: #f59e0b;
  }

  .tool-section {
    border-bottom: 1px solid var(--border-color);
  }

  .tool-section:last-child {
    border-bottom: none;
  }

  .section-summary {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 4px 10px;
    font-size: 0.72rem;
    color: var(--text-secondary);
    font-weight: 500;
    cursor: pointer;
    background: var(--bg-secondary);
    user-select: none;
  }

  .section-summary:hover {
    color: var(--text-primary);
  }

  .code-wrapper {
    padding: 6px 10px;
    background: var(--bg-primary);
    overflow-x: auto;
  }

  .code-wrapper pre {
    margin: 0;
    font-size: 0.75rem;
    font-family: var(--font-mono);
  }

  .output-section {
    padding: 8px 10px;
    background: var(--bg-primary);
  }

  .output-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 4px;
  }

  .output-label {
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--text-secondary);
  }

  .output-content {
    margin: 0;
    padding: 8px;
    border-radius: 4px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    font-family: var(--font-mono);
    font-size: 0.75rem;
    line-height: 1.4;
    white-space: pre-wrap;
    word-break: break-all;
    max-height: 350px;
    overflow-y: auto;
  }

  .truncated-banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 6px;
    padding: 4px 8px;
    background: rgba(245, 158, 11, 0.1);
    border: 1px dashed rgba(245, 158, 11, 0.4);
    border-radius: 4px;
    font-size: 0.72rem;
    color: var(--text-secondary);
  }

  .load-blob-btn {
    padding: 2px 8px;
    font-size: 0.7rem;
    font-weight: 500;
    border-radius: 4px;
    background: var(--accent-color);
    color: white;
    cursor: pointer;
  }

  .load-blob-btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }

  .mini-copy-btn {
    padding: 1px 6px;
    font-size: 0.68rem;
    border-radius: 3px;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
    border: 1px solid var(--border-color);
    cursor: pointer;
  }

  .mini-copy-btn:hover {
    color: var(--text-primary);
  }

  .blob-error-msg {
    margin-top: 6px;
    font-size: 0.72rem;
    color: #ef4444;
  }

  .tool-child-section {
    padding: 6px 10px;
    background: var(--bg-secondary);
  }
</style>
