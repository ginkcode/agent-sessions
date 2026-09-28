<script lang="ts">
  // Small per-agent glyphs in each agent's brand color. These are simplified
  // marks drawn inline, not the official logo files.
  interface Props {
    agent?: string;
    size?: number;
  }

  let { agent, size = 14 }: Props = $props();

  function title(agent?: string): string {
    if (agent === 'claude-code') return 'Claude Code';
    if (agent === 'codex') return 'Codex';
    if (agent === 'opencode') return 'OpenCode';
    return agent || 'Session';
  }
</script>

<span class="agent-icon" title={title(agent)} aria-hidden="true">
  {#if agent === 'claude-code'}
    <!-- Claude spark: radial spokes around a center. -->
    <svg width={size} height={size} viewBox="0 0 16 16" class="claude">
      <g stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
        <line x1="8" y1="1.5" x2="8" y2="14.5" />
        <line x1="1.5" y1="8" x2="14.5" y2="8" />
        <line x1="3.4" y1="3.4" x2="12.6" y2="12.6" />
        <line x1="12.6" y1="3.4" x2="3.4" y2="12.6" />
      </g>
    </svg>
  {:else if agent === 'codex'}
    <!-- Codex CLI: terminal prompt. -->
    <svg width={size} height={size} viewBox="0 0 16 16" class="codex">
      <rect x="1" y="2" width="14" height="12" rx="3" fill="currentColor" />
      <path
        d="M4.5 6 L6.8 8 L4.5 10 M8.5 10.5 H11.5"
        stroke="#fff"
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        fill="none"
      />
    </svg>
  {:else if agent === 'opencode'}
    <!-- OpenCode: blocky square ring. -->
    <svg width={size} height={size} viewBox="0 0 16 16" class="opencode">
      <path
        fill="currentColor"
        fill-rule="evenodd"
        d="M2 2 H14 V14 H2 Z M5.5 5.5 V10.5 H10.5 V5.5 Z"
      />
    </svg>
  {:else}
    <span class="fallback">📄</span>
  {/if}
</span>

<style>
  .agent-icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
  }

  svg {
    display: block;
  }

  .claude {
    color: var(--agent-claude);
  }

  .codex {
    color: var(--agent-codex);
  }

  .opencode {
    color: var(--agent-opencode);
  }

  .fallback {
    font-size: 0.85rem;
  }
</style>
