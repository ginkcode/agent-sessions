<script lang="ts">
  import type { SessionMeta, SortOpts } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import { onDestroy } from 'svelte';
  import VirtualList from './VirtualList.svelte';
  import SessionRow from './SessionRow.svelte';

  let listEl: HTMLElement | null = $state(null);

  let searchTimer: ReturnType<typeof setTimeout> | undefined;

  // Debounce so fast typing doesn't trigger a backend query per keystroke.
  function handleSearchInput(e: Event) {
    const val = (e.target as HTMLInputElement).value;
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      appState.setFilter({ query: val });
    }, 250);
  }

  onDestroy(() => {
    clearTimeout(searchTimer);
  });

  function toggleLiveOnly() {
    appState.setFilter({ liveOnly: !appState.filter.liveOnly });
  }

  function toggleArchived() {
    appState.setFilter({ archived: !appState.filter.archived });
  }

  function handleSortChange(e: Event) {
    const field = (e.target as HTMLSelectElement).value;
    appState.setSort({ field, desc: appState.sort.desc });
  }

  function toggleSortDirection() {
    appState.setSort({ field: appState.sort.field, desc: !appState.sort.desc });
  }

  function handleSelectSession(session: SessionMeta) {
    appState.selectSession(session.ref);
  }

  function handleKeyDown(e: KeyboardEvent) {
    if (appState.sessions.length === 0) return;

    const currentIndex = appState.sessions.findIndex(
      (s) =>
        s.ref.agent === appState.selectedSessionRef?.agent &&
        s.ref.id === appState.selectedSessionRef?.id
    );

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      const nextIndex = Math.min(appState.sessions.length - 1, currentIndex + 1);
      appState.selectSession(appState.sessions[nextIndex].ref);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      const prevIndex = Math.max(0, currentIndex - 1);
      appState.selectSession(appState.sessions[prevIndex].ref);
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  bind:this={listEl}
  class="session-list-pane"
  role="region"
  aria-label="Session list"
  onkeydown={handleKeyDown}
  tabindex="-1"
>
  <div class="search-toolbar">
    <div class="search-input-wrapper">
      <span class="search-icon">🔍</span>
      <input
        type="search"
        placeholder="Filter sessions (title, prompt, cwd, model)…"
        value={appState.filter.query || ''}
        oninput={handleSearchInput}
        aria-label="Search sessions"
      />
    </div>

    <div class="filter-controls">
      <button
        type="button"
        class="filter-btn live-btn"
        class:active={appState.filter.liveOnly}
        title="Show only active sessions"
        onclick={toggleLiveOnly}
      >
        <span class="live-indicator">●</span> Live
      </button>

      <button
        type="button"
        class="filter-btn"
        class:active={appState.filter.archived}
        title="Include archived sessions"
        onclick={toggleArchived}
      >
        Archived
      </button>

      <div class="sort-wrapper">
        <select
          class="sort-select"
          value={appState.sort.field}
          onchange={handleSortChange}
          aria-label="Sort sessions by"
        >
          <option value="updated">Updated</option>
          <option value="created">Created</option>
          <option value="title">Title</option>
          <option value="messages">Messages</option>
          <option value="tokens">Tokens</option>
          <option value="cost">Cost</option>
        </select>
        <button
          type="button"
          class="sort-dir-btn"
          title={appState.sort.desc ? 'Descending (click for ascending)' : 'Ascending (click for descending)'}
          onclick={toggleSortDirection}
          aria-label="Toggle sort order"
        >
          {appState.sort.desc ? '↓' : '↑'}
        </button>
      </div>
    </div>
  </div>

  <div class="list-subhead">
    <span class="session-count">
      {appState.sessions.length} {appState.sessions.length === 1 ? 'session' : 'sessions'}
    </span>
  </div>

  <div class="list-body">
    {#if appState.loadingSessions}
      <div class="status-msg">Loading sessions…</div>
    {:else if appState.sessions.length === 0}
      <div class="status-msg empty">No matching sessions found</div>
    {:else}
      <VirtualList items={appState.sessions} itemHeight={58} overscan={5}>
        {#snippet children(session: SessionMeta)}
          <SessionRow
            {session}
            isSelected={appState.selectedSessionRef?.agent === session.ref.agent &&
              appState.selectedSessionRef?.id === session.ref.id}
            onSelect={handleSelectSession}
          />
        {/snippet}
      </VirtualList>
    {/if}
  </div>
</div>

<style>
  .session-list-pane {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    background-color: var(--bg-primary);
    overflow: hidden;
  }

  .search-toolbar {
    padding: 8px 10px;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-primary);
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .search-input-wrapper {
    display: flex;
    align-items: center;
    gap: 6px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 4px 8px;
  }

  .search-icon {
    font-size: 0.8rem;
    opacity: 0.5;
  }

  .search-input-wrapper input {
    border: none;
    outline: none;
    width: 100%;
    font-size: 0.8rem;
    color: var(--text-primary);
  }

  .filter-controls {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .filter-btn {
    padding: 2px 7px;
    border-radius: 4px;
    font-size: 0.72rem;
    font-weight: 500;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    transition: all 0.15s ease;
  }

  .filter-btn:hover {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .filter-btn.active {
    background-color: var(--active-bg);
    color: var(--accent-color);
    border-color: var(--accent-color);
    font-weight: 600;
  }

  .live-btn .live-indicator {
    color: var(--status-live);
    font-size: 0.75rem;
  }

  .sort-wrapper {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 2px;
  }

  .sort-select {
    padding: 2px 4px;
    font-size: 0.72rem;
    border-radius: 4px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    outline: none;
  }

  .sort-dir-btn {
    padding: 2px 5px;
    font-size: 0.75rem;
    font-weight: bold;
    border-radius: 4px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
  }

  .sort-dir-btn:hover {
    color: var(--text-primary);
  }

  .list-subhead {
    padding: 4px 10px;
    background-color: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
    font-size: 0.7rem;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .list-body {
    flex: 1;
    overflow: hidden;
    position: relative;
  }

  .status-msg {
    padding: 30px;
    text-align: center;
    color: var(--text-muted);
    font-size: 0.825rem;
  }
</style>
