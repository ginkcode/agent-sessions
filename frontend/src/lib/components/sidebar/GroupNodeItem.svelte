<script lang="ts">
  import type { GroupNode } from '../../types';
  import GroupNodeItem from './GroupNodeItem.svelte';
  import AgentIcon from '../common/AgentIcon.svelte';
  import { isCollapsed as isKeyCollapsed, nodeKind } from '../../tree';

  interface Props {
    node: GroupNode;
    level?: number;
    selectedKey: string | null;
    collapsedKeys: Set<string>;
    defaultCollapsed: Set<string>;
    onSelect: (node: GroupNode) => void;
    onToggleCollapse: (key: string) => void;
  }

  let {
    node,
    level = 0,
    selectedKey,
    collapsedKeys,
    defaultCollapsed,
    onSelect,
    onToggleCollapse,
  }: Props = $props();

  let hasChildren = $derived(Boolean(node.children && node.children.length > 0));
  let kind = $derived(nodeKind(node));
  let isCollapsed = $derived(isKeyCollapsed(node.key, collapsedKeys, defaultCollapsed));
  let isSelected = $derived(selectedKey === node.key);

  function handleRowClick() {
    onSelect(node);
  }

  function handleDisclosureClick(e: MouseEvent) {
    e.stopPropagation();
    onToggleCollapse(node.key);
  }

  function formatAgent(agent?: string): string {
    if (!agent) return '';
    if (agent === 'claude-code') return 'Claude';
    if (agent === 'codex') return 'Codex';
    if (agent === 'opencode') return 'OpenCode';
    return agent;
  }
</script>

<li
  class="group-node-container"
  role="treeitem"
  aria-selected={isSelected}
  aria-expanded={hasChildren ? !isCollapsed : undefined}
>
  <div
    class="node-row"
    class:dir={kind === 'directory'}
    class:group={kind === 'agent'}
    class:selected={isSelected}
    class:missing={node.cwdMissing}
    style="padding-left: {level * 12 + 6}px"
    onclick={handleRowClick}
    role="button"
    tabindex="0"
    onkeydown={(e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        handleRowClick();
      }
    }}
  >
    {#if hasChildren}
      <button
        type="button"
        class="disclosure-btn"
        aria-label={isCollapsed ? 'Expand' : 'Collapse'}
        onclick={handleDisclosureClick}
      >
        <span class="disclosure-arrow" class:open={!isCollapsed}>▶</span>
      </button>
    {:else}
      <span class="disclosure-spacer"></span>
    {/if}

    <div class="icon-area">
      {#if kind === 'agent'}
        <span class="agent-chip agent-{node.agent}">{formatAgent(node.agent)}</span>
      {:else if kind === 'directory' && node.cwdMissing}
        <span class="node-icon missing" title="Directory no longer exists on disk">⚠️</span>
      {:else if kind === 'directory'}
        <span class="node-icon folder">📁</span>
      {:else}
        <AgentIcon agent={node.agent} />
      {/if}
    </div>

    <span class="node-label" title={node.secondary || node.label}>
      {node.label}
      {#if node.cwdMissing}
        <span class="missing-badge">[missing]</span>
      {/if}
    </span>

    <span class="count-badge" title="{node.sessionCount} sessions">
      {node.sessionCount}
    </span>
  </div>

  {#if hasChildren && !isCollapsed && node.children}
    <ul class="children-list" role="group">
      {#each node.children as child (child.key)}
        <GroupNodeItem
          node={child}
          level={level + 1}
          {selectedKey}
          {collapsedKeys}
          {defaultCollapsed}
          {onSelect}
          {onToggleCollapse}
        />
      {/each}
    </ul>
  {/if}
</li>

<style>
  .group-node-container {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .node-row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding-top: 4px;
    padding-bottom: 4px;
    padding-right: 8px;
    border-radius: 4px;
    cursor: pointer;
    user-select: none;
    font-size: 0.825rem;
    color: var(--text-secondary);
    transition: background-color 0.1s ease, color 0.1s ease;
  }

  /* Directories read as section headers; agent groups sit beneath them. */
  .node-row.dir {
    font-weight: 600;
    color: var(--text-primary);
  }

  .node-row.group {
    font-weight: 500;
    font-size: 0.8rem;
    color: var(--text-secondary);
  }

  .node-row:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .node-row.selected {
    background-color: var(--active-bg);
    color: var(--accent-color);
    font-weight: 600;
  }

  .node-row.missing {
    opacity: 0.75;
  }

  .disclosure-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
    padding: 0;
    cursor: pointer;
    color: var(--text-muted);
    border-radius: 2px;
  }

  .disclosure-btn:hover {
    color: var(--text-primary);
  }

  .disclosure-arrow {
    display: inline-block;
    font-size: 0.65rem;
    transition: transform 0.15s ease;
  }

  .disclosure-arrow.open {
    transform: rotate(90deg);
  }

  .disclosure-spacer {
    width: 16px;
    height: 16px;
    flex-shrink: 0;
  }

  .icon-area {
    display: flex;
    align-items: center;
    flex-shrink: 0;
  }

  .node-icon {
    font-size: 0.85rem;
  }

  .node-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .missing-badge {
    margin-left: 4px;
    font-size: 0.7rem;
    color: #ef4444;
    font-weight: 500;
  }

  .count-badge {
    font-size: 0.7rem;
    padding: 1px 6px;
    border-radius: 10px;
    background-color: var(--bg-tertiary);
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
    flex-shrink: 0;
  }

  .selected .count-badge {
    background-color: rgba(37, 99, 235, 0.2);
    color: var(--accent-color);
  }

  .agent-chip {
    font-size: 0.65rem;
    padding: 1px 4px;
    border-radius: 3px;
    font-weight: 600;
    text-transform: uppercase;
  }

  .agent-chip.agent-claude-code {
    background: rgba(217, 119, 87, 0.15);
    color: var(--agent-claude);
  }

  .agent-chip.agent-codex {
    background: rgba(16, 163, 127, 0.15);
    color: var(--agent-codex);
  }

  .agent-chip.agent-opencode {
    background: rgba(59, 130, 246, 0.15);
    color: var(--agent-opencode);
  }

  .children-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
</style>
