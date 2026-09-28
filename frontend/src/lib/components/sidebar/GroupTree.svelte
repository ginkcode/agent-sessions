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
</script>

<div class="tree-container" role="tree" aria-label="Session groups">
  {#if appState.loadingGroups}
    <div class="tree-message">Loading groups…</div>
  {:else if appState.groups.length === 0}
    <div class="tree-message">No sessions match current filter</div>
  {:else}
    <ul class="root-list">
      {#each appState.groups as node (node.key)}
        <GroupNodeItem
          {node}
          level={0}
          selectedKey={appState.selectedGroupKey}
          collapsedKeys={appState.collapsedKeys}
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
  }
</style>
