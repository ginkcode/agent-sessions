<script lang="ts">
  import type { GroupNode, SessionRef } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import { discovery } from '../../stores/discovery.svelte';
  import { catalogNotice, emptyCatalogState, discoveryProblem } from '../../discovery';
  import { manage } from '../../stores/manage.svelte';
  import { filterSessionsByAge, refsEqual } from '../../manage';
  import { nodeToHighlight, sessionToSelect } from '../../tree';
  import GroupNodeItem from './GroupNodeItem.svelte';

  // Selecting a group also selects the first session shown in the list
  // (after the age filter), so the transcript follows the tree.
  async function handleSelect(node: GroupNode) {
    // The clicked row shows as selected at once, before its session opens.
    clicked = node.key;
    try {
      await appState.selectGroup(node.key, 'push');
      // A later click superseded this one while sessions were loading.
      if (appState.selectedGroupKey !== node.key) return;
      const visible = filterSessionsByAge(appState.sessions, manage.ageFilter);
      const ref = sessionToSelect(node, visible);
      if (ref) await appState.selectSession(ref);
    } finally {
      if (clicked === node.key) clicked = null;
    }
  }

  // The row shown as selected: the clicked group, or where the open session
  // is when it was opened from outside that group.
  let clicked = $state<string | null>(null);
  let highlightedKey = $derived(
    clicked ??
    nodeToHighlight(
      appState.groups,
      appState.selectedSessionRef,
      appState.selectedGroupKey,
      appState.collapsedKeys,
      appState.defaultCollapsed
    )
  );
  let container = $state<HTMLDivElement>();

  // Another session opened: its row comes into view. Only once per session,
  // so tree reloads leave the scrolling alone.
  let revealed: SessionRef | null = null;
  $effect(() => {
    const ref = appState.selectedSessionRef;
    if (!ref || refsEqual(ref, revealed) || !highlightedKey || !container) return;
    revealed = ref;
    const tree = container;
    // After the rows render, and with them any group that opened.
    requestAnimationFrame(() => {
      tree.querySelector('.node-row.selected')?.scrollIntoView({ block: 'nearest' });
    });
  });

  function handleToggleCollapse(key: string) {
    appState.toggleCollapsed(key);
  }

  let hasFilter = $derived(
    Boolean(
      appState.filter.agent ||
        appState.filter.hasSubagents ||
        appState.filter.query ||
        appState.filter.path ||
        appState.filter.liveOnly ||
        appState.filter.archived
    )
  );

  let emptyState = $derived(
    emptyCatalogState(discovery.outcome, discovery.status, hasFilter, appState.discoveryReloading)
  );
  let notice = $derived(catalogNotice(discovery.outcome, discovery.status));
  let problem = $derived(
    emptyState === 'failed' || emptyState === 'degraded'
      ? discoveryProblem(emptyState, discovery.status)
      : null
  );

  // The session list clears its own filters; this one is the directory's.
  function handleClearPath() {
    appState.setFilter({ path: '' });
  }
</script>

<div class="tree-container" role="tree" aria-label="Session groups" bind:this={container}>
  {#if notice && appState.groups.length > 0}
    <div class="discovery-notice" class:warning={notice.tone === 'warning'} role="status" title={notice.title}>
      {notice.text}
    </div>
  {/if}
  {#if appState.loadingGroups}
    <div class="tree-message loading">Loading groups…</div>
  {:else if appState.groups.length === 0}
    {#if emptyState === 'discovering'}
      <div class="tree-message loading" role="status">Discovering agent sessions…</div>
    {:else if emptyState === 'no-sessions'}
      <div class="tree-message" role="status">
        <p class="tree-message-title">No sessions found</p>
        <p>Agents were detected, but they have no session history yet.</p>
      </div>
    {:else if problem}
      <div class="tree-message" role="status">
        <p class="tree-message-title">{problem.title}</p>
        <p>{problem.description}</p>
      </div>
    {:else if hasFilter}
      <div class="tree-message">
        <p>No sessions match current filter.</p>
        {#if appState.filter.path}
          <button type="button" class="clear-filters-btn" onclick={handleClearPath}>
            Clear directory filter
          </button>
        {/if}
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
          selectedKey={highlightedKey}
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

  .tree-message-title {
    margin: 0;
    color: var(--text-secondary);
    font-weight: 600;
  }

  .discovery-notice {
    padding: 4px 8px;
    color: var(--text-muted);
    font-size: 0.72rem;
  }

  .discovery-notice.warning {
    color: var(--text-secondary);
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
