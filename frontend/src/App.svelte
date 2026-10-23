<script lang="ts">
  import { onMount } from 'svelte';
  import PortForwardDialog from './lib/components/common/PortForwardDialog.svelte';
  import { portForward } from './lib/stores/portForward.svelte';
  import { wslDistro } from './lib/hosts';
  import { isFullscreenShortcut, toggleFullscreen, preventDesktopEscapeFullscreen } from './lib/fullscreen';
  import { toast } from './lib/stores/toast.svelte';
  import Splitter from './lib/components/common/Splitter.svelte';
  import ModeSelector from './lib/components/sidebar/ModeSelector.svelte';
  import PathFilter from './lib/components/sidebar/PathFilter.svelte';
  import GroupTree from './lib/components/sidebar/GroupTree.svelte';
  import AgentSummary from './lib/components/sidebar/AgentSummary.svelte';
  import SessionList from './lib/components/sessionlist/SessionList.svelte';
  import TranscriptView from './lib/components/transcript/TranscriptView.svelte';
  import ManageSettingsDialog from './lib/components/common/ManageSettingsDialog.svelte';
  import GlobalSearchDialog from './lib/components/common/GlobalSearchDialog.svelte';
  import GoToFileDialog from './lib/components/common/GoToFileDialog.svelte';
  import HandoffDialog from './lib/components/common/HandoffDialog.svelte';
  import ExportDialog from './lib/components/common/ExportDialog.svelte';
  import ImportDialog from './lib/components/common/ImportDialog.svelte';
  import ImportDirectoryDialog from './lib/components/common/ImportDirectoryDialog.svelte';
  import MediaViewer from './lib/components/common/MediaViewer.svelte';
  import PrintSheet from './lib/components/common/PrintSheet.svelte';
  import PrintDialog from './lib/components/common/PrintDialog.svelte';
  import AskpassDialog from './lib/components/common/AskpassDialog.svelte';
  import ReconnectBanner from './lib/components/common/ReconnectBanner.svelte';
  import HostSelector from './lib/components/sidebar/HostSelector.svelte';
  import Toaster from './lib/components/common/Toaster.svelte';
  import TerminalPanel from './lib/components/terminal/TerminalPanel.svelte';
  import { theme } from './lib/stores/theme.svelte';
  import { preferences } from './lib/stores/preferences.svelte';
  import { appState } from './lib/stores/appState.svelte';
  import { manage } from './lib/stores/manage.svelte';
  import { search } from './lib/stores/search.svelte';
  import { importer } from './lib/stores/importer.svelte';
  import { connectionStore } from './lib/stores/connection.svelte';
  import { link } from './lib/stores/link.svelte';
  import { launcher } from './lib/stores/launcher.svelte';
  import { translateSettings } from './lib/stores/translate.svelte';
  import { terminals } from './lib/stores/terminals.svelte';
  import { alerts } from './lib/stores/alerts.svelte';
  import { attentionSummary, mostUrgent } from './lib/alerts';
  import AttentionDot from './lib/components/common/AttentionDot.svelte';
  import { newSession } from './lib/stores/newSession.svelte';
  import { sessionLinks } from './lib/stores/sessionLinks.svelte';
  import { files } from './lib/stores/files.svelte';
  import { contentSearch } from './lib/stores/contentSearch.svelte';
  import { isSearchShortcut, searchSeed } from './lib/contentSearch';
  import { hasOpenModal, isEditableTarget } from './lib/search';
  import { navigationButton, navigationShortcut, type NavDirection } from './lib/navHistory';
  import { api, subscribeMouseNavigate } from './lib/api';
  import { formatVersion } from './lib/format';
  import { themeButtonTitle } from './lib/theme';
  import { hostLabel } from './lib/hosts';
  import {
    isNewSessionShortcut,
    isNewShellShortcut,
    isNewWindowShortcut,
    isPanelToggle,
    newWindowShortcutLabel,
    terminalTabShortcut,
    windowCycleShortcut,
  } from './lib/terminal';
  import { isMac } from './main';

  let isWails = $state(false);
  let appVersion = $state('');
  // The panes show a host's last data while it is not serving, or stay
  // inert during a first connect so nothing on screen passes for the
  // host's sessions.
  let stale = $derived(link.stale || link.locked);
  // A new shell starts in the shown session's directory.
  let shellCwd = $derived(appState.selectedSessionMeta?.cwd ?? '');
  // While the panel is hidden, its toggle shows the most urgent agent tab.
  let panelAttention = $derived(terminals.open ? null : mostUrgent(Object.values(terminals.attention)));
  let terminalButtonTitle = $derived.by(() => {
    if (terminals.open) return 'Hide terminal (Ctrl+`)';
    const summary = attentionSummary(panelAttention);
    return summary ? `${summary}. Open terminal (Ctrl+\`)` : 'Open terminal (Ctrl+`)';
  });
  // Only the desktop app opens more windows, each with its own connection.
  let canOpenWindow = $derived(isWails && typeof api.newWindow === 'function');

  function openNewWindow() {
    api.newWindow?.().catch((err: unknown) =>
      toast.show({
        title: "Couldn't open a new window",
        body: err instanceof Error ? err.message : String(err),
        tone: 'error',
      }),
    );
  }

  $effect(() => {
    const host = connectionStore.host;
    void connectionStore.phase;
    if (!host || wslDistro(host)) {
      portForward.data = { host: '', rules: [] };
      return;
    }
    void portForward.refresh();
    const timer = setInterval(() => void portForward.refresh(), 2000);
    return () => clearInterval(timer);
  });

  onMount(() => {
    theme.init();
    preferences.init();
    connectionStore.init();
    appState.init();
    manage.init();
    search.init();
    void launcher.init();
    void translateSettings.init();
    terminals.init();
    alerts.init();
    newSession.init();
    sessionLinks.init();
    isWails = typeof window !== 'undefined' && Boolean((window as any).go?.app?.App);
    api
      .appVersion()
      .then((v) => (appVersion = formatVersion(v)))
      .catch(() => {});

    const go = (direction: NavDirection) => {
      if (stale) return;
      void (direction === 'back' ? appState.goBack() : appState.goForward());
    };
    const onKeydown = (event: KeyboardEvent) => {
      if (isFullscreenShortcut(event, isMac)) {
        event.preventDefault();
        if (!event.repeat) void toggleFullscreen().catch((err) => {
          toast.show({ title: 'Could not toggle fullscreen', body: String(err), tone: 'error' });
        });
        return;
      }
      const direction = navigationShortcut(event, isMac);
      if (direction) {
        // Alt+Left/Right and Cmd+[ move the caret or indent in a text field.
        if (event.key.startsWith('Browser') ? hasOpenModal() : isEditableTarget(event.target) || hasOpenModal()) return;
        event.preventDefault();
        go(direction);
        return;
      }
      // Ctrl+`, as in editors: shows or hides the terminal panel.
      if (isPanelToggle(event)) {
        if (!launcher.info.panel || hasOpenModal()) return;
        event.preventDefault();
        void terminals.toggle(shellCwd);
        return;
      }
      // Cmd+T on macOS, Ctrl+Shift+T elsewhere: a new shell tab, showing the panel.
      if (isNewShellShortcut(event, isMac)) {
        if (!launcher.info.panel || hasOpenModal()) return;
        event.preventDefault();
        if (!event.repeat) void terminals.newShell(shellCwd);
        return;
      }
      // Cmd+Shift+A on macOS, Ctrl+Shift+A elsewhere: a new session of the
      // agent last started, in the shown session's project. Go to File
      // handles it for its own project.
      if (isNewSessionShortcut(event, isMac)) {
        if (!newSession.available || !shellCwd || files.open || hasOpenModal() || link.stale) return;
        event.preventDefault();
        if (!event.repeat) void newSession.start(newSession.defaultAgent, shellCwd);
        return;
      }
      // Cmd+N on macOS, Ctrl+Shift+N elsewhere: another window of the app.
      if (canOpenWindow && isNewWindowShortcut(event, isMac)) {
        event.preventDefault();
        if (!event.repeat) openNewWindow();
        return;
      }
      // Alt+` on Windows and Linux: the app's next window (Alt+Shift+`, the
      // previous). Cmd+` on macOS is handled natively.
      const cycle = canOpenWindow ? windowCycleShortcut(event, isMac) : null;
      if (cycle) {
        event.preventDefault();
        if (!event.repeat) void api.focusNextWindow?.(cycle === 'next').catch(() => {});
        return;
      }
      // Cmd+1–9 on macOS, Alt+1–9 elsewhere: selects a terminal tab.
      const tabNumber = terminalTabShortcut(event, isMac);
      if (tabNumber !== null) {
        if (!launcher.info.panel || hasOpenModal()) return;
        if (terminals.selectNth(tabNumber)) event.preventDefault();
        return;
      }
      // Cmd+Shift+F on macOS, Ctrl+Shift+F elsewhere: searches the files of
      // the selected session's project, from the selected text. Go to File
      // handles it itself while open.
      if (isSearchShortcut(event, isMac)) {
        const cwd = appState.selectedSessionMeta?.cwd;
        if (!cwd || files.open || hasOpenModal() || link.stale) return;
        event.preventDefault();
        if (event.repeat) return;
        const seed = searchSeed(document.getSelection()?.toString() ?? '');
        void files.show(cwd);
        if (files.dir === cwd) contentSearch.open(seed);
        return;
      }
      // Cmd+, on macOS, Ctrl+, elsewhere: the convention for Settings.
      if (event.key === ',' && (isMac ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey) && !event.altKey) {
        event.preventDefault();
        if (hasOpenModal()) return;
        manage.openSettings();
        return;
      }
      if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isEditableTarget(event.target) || hasOpenModal() || link.stale) return;
      event.preventDefault();
      search.show();
    };
    // The mouse's side buttons, as WebView2 and browsers report them. The
    // webview's own back/forward on them is cancelled: the app is one page
    // and must never navigate away. On Linux and macOS they come as an event.
    const onMouseButton = (event: MouseEvent) => {
      const direction = navigationButton(event.button);
      if (!direction) return;
      event.preventDefault();
      if (event.type === 'mouseup' && !hasOpenModal()) go(direction);
    };
    const unsubscribeMouseNavigate = subscribeMouseNavigate((direction) => {
      if (!hasOpenModal()) go(direction);
    });
    window.addEventListener('keydown', onKeydown);
    window.addEventListener('keydown', preventDesktopEscapeFullscreen, true);
    window.addEventListener('mousedown', onMouseButton);
    window.addEventListener('mouseup', onMouseButton);
    return () => {
      unsubscribeMouseNavigate();
      window.removeEventListener('keydown', preventDesktopEscapeFullscreen, true);
      window.removeEventListener('keydown', onKeydown);
      window.removeEventListener('mousedown', onMouseButton);
      window.removeEventListener('mouseup', onMouseButton);
      search.destroy();
      alerts.destroy();
      terminals.destroy();
      sessionLinks.destroy();
      appState.destroy();
      connectionStore.destroy();
    };
  });

  // Each returns how far the edge moved, for the splitter to keep it under
  // the pointer at a size limit.
  function handleSidebarResize(delta: number): number {
    const before = preferences.widths.sidebar;
    preferences.setSidebarWidth(before + delta);
    return preferences.widths.sidebar - before;
  }

  function handleSessionListResize(delta: number): number {
    const before = preferences.widths.sessionList;
    preferences.setSessionListWidth(before + delta);
    return preferences.widths.sessionList - before;
  }

  // Dragging the splitter down shrinks the panel.
  function handlePanelResize(delta: number): number {
    const before = terminals.height;
    terminals.setHeight(before - delta, window.innerHeight);
    return before - terminals.height;
  }

  // On the left the panel sits below the sidebar and the session list, as
  // wide as both, and the transcript keeps the full height.
  let panelLeft = $derived(launcher.info.panel && terminals.position === 'left');
</script>

<div class="app-layout">
  <ReconnectBanner />
  <div
    class="workspace"
    class:panel-left={panelLeft}
    style:grid-template-columns={panelLeft
      ? `${preferences.widths.sidebar}px 0 ${preferences.widths.sessionList}px 0 minmax(0, 1fr)`
      : undefined}
  >
  <div class="shell">
    <!-- Pane 1: Sidebar -->
    <aside
      class="pane sidebar"
      style="width: {preferences.widths.sidebar}px"
      aria-label="Navigation and grouping"
    >
    <header class="pane-header sidebar-header">
      <div class="brand">
        <span class="app-icon">⚡</span>
        <h1 class="app-title">Agent Sessions</h1>
      </div>
      <div class="header-actions">
        <button
          type="button"
          class="icon-button search-toggle"
          title="Search all sessions (/)"
          aria-label="Search all sessions"
          disabled={stale}
          onclick={() => search.show()}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.35-4.35" />
          </svg>
        </button>
        {#if canOpenWindow}
          <button
            type="button"
            class="icon-button new-window"
            title={`New window (${newWindowShortcutLabel(isMac)}). Each window connects to its own host`}
            aria-label="New window"
            onclick={openNewWindow}
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <rect x="3" y="5" width="14" height="14" rx="2" />
              <path d="M7 5V3h14v14h-2" />
              <path d="M10 9v6M7 12h6" />
            </svg>
          </button>
        {/if}
        <button
          type="button"
          class="icon-button settings-toggle"
          title={isMac ? 'Settings (⌘,)' : 'Settings (Ctrl+,)'}
          aria-label="Settings"
          onclick={() => manage.openSettings()}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <circle cx="12" cy="12" r="3" />
            <path
              d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"
            />
          </svg>
        </button>
        <button
          type="button"
          class="icon-button theme-toggle"
          title={themeButtonTitle(theme.mode, theme.resolved)}
          onclick={() => theme.toggle()}
          aria-label={themeButtonTitle(theme.mode, theme.resolved)}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            {#if theme.mode === 'system'}
              <rect x="2" y="3" width="20" height="14" rx="2" />
              <path d="M8 21h8M12 17v4" />
            {:else if theme.mode === 'dark'}
              <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
            {:else}
              <circle cx="12" cy="12" r="5" />
              <path
                d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"
              />
            {/if}
          </svg>
        </button>
      </div>
    </header>

    <div class="sidebar-mode-section" class:stale inert={stale}>
      <ModeSelector />
      <PathFilter />
    </div>

    <div class="sidebar-content" class:stale inert={stale}>
      <GroupTree />
    </div>

    <div class:stale inert={stale}>
      <AgentSummary />
    </div>

    <footer class="sidebar-footer" class:remote={connectionStore.isRemote}>
      <div class="footer-status">
        <HostSelector />
        {#if isWails}
          {#if appVersion}
            <span class="runtime-badge wails" title="Agent Sessions {appVersion}">{appVersion}</span>
          {/if}
        {:else}
          <span class="runtime-badge">Mock (Browser)</span>
        {/if}
      </div>
      <div class="footer-actions">
        {#if launcher.info.panel}
          <button
            type="button"
            class="icon-button terminal-button"
            class:active={terminals.open}
            title={terminalButtonTitle}
            aria-label={terminals.open ? 'Hide terminal' : 'Open terminal'}
            aria-pressed={terminals.open}
            onclick={() => void terminals.toggle(shellCwd)}
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <polyline points="4 17 10 11 4 5" />
              <line x1="12" y1="19" x2="20" y2="19" />
            </svg>
            {#if panelAttention}
              <span class="terminal-attention"><AttentionDot state={panelAttention} announce /></span>
            {/if}
          </button>
        {/if}
        <button
          type="button"
          class="icon-button forwarding-button"
          title="Port forwarding"
          aria-label="Port forwarding"
          disabled={!connectionStore.host || Boolean(wslDistro(connectionStore.host))}
          onclick={() => void portForward.show()}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="4" width="7" height="6" rx="1" /><rect x="15" y="14" width="7" height="6" rx="1" /><path d="M5 10v7h7m-2-3 3 3-3 3M19 14V7h-7m2-3-3 3 3 3" /></svg>
          {#if portForward.data.host === connectionStore.host}
            {@const activeCount = portForward.data.rules.filter(r => r.status === 'active').length}
            {#if activeCount}<span class="forward-count">{activeCount}</span>{/if}
            {#if portForward.data.rules.some(r => r.status === 'failed')}<span class="forward-error" aria-label="A forwarding rule failed">!</span>{/if}
          {/if}
        </button>
        <button
          type="button"
          class="icon-button import-button"
          title="Import session bundle into a directory"
          aria-label="Import session bundle into a directory"
          disabled={importer.opening || stale}
          onclick={() => void importer.start()}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="7 10 12 15 17 10" />
            <line x1="12" y1="15" x2="12" y2="3" />
          </svg>
        </button>
        <button
          type="button"
          class="icon-button refresh-button"
          class:refreshing={appState.refreshing}
          title={appState.refreshing ? 'Refreshing…' : 'Refresh sessions'}
          aria-label={appState.refreshing ? 'Refreshing…' : 'Refresh sessions'}
          disabled={appState.refreshing || stale}
          onclick={() => void appState.refresh()}
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M23 4v6h-6M1 20v-6h6" />
            <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
          </svg>
        </button>
      </div>
    </footer>
  </aside>

  <!-- Splitter 1 -->
  <Splitter
    direction="horizontal"
    ariaLabel="Resize sidebar"
    onResize={handleSidebarResize}
  />

  <!-- Pane 2: Session List -->
  <section
    class="pane session-list-container"
    class:stale
    style="width: {preferences.widths.sessionList}px"
    aria-label="Session list"
    inert={stale}
  >
    <SessionList />
  </section>

  <!-- Splitter 2 -->
  <Splitter
    direction="horizontal"
    ariaLabel="Resize session list"
    onResize={handleSessionListResize}
  />

  <!-- Pane 3: Transcript View -->
  <main class="pane transcript-container" class:stale aria-label="Transcript details" inert={stale}>
    <TranscriptView />
  </main>

  {#if stale}
    <div class="stale-overlay" role="status">
      {#if link.locked && !link.stale}
        Connecting to <strong>{link.host && hostLabel(link.host)}</strong>… Changes are disabled until the connection is established or you switch back to Local.
      {:else}
        Showing data last loaded from <strong>{link.dataHost && hostLabel(link.dataHost)}</strong>. Changes are disabled until it reconnects.
      {/if}
    </div>
  {/if}

  <ManageSettingsDialog />
  <GlobalSearchDialog />
  <GoToFileDialog />
  <PortForwardDialog />
  <HandoffDialog />
  <ExportDialog />
  <ImportDialog />
  <ImportDirectoryDialog />
  <MediaViewer />
  <PrintDialog />
  <PrintSheet />
  <AskpassDialog />
  <Toaster />
  </div>
  <!-- Outside .shell: the inert on stale panes never reaches the terminals. -->
  {#if launcher.info.panel}
    {#if terminals.open}
      <div class="panel-splitter">
        <Splitter direction="vertical" ariaLabel="Resize terminal panel" onResize={handlePanelResize} />
      </div>
    {/if}
    <!-- Hidden, not unmounted: the terminals keep their scrollback. Moving
         the panel only changes the grid, for the same reason. -->
    <TerminalPanel cwd={shellCwd} hidden={!terminals.open} />
  {/if}
  </div>
</div>

<style>
  .app-layout {
    display: flex;
    flex-direction: column;
    width: 100vw;
    height: 100vh;
    overflow: hidden;
  }

  /* The panes and the terminal panel below them. On the left the panes'
     own box steps aside so they and the panel share one grid: the sidebar
     and the session list above the panel, the transcript beside both. The
     column widths come from the pane widths. */
  .workspace {
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1 1 0;
    min-height: 0;
    overflow: hidden;
  }

  .panel-splitter {
    display: flex;
    flex-shrink: 0;
  }

  .workspace.panel-left {
    display: grid;
    grid-template-rows: minmax(0, 1fr) auto auto;
    grid-template-areas:
      'sidebar split-sidebar list split-list main'
      'panel-split panel-split panel-split split-list main'
      'panel panel panel split-list main';
    background-color: var(--bg-primary);
    color: var(--text-primary);
  }

  .panel-left .shell {
    display: contents;
  }

  .panel-left .sidebar {
    grid-area: sidebar;
    width: auto !important;
    min-width: 0;
  }

  .panel-left .sidebar + :global(.splitter) {
    grid-area: split-sidebar;
  }

  .panel-left .session-list-container {
    grid-area: list;
    width: auto !important;
    min-width: 0;
  }

  .panel-left .session-list-container + :global(.splitter) {
    grid-area: split-list;
  }

  .panel-left .transcript-container {
    grid-area: main;
    min-width: 0;
  }

  .panel-left .panel-splitter {
    grid-area: panel-split;
  }

  .panel-left > :global(.terminal-panel) {
    grid-area: panel;
    min-width: 0;
  }

  .shell {
    position: relative;
    display: flex;
    flex-direction: row;
    flex: 1 1 0;
    min-height: 300px;
    width: 100%;
    overflow: hidden;
    background-color: var(--bg-primary);
    color: var(--text-primary);
  }

  .pane {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
    background-color: var(--bg-primary);
  }

  .pane-header {
    height: 48px;
    min-height: 48px;
    padding: 0 12px;
    display: flex;
    align-items: center;
    background-color: var(--bg-secondary);
  }

  /* Sidebar */
  .sidebar {
    background-color: var(--bg-secondary);
  }

  .sidebar-header {
    justify-content: space-between;
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .app-icon {
    font-size: 1.1rem;
  }

  .app-title {
    font-size: 0.95rem;
    font-weight: 600;
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  /* Fixed square so SVG icons of any shape share one center line. */
  .icon-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    padding: 0;
    border-radius: 4px;
    color: var(--text-secondary);
  }

  .icon-button:hover {
    color: var(--text-primary);
    background-color: var(--hover-bg);
  }

  .terminal-button.active {
    color: var(--accent-color);
  }

  .terminal-button {
    position: relative;
  }

  .terminal-attention {
    position: absolute;
    top: 4px;
    right: 3px;
    display: flex;
  }

  .sidebar-mode-section {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border-color);
  }

  .sidebar-content {
    flex: 1;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }

  .sidebar-footer {
    height: 32px;
    border-top: 1px solid var(--border-color);
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0 6px 0 10px;
    font-size: 0.72rem;
  }

  .sidebar-footer.remote {
    background: var(--remote-footer-bg);
  }

  .footer-status {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .forwarding-button { position: relative; }
  .forwarding-button:disabled { opacity: 0.4; cursor: default; }
  .forward-count, .forward-error { position: absolute; right: -3px; top: -4px; min-width: 12px; height: 12px; border-radius: 8px; font-size: 9px; line-height: 12px; background: var(--accent-color); color: white; }
  .forward-error { background: var(--danger, #ef4444); left: -3px; right: auto; }

  .footer-actions {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  .refresh-button:disabled,
  .import-button:disabled {
    cursor: default;
    opacity: 0.5;
  }

  .refresh-button.refreshing svg {
    animation: spin 1s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .runtime-badge {
    padding: 2px 6px;
    border-radius: 4px;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
  }

  .runtime-badge.wails {
    background: rgba(16, 185, 129, 0.15);
    color: #10b981;
    font-weight: 500;
  }

  .stale {
    opacity: 0.55;
    filter: grayscale(0.6);
  }

  .stale-overlay {
    position: absolute;
    top: 10px;
    left: 50%;
    transform: translateX(-50%);
    max-width: 70%;
    padding: 6px 12px;
    border-radius: 6px;
    border: 1px solid var(--border-color);
    background: var(--bg-tertiary);
    color: var(--text-primary);
    font-size: 0.78rem;
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.25);
    z-index: 50;
    pointer-events: none;
  }

  .search-toggle:disabled {
    cursor: default;
    opacity: 0.5;
  }

  /* Session List */
  .session-list-container {
    background-color: var(--bg-primary);
  }

  /* Transcript View */
  .transcript-container {
    flex: 1;
    min-width: 380px;
    background-color: var(--bg-primary);
    overflow: hidden;
  }
</style>
