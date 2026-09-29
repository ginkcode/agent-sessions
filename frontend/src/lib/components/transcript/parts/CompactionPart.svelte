<script lang="ts">
  interface Props {
    text?: string;
  }

  let { text = '' }: Props = $props();

  let isExpanded = $state(false);
</script>

<div class="compaction-divider">
  <div class="divider-line"></div>
  <button
    type="button"
    class="compaction-pill"
    title={text ? 'Click to toggle compaction summary' : 'Context was compacted here'}
    onclick={() => {
      if (text) isExpanded = !isExpanded;
    }}
  >
    <span class="pill-icon">✂️</span>
    <span class="pill-text">Context Compacted</span>
    {#if text}
      <span class="pill-chevron">{isExpanded ? '▲' : '▼'}</span>
    {/if}
  </button>
  <div class="divider-line"></div>
</div>

{#if text && isExpanded}
  <div class="compaction-details">
    <div class="summary-header">Summary:</div>
    <div class="summary-text">{text}</div>
  </div>
{/if}

<style>
  .compaction-divider {
    display: flex;
    align-items: center;
    gap: 12px;
    margin: 16px 0;
    user-select: none;
  }

  .divider-line {
    flex: 1;
    height: 1px;
    background-color: var(--border-color);
  }

  .compaction-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 10px;
    border-radius: 12px;
    background-color: var(--bg-tertiary);
    border: 1px solid var(--border-color);
    color: var(--text-muted);
    font-size: 11.52px;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .compaction-pill:hover {
    color: var(--text-primary);
    border-color: var(--text-secondary);
  }

  .pill-icon {
    font-size: 12px;
  }

  .pill-chevron {
    font-size: 10.4px;
    opacity: 0.7;
  }

  .compaction-details {
    margin: -8px 0 16px 0;
    padding: 10px 14px;
    border-radius: 6px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    font-size: 12.48px;
    color: var(--text-secondary);
    line-height: 1.45;
  }

  .summary-header {
    font-weight: 600;
    text-transform: uppercase;
    font-size: 10.88px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }

  .summary-text {
    white-space: pre-wrap;
  }
</style>
