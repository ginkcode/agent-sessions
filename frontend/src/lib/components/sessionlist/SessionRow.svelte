<script lang="ts">
  import type { SessionMeta } from '../../types';
  import { formatRelativeTime, formatAbsoluteTime } from '../../date';
  import CountTooltip from './CountTooltip.svelte';
  import { manage } from '../../stores/manage.svelte';

  interface Props {
    session: SessionMeta;
    isSelected: boolean;
    onSelect: (session: SessionMeta) => void;
  }

  let { session, isSelected, onSelect }: Props = $props();

  let showCountTooltip = $state(false);

  let messageTotal = $derived(session.counts.user + session.counts.assistant);
  let displayTitle = $derived(session.title || session.firstPrompt || '(untitled)');
  let relativeTime = $derived(formatRelativeTime(session.updatedAt));
  let absoluteTime = $derived(formatAbsoluteTime(session.updatedAt));
  let bulkSelected = $derived(manage.isRefSelected(session.ref));

  function handleClick() {
    onSelect(session);
  }

  function handleBulkToggle(e: Event) {
    e.stopPropagation();
    manage.toggleRefSelected(session.ref);
  }

  function handleBulkKeydown(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleBulkToggle(e);
    }
  }
</script>

<div
  class="session-row"
  class:selected={isSelected}
  class:bulk-selected={bulkSelected}
  class:archived={session.archived}
  role="option"
  tabindex="0"
  aria-selected={isSelected}
  onclick={handleClick}
  onkeydown={(e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      handleClick();
    }
  }}
>
  <div class="row-top">
    {#if manage.settings.enabled}
      <input
        type="checkbox"
        class="bulk-checkbox"
        checked={bulkSelected}
        onclick={handleBulkToggle}
        onkeydown={handleBulkKeydown}
        aria-label="Select session {session.title || session.ref.id}"
      />
    {/if}
    {#if session.live}
      <span class="live-dot" title="Active session"></span>
    {/if}
    <span class="agent-badge agent-{session.ref.agent}">{session.ref.agent}</span>
    {#if session.archived}
      <span class="archived-tag">archived</span>
    {/if}
    <span
      class="time-label"
      title={absoluteTime}
    >
      {relativeTime}
    </span>
  </div>

  <div class="row-middle">
    <span class="title-text" title={displayTitle}>
      {displayTitle}
    </span>
  </div>

  <div class="row-bottom">
    <div
      class="count-wrapper"
      role="region"
      aria-label="Message count details"
      onmouseenter={() => (showCountTooltip = true)}
      onmouseleave={() => (showCountTooltip = false)}
    >
      <span class="count-badge">
        <span class="count-icon">💬</span>
        <span class="count-num">{messageTotal}</span>
      </span>
      {#if showCountTooltip}
        <div class="tooltip-container">
          <CountTooltip counts={session.counts} />
        </div>
      {/if}
    </div>

    {#if session.model}
      <span class="model-tag" title="Model: {session.model}">{session.model}</span>
    {/if}

    {#if session.gitBranch}
      <span class="branch-tag" title="Git Branch: {session.gitBranch}">🌿 {session.gitBranch}</span>
    {/if}
  </div>
</div>

<style>
  .session-row {
    height: 58px;
    margin-bottom: 4px;
    box-sizing: border-box;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    padding: 6px 12px;
    border-radius: var(--radius-sm);
    background-color: var(--bg-primary);
    cursor: pointer;
    user-select: none;
    position: relative;
    transition: background-color 0.1s ease;
  }

  .session-row:hover {
    background-color: var(--bg-secondary);
  }

  .session-row.selected {
    background-color: var(--active-bg);
    border-left: 3px solid var(--accent-color);
    padding-left: 9px;
  }

  .session-row.bulk-selected:not(.selected) {
    background-color: rgba(239, 68, 68, 0.06);
  }

  .bulk-checkbox {
    width: 13px;
    height: 13px;
    margin: 0;
    accent-color: var(--danger);
    cursor: pointer;
    flex-shrink: 0;
  }

  .session-row.archived {
    opacity: 0.75;
  }

  .row-top {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.68rem;
  }

  .live-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background-color: var(--status-live);
    box-shadow: 0 0 6px var(--status-live);
    flex-shrink: 0;
    animation: pulse 2s infinite ease-in-out;
  }

  @keyframes pulse {
    0% { opacity: 0.6; }
    50% { opacity: 1; transform: scale(1.1); }
    100% { opacity: 0.6; }
  }

  .agent-badge {
    padding: 1px 4px;
    border-radius: 3px;
    font-weight: 600;
    text-transform: uppercase;
    font-size: 0.65rem;
  }

  .agent-badge.agent-claude-code {
    background: rgba(217, 119, 87, 0.15);
    color: var(--agent-claude);
  }

  .agent-badge.agent-codex {
    background: rgba(16, 163, 127, 0.15);
    color: var(--agent-codex);
  }

  .agent-badge.agent-opencode {
    background: rgba(59, 130, 246, 0.15);
    color: var(--agent-opencode);
  }

  .archived-tag {
    font-size: 0.65rem;
    color: var(--text-muted);
    font-style: italic;
  }

  .time-label {
    margin-left: auto;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .row-middle {
    display: flex;
    align-items: center;
    overflow: hidden;
  }

  .title-text {
    font-size: 0.825rem;
    font-weight: 500;
    color: var(--text-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    width: 100%;
  }

  .row-bottom {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 0.68rem;
    color: var(--text-secondary);
  }

  .count-wrapper {
    position: relative;
    display: inline-flex;
    align-items: center;
  }

  .count-badge {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    font-variant-numeric: tabular-nums;
  }

  .count-icon {
    font-size: 0.65rem;
    opacity: 0.7;
  }

  .tooltip-container {
    position: absolute;
    bottom: calc(100% + 4px);
    left: 0;
  }

  .model-tag, .branch-tag {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 120px;
    font-family: var(--font-mono);
    opacity: 0.75;
  }
</style>
