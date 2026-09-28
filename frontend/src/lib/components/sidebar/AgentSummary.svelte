<script lang="ts">
  import { appState } from '../../stores/appState.svelte';

  // Calculate totals per agent from sessions
  let counts = $derived.by(() => {
    let claude = 0;
    let codex = 0;
    let opencode = 0;

    for (const s of appState.sessions) {
      if (s.ref.agent === 'claude-code') claude++;
      else if (s.ref.agent === 'codex') codex++;
      else if (s.ref.agent === 'opencode') opencode++;
    }
    return { claude, codex, opencode, total: appState.sessions.length };
  });

  function toggleAgentFilter(agent: string) {
    if (appState.filter.agent === agent) {
      appState.setFilter({ agent: undefined });
    } else {
      appState.setFilter({ agent });
    }
  }
</script>

<div class="agent-summary" aria-label="Session summary by agent">
  <div class="summary-chips">
    <button
      type="button"
      class="chip agent-claude-code"
      class:active={appState.filter.agent === 'claude-code'}
      title="Filter Claude Code sessions ({counts.claude})"
      onclick={() => toggleAgentFilter('claude-code')}
    >
      <span class="chip-name">Claude</span>
      <span class="chip-count">{counts.claude}</span>
    </button>

    <button
      type="button"
      class="chip agent-codex"
      class:active={appState.filter.agent === 'codex'}
      title="Filter Codex sessions ({counts.codex})"
      onclick={() => toggleAgentFilter('codex')}
    >
      <span class="chip-name">Codex</span>
      <span class="chip-count">{counts.codex}</span>
    </button>

    <button
      type="button"
      class="chip agent-opencode"
      class:active={appState.filter.agent === 'opencode'}
      title="Filter OpenCode sessions ({counts.opencode})"
      onclick={() => toggleAgentFilter('opencode')}
    >
      <span class="chip-name">OpenCode</span>
      <span class="chip-count">{counts.opencode}</span>
    </button>
  </div>
</div>

<style>
  .agent-summary {
    width: 100%;
    padding: 6px 8px;
    border-top: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .summary-chips {
    display: flex;
    gap: 4px;
    justify-content: space-between;
  }

  .chip {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 4px;
    padding: 3px 6px;
    border-radius: 4px;
    font-size: 0.72rem;
    font-weight: 500;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    transition: all 0.15s ease;
  }

  .chip:hover {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .chip.active {
    border-color: var(--accent-color);
    background-color: var(--active-bg);
  }

  .chip.agent-claude-code.active {
    color: var(--agent-claude);
    border-color: var(--agent-claude);
  }

  .chip.agent-codex.active {
    color: var(--agent-codex);
    border-color: var(--agent-codex);
  }

  .chip.agent-opencode.active {
    color: var(--agent-opencode);
    border-color: var(--agent-opencode);
  }

  .chip-name {
    font-weight: 600;
  }

  .chip-count {
    font-variant-numeric: tabular-nums;
    font-size: 0.7rem;
    opacity: 0.8;
  }
</style>
