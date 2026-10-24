<script lang="ts">
  import type { AgentID, SessionMeta, TerminalOpenMode } from '../../types';
  import { formatTokens, formatCost } from '../../format';
  import { formatAbsoluteTime, formatAgo, isKnownTime } from '../../date';
  import { appState } from '../../stores/appState.svelte';
  import { handoff } from '../../stores/handoff.svelte';
  import { exporter } from '../../stores/export.svelte';
  import { ALL_AGENTS, copyToClipboard } from '../../portable';
  import { copyFailed } from '../../guidance';
  import { toast } from '../../stores/toast.svelte';
  import AgentIcon from '../common/AgentIcon.svelte';
  import NavButtons from './NavButtons.svelte';
  import { link } from '../../stores/link.svelte';
  import { launcher } from '../../stores/launcher.svelte';
  import { preferences } from '../../stores/preferences.svelte';
  import { resumeButtonTitle } from '../../link';
  import { transcriptModes, type TranscriptMode } from '../../transcript';
  import { newSession } from '../../stores/newSession.svelte';
  import { terminals } from '../../stores/terminals.svelte';
  import { newSessionShortcutLabel } from '../../terminal';

  interface Props {
    meta: SessionMeta;
    mode: TranscriptMode;
    onModeChange: (mode: TranscriptMode) => void;
    /** Copies the resume command. */
    onResume: () => void;
    /**
     * Resumes in a terminal: where agents open by default, or the other
     * place when where is given. Offered where launcher can open it.
     */
    onOpenResume?: (where?: TerminalOpenMode) => void;
    resumeCopied?: boolean;
    resumeOpening?: boolean;
  }

  let {
    meta,
    mode,
    onModeChange,
    onResume,
    onOpenResume = () => {},
    resumeCopied = false,
    resumeOpening = false,
  }: Props = $props();

  // The agent's name, for the icon badge's tooltip and screen readers.
  let agentName = $derived(ALL_AGENTS.find((a) => a.id === meta.ref.agent)?.label ?? meta.ref.agent);

  let messageTotal = $derived(meta.counts.user + meta.counts.assistant);
  // The ▾ menu offers the place agents do not open by default.
  let otherOpen = $derived<TerminalOpenMode>(launcher.openMode === 'panel' ? 'external' : 'panel');
  let canOpenOther = $derived(Boolean(otherOpen === 'panel' ? launcher.canOpenPanel : launcher.canOpenExternal));
  const OPEN_IN: Record<TerminalOpenMode, { label: string; title: string }> = {
    panel: { label: 'Open in built-in terminal', title: 'Resume this session in the terminal panel below' },
    external: { label: 'Open in external terminal', title: 'Resume this session in a new terminal window' },
  };
  // A panel tab already runs this session: Resume shows it rather than
  // starting a second copy, which the ▾ menu still offers.
  let runningTab = $derived(terminals.tabFor(meta.ref, link.dataHost ?? ''));

  // New session: the main part starts this session's agent in its directory,
  // ▾ offers every agent, those the host lacks disabled with what to install.
  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  let newChoices = $derived(newSession.choices);
  let sameAgent = $derived(newChoices.find((c) => c.agent === meta.ref.agent));
  let newDisabledReason = $derived(meta.cwdMissing ? 'This directory no longer exists on disk' : '');
  let newMainTitle = $derived(
    newDisabledReason ||
      sameAgent?.missing ||
      `Start a new ${agentName} session in ${meta.cwd} (${newSessionShortcutLabel(isMac)} starts the agent last used)`,
  );
  let newMenuOpen = $state(false);
  let newMenuRoot: HTMLDivElement | undefined = $state();

  $effect(() => {
    void link.dataHost;
    if (newSession.available) void newSession.refresh();
  });

  function startNew(agent: AgentID) {
    newMenuOpen = false;
    void newSession.start(agent, meta.cwd);
  }

  let resumeMenuOpen = $state(false);
  let continueMenuOpen = $state(false);
  let exportMenuOpen = $state(false);
  let resumeMenuRoot: HTMLDivElement | undefined = $state();
  let continueMenuRoot: HTMLDivElement | undefined = $state();
  let exportMenuRoot: HTMLDivElement | undefined = $state();

  // The copy button shows a tick for a moment after copying the path.
  let cwdCopied = $state(false);
  let cwdCopiedTimer: ReturnType<typeof setTimeout> | undefined;

  async function copyCwd() {
    try {
      if (!(await copyToClipboard(meta.cwd))) {
        toast.show(copyFailed('the path', 'The clipboard is not available.'));
        return;
      }
      cwdCopied = true;
      clearTimeout(cwdCopiedTimer);
      cwdCopiedTimer = setTimeout(() => (cwdCopied = false), 1500);
    } catch (err) {
      toast.show(copyFailed('the path', err));
    }
  }

  $effect(() => () => clearTimeout(cwdCopiedTimer));

  function handleNavigateToParent() {
    if (meta.parentId) {
      appState.selectSession({ agent: meta.ref.agent, id: meta.parentId }, 'push');
    }
  }

  function handleWindowPointerDown(e: PointerEvent) {
    const target = e.target as Node;
    if (resumeMenuOpen && resumeMenuRoot && !resumeMenuRoot.contains(target)) {
      resumeMenuOpen = false;
    }
    if (continueMenuOpen && continueMenuRoot && !continueMenuRoot.contains(target)) {
      continueMenuOpen = false;
    }
    if (exportMenuOpen && exportMenuRoot && !exportMenuRoot.contains(target)) {
      exportMenuOpen = false;
    }
    if (newMenuOpen && newMenuRoot && !newMenuRoot.contains(target)) {
      newMenuOpen = false;
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
      <NavButtons />
      <span
        class="agent-badge agent-{meta.ref.agent}"
        role="img"
        aria-label={agentName}
        title={agentName}
      >
        <AgentIcon agent={meta.ref.agent} size={14} />
      </span>
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
      {#if runningTab}
        <span class="running-badge" title="This session is running in the terminal panel">● Running in terminal</span>
      {/if}
      {#if launcher.canOpen || canOpenOther}
        <div class="menu-container resume-split" bind:this={resumeMenuRoot}>
          {#if runningTab}
            {@const tabId = runningTab.id}
            <button
              type="button"
              class="action-btn resume-btn resume-main"
              title="Show the terminal tab running this session"
              onclick={() => terminals.show(tabId)}
            >
              Show terminal
            </button>
          {:else}
            <button
              type="button"
              class="action-btn resume-btn resume-main"
              title={launcher.canOpen ? resumeButtonTitle(link.dataHost, true, launcher.openMode) : resumeButtonTitle(link.dataHost)}
              disabled={resumeOpening}
              onclick={() => (launcher.canOpen ? onOpenResume() : onResume())}
            >
              {resumeCopied ? '✓ Copied' : resumeOpening ? 'Opening…' : 'Resume'}
            </button>
          {/if}
          <button
            type="button"
            class="action-btn resume-btn resume-toggle"
            title="More ways to resume"
            aria-label="More ways to resume"
            aria-haspopup="menu"
            aria-expanded={resumeMenuOpen}
            onclick={() => {
              resumeMenuOpen = !resumeMenuOpen;
              if (resumeMenuOpen) {
                continueMenuOpen = false;
                exportMenuOpen = false;
                newMenuOpen = false;
              }
            }}
          >
            ▾
          </button>
          {#if resumeMenuOpen}
            <div class="action-dropdown-menu" role="menu">
              {#if runningTab && launcher.openMode === 'panel' && launcher.canOpen}
                <button
                  type="button"
                  class="action-menu-item"
                  role="menuitem"
                  title="Resume this session again in another terminal tab"
                  disabled={resumeOpening}
                  onclick={() => {
                    resumeMenuOpen = false;
                    onOpenResume();
                  }}
                >
                  <span>Resume in a new tab</span>
                </button>
              {/if}
              {#if canOpenOther}
                <button
                  type="button"
                  class="action-menu-item"
                  role="menuitem"
                  title={OPEN_IN[otherOpen].title}
                  onclick={() => {
                    resumeMenuOpen = false;
                    onOpenResume(otherOpen);
                  }}
                >
                  <span>{OPEN_IN[otherOpen].label}</span>
                </button>
              {/if}
              {#if launcher.canOpen}
                <button
                  type="button"
                  class="action-menu-item"
                  role="menuitem"
                  title="Copy the resume command to run it yourself"
                  onclick={() => {
                    resumeMenuOpen = false;
                    onResume();
                  }}
                >
                  <span>Copy command</span>
                </button>
              {/if}
            </div>
          {/if}
        </div>
      {:else}
        <button
          type="button"
          class="action-btn resume-btn"
          title={resumeButtonTitle(link.dataHost)}
          onclick={onResume}
        >
          {resumeCopied ? '✓ Copied' : 'Resume'}
        </button>
      {/if}

      {#if newSession.available && meta.cwd}
        <div class="menu-container new-split" bind:this={newMenuRoot}>
          <button
            type="button"
            class="action-btn new-btn new-main"
            title={newMainTitle}
            disabled={newSession.starting || Boolean(newDisabledReason || sameAgent?.missing)}
            onclick={() => startNew(meta.ref.agent)}
          >
            {newSession.starting ? 'Starting…' : 'New'}
          </button>
          <button
            type="button"
            class="action-btn new-btn new-toggle"
            title="Start a new session with another agent"
            aria-label="New session with another agent"
            aria-haspopup="menu"
            aria-expanded={newMenuOpen}
            disabled={Boolean(newDisabledReason)}
            onclick={() => {
              newMenuOpen = !newMenuOpen;
              if (newMenuOpen) {
                resumeMenuOpen = false;
                continueMenuOpen = false;
                exportMenuOpen = false;
                void newSession.refresh();
              }
            }}
          >
            ▾
          </button>
          {#if newMenuOpen}
            <div class="action-dropdown-menu" role="menu">
              {#each newChoices as choice (choice.agent)}
                <button
                  type="button"
                  class="action-menu-item"
                  role="menuitem"
                  title={choice.missing || `Start a new ${choice.name} session in ${meta.cwd}`}
                  disabled={Boolean(choice.missing) || newSession.starting}
                  onclick={() => startNew(choice.agent)}
                >
                  <AgentIcon agent={choice.agent} size={14} />
                  <span>New {choice.name} session</span>
                  {#if choice.missing}
                    <span class="menu-note">not installed</span>
                  {/if}
                </button>
              {/each}
            </div>
          {/if}
        </div>
      {/if}

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
            if (continueMenuOpen) {
              exportMenuOpen = false;
              resumeMenuOpen = false;
              newMenuOpen = false;
            }
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
            if (exportMenuOpen) {
              continueMenuOpen = false;
              resumeMenuOpen = false;
              newMenuOpen = false;
            }
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

      <div class="mode-switch" role="group" aria-label="Transcript display">
        {#each transcriptModes as option (option.value)}
          <button
            type="button"
            class="action-btn mode-btn"
            class:active={mode === option.value}
            aria-pressed={mode === option.value}
            title={option.title}
            onclick={() => onModeChange(option.value)}
          >
            {option.label}
          </button>
        {/each}
      </div>
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
      {#if meta.cwd}
        <button
          type="button"
          class="cwd-copy-btn"
          class:copied={cwdCopied}
          title={cwdCopied ? 'Copied' : 'Copy path'}
          aria-label={cwdCopied ? 'Path copied' : 'Copy path'}
          onclick={() => void copyCwd()}
        >
          {#if cwdCopied}
            <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M3 8.5 6.5 12 13 4.5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          {:else}
            <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
              <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.4" />
              <path d="M10.5 3.5V3A1.5 1.5 0 0 0 9 1.5H3A1.5 1.5 0 0 0 1.5 3v6A1.5 1.5 0 0 0 3 10.5h.5" fill="none" stroke="currentColor" stroke-width="1.4" />
            </svg>
          {/if}
        </button>
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

    <!-- Token use, on the path's line; it wraps below when space runs out. -->
    <div class="stats-group">
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
  </div>

  <div class="header-time-row">
    {#each [{ label: 'Created', value: meta.createdAt }, { label: 'Updated', value: meta.updatedAt }] as stamp (stamp.label)}
      {#if isKnownTime(stamp.value)}
        <div class="meta-item time-item" title="{stamp.label} {formatAgo(stamp.value)}">
          <span class="stat-label">{stamp.label}:</span>
          <span class="time-val">{formatAbsoluteTime(stamp.value, preferences.timeZone)}</span>
        </div>
      {/if}
    {/each}
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
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 20px;
    border-radius: 4px;
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

  .action-btn.resume-btn:disabled {
    opacity: 0.7;
    cursor: progress;
  }

  .resume-split .resume-main {
    border-top-right-radius: 0;
    border-bottom-right-radius: 0;
  }

  .resume-split .resume-toggle {
    padding: 3px 6px;
    border-top-left-radius: 0;
    border-bottom-left-radius: 0;
    border-left: 1px solid rgba(255, 255, 255, 0.35);
  }

  .running-badge {
    align-self: center;
    font-size: 0.72rem;
    font-weight: 600;
    color: var(--status-live);
    white-space: nowrap;
  }

  /* New session: outlined, so Resume stays the main action. */
  .action-btn.new-btn {
    border: 1px solid color-mix(in srgb, var(--text-muted) 35%, transparent);
  }

  .action-btn.new-btn:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .new-split .new-main {
    border-top-right-radius: 0;
    border-bottom-right-radius: 0;
  }

  .new-split .new-toggle {
    padding: 3px 6px;
    border-left: none;
    border-top-left-radius: 0;
    border-bottom-left-radius: 0;
  }

  .action-menu-item:disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  .action-menu-item:disabled:hover {
    background: transparent;
    color: var(--text-secondary);
  }

  .menu-note {
    margin-left: auto;
    padding-left: 8px;
    font-size: 0.68rem;
    color: var(--text-muted);
  }

  /* A pill-shaped segmented control: a hairline outline, dividers between
     the levels and an accent tint filling the chosen one. */
  .mode-switch {
    display: inline-flex;
    overflow: hidden;
    border: 1px solid color-mix(in srgb, var(--text-muted) 35%, transparent);
    border-radius: 999px;
  }

  .mode-switch .mode-btn {
    padding: 2px 12px;
    border-radius: 0;
    transition: background-color 0.15s ease, color 0.15s ease;
  }

  .mode-switch .mode-btn + .mode-btn {
    border-left: 1px solid color-mix(in srgb, var(--text-muted) 35%, transparent);
  }

  .mode-switch .mode-btn:hover {
    background-color: var(--hover-bg);
  }

  .mode-switch .mode-btn:focus-visible {
    outline: 2px solid var(--accent-color);
    outline-offset: -2px;
  }

  .action-btn.mode-btn.active,
  .action-btn.mode-btn.active:hover {
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
    min-width: 0;
    white-space: nowrap;
  }

  /* One line: the path gives up room (ending in …, full on hover) so the
     branch, message count and token use stay beside it. Narrow panes wrap. */
  @container (min-width: 641px) {
    .header-meta-row {
      flex-wrap: nowrap;
    }
    .cwd-item {
      flex: 0 1 auto;
      min-width: 140px;
    }
    .header-meta-row > .meta-item:not(.cwd-item),
    .stats-group {
      flex-shrink: 0;
    }
    .branch-item {
      flex-shrink: 1;
      min-width: 0;
      max-width: 180px;
    }
    .branch-text {
      overflow: hidden;
      text-overflow: ellipsis;
    }
  }

  /* The path is cut short, never the copy button after it. */
  .cwd-text {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .cwd-copy-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 18px;
    height: 18px;
    padding: 0;
    border-radius: 3px;
    color: var(--text-muted);
    cursor: pointer;
  }

  .cwd-copy-btn:hover {
    color: var(--text-primary);
    background-color: var(--hover-bg);
  }

  .cwd-copy-btn.copied {
    color: var(--success, #10b981);
  }

  .missing-badge {
    color: var(--danger);
    font-weight: 600;
    font-size: 0.7rem;
  }

  .branch-item {
    font-family: var(--font-mono);
  }

  .stats-group {
    display: flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  .header-time-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 12px;
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
