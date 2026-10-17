<script lang="ts">
  import { onMount } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import { WebLinksAddon } from '@xterm/addon-web-links';
  import '@xterm/xterm/css/xterm.css';
  import ContextMenu, { type ContextMenuItem } from '../common/ContextMenu.svelte';
  import { api } from '../../api';
  import { terminals, type PanelTab } from '../../stores/terminals.svelte';
  import { theme } from '../../stores/theme.svelte';
  import { terminalKeyAction, terminalTheme } from '../../terminal';
  import { copyToClipboard, readClipboard } from '../../portable';
  import { isMac } from '../../../main';

  interface Props {
    tab: PanelTab;
    active: boolean;
  }

  let { tab, active }: Props = $props();

  let host: HTMLDivElement;
  let term = $state<Terminal | null>(null);
  let fit: FitAddon | null = null;
  // The size the process was last told, to send only changes.
  let sent = { cols: 0, rows: 0 };
  let menu = $state<{ x: number; y: number; items: ContextMenuItem[] } | null>(null);

  function cssVar(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  onMount(() => {
    let disposed = false;
    let cleanup = () => {};
    // Cells are measured when the terminal opens, so the font must be there.
    const font = cssVar('--font-mono');
    const ready = document.fonts?.load ? document.fonts.load(`13px ${font}`).catch(() => {}) : Promise.resolve();
    void ready.then(() => {
      if (disposed) return;
      cleanup = create(font);
    });
    return () => {
      disposed = true;
      cleanup();
    };
  });

  function create(font: string): () => void {
    const t = new Terminal({
      fontFamily: font,
      fontSize: 13,
      cursorBlink: true,
      scrollback: 5000,
      theme: terminalTheme(theme.resolved, cssVar),
    });
    const f = new FitAddon();
    t.loadAddon(f);
    t.loadAddon(
      new WebLinksAddon((event, uri) => {
        event.preventDefault();
        void api.openURL(uri);
      }),
    );
    t.attachCustomKeyEventHandler((event) => {
      const action = terminalKeyAction(event, isMac);
      if (action === 'app') return false;
      if (action === 'copy') {
        event.preventDefault();
        void copySelection();
        return false;
      }
      if (action === 'paste') {
        event.preventDefault();
        void paste();
        return false;
      }
      return true;
    });
    t.open(host);
    term = t;
    fit = f;

    const id = tab.id;
    const input = t.onData((data) => {
      if (tab.status === 'running') terminals.write(id, data);
    });
    const detach = terminals.attach(id, {
      write: (data) => t.write(data, () => void api.terminalAck(id, data.length).catch(() => {})),
      exit: (code) => t.write(`\r\n\x1b[2m[Process exited with code ${code}]\x1b[0m\r\n`),
      paste: (text) => {
        t.paste(text);
        t.focus();
      },
    });
    const observer = new ResizeObserver(() => resize());
    observer.observe(host);
    resize();
    if (active) t.focus();

    return () => {
      observer.disconnect();
      input.dispose();
      detach();
      t.dispose();
      term = null;
      fit = null;
    };
  }

  // Fits the terminal to its box and tells the process its new size. A
  // hidden tab has no box and keeps its size until it is shown.
  function resize() {
    if (!term || !fit || host.clientWidth === 0 || host.clientHeight === 0) return;
    const dims = fit.proposeDimensions();
    if (!dims || !Number.isFinite(dims.cols) || !Number.isFinite(dims.rows) || dims.cols < 1 || dims.rows < 1) return;
    if (dims.cols !== term.cols || dims.rows !== term.rows) fit.fit();
    terminals.setSize(term.cols, term.rows);
    if (tab.status !== 'running' || (term.cols === sent.cols && term.rows === sent.rows)) return;
    sent = { cols: term.cols, rows: term.rows };
    void api.terminalResize(tab.id, term.cols, term.rows).catch(() => {});
  }

  async function copySelection() {
    const text = term?.getSelection();
    if (text) await copyToClipboard(text);
  }

  async function paste() {
    const text = await readClipboard();
    if (text && tab.status === 'running') term?.paste(text);
  }

  $effect(() => {
    const resolved = theme.resolved;
    if (term) term.options.theme = terminalTheme(resolved, cssVar);
  });

  $effect(() => {
    if (!active || !term) return;
    const t = term;
    requestAnimationFrame(() => {
      resize();
      t.focus();
    });
  });

  function openMenu(event: MouseEvent) {
    event.preventDefault();
    menu = {
      x: event.clientX,
      y: event.clientY,
      items: [
        { label: 'Copy', disabled: !term?.hasSelection(), onSelect: () => void copySelection() },
        { label: 'Paste', disabled: tab.status !== 'running', onSelect: () => void paste() },
        { label: 'Clear', onSelect: () => term?.clear() },
      ],
    };
  }
</script>

<!-- A running tab takes dropped files: terminals.dropFiles types their paths. -->
<div
  class="terminal-view"
  class:hidden={!active}
  style:--wails-drop-target={tab.status === 'running' ? 'drop' : undefined}
  oncontextmenu={openMenu}
  role="presentation"
>
  <div class="xterm-host" bind:this={host}></div>
</div>

{#if menu}
  <ContextMenu
    x={menu.x}
    y={menu.y}
    items={menu.items}
    ariaLabel="Terminal actions"
    onClose={() => {
      menu = null;
      term?.focus();
    }}
  />
{/if}

<style>
  .terminal-view {
    position: absolute;
    inset: 0;
    padding: 4px 0 0 8px;
    background: var(--bg-primary);
  }

  .terminal-view.hidden {
    display: none;
  }

  /* The runtime marks the drop target while files are dragged over it. */
  .terminal-view:global(.wails-drop-target-active)::after {
    content: '';
    position: absolute;
    inset: 4px;
    border: 2px dashed var(--accent-color);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--accent-color) 8%, transparent);
    pointer-events: none;
  }

  .xterm-host {
    width: 100%;
    height: 100%;
  }

  .xterm-host :global(.xterm) {
    height: 100%;
  }

  /* xterm.css paints the viewport black; xterm 6 themes only the scrollable
     element inside it, which stops at the last whole row. */
  .xterm-host :global(.xterm .xterm-viewport) {
    background-color: var(--bg-primary);
  }
</style>
