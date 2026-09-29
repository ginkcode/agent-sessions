<script lang="ts">
  import { highlightCode } from '../../../highlight';

  interface Props {
    files?: string[];
    text?: string;
  }

  let { files = [], text }: Props = $props();

  let highlightedDiff = $derived(text ? highlightCode(text, 'diff') : '');
  let isExpanded = $state(false);
</script>

<div class="patch-card">
  <div class="patch-header">
    <div class="patch-title-group">
      <span class="patch-icon">📝</span>
      <span class="patch-title">File Changes</span>
      {#if files && files.length > 0}
        <span class="patch-count">{files.length} {files.length === 1 ? 'file' : 'files'}</span>
      {/if}
    </div>

    {#if text}
      <button
        type="button"
        class="toggle-diff-btn"
        onclick={() => (isExpanded = !isExpanded)}
      >
        {isExpanded ? 'Hide Diff' : 'View Diff'}
      </button>
    {/if}
  </div>

  {#if files && files.length > 0}
    <ul class="patch-files-list">
      {#each files as file}
        <li class="patch-file-item">
          <span class="file-icon">📄</span>
          <span class="file-name">{file}</span>
        </li>
      {/each}
    </ul>
  {/if}

  {#if text && isExpanded}
    <div class="diff-viewer">
      <pre class="diff-code"><code class="hljs language-diff">{@html highlightedDiff}</code></pre>
    </div>
  {/if}
</div>

<style>
  .patch-card {
    margin: 8px 0;
    border-radius: 6px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    overflow: hidden;
  }

  .patch-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 10px;
    background-color: var(--bg-tertiary);
    border-bottom: 1px solid var(--border-color);
  }

  .patch-title-group {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .patch-icon {
    font-size: 13.6px;
  }

  .patch-title {
    font-size: 12.48px;
    font-weight: 600;
    color: var(--text-primary);
  }

  .patch-count {
    font-size: 11.2px;
    color: var(--text-muted);
  }

  .toggle-diff-btn {
    padding: 2px 8px;
    font-size: 11.2px;
    border-radius: 4px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .toggle-diff-btn:hover {
    color: var(--text-primary);
  }

  .patch-files-list {
    margin: 0;
    padding: 6px 10px;
    list-style: none;
    display: flex;
    flex-direction: column;
    gap: 4px;
    background: var(--bg-primary);
  }

  .patch-file-item {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    font-family: var(--font-mono);
    color: var(--text-secondary);
  }

  .file-icon {
    font-size: 12px;
    opacity: 0.7;
  }

  .diff-viewer {
    border-top: 1px solid var(--border-color);
    max-height: 400px;
    overflow-y: auto;
    background: var(--bg-primary);
    padding: 8px 10px;
  }

  .diff-code {
    margin: 0;
    font-family: var(--font-mono);
    font-size: 11.52px;
    line-height: 1.4;
    white-space: pre-wrap;
    word-break: break-all;
  }
</style>
