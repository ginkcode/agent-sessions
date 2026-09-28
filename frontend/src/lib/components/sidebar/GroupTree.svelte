<script lang="ts">
  import type { GroupNode } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import GroupNodeItem from './GroupNodeItem.svelte';

  function handleSelect(node: GroupNode) {
    appState.selectGroup(node.key);
  }

  function handleToggleCollapse(key: string) {
    appState.toggleCollapsed(key);
  }

  let hasFilter = $derived(
    Boolean(appState.filter.query || appState.filter.liveOnly || appState.filter.archived)
  );

  function handleClearFilters() {
    appState.setFilter({ query: '', liveOnly: false, archived: false });
  }
</script>

<div class="tree-container" role="tree" aria-label="Session groups">
  {#if appState.loadingGroups}
    <div class="tree-message loading">Loading groups…</div>
  {:else if appState.groups.length === 0}
    {#if hasFilter}
      <div class="tree-message">
        <p>No sessions match current filter.</p>
        <button type="button" class="clear-filters-btn" onclick={handleClearFilters}>
          Clear filters
        </button>
      </div>
    {:else}
      <div class="tree-empty-card">
        <span class="tree-empty-icon">📁</span>
        <h4>No Agents Detected</h4>
        <p class="tree-empty-sub">
          Looking for session history in:
        </p>
        <ul class="roots-list">
          <li><code>~/.claude/projects/</code></li>
          <li><code>~/.codex/sessions/</code></li>
          <li><code>~/.local/share/opencode/</code></li>
        </ul>
        <p class="tree-empty-hint">
          Start a session with Claude Code, Codex CLI, or OpenCode to begin.
        </p>
      </div>
    {/if}
  {:else}
    <ul class="root-list">
      {#each appState.groups as node (node.key)}
        <GroupNodeItem
          {node}
          level={0}
          selectedKey={appState.selectedGroupKey}
          collapsedKeys={appState.collapsedKeys}
          defaultCollapsed={appState.defaultCollapsed}
          onSelect={handleSelect}
          onToggleCollapse={handleToggleCollapse}
        />
      {/each}
    </ul>
  {/if}
</div>

<style>
  .tree-container {
    width: 100%;
    height: 100%;
    overflow-y: auto;
    padding: 6px 4px;
  }

  .root-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .tree-message {
    padding: 24px 12px;
    text-align: center;
    color: var(--text-muted);
    font-size: 0.8rem;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 8px;
  }

  .tree-message.loading {
    animation: pulse 1.5s ease-in-out infinite;
  }

  .clear-filters-btn {
    padding: 4px 12px;
    border-radius: 6px;
    background-color: var(--accent-color);
    color: white;
    font-size: 0.75rem;
    font-weight: 500;
    cursor: pointer;
  }

  .clear-filters-btn:hover {
    opacity: 0.9;
  }

  .tree-empty-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    gap: 6px;
    padding: 28px 14px;
    color: var(--text-muted);
    font-size: 0.8rem;
  }

  .tree-empty-icon {
    font-size: 2rem;
    opacity: 0.6;
  }

  .tree-empty-card h4 {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--text-secondary);
    margin: 0;
  }

  .tree-empty-sub {
    margin: 4px 0 0 0;
    font-size: 0.78rem;
  }

  .roots-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }

  .roots-list code {
    font-family: var(--font-mono);
    font-size: 0.7rem;
    padding: 2px 6px;
    border-radius: 4px;
    background-color: var(--bg-tertiary);
    color: var(--text-secondary);
  }

  .tree-empty-hint {
    margin-top: 6px;
    font-size: 0.72rem;
    line-height: 1.4;
    max-width: 220px;
  }

  @keyframes pulse {
    0%,
    100% {
      opacity: 1;
    }
    50% {
      opacity: 0.5;
    }
  }
</style>
