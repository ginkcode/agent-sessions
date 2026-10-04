<script lang="ts">
  import { onMount } from 'svelte';
  import Splitter from './lib/components/common/Splitter.svelte';
  import ModeSelector from './lib/components/sidebar/ModeSelector.svelte';
  import PathFilter from './lib/components/sidebar/PathFilter.svelte';
  import GroupTree from './lib/components/sidebar/GroupTree.svelte';
  import AgentSummary from './lib/components/sidebar/AgentSummary.svelte';
  import SessionList from './lib/components/sessionlist/SessionList.svelte';
  import TranscriptView from './lib/components/transcript/TranscriptView.svelte';
  import ManageSettingsDialog from './lib/components/common/ManageSettingsDialog.svelte';
  import GlobalSearchDialog from './lib/components/common/GlobalSearchDialog.svelte';
  import HandoffDialog from './lib/components/common/HandoffDialog.svelte';
  import ExportDialog from './lib/components/common/ExportDialog.svelte';
  import ImportDialog from './lib/components/common/ImportDialog.svelte';
  import AskpassDialog from './lib/components/common/AskpassDialog.svelte';
  import ReconnectBanner from './lib/components/common/ReconnectBanner.svelte';
  import HostSelector from './lib/components/sidebar/HostSelector.svelte';
  import Toaster from './lib/components/common/Toaster.svelte';
  import { theme } from './lib/stores/theme.svelte';
  import { preferences } from './lib/stores/preferences.svelte';
  import { appState } from './lib/stores/appState.svelte';
  import { manage } from './lib/stores/manage.svelte';
  import { search } from './lib/stores/search.svelte';
  import { importer } from './lib/stores/importer.svelte';
  import { connectionStore } from './lib/stores/connection.svelte';
  import { link } from './lib/stores/link.svelte';
  import { launcher } from './lib/stores/launcher.svelte';
  import { hasOpenModal, isEditableTarget } from './lib/search';
  import { api } from './lib/api';
  import { formatVersion } from './lib/format';
  import { themeButtonTitle } from './lib/theme';

  let isWails = $state(false);
  let appVersion = $state('');
  // The panes show a host's last data while it is not serving, or stay
  // inert during a first connect so nothing on screen passes for the
  // host's sessions.
  let stale = $derived(link.stale || link.locked);

  onMount(() => {
    theme.init();
    preferences.init();
    connectionStore.init();
    appState.init();
    manage.init();
    search.init();
    void launcher.init();
    isWails = typeof window !== 'undefined' && Boolean((window as any).go?.app?.App);
    api
      .appVersion()
      .then((v) => (appVersion = formatVersion(v)))
      .catch(() => {});

    const onKeydown = (event: KeyboardEvent) => {
      if (event.key !== '/' || event.metaKey || event.ctrlKey || event.altKey) return;
      if (isEditableTarget(event.target) || hasOpenModal() || link.stale) return;
      event.preventDefault();
      search.show();
    };
    window.addEventListener('keydown', onKeydown);
    return () => {
      window.removeEventListener('keydown', onKeydown);
      search.destroy();
      appState.destroy();
      connectionStore.destroy();
    };
  });

  function handleSidebarResize(delta: number) {
    preferences.setSidebarWidth(preferences.widths.sidebar + delta);
  }

  function handleSessionListResize(delta: number) {
    preferences.setSessionListWidth(preferences.widths.sessionList + delta);
  }
</script>

<div class="app-layout">
  <ReconnectBanner />
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
        <button
          type="button"
          class="icon-button settings-toggle"
          title="Session management settings"
          aria-label="Session management settings"
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
        <button
          type="button"
          class="icon-button import-button"
          title="Import session bundle"
          aria-label="Import session bundle"
          disabled={importer.opening || stale}
          onclick={() => void importer.open()}
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
        Connecting to <strong>{link.host}</strong>… Changes are disabled until the connection is established or you switch back to Local.
      {:else}
        Showing data last loaded from <strong>{link.dataHost}</strong>. Changes are disabled until it reconnects.
      {/if}
    </div>
  {/if}

  <ManageSettingsDialog />
  <GlobalSearchDialog />
  <HandoffDialog />
  <ExportDialog />
  <ImportDialog />
  <AskpassDialog />
  <Toaster />
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

  .shell {
    position: relative;
    display: flex;
    flex-direction: row;
    flex: 1;
    min-height: 0;
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
