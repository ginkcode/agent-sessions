<script lang="ts">
  import type { MessageCounts } from '../../types';

  interface Props {
    counts: MessageCounts;
  }

  let { counts }: Props = $props();
</script>

<div class="count-tooltip" role="tooltip">
  <span class="count-item user">user {counts.user}</span>
  <span class="dot">·</span>
  <span class="count-item assistant">assistant {counts.assistant}</span>
  <span class="dot">·</span>
  <span class="count-item tools">tool calls {counts.toolCalls}</span>
  {#if counts.errors && counts.errors > 0}
    <span class="dot">·</span>
    <span class="count-item errors">errors {counts.errors}</span>
  {/if}
</div>

<style>
  .count-tooltip {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    background-color: var(--text-primary);
    color: var(--bg-primary);
    border-radius: 4px;
    font-size: 0.7rem;
    font-family: var(--font-mono);
    white-space: nowrap;
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
    z-index: 100;
    pointer-events: none;
  }

  .dot {
    opacity: 0.5;
  }

  .count-item.user {
    color: #60a5fa;
  }

  .count-item.assistant {
    color: #a78bfa;
  }

  .count-item.tools {
    color: #34d399;
  }

  .count-item.errors {
    color: #f87171;
  }
</style>
