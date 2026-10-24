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
  import { preferences } from '../../stores/preferences.svelte';
  import { terminalFontFamily } from '../../terminalFont';
  import { terminalKeyAction, terminalTabShortcut, terminalTheme } from '../../terminal';
  import { copyToClipboard, readClipboard } from '../../portable';
  import { isMac } from '../../../main';
  import { onSplitterDragEnd, splitterDragging } from '../../splitterDrag';
  import { installTerminalFileLinks } from '../../terminalFileLinks';
  import { createTerminalFileOpener } from '../../terminalFileOpen';
  import { installTerminalColorScheme } from '../../terminalColorScheme';
  import { files } from '../../stores/files.svelte';
  import { link } from '../../stores/link.svelte';
  import { epoch, isStaleReply } from '../../link';
  import { toast } from '../../stores/toast.svelte';

  interface Props {
    tab: PanelTab;
    /** The focused pane, which takes typing; its panel tab shows it. */
    active: boolean;
  }

  let { tab, active }: Props = $props();

  let host: HTMLDivElement;
  let term = $state<Terminal | null>(null);
  let fit: FitAddon | null = null;
  let colorScheme: ReturnType<typeof installTerminalColorScheme> | null = null;
  // The size the process was last told, to send only changes.
  let sent = { cols: 0, rows: 0 };
  let sendTimer: ReturnType<typeof setTimeout> | undefined;
  // Each new size makes a full-screen program redraw everything, so while
  // the panel is dragged the process hears the size once it settles.
  const SEND_SIZE_DELAY = 80;
  let menu = $state<{ x: number; y: number; items: ContextMenuItem[] } | null>(null);

  function cssVar(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  onMount(() => {
    let disposed = false;
    let cleanup = () => {};
    // Cells are measured when the terminal opens, so the font must be there.
    const font = terminalFontFamily(preferences.terminalFont, cssVar('--font-mono'));
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
      // A tab shortcut goes to the app only when that tab exists: programs
      // in the terminal may use Alt+digit themselves.
      const tabNumber = event.type === 'keydown' ? terminalTabShortcut(event, isMac) : null;
      if (tabNumber !== null && tabNumber <= terminals.groups.length) return false;
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

    const opener = createTerminalFileOpener({
      link: () => link,
      epoch,
      resolve: (cwd, path) => api.resolveTerminalFile(cwd, path),
      files,
      report: (body) => toast.show({ title: 'Could not open terminal file', body, tone: 'error' }),
      isStale: isStaleReply,
    });
    const fileLinks = installTerminalFileLinks(t, {
      isMac,
      cwd: tab.dir,
      onCwd: (cwd) => terminals.setCwd(tab.id, cwd),
      onOpen: (path, cwd) => { void opener.open(tab.host, path, cwd); },
    });
    const id = tab.id;
    const input = t.onData((data) => {
      if (tab.status === 'running') terminals.write(id, data);
    });
    const scheme = installTerminalColorScheme(t, theme.resolved);
    colorScheme = scheme;
    const detach = terminals.attach(id, {
      write: (data) => t.write(data, () => void api.terminalAck(id, data.length).catch(() => {})),
      exit: (code) => t.write(`\r\n\x1b[2m[Process exited with code ${code}]\x1b[0m\r\n`),
      paste: (text) => {
        t.paste(text);
        t.focus();
      },
    });
    const observer = new ResizeObserver((entries) => {
      if (splitterDragging()) keepCursorInView(entries[entries.length - 1].contentRect.height);
      else resize();
    });
    observer.observe(host);
    const offDragEnd = onSplitterDragEnd(() => {
      if (t.element) t.element.style.transform = '';
      resize();
    });
    resize();
    if (active) t.focus();

    return () => {
      clearTimeout(sendTimer);
      observer.disconnect();
      offDragEnd();
      input.dispose();
      scheme.dispose();
      colorScheme = null;
      fileLinks.dispose();
      opener.dispose();
      detach();
      t.dispose();
      term = null;
      fit = null;
    };
  }

  // Fits the terminal to its box and tells the process its new size. A
  // hidden tab has no box and keeps its size until it is shown. Refitting
  // at every frame of a splitter drag held each frame up, so the terminal
  // fits once the drag ends.
  function resize() {
    if (!term || !fit || splitterDragging() || host.clientWidth === 0 || host.clientHeight === 0) return;
    const dims = fit.proposeDimensions();
    if (!dims || !Number.isFinite(dims.cols) || !Number.isFinite(dims.rows) || dims.cols < 1 || dims.rows < 1) return;
    if (dims.cols !== term.cols || dims.rows !== term.rows) fit.fit();
    terminals.setSize(term.cols, term.rows, tab.id);
    clearTimeout(sendTimer);
    sendTimer = setTimeout(sendSize, SEND_SIZE_DELAY);
  }

  // Until it refits, the terminal keeps its rows. While the panel is too
  // short for them, they move up as far as the cursor row needs to stay in
  // view, which is where the refit puts them.
  function keepCursorInView(height: number) {
    const el = term?.element;
    const screen = el?.querySelector<HTMLElement>('.xterm-screen');
    if (!term || !el || !screen) return;
    const buffer = term.buffer.active;
    const row = buffer.baseY + buffer.cursorY - buffer.viewportY;
    const rowBottom = ((row + 1) * screen.offsetHeight) / term.rows;
    const shift = row < 0 || row >= term.rows ? 0 : Math.max(0, rowBottom - height);
    el.style.transform = shift ? `translateY(${-shift}px)` : '';
  }

  function sendSize() {
    if (!term || tab.status !== 'running' || (term.cols === sent.cols && term.rows === sent.rows)) return;
    sent = { cols: term.cols, rows: term.rows };
    terminals.resized(tab.id);
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
    const font = terminalFontFamily(preferences.terminalFont, cssVar('--font-mono'));
    const t = term;
    if (!t || t.options.fontFamily === font) return;
    let current = true;
    const ready = document.fonts?.load ? document.fonts.load(`13px ${font}`).catch(() => {}) : Promise.resolve();
    void ready.then(() => {
      if (!current || term !== t) return;
      t.options.fontFamily = font;
      resize();
    });
    return () => { current = false; };
  });

  $effect(() => {
    const resolved = theme.resolved;
    if (term) {
      term.options.theme = terminalTheme(resolved, cssVar);
      colorScheme?.update(resolved);
    }
  });

  $effect(() => {
    // A toolbar resize keeps the active tab, but should restore typing focus.
    void terminals.maximized;
    if (!active || !term || !terminals.open) return;
    const t = term;
    const frame = requestAnimationFrame(() => {
      resize();
      t.focus();
    });
    return () => cancelAnimationFrame(frame);
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

<!-- A running tab takes dropped files: terminals.dropFiles types their paths.
     The panel hides the panes of tabs not shown. -->
<div
  class="terminal-view"
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

  /* Until the terminal refits after a splitter drag, it keeps its rows and
     the panel clips them; keepCursorInView moves them. */
  :global(html.splitter-dragging) .xterm-host {
    overflow: hidden;
  }

  :global(html.splitter-dragging) .xterm-host :global(.xterm) {
    height: auto;
  }

  /* xterm.css paints the viewport black; xterm 6 themes only the scrollable
     element inside it, which stops at the last whole row. */
  .xterm-host :global(.xterm .xterm-viewport) {
    background-color: var(--bg-primary);
  }
</style>
