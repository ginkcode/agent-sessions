<script lang="ts">
  import type { SessionMeta, SortOpts } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import { onDestroy } from 'svelte';
  import VirtualList from './VirtualList.svelte';
  import SessionListSkeleton from './SessionListSkeleton.svelte';
  import SessionRow from './SessionRow.svelte';
  import EmptyState from '../common/EmptyState.svelte';
  import ErrorState from '../common/ErrorState.svelte';
  import { manage } from '../../stores/manage.svelte';
  import { filterSessionsByAge } from '../../manage';

  let listEl: HTMLElement | null = $state(null);

  let searchTimer: ReturnType<typeof setTimeout> | undefined;

  let visibleSessions = $derived(
    filterSessionsByAge(appState.sessions, manage.ageFilterDays)
  );

  let selectedVisibleCount = $derived(
    manage.getSelectedRefs(visibleSessions).length
  );

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

  function handleClearFilters() {
    clearTimeout(searchTimer);
    appState.setFilter({ query: '', liveOnly: false, archived: false });
    manage.setAgeFilter(0);
  }

  function handleAgeChange(e: Event) {
    const days = Number((e.target as HTMLSelectElement).value);
    manage.setAgeFilter(Number.isFinite(days) ? days : 0);
    manage.clearSelection();
  }

  function toggleSelectAll() {
    if (selectedVisibleCount === visibleSessions.length) {
      manage.clearSelection();
    } else {
      manage.selectAll(visibleSessions);
    }
  }

  function handleBulkDelete() {
    const refs = manage.getSelectedRefs(visibleSessions);
    if (!refs.length) return;
    void manage.requestDelete(refs);
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

      {#if manage.settings.enabled}
        <select
          class="age-select"
          value={String(manage.ageFilterDays)}
          onchange={handleAgeChange}
          aria-label="Filter sessions by age"
        >
          <option value="0">Any age</option>
          <option value="30">Older than 30d</option>
          <option value="90">Older than 90d</option>
          <option value="180">Older than 180d</option>
          <option value="365">Older than 1y</option>
        </select>
      {/if}

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
      {visibleSessions.length} {visibleSessions.length === 1 ? 'session' : 'sessions'}
    </span>

    {#if manage.settings.enabled}
      <label class="select-all-label">
        <input
          type="checkbox"
          checked={visibleSessions.length > 0 &&
            selectedVisibleCount === visibleSessions.length}
          onclick={toggleSelectAll}
        />
        Select all
      </label>

      <button
        type="button"
        class="bulk-delete-btn"
        disabled={selectedVisibleCount === 0 || manage.deleting}
        title="Delete the selected sessions"
        onclick={handleBulkDelete}
      >
        Delete ({selectedVisibleCount})
      </button>
    {/if}
  </div>

  <div class="list-body">
    {#if appState.loadingSessions}
      <SessionListSkeleton />
    {:else if appState.error}
      <ErrorState
        title="Could not load sessions"
        message={appState.error}
        onRetry={() => appState.loadSessions()}
      />
    {:else if visibleSessions.length === 0}
      <EmptyState
        icon="🔍"
        title="No matching sessions"
        description="No sessions match the current search and filters."
        actionText="Clear filters"
        onAction={handleClearFilters}
      />
    {:else}
      <VirtualList items={visibleSessions} itemHeight={58} overscan={5}>
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

  .age-select {
    max-width: 120px;
    padding: 2px 4px;
    font-size: 0.72rem;
    border-radius: 4px;
    background: var(--bg-secondary);
    color: var(--text-secondary);
    border: 1px solid var(--border-color);
    cursor: pointer;
  }

  .age-select option {
    background-color: var(--bg-secondary);
    color: var(--text-primary);
  }

  /* appearance: none stops WebKitGTK painting the GTK (light) menulist, so
     the theme colors apply; the caret is drawn with two gradients. */
  .sort-select {
    appearance: none;
    -webkit-appearance: none;
    padding: 2px 18px 2px 6px;
    font-size: 0.72rem;
    border-radius: 4px;
    background-color: var(--bg-secondary);
    background-image:
      linear-gradient(45deg, transparent 50%, var(--text-muted) 50%),
      linear-gradient(135deg, var(--text-muted) 50%, transparent 50%);
    background-position:
      calc(100% - 10px) 55%,
      calc(100% - 6px) 55%;
    background-size: 4px 4px;
    background-repeat: no-repeat;
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    outline: none;
    cursor: pointer;
  }

  .sort-select:hover,
  .sort-select:focus-visible {
    color: var(--text-primary);
    border-color: var(--text-muted);
  }

  .sort-select option {
    background-color: var(--bg-secondary);
    color: var(--text-primary);
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
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .select-all-label {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 0.7rem;
    cursor: pointer;
  }

  .select-all-label input {
    margin: 0;
    accent-color: #ef4444;
  }

  .bulk-delete-btn {
    margin-left: auto;
    padding: 2px 8px;
    border-radius: 4px;
    font-size: 0.7rem;
    font-weight: 500;
    background-color: rgba(239, 68, 68, 0.12);
    color: #ef4444;
    border: 1px solid rgba(239, 68, 68, 0.4);
    cursor: pointer;
  }

  .bulk-delete-btn:hover:not(:disabled) {
    background-color: rgba(239, 68, 68, 0.2);
    border-color: #ef4444;
  }

  .bulk-delete-btn:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }

  .list-body {
    flex: 1;
    overflow: hidden;
    position: relative;
  }
</style>
