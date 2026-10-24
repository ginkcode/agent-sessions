<script lang="ts">
  import { onMount } from 'svelte';
  import TerminalView from './TerminalView.svelte';
  import { api } from '../../api';
  import { terminals } from '../../stores/terminals.svelte';
  import { hostLabel } from '../../hosts';
  import { isMac } from '../../../main';
  import { newShellShortcutLabel } from '../../terminal';
  import ContextMenu from '../common/ContextMenu.svelte';
  import Splitter from '../common/Splitter.svelte';
  import AgentIcon from '../common/AgentIcon.svelte';
  import AttentionDot from '../common/AttentionDot.svelte';
  import { attentionLabel } from '../../alerts';
  import { TAB_CLOSE_ACTIONS, tabsToClose } from '../../tabActions';
  import { appState } from '../../stores/appState.svelte';
  import { link } from '../../stores/link.svelte';
  import type { ContextMenuItem } from '../common/ContextMenu.svelte';

  type Pane = (typeof terminals.tabs)[number];
  type Group = (typeof terminals.groups)[number];

  let panesById = $derived(new Map(terminals.tabs.map((t) => [t.id, t])));
  // The tab shown: the focused pane's.
  let shown = $derived(terminals.groups.find((g) => terminals.active !== null && g.panes.includes(terminals.active)));
  // Where each pane sits in its tab, and its share of it.
  let places = $derived.by(() => {
    const places = new Map<string, { index: number; share: number; shown: boolean }>();
    for (const group of terminals.groups) {
      group.panes.forEach((id, index) => places.set(id, { index, share: group.sizes[index] ?? 1, shown: group.id === shown?.id }));
    }
    return places;
  });
  let stacked = $derived(terminals.position === 'left');
  let bodyWidth = $state(0);
  let bodyHeight = $state(0);

  function panesOf(group: Group): Pane[] {
    return group.panes.map((id) => panesById.get(id)).filter((t): t is Pane => !!t);
  }

  // Panes keep room for a few columns or rows of their own.
  const PANE_MIN = { width: 120, height: 60 };

  // Moves the border after pane index by delta pixels, returning what applied.
  function resizePanes(group: Group, index: number, delta: number): number {
    const length = stacked ? bodyHeight : bodyWidth;
    if (length <= 0) return 0;
    const min = (stacked ? PANE_MIN.height : PANE_MIN.width) / length;
    return terminals.resizePanes(group.id, index, delta / length, min) * length;
  }

  let tabContext = $state<{ id: string; x: number; y: number } | null>(null);
  let tabItems = $derived.by((): ContextMenuItem[] => {
    if (!tabContext) return [];
    const { id } = tabContext;
    const close: ContextMenuItem[] = TAB_CLOSE_ACTIONS.map(({ action, label }, i) => {
      const targets = tabsToClose(terminals.groups.map((g) => g.id), id, action).flatMap(
        (gid) => terminals.groups.find((g) => g.id === gid)?.panes ?? [],
      );
      return { label, disabled: targets.length === 0, separator: i === 0, onSelect: () => terminals.closeMany(targets) };
    });
    const group = terminals.groups.find((g) => g.id === id);
    const panes = group ? panesOf(group) : [];
    // A split tab's transcript is its focused agent's, or its first agent's.
    const tab = panes.find((t) => t.id === group?.focus && t.kind === 'agent') ?? panes.find((t) => t.kind === 'agent');
    if (tab?.kind !== 'agent') return close.map((item) => ({ ...item, separator: false }));
    const session = tab.session;
    // Only the shown host's sessions can be shown.
    const elsewhere = tab.host !== (link.dataHost ?? '');
    const show: ContextMenuItem = {
      label: 'Show Transcript',
      disabled: !session || elsewhere,
      title: !session
        ? 'The session appears once the agent saves it, usually after the first prompt'
        : elsewhere
          ? `Switch to ${hostLabel(tab.host) || 'Local'} to see this session`
          : 'Show this session in the transcript',
      onSelect: () => {
        if (session) void appState.selectSession(session, 'push');
      },
    };
    return [show, ...close];
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

  // The directory each pane is in, the tab itself shows the rest, and the
  // shortcut that selects the tab.
  function tabTitle(group: Group, index: number): string {
    const title = panesOf(group)
      .map((tab) => {
        const where = tab.cwd || tab.dir || (tab.kind === 'shell' ? 'Home directory' : tab.label);
        // The tab may shorten the host's name, so the tooltip gives it in full.
        const line = tab.host ? `${where} on ${hostLabel(tab.host)}` : where;
        const state = attentionLabel(terminals.attention[tab.id]);
        return state ? `${state} — ${line}` : line;
      })
      .join('\n');
    return index < 9 ? `${title} (${isMac ? '⌘' : 'Alt+'}${index + 1})` : title;
  }

  function tabLabel(group: Group): string {
    return panesOf(group)
      .map((t) => t.label)
      .join(', ');
  }

  // A click on a pane's part of a split tab focuses that pane.
  function selectTab(event: MouseEvent, group: Group) {
    const id = (event.target as Element | null)?.closest?.('[data-segment]')?.getAttribute('data-segment');
    terminals.select(id && group.panes.includes(id) ? id : group.focus);
  }

  function handleTabKeydown(event: KeyboardEvent, group: Group) {
    if (event.key === 'Delete') {
      event.preventDefault();
      terminals.closeMany(group.panes);
    }
  }
</script>

{#if tabContext && !hidden}
  <ContextMenu x={tabContext.x} y={tabContext.y} items={tabItems} ariaLabel="Terminal tab actions" onClose={() => (tabContext = null)} />
{/if}

<section class="terminal-panel" class:hidden class:maximized={terminals.maximized} aria-label="Terminal" style:height={terminals.maximized ? undefined : `${terminals.height}px`}>
  <header class="panel-header">
    <div class="tabs" role="tablist" aria-label="Terminals">
      {#each terminals.groups as group, index (group.id)}
        {@const panes = panesOf(group)}
        {@const split = panes.length > 1}
        <div class="tab" role="presentation" class:active={group.id === shown?.id} class:split title={tabTitle(group, index)} oncontextmenu={(e) => {
          e.preventDefault();
          tabContext = { id: group.id, x: e.clientX, y: e.clientY };
        }}>
          <button
            type="button"
            class="tab-select"
            role="tab"
            aria-selected={group.id === shown?.id}
            onclick={(e) => selectTab(e, group)}
            onkeydown={(e) => handleTabKeydown(e, group)}
          >
            {#each panes as tab, i (tab.id)}
              {#if i > 0}
                <span class="segment-divider" aria-hidden="true"></span>
              {/if}
              <span class="segment" class:focused={split && tab.id === group.focus} data-segment={tab.id}>
                {#if tab.kind === 'agent' && tab.status === 'running' && attentionLabel(terminals.attention[tab.id])}
                  <AttentionDot state={terminals.attention[tab.id]} announce />
                {:else}
                  <!-- A running agent tab's dot is its state; quiet, it is grey. -->
                  <span
                    class="status-dot"
                    class:running={tab.status === 'running' && tab.kind !== 'agent'}
                    class:failed={tab.status === 'exited' && tab.exitCode !== 0}
                    aria-hidden="true"
                  ></span>
                {/if}
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
              </span>
            {/each}
          </button>
          <button
            type="button"
            class="tab-close"
            aria-label="Close {tabLabel(group)}"
            title="Close"
            onclick={() => terminals.closeMany(group.panes)}
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
        disabled={terminals.maximized}
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
        title={terminals.maximized ? 'Restore terminal' : 'Maximize terminal'}
        aria-label={terminals.maximized ? 'Restore terminal' : 'Maximize terminal'}
        aria-pressed={terminals.maximized}
        onclick={() => terminals.toggleMaximized()}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          {#if terminals.maximized}
            <path d="M8 3v5H3m13-5v5h5M8 21v-5H3m13 5v-5h5" />
          {:else}
            <path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5" />
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

  <!-- One flat list of views, so a pane never remounts (and loses its
       scrollback) when tabs change; the shown tab's panes are laid out by
       order, with splitters between them. -->
  <div class="panel-body" class:stacked bind:clientWidth={bodyWidth} bind:clientHeight={bodyHeight}>
    {#each terminals.tabs as tab (tab.id)}
      {@const place = places.get(tab.id)}
      <div
        class="pane"
        class:hidden={!place?.shown}
        class:split={!!place?.shown && (shown?.panes.length ?? 0) > 1}
        class:focused={tab.id === terminals.active}
        data-pane={tab.id}
        style:order={(place?.index ?? 0) * 2}
        style:flex-grow={place?.share ?? 1}
        onfocusin={() => {
          if (tab.id !== terminals.active) terminals.select(tab.id);
        }}
      >
        <TerminalView {tab} active={tab.id === terminals.active} />
      </div>
    {:else}
      <div class="empty">
        <p>No terminals are open.</p>
        <button type="button" class="btn" onclick={() => void terminals.newShell(cwd)}>New shell</button>
      </div>
    {/each}
    {#if shown && shown.panes.length > 1}
      {@const group = shown}
      {#each group.panes.slice(1) as id, i (id)}
        <div class="pane-border" style:order={i * 2 + 1}>
          <Splitter
            direction={stacked ? 'vertical' : 'horizontal'}
            ariaLabel="Resize terminal panes"
            onResize={(delta) => resizePanes(group, i, delta)}
          />
        </div>
      {/each}
    {/if}
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

  .terminal-panel.maximized {
    flex: 1 1 0;
    min-height: 0;
    max-height: none;
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

  /* A split tab has room for each pane's name. */
  .tab.split {
    flex: 0 1 auto;
    max-width: 520px;
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

  .segment {
    display: flex;
    flex: 0 1 auto;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  .segment-divider {
    flex-shrink: 0;
    align-self: stretch;
    width: 1px;
    margin: 9px 0;
    background: var(--border-color);
  }

  /* In the shown split tab, the focused pane's name stands out. */
  .tab.active.split .segment:not(.focused) {
    color: var(--text-secondary);
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

  /* A long session name gives way first, so the host stays readable; the
     host shortens only once a crowded tab has no room left for the name. */
  .tab-label {
    flex-shrink: 1000;
  }

  .tab-host {
    max-width: 12ch;
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
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  /* Docked left, the panel is narrow: panes stack. */
  .panel-body.stacked {
    flex-direction: column;
  }

  /* Each pane shrinks freely; its terminal clips until it refits. */
  .pane {
    position: relative;
    flex: 1 1 0;
    min-width: 0;
    min-height: 0;
    overflow: hidden;
  }

  .pane.hidden {
    display: none;
  }

  /* Side by side, the focused pane is outlined; the border sits outside its
     terminal, so it needs no layer above it. */
  .pane.split {
    border: 1px solid transparent;
  }

  .pane.split.focused {
    border-color: color-mix(in srgb, var(--accent-color) 55%, transparent);
  }

  /* Holds a splitter between two panes; the splitter overhangs both. */
  .pane-border {
    display: flex;
    flex: 0 0 0;
  }

  .empty {
    flex: 1;
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
