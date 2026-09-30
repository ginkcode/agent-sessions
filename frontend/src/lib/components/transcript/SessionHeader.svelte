<script lang="ts">
  import type { SessionMeta } from '../../types';
  import { formatTokens, formatCost } from '../../format';
  import { formatAbsoluteTime, formatAgo, isKnownTime } from '../../date';
  import { appState } from '../../stores/appState.svelte';
  import { manage } from '../../stores/manage.svelte';
  import { handoff } from '../../stores/handoff.svelte';
  import { exporter } from '../../stores/export.svelte';
  import { ALL_AGENTS } from '../../portable';
  import AgentIcon from '../common/AgentIcon.svelte';
  import { link } from '../../stores/link.svelte';

  interface Props {
    meta: SessionMeta;
    showMeta: boolean;
    onToggleMeta: () => void;
    onResume: () => void;
    onReveal: () => void;
    onDelete: () => void;
    resumeCopied?: boolean;
  }

  let {
    meta,
    showMeta,
    onToggleMeta,
    onResume,
    onReveal,
    onDelete,
    resumeCopied = false,
  }: Props = $props();

  let messageTotal = $derived(meta.counts.user + meta.counts.assistant);
  let continueMenuOpen = $state(false);
  let exportMenuOpen = $state(false);
  let continueMenuRoot: HTMLDivElement | undefined = $state();
  let exportMenuRoot: HTMLDivElement | undefined = $state();

  function handleNavigateToParent() {
    if (meta.parentId) {
      appState.selectSession({ agent: meta.ref.agent, id: meta.parentId });
    }
  }

  function handleWindowPointerDown(e: PointerEvent) {
    const target = e.target as Node;
    if (continueMenuOpen && continueMenuRoot && !continueMenuRoot.contains(target)) {
      continueMenuOpen = false;
    }
    if (exportMenuOpen && exportMenuRoot && !exportMenuRoot.contains(target)) {
      exportMenuOpen = false;
    }
  }
</script>

<svelte:window onpointerdown={handleWindowPointerDown} />

<header class="session-header">
  {#if meta.parentId}
    <div class="parent-banner">
      <span>Subagent session of parent:</span>
      <button
        type="button"
        class="parent-link-btn"
        onclick={handleNavigateToParent}
      >
        {meta.parentId} → View Parent
      </button>
    </div>
  {/if}

  <div class="header-top-row">
    <div class="header-badges">
      <span class="agent-badge agent-{meta.ref.agent}">{meta.ref.agent}</span>
      {#if meta.live}
        <span class="live-badge">● LIVE</span>
      {/if}
      {#if meta.archived}
        <span class="archived-badge">ARCHIVED</span>
      {/if}
      {#if meta.model}
        <span class="model-badge">{meta.model}</span>
      {/if}
      {#if meta.agentVersion}
        <span class="version-badge">v{meta.agentVersion}</span>
      {/if}
    </div>

    <div class="header-actions">
      <button
        type="button"
        class="action-btn resume-btn"
        title="Copy shell command to resume this session"
        onclick={onResume}
      >
        {resumeCopied ? '✓ Copied' : 'Resume'}
      </button>

      <div class="menu-container" bind:this={continueMenuRoot}>
        <button
          type="button"
          class="action-btn continue-btn"
          class:active={continueMenuOpen}
          title="Continue this session in another agent"
          aria-haspopup="menu"
          aria-expanded={continueMenuOpen}
          onclick={() => {
            continueMenuOpen = !continueMenuOpen;
            if (continueMenuOpen) exportMenuOpen = false;
          }}
        >
          Continue in ▾
        </button>
        {#if continueMenuOpen}
          <div class="action-dropdown-menu" role="menu">
            {#each ALL_AGENTS as agent (agent.id)}
              <button
                type="button"
                class="action-menu-item"
                role="menuitem"
                onclick={() => {
                  continueMenuOpen = false;
                  handoff.open(meta, agent.id);
                }}
              >
                <AgentIcon agent={agent.id} size={14} />
                <span>{agent.label}</span>
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <div class="menu-container" bind:this={exportMenuRoot}>
        <button
          type="button"
          class="action-btn export-btn"
          class:active={exportMenuOpen}
          title="Export session bundle"
          aria-haspopup="menu"
          aria-expanded={exportMenuOpen}
          onclick={() => {
            exportMenuOpen = !exportMenuOpen;
            if (exportMenuOpen) continueMenuOpen = false;
          }}
        >
          Export ▾
        </button>
        {#if exportMenuOpen}
          <div class="action-dropdown-menu" role="menu">
            <button
              type="button"
              class="action-menu-item"
              role="menuitem"
              onclick={() => {
                exportMenuOpen = false;
                exporter.open(meta, 'complete');
              }}
            >
              <span>Complete (Restorable)…</span>
            </button>
            <button
              type="button"
              class="action-menu-item"
              role="menuitem"
              onclick={() => {
                exportMenuOpen = false;
                exporter.open(meta, 'share-safe');
              }}
            >
              <span>Share-safe (Redacted)…</span>
            </button>
          </div>
        {/if}
      </div>

      {#if meta.sourcePath && !link.dataHost}
        <button
          type="button"
          class="action-btn reveal-btn"
          title="Reveal session file in system file manager"
          onclick={onReveal}
        >
          Reveal
        </button>
      {/if}

      {#if manage.settings.enabled}
        <button
          type="button"
          class="action-btn delete-btn"
          title={meta.live ? 'Live sessions cannot be deleted' : 'Preview deleting this session'}
          disabled={meta.live}
          onclick={onDelete}
        >
          Delete…
        </button>
      {/if}

      <button
        type="button"
        class="action-btn meta-btn"
        class:active={showMeta}
        title="Toggle visibility of meta and system messages"
        onclick={onToggleMeta}
      >
        {showMeta ? 'Hide Meta' : 'Show Meta'}
      </button>
    </div>
  </div>

  <div class="header-title-row">
    <h2 class="title-text" title={meta.title || meta.ref.id}>
      {meta.title || meta.firstPrompt || meta.ref.id}
    </h2>
  </div>

  <div class="header-meta-row">
    <div class="meta-item cwd-item" title={meta.cwd}>
      <span class="meta-icon">📁</span>
      <span class="cwd-text">{meta.cwd}</span>
      {#if meta.cwdMissing}
        <span class="missing-badge">[missing]</span>
      {/if}
    </div>

    {#if meta.gitBranch}
      <div class="meta-item branch-item" title="Git Branch: {meta.gitBranch}">
        <span class="meta-icon">🌿</span>
        <span class="branch-text">{meta.gitBranch}</span>
      </div>
    {/if}

    <div class="meta-item count-item">
      <span class="meta-icon">💬</span>
      <span class="count-val">{messageTotal} msgs</span>
    </div>

    {#each [{ label: 'Created', value: meta.createdAt }, { label: 'Updated', value: meta.updatedAt }] as stamp (stamp.label)}
      {#if isKnownTime(stamp.value)}
        <div class="meta-item time-item" title="{stamp.label} {formatAgo(stamp.value)}">
          <span class="stat-label">{stamp.label}:</span>
          <span class="time-val">{formatAbsoluteTime(stamp.value)}</span>
        </div>
      {/if}
    {/each}
  </div>

  <div class="header-stats-row">
    <div class="stat-pill">
      <span class="stat-label">In:</span>
      <span class="stat-val">{formatTokens(meta.tokens.input)}</span>
    </div>
    <span class="stat-sep">·</span>
    <div class="stat-pill">
      <span class="stat-label">Out:</span>
      <span class="stat-val">{formatTokens(meta.tokens.output)}</span>
    </div>
    {#if meta.tokens.cacheRead && meta.tokens.cacheRead > 0}
      <span class="stat-sep">·</span>
      <div class="stat-pill">
        <span class="stat-label">Cache:</span>
        <span class="stat-val">{formatTokens(meta.tokens.cacheRead)}</span>
      </div>
    {/if}
    {#if meta.costUsd && meta.costUsd > 0}
      <span class="stat-sep">·</span>
      <div class="stat-pill">
        <span class="stat-label">Cost:</span>
        <span class="stat-val">{formatCost(meta.costUsd)}</span>
      </div>
    {/if}
  </div>
</header>

<style>
  .session-header {
    container-type: inline-size;
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 16px;
    background-color: var(--bg-secondary);
  }

  .parent-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 8px;
    border-radius: 4px;
    background: rgba(168, 85, 247, 0.1);
    border: 1px solid rgba(168, 85, 247, 0.25);
    font-size: 0.72rem;
    color: #a855f7;
  }

  .parent-link-btn {
    font-family: var(--font-mono);
    font-weight: 600;
    background: none;
    border: none;
    color: #a855f7;
    cursor: pointer;
    text-decoration: underline;
  }

  .header-top-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    row-gap: 8px;
  }

  .header-badges {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .agent-badge {
    font-size: 0.68rem;
    font-weight: 600;
    padding: 1px 6px;
    border-radius: 4px;
    text-transform: uppercase;
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

  .live-badge {
    font-size: 0.68rem;
    font-weight: 600;
    color: var(--status-live);
  }

  .archived-badge {
    font-size: 0.68rem;
    color: var(--text-muted);
  }

  .model-badge, .version-badge {
    font-size: 0.7rem;
    font-family: var(--font-mono);
    color: var(--text-secondary);
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1 1 auto;
    justify-content: flex-end;
    flex-wrap: wrap;
  }

  @container (max-width: 460px) {
    .header-actions {
      flex-basis: 100%;
      justify-content: flex-start;
    }
  }

  .action-btn {
    padding: 3px 8px;
    border-radius: var(--radius-sm, 6px);
    font-size: 0.72rem;
    font-weight: 500;
    cursor: pointer;
    background-color: transparent;
    color: var(--text-secondary);
    border: none;
    transition: all 0.15s ease;
  }

  .action-btn:hover {
    color: var(--text-primary);
    background-color: var(--bg-tertiary);
  }

  .action-btn.resume-btn {
    background-color: var(--accent-color);
    color: white;
  }

  .action-btn.resume-btn:hover {
    background-color: var(--accent-hover);
    color: white;
  }

  .action-btn.meta-btn.active {
    background-color: var(--active-bg);
    color: var(--accent-color);
  }

  .action-btn.continue-btn.active,
  .action-btn.export-btn.active {
    background-color: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .menu-container {
    position: relative;
    display: inline-flex;
  }

  .action-dropdown-menu {
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 100;
    min-width: 140px;
    background: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-sm, 6px);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25);
    padding: 4px;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .action-menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border: none;
    background: transparent;
    border-radius: var(--radius-sm, 4px);
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--text-secondary);
    cursor: pointer;
    text-align: left;
    width: 100%;
    box-sizing: border-box;
  }

  .action-menu-item:hover {
    background: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .action-btn.delete-btn {
    color: var(--danger);
  }

  .action-btn.delete-btn:hover:not(:disabled) {
    background-color: color-mix(in srgb, var(--danger) 12%, transparent);
  }

  .action-btn.delete-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .header-title-row {
    margin: 2px 0;
  }

  .title-text {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text-primary);
    line-height: 1.3;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .header-meta-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 12px;
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .meta-item {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .cwd-item {
    font-family: var(--font-mono);
    max-width: 500px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .missing-badge {
    color: var(--danger);
    font-weight: 600;
    font-size: 0.7rem;
  }

  .branch-item {
    font-family: var(--font-mono);
  }

  .header-stats-row {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 0.72rem;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .stat-pill {
    display: flex;
    align-items: center;
    gap: 3px;
  }

  .stat-label {
    font-weight: 500;
  }

  .time-item {
    white-space: nowrap;
  }

  .stat-val {
    font-weight: 600;
    color: var(--text-secondary);
  }

  .stat-sep {
    opacity: 0.4;
  }
</style>
