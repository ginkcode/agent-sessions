<script lang="ts">
  import { tick, untrack } from 'svelte';
  import type { SearchHit } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import { search } from '../../stores/search.svelte';
  import { progressIncomplete, safeSnippetHTML, searchFilterFromApp } from '../../search';
  import AgentIcon from './AgentIcon.svelte';
  import LoadingSpinner from './LoadingSpinner.svelte';

  let inputEl: HTMLInputElement | null = $state(null);
  let resultsEl: HTMLElement | null = $state(null);
  let restoreFocus: HTMLElement | null = null;
  let wasOpen = false;

  $effect(() => {
    // Search follows the same agent/path scope as the catalog. The filter is
    // passed separately so snippets and ranking remain backend-owned.
    const filter = searchFilterFromApp(appState.filter.agent, appState.filter.path);
    // setFilter reads the query; only filter changes should re-run this.
    untrack(() => search.setFilter(filter));
  });

  $effect(() => {
    if (search.open && !wasOpen) {
      restoreFocus = document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
      void tick().then(() => inputEl?.focus());
    } else if (!search.open && wasOpen) {
      void tick().then(() => restoreFocus?.focus());
    }
    wasOpen = search.open;
  });

  $effect(() => {
    const index = search.activeIndex;
    if (index < 0) return;
    void tick().then(() => {
      resultsEl
        ?.querySelector<HTMLElement>(`[data-result-index="${index}"]`)
        ?.scrollIntoView({ block: 'nearest' });
    });
  });

  function resultID(index: number): string {
    return `global-search-result-${index}`;
  }

  function sessionTitle(hit: SearchHit): string {
    return appState.sessions.find(
      (session) => session.ref.agent === hit.ref.agent && session.ref.id === hit.ref.id
    )?.title || hit.ref.id;
  }

  function hitLabel(hit: SearchHit): string {
    if (hit.kind === 'title') return 'Session title';
    const kind = hit.kind === 'text' ? 'Message' : hit.kind[0].toUpperCase() + hit.kind.slice(1);
    return `${kind} ${hit.messageIndex + 1}`;
  }

  function choose(hit: SearchHit): void {
    // selectSession changes the ref synchronously (before its first await), so
    // the transcript sees the ref and the jump token in the same effect flush
    // and loads only the page around the hit's global index.
    const ready = appState.selectSession(hit.ref);
    search.requestJump(hit, ready);
    search.close();
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      event.preventDefault();
      search.close();
      return;
    }
    if (event.key === 'ArrowDown') {
      event.preventDefault();
      search.moveActive(1);
      return;
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      search.moveActive(-1);
      return;
    }
    if (event.key === 'Enter') {
      const hit = search.activeHit();
      if (hit) {
        event.preventDefault();
        choose(hit);
      }
    }
  }
</script>

{#if search.open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="search-backdrop"
    role="presentation"
    onkeydown={handleKeydown}
    onclick={(event) => {
      if (event.target === event.currentTarget) search.close();
    }}
  >
    <div
      class="search-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="global-search-title"
    >
      <header class="search-header">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="8" />
          <path d="m21 21-4.35-4.35" />
        </svg>
        <h2 id="global-search-title" class="sr-only">Search all sessions</h2>
        <input
          bind:this={inputEl}
          class="search-input"
          type="search"
          placeholder="Search every transcript…"
          value={search.query}
          oninput={(event) => search.setQuery(event.currentTarget.value)}
          aria-label="Search every transcript"
          aria-controls="global-search-results"
          aria-activedescendant={search.activeIndex >= 0 ? resultID(search.activeIndex) : undefined}
          autocomplete="off"
          spellcheck="false"
        />
        {#if search.loading}
          <LoadingSpinner size={16} label="Searching…" />
        {:else}
          <kbd>/</kbd>
        {/if}
        <button type="button" class="close-button" aria-label="Close search" title="Close search" onclick={() => search.close()}>×</button>
      </header>

      {#if appState.filter.agent || appState.filter.path}
        <div class="active-scope">
          Searching within
          {#if appState.filter.agent}<strong>{appState.filter.agent}</strong>{/if}
          {#if appState.filter.agent && appState.filter.path}<span>·</span>{/if}
          {#if appState.filter.path}<strong title={appState.filter.path}>{appState.filter.path}</strong>{/if}
        </div>
      {/if}

      {#if progressIncomplete(search.progress)}
        <div class="index-progress" role="status">
          <span class="progress-dot"></span>
          Search index is updating: {search.progress?.done ?? 0} ready,
          {search.progress?.pending ?? 0} pending.
        </div>
      {:else if search.progress && search.progress.failed > 0}
        <div class="index-warning" role="status">
          {search.progress.failed} {search.progress.failed === 1 ? 'session' : 'sessions'} could not be indexed.
        </div>
      {/if}

      <div
        id="global-search-results"
        class="search-results"
        bind:this={resultsEl}
        role="listbox"
        aria-label="Search results"
      >
        {#if search.error}
          <div class="result-state error-state" role="alert">
            <strong>Search failed</strong>
            <span>{search.error}</span>
            <button type="button" onclick={() => void search.runNow()}>Try again</button>
          </div>
        {:else if !search.query.trim()}
          <div class="result-state">
            <strong>Find text across all sessions</strong>
            <span>Use <code>agent:</code>, <code>dir:</code>, or quoted phrases to narrow results.</span>
          </div>
        {:else if !search.loading && search.results.length === 0}
          <div class="result-state">
            <strong>No matches</strong>
            <span>Try a shorter term or a different filter.</span>
          </div>
        {:else}
          {#each search.results as hit, index (`${hit.ref.agent}:${hit.ref.id}:${hit.messageIndex}:${hit.kind}`)}
            <button
              id={resultID(index)}
              type="button"
              class="search-result"
              class:active={search.activeIndex === index}
              data-result-index={index}
              role="option"
              aria-selected={search.activeIndex === index}
              onmouseenter={() => (search.activeIndex = index)}
              onclick={() => choose(hit)}
            >
              <span class="result-icon"><AgentIcon agent={hit.ref.agent} size={16} /></span>
              <span class="result-main">
                <span class="result-heading">
                  <strong title={sessionTitle(hit)}>{sessionTitle(hit)}</strong>
                  <span>{hitLabel(hit)}</span>
                </span>
                <span class="result-snippet">{@html safeSnippetHTML(hit.snippet)}</span>
              </span>
            </button>
          {/each}
        {/if}
      </div>

      <footer class="search-footer">
        <span><kbd>↑</kbd><kbd>↓</kbd> navigate</span>
        <span><kbd>↵</kbd> open</span>
        <span><kbd>esc</kbd> close</span>
      </footer>
    </div>
  </div>
{/if}

<style>
  .search-backdrop {
    position: fixed;
    inset: 0;
    z-index: 1200;
    display: flex;
    justify-content: center;
    align-items: flex-start;
    padding-top: min(14vh, 110px);
    background: rgba(8, 12, 20, 0.56);
    backdrop-filter: blur(2px);
  }

  .search-dialog {
    width: min(720px, calc(100vw - 32px));
    max-height: min(680px, calc(100vh - 140px));
    display: flex;
    flex-direction: column;
    overflow: hidden;
    border: 1px solid var(--border-color);
    border-radius: 10px;
    background: var(--bg-primary);
    box-shadow: 0 18px 60px rgba(0, 0, 0, 0.35);
  }

  .search-header {
    min-height: 54px;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 12px 0 16px;
    border-bottom: 1px solid var(--border-color);
    color: var(--text-muted);
  }

  .search-input {
    min-width: 0;
    flex: 1;
    padding: 14px 0;
    border: 0;
    outline: 0;
    background: transparent;
    color: var(--text-primary);
    font: inherit;
    font-size: 1rem;
  }

  .search-input::placeholder { color: var(--text-muted); }
  .search-input::-webkit-search-cancel-button { display: none; }

  kbd {
    min-width: 20px;
    padding: 2px 5px;
    border: 1px solid var(--border-color);
    border-bottom-width: 2px;
    border-radius: 4px;
    background: var(--bg-tertiary);
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 0.68rem;
    text-align: center;
  }

  .close-button {
    width: 28px;
    height: 28px;
    border-radius: 5px;
    color: var(--text-muted);
    font-size: 1.25rem;
    line-height: 1;
  }

  .close-button:hover { background: var(--hover-bg); color: var(--text-primary); }

  .active-scope,
  .index-progress,
  .index-warning {
    display: flex;
    align-items: center;
    gap: 5px;
    min-height: 30px;
    padding: 5px 16px;
    border-bottom: 1px solid var(--border-color);
    background: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 0.72rem;
  }

  .active-scope strong {
    max-width: 58%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-secondary);
  }

  .index-progress { color: var(--text-secondary); }
  .index-warning { color: #f59e0b; }

  .progress-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent-color);
    animation: pulse 1.2s ease-in-out infinite;
  }

  @keyframes pulse { 50% { opacity: 0.35; } }

  .search-results {
    min-height: 180px;
    overflow-y: auto;
    padding: 6px;
  }

  .search-result {
    width: 100%;
    display: flex;
    gap: 10px;
    padding: 10px;
    border: 1px solid transparent;
    border-radius: 7px;
    color: inherit;
    text-align: left;
  }

  .search-result:hover,
  .search-result.active {
    border-color: var(--border-color);
    background: var(--hover-bg);
  }

  .search-result.active { border-color: color-mix(in srgb, var(--accent-color) 55%, var(--border-color)); }

  .result-icon { flex: none; padding-top: 1px; }
  .result-main { min-width: 0; flex: 1; display: flex; flex-direction: column; gap: 5px; }
  .result-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .result-heading strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 0.8rem; }
  .result-heading > span { flex: none; color: var(--text-muted); font-size: 0.68rem; }
  .result-snippet { color: var(--text-secondary); font-size: 0.8rem; line-height: 1.45; overflow-wrap: anywhere; }
  .result-snippet :global(mark) { padding: 0 1px; border-radius: 2px; background: rgba(245, 158, 11, 0.34); color: var(--text-primary); }

  .result-state {
    min-height: 170px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 7px;
    padding: 24px;
    color: var(--text-muted);
    font-size: 0.8rem;
    text-align: center;
  }

  .result-state strong { color: var(--text-secondary); font-size: 0.9rem; }
  .result-state code { font-family: var(--font-mono); }
  .result-state button { margin-top: 4px; padding: 5px 10px; border-radius: 5px; background: var(--accent-color); color: white; }
  .error-state strong, .error-state span { color: #ef4444; }

  .search-footer {
    min-height: 34px;
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: 14px;
    padding: 0 12px;
    border-top: 1px solid var(--border-color);
    background: var(--bg-secondary);
    color: var(--text-muted);
    font-size: 0.68rem;
  }

  .search-footer span { display: flex; align-items: center; gap: 3px; }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  @media (max-width: 600px) {
    .search-backdrop { padding-top: 12px; }
    .search-dialog { max-height: calc(100vh - 24px); }
    .search-footer { display: none; }
  }
</style>
