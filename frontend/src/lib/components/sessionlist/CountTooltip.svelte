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
  /* An elevated surface of the current theme, not an inverted chip. */
  .count-tooltip {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 3px 8px;
    background-color: var(--bg-primary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    font-size: 0.7rem;
    font-family: var(--font-mono);
    white-space: nowrap;
    box-shadow: 0 2px 8px rgba(15, 23, 42, 0.12);
    z-index: 100;
    pointer-events: none;
  }

  .dot {
    opacity: 0.5;
  }

  .count-item.user {
    color: #2563eb;
  }

  .count-item.assistant {
    color: #7c3aed;
  }

  .count-item.tools {
    color: #047857;
  }

  .count-item.errors {
    color: #dc2626;
  }

  /* Role tints stay readable on the raised dark surface. */
  @media screen {
    :global([data-theme="dark"]) .count-tooltip {
      background-color: var(--bg-tertiary);
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.45);
    }

    :global([data-theme="dark"]) .count-item.user {
      color: #93c5fd;
    }

    :global([data-theme="dark"]) .count-item.assistant {
      color: #c4b5fd;
    }

    :global([data-theme="dark"]) .count-item.tools {
      color: #34d399;
    }

    :global([data-theme="dark"]) .count-item.errors {
      color: #fca5a5;
    }
  }
</style>
