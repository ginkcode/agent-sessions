<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    variant?: 'default' | 'claude' | 'codex' | 'opencode' | 'live' | 'archived' | 'subagent';
    children: Snippet;
  }

  let { variant = 'default', children }: Props = $props();
</script>

<span class="badge badge-{variant}">
  {@render children()}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 1px 6px;
    border-radius: 4px;
    font-size: 0.68rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.02em;
    user-select: none;
    line-height: 1.4;
  }

  .badge-default {
    background: var(--bg-tertiary);
    color: var(--text-secondary);
  }

  .badge-claude {
    background: rgba(217, 119, 87, 0.15);
    color: var(--agent-claude);
  }

  .badge-codex {
    background: rgba(16, 163, 127, 0.15);
    color: var(--agent-codex);
  }

  .badge-opencode {
    background: rgba(59, 130, 246, 0.15);
    color: var(--agent-opencode);
  }

  .badge-live {
    background: rgba(16, 185, 129, 0.15);
    color: var(--status-live);
  }

  .badge-archived {
    background: var(--bg-tertiary);
    color: var(--text-muted);
  }

  .badge-subagent {
    background: rgba(168, 85, 247, 0.15);
    color: #a855f7;
  }
</style>
