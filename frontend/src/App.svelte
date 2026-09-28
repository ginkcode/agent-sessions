<script lang="ts">
  import { onMount } from 'svelte';
  import Splitter from './lib/components/common/Splitter.svelte';
  import ModeSelector from './lib/components/sidebar/ModeSelector.svelte';
  import GroupTree from './lib/components/sidebar/GroupTree.svelte';
  import AgentSummary from './lib/components/sidebar/AgentSummary.svelte';
  import SessionList from './lib/components/sessionlist/SessionList.svelte';
  import TranscriptView from './lib/components/transcript/TranscriptView.svelte';
  import { theme } from './lib/stores/theme.svelte';
  import { preferences } from './lib/stores/preferences.svelte';
  import { appState } from './lib/stores/appState.svelte';

  let isWails = $state(false);

  onMount(() => {
    theme.init();
    preferences.init();
    appState.init();
    isWails = typeof window !== 'undefined' && Boolean((window as any).go?.app?.App);
  });

  function handleSidebarResize(delta: number) {
    preferences.setSidebarWidth(preferences.widths.sidebar + delta);
  }

  function handleSessionListResize(delta: number) {
    preferences.setSessionListWidth(preferences.widths.sessionList + delta);
  }
</script>

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
      <button
        type="button"
        class="icon-button theme-toggle"
        title="Toggle Theme ({theme.mode} mode, resolved: {theme.resolved})"
        onclick={() => theme.toggle()}
        aria-label="Toggle theme"
      >
        {#if theme.resolved === 'dark'}
          <span>🌙</span>
        {:else}
          <span>☀️</span>
        {/if}
      </button>
    </header>

    <div class="sidebar-mode-section">
      <ModeSelector />
    </div>

    <div class="sidebar-content">
      <GroupTree />
    </div>

    <AgentSummary />

    <footer class="sidebar-footer">
      <div class="footer-status">
        <span class="runtime-badge" class:wails={isWails}>
          {isWails ? 'Desktop' : 'Mock (Browser)'}
        </span>
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
    style="width: {preferences.widths.sessionList}px"
    aria-label="Session list"
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
  <main class="pane transcript-container" aria-label="Transcript details">
    <TranscriptView />
  </main>
</div>

<style>
  .shell {
    display: flex;
    flex-direction: row;
    width: 100vw;
    height: 100vh;
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
    border-bottom: 1px solid var(--border-color);
    padding: 0 12px;
    display: flex;
    align-items: center;
    background-color: var(--bg-secondary);
  }

  /* Sidebar */
  .sidebar {
    background-color: var(--bg-secondary);
    border-right: 1px solid var(--border-color);
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

  .icon-button {
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 0.9rem;
  }

  .icon-button:hover {
    background-color: var(--hover-bg);
  }

  .sidebar-mode-section {
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
    padding: 0 10px;
    font-size: 0.72rem;
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

  /* Session List */
  .session-list-container {
    background-color: var(--bg-primary);
    border-right: 1px solid var(--border-color);
  }

  /* Transcript View */
  .transcript-container {
    flex: 1;
    min-width: 380px;
    background-color: var(--bg-primary);
    overflow: hidden;
  }
</style>
