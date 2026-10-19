<script lang="ts">
  import { onMount } from 'svelte';
  import TerminalView from './TerminalView.svelte';
  import { api } from '../../api';
  import { terminals } from '../../stores/terminals.svelte';
  import { hostLabel } from '../../hosts';
  import { isMac } from '../../../main';
  import { newShellShortcutLabel } from '../../terminal';
  import ContextMenu from '../common/ContextMenu.svelte';
  import AgentIcon from '../common/AgentIcon.svelte';
  import { TAB_CLOSE_ACTIONS, tabsToClose } from '../../tabActions';

  let tabContext = $state<{ id: string; x: number; y: number } | null>(null);
  let tabItems = $derived.by(() => {
    if (!tabContext) return [];
    const { id } = tabContext;
    return TAB_CLOSE_ACTIONS.map(({ action, label }) => {
      const targets = tabsToClose(terminals.tabs.map((t) => t.id), id, action);
      return { label, disabled: targets.length === 0, onSelect: () => terminals.closeMany(targets) };
    });
  });

  interface Props {
    /** The directory a new shell starts in, on the selected host. */
    cwd?: string;
    hidden?: boolean;
  }

  let { cwd = '', hidden = false }: Props = $props();

  // Only the active tab is shown, so files dropped on the panel are its.
  onMount(() => api.onFileDrop((_x, _y, paths) => void terminals.dropFiles(paths)));

  let confirmingTab = $derived(terminals.tabs.find((t) => t.id === terminals.confirming));

  // The directory the tab started in, the tab itself shows the rest, and
  // the shortcut that selects it.
  function tabTitle(tab: (typeof terminals.tabs)[number], index: number): string {
    const title = tab.cwd || tab.dir || (tab.kind === 'shell' ? 'Home directory' : tab.label);
    return index < 9 ? `${title} (${isMac ? '⌘' : 'Alt+'}${index + 1})` : title;
  }

  function handleTabKeydown(event: KeyboardEvent, id: string) {
    if (event.key === 'Delete') {
      event.preventDefault();
      terminals.close(id);
    }
  }
</script>

{#if tabContext && !hidden}
  <ContextMenu x={tabContext.x} y={tabContext.y} items={tabItems} ariaLabel="Terminal tab actions" onClose={() => (tabContext = null)} />
{/if}

<section class="terminal-panel" class:hidden aria-label="Terminal" style="height: {terminals.height}px">
  <header class="panel-header">
    <div class="tabs" role="tablist" aria-label="Terminals">
      {#each terminals.tabs as tab, index (tab.id)}
        <div class="tab" role="presentation" class:active={tab.id === terminals.active} title={tabTitle(tab, index)} oncontextmenu={(e) => {
          e.preventDefault();
          tabContext = { id: tab.id, x: e.clientX, y: e.clientY };
        }}>
          <button
            type="button"
            class="tab-select"
            role="tab"
            aria-selected={tab.id === terminals.active}
            onclick={() => terminals.select(tab.id)}
            onkeydown={(e) => handleTabKeydown(e, tab.id)}
          >
            <span
              class="status-dot"
              class:running={tab.status === 'running'}
              class:failed={tab.status === 'exited' && tab.exitCode !== 0}
              aria-hidden="true"
            ></span>
            {#if tab.agent}
              <AgentIcon agent={tab.agent} size={12} />
            {/if}
            <span class="tab-label">{tab.label}</span>
            {#if tab.host}
              <span class="tab-host">{hostLabel(tab.host)}</span>
            {/if}
            {#if tab.status === 'exited'}
              <span class="tab-exit">exit {tab.exitCode}</span>
            {/if}
          </button>
          <button
            type="button"
            class="tab-close"
            aria-label="Close {tab.label}"
            title="Close"
            onclick={() => terminals.close(tab.id)}
          >×</button>
        </div>
      {/each}
    </div>
    <div class="panel-actions">
      <button
        type="button"
        class="icon-button"
        title="New shell ({newShellShortcutLabel(isMac)})"
        aria-label="New shell"
        onclick={() => void terminals.newShell(cwd)}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <path d="M12 5v14M5 12h14" />
        </svg>
      </button>
      <button
        type="button"
        class="icon-button"
        title={terminals.position === 'left' ? 'Move terminal to the bottom' : 'Move terminal to the left, below the session list'}
        aria-label={terminals.position === 'left' ? 'Move terminal to the bottom' : 'Move terminal to the left'}
        onclick={() => terminals.setPosition(terminals.position === 'left' ? 'bottom' : 'left')}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <!-- The layout it moves to. -->
          <rect x="3" y="3" width="18" height="18" rx="2" />
          {#if terminals.position === 'left'}
            <path d="M3 15h18" />
          {:else}
            <path d="M3 15h11M14 3v18" />
          {/if}
        </svg>
      </button>
      <button
        type="button"
        class="icon-button"
        title="Hide terminal (Ctrl+`)"
        aria-label="Hide terminal"
        onclick={() => terminals.setOpen(false)}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="m6 9 6 6 6-6" />
        </svg>
      </button>
    </div>
  </header>

  {#if confirmingTab}
    <div class="confirm-bar" role="alertdialog" aria-label="Close running agent">
      <span><strong>{confirmingTab.label}</strong> is still running. Closing the tab ends it.</span>
      <button type="button" class="btn danger-btn" onclick={() => terminals.confirmClose()}>End and close</button>
      <button type="button" class="btn" onclick={() => terminals.cancelClose()}>Cancel</button>
    </div>
  {/if}

  <div class="panel-body">
    {#each terminals.tabs as tab (tab.id)}
      <TerminalView {tab} active={tab.id === terminals.active} />
    {:else}
      <div class="empty">
        <p>No terminals are open.</p>
        <button type="button" class="btn" onclick={() => void terminals.newShell(cwd)}>New shell</button>
      </div>
    {/each}
  </div>
</section>

<style>
  .terminal-panel {
    display: flex;
    flex-direction: column;
    flex: 0 1 auto;
    min-height: 120px;
    max-height: 70vh;
    background: var(--bg-primary);
    color: var(--text-primary);
  }

  .terminal-panel.hidden {
    display: none;
  }

  .panel-header {
    display: flex;
    align-items: stretch;
    justify-content: space-between;
    height: 32px;
    min-height: 32px;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
  }

  .tabs {
    display: flex;
    flex: 1;
    min-width: 0;
    overflow-x: auto;
    scrollbar-width: thin;
  }

  /* Tabs are roomy while they fit and shrink as more open; past their
     minimum the bar scrolls. */
  .tab {
    display: flex;
    flex: 0 1 180px;
    align-items: center;
    min-width: 110px;
    max-width: 260px;
    border-right: 1px solid var(--border-color);
    color: var(--text-secondary);
    font-size: 0.78rem;
  }

  .tab.active {
    background: var(--bg-primary);
    color: var(--text-primary);
    box-shadow: inset 0 2px 0 var(--accent-color);
  }

  .tab:hover:not(.active) {
    background: var(--hover-bg);
  }

  .tab-select {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 6px;
    min-width: 0;
    height: 100%;
    padding: 0 4px 0 10px;
    color: inherit;
  }

  .tab-label,
  .tab-host {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tab-host,
  .tab-exit {
    color: var(--text-muted);
    font-size: 0.72rem;
  }

  .status-dot {
    flex-shrink: 0;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--text-muted);
  }

  .status-dot.running {
    background: var(--status-live);
  }

  .status-dot.failed {
    background: var(--danger);
  }

  .tab-close {
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    margin-right: 4px;
    border-radius: 4px;
    color: var(--text-muted);
    font-size: 1rem;
    line-height: 1;
  }

  .tab-close:hover {
    color: var(--text-primary);
    background: var(--hover-bg);
  }

  .panel-actions {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 6px;
  }

  .icon-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border-radius: 4px;
    color: var(--text-secondary);
  }

  .icon-button:hover {
    color: var(--text-primary);
    background: var(--hover-bg);
  }

  .confirm-bar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--border-color);
    background: var(--bg-tertiary);
    font-size: 0.8rem;
  }

  .confirm-bar span {
    flex: 1;
  }

  .btn {
    height: 26px;
    padding: 0 12px;
    border-radius: var(--radius-sm);
    font-size: 0.78rem;
    font-weight: 500;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
    border: 1px solid var(--border-color);
  }

  .btn:hover {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .danger-btn {
    background: var(--danger);
    border-color: var(--danger);
    color: white;
  }

  .danger-btn:hover {
    background: color-mix(in srgb, var(--danger) 85%, black);
    color: white;
  }

  .panel-body {
    position: relative;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    height: 100%;
    color: var(--text-muted);
    font-size: 0.85rem;
  }
</style>
