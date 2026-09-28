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
  import Dropdown from '../common/Dropdown.svelte';

  const AGE_OPTIONS = [
    { value: '0', label: 'Any age' },
    { value: '30', label: 'Older than 30d' },
    { value: '90', label: 'Older than 90d' },
    { value: '180', label: 'Older than 180d' },
    { value: '365', label: 'Older than 1y' },
  ];

  const SORT_OPTIONS = [
    { value: 'updated', label: 'Updated' },
    { value: 'created', label: 'Created' },
    { value: 'title', label: 'Title' },
    { value: 'messages', label: 'Messages' },
    { value: 'tokens', label: 'Tokens' },
    { value: 'cost', label: 'Cost' },
  ];

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

  function handleSortChange(field: string) {
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

  function handleAgeChange(value: string) {
    const days = Number(value);
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
        <Dropdown
          options={AGE_OPTIONS}
          value={String(manage.ageFilterDays)}
          onChange={handleAgeChange}
          ariaLabel="Filter sessions by age"
        />
      {/if}

      <div class="sort-wrapper">
        <Dropdown
          options={SORT_OPTIONS}
          value={appState.sort.field}
          onChange={handleSortChange}
          ariaLabel="Sort sessions by"
          align="right"
        />
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

  /* Every control in the row shares one height (also read by Dropdown). */
  .filter-controls {
    --control-height: 24px;
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .filter-btn {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    height: var(--control-height);
    padding: 0 7px;
    white-space: nowrap;
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
    gap: 4px;
  }

  .sort-dir-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: var(--control-height);
    height: var(--control-height);
    padding: 0;
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
