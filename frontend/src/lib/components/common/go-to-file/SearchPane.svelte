<script lang="ts">
  import { tick } from 'svelte';
  import { contentSearch } from '../../../stores/contentSearch.svelte';
  import { connectionStore } from '../../../stores/connection.svelte';
  import { matchKey, searchSummary, type MatchRef } from '../../../contentSearch';
  import type { SearchFile, SearchMatch } from '../../../types';
  import LoadingSpinner from '../LoadingSpinner.svelte';
  import FileIcon from '../FileIcon.svelte';

  interface Props {
    /** Opens the file at a match; keep keeps it in a tab rather than the preview. */
    onOpen: (path: string, match: SearchMatch, keep?: boolean) => void;
  }

  let { onOpen }: Props = $props();

  let field: HTMLInputElement | undefined = $state();
  let resultsEl: HTMLElement | undefined = $state();

  let supported = $derived(connectionStore.capabilities.searchFiles === true);
  let o = $derived(contentSearch.options);
  let overlaid = $derived(contentSearch.overlaid);
  let result = $derived(overlaid.result);
  let active = $derived(contentSearch.active);

  // Focus the field when the pane mounts and whenever it is asked to.
  $effect(() => {
    void contentSearch.focusRequest;
    void tick().then(() => { field?.focus(); field?.select(); });
  });

  function rowId(ref: MatchRef): string {
    return `search-match-${matchKey(ref).replace(/[^\w-]/g, c => `_${c.charCodeAt(0).toString(16)}`)}`;
  }

  function isActive(path: string, index: number): boolean {
    return active?.path === path && active.index === index;
  }

  function reveal(ref: MatchRef | null) {
    if (!ref) return;
    void tick().then(() => resultsEl?.querySelector(`#${rowId(ref)}`)?.scrollIntoView({ block: 'nearest' }));
  }

  function open(ref: MatchRef | null, keep = false) {
    const m = contentSearch.match(ref);
    if (!ref || !m) return;
    contentSearch.active = ref;
    onOpen(ref.path, m, keep);
  }

  function handleFieldKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      reveal(contentSearch.move(e.key === 'ArrowDown' ? 1 : -1));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      // Enter goes to the match the keyboard is on, else the first one; a
      // search still waiting on the typing pause starts now.
      if (!contentSearch.upToDate) {
        contentSearch.searchNow();
        return;
      }
      const ref = active ?? contentSearch.order[0] ?? null;
      open(ref, true);
      reveal(ref);
    }
  }

  function splitPath(path: string): { name: string; dir: string } {
    const at = path.lastIndexOf('/');
    return { name: path.slice(at + 1), dir: at >= 0 ? path.slice(0, at) : '' };
  }

  function fileTitle(file: SearchFile): string {
    return [
      file.path,
      file.draft ? 'Searched in the unsaved draft' : '',
      file.truncated ? 'More matches in this file were not listed' : '',
    ].filter(Boolean).join(' · ');
  }
</script>

{#snippet toggle(option: 'matchCase' | 'wholeWord' | 'regex', label: string, text: string)}
  <button
    type="button"
    class="field-toggle"
    class:on={o[option]}
    aria-pressed={o[option]}
    aria-label={label}
    title={label}
    onclick={() => contentSearch.toggle(option)}
  >{text}</button>
{/snippet}

<div class="search-pane">
  {#if !supported}
    <div class="state" role="note">
      This host's agent-sessions server cannot search file contents. Update agent-sessions on the host, then reconnect.
    </div>
  {:else}
    <div class="search-field">
      <input
        bind:this={field}
        class="field-input"
        type="search"
        placeholder="Search"
        value={o.text}
        oninput={(e) => contentSearch.set({ text: e.currentTarget.value })}
        onkeydown={handleFieldKey}
        aria-label="Search file contents"
        aria-controls="search-results"
        aria-activedescendant={active ? rowId(active) : undefined}
        autocomplete="off"
        autocorrect="off"
        autocapitalize="off"
        spellcheck="false"
      />
      {@render toggle('matchCase', 'Match case', 'Aa')}
      {@render toggle('wholeWord', 'Match whole word', 'ab')}
      {@render toggle('regex', 'Use regular expression', '.*')}
    </div>
    <button
      type="button"
      class="filters-btn"
      aria-expanded={o.filtersOpen}
      aria-controls="search-filters"
      onclick={() => contentSearch.toggle('filtersOpen')}
    >
      <span class="chevron" class:open={o.filtersOpen}>▸</span>
      Files to include and exclude
      {#if !o.filtersOpen && (o.include || o.exclude)}<span class="filters-on" title="Filters apply">●</span>{/if}
    </button>
    {#if o.filtersOpen}
      <div id="search-filters" class="filters">
        <label>
          <span>files to include</span>
          <input class="field-input" type="text" placeholder="e.g. *.ts, src/**" value={o.include}
            oninput={(e) => contentSearch.set({ include: e.currentTarget.value })}
            onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); contentSearch.searchNow(); } }}
            autocomplete="off" spellcheck="false" />
        </label>
        <label>
          <span>files to exclude</span>
          <input class="field-input" type="text" placeholder="e.g. *.min.js, vendor/" value={o.exclude}
            oninput={(e) => contentSearch.set({ exclude: e.currentTarget.value })}
            onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); contentSearch.searchNow(); } }}
            autocomplete="off" spellcheck="false" />
        </label>
      </div>
    {/if}

    <div class="summary" role="status" aria-live="polite">
      {#if contentSearch.searching}
        <LoadingSpinner size={12} label="Searching…" /> Searching…
      {:else if contentSearch.error}
        <span class="error">{contentSearch.error}</span>
      {:else if result && !contentSearch.idle}
        {result.matches === 0 ? 'No results' : searchSummary(result, contentSearch.limit)}
      {/if}
    </div>
    {#if overlaid.unsearched.length > 0}
      <div class="note">
        Unsaved {overlaid.unsearched.length === 1 ? 'draft' : 'drafts'} shown as saved: the browser cannot read this pattern.
      </div>
    {/if}

    <div id="search-results" class="results" role="region" aria-label="Search results" bind:this={resultsEl}>
      {#if result && !contentSearch.idle && !contentSearch.error}
        {#each result.files as file (file.path)}
          {@const parts = splitPath(file.path)}
          {@const folded = contentSearch.collapsed.has(file.path)}
          <div class="file-group">
            <button
              type="button"
              class="file-row"
              aria-expanded={!folded}
              title={fileTitle(file)}
              onclick={() => contentSearch.toggleCollapsed(file.path)}
            >
              <span class="chevron" class:open={!folded}>▸</span>
              <FileIcon path={file.path} />
              <span class="file-name">{parts.name}</span>
              {#if parts.dir}<span class="file-dir">{parts.dir}</span>{/if}
              {#if file.draft}<span class="draft-tag" title="Searched in the unsaved draft">draft</span>{/if}
              <span class="count" aria-label="{file.matches.length}{file.truncated ? '+' : ''} matches">{file.matches.length}{file.truncated ? '+' : ''}</span>
            </button>
            {#if !folded}
              <ul class="match-list" role="listbox" aria-label="Matches in {file.path}">
                {#each file.matches as m, i (i)}
                  <li
                    id={rowId({ path: file.path, index: i })}
                    role="option"
                    aria-selected={isActive(file.path, i)}
                    class="match-row"
                    class:active={isActive(file.path, i)}
                    title="Line {m.line}"
                    onclick={() => open({ path: file.path, index: i })}
                    ondblclick={() => open({ path: file.path, index: i }, true)}
                    onkeydown={() => {}}
                  >
                    <span class="preview">{m.preview.slice(0, m.previewStart)}<mark>{m.preview.slice(m.previewStart, m.previewEnd)}</mark>{m.preview.slice(m.previewEnd)}</span>
                    <span class="line-no">{m.line}</span>
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        {/each}
      {/if}
    </div>
  {/if}
</div>

<style>
  .search-pane {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .search-field {
    display: flex;
    align-items: center;
    gap: 2px;
    margin: 10px 10px 6px;
    padding-right: 3px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
  }

  .search-field:focus-within {
    border-color: var(--accent-color);
  }

  .search-field .field-input {
    flex: 1;
    min-width: 0;
    border: none;
    background: transparent;
  }

  .field-input {
    padding: 7px 10px;
    font-size: 0.85rem;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
  }

  .field-input:focus {
    outline: none;
  }

  .filters .field-input:focus {
    border-color: var(--accent-color);
  }

  .field-toggle {
    flex-shrink: 0;
    min-width: 22px;
    height: 22px;
    padding: 0 4px;
    border: 1px solid transparent;
    border-radius: 3px;
    font-family: var(--font-mono, monospace);
    font-size: 0.72rem;
    color: var(--text-muted);
    cursor: pointer;
  }

  .field-toggle:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .field-toggle.on {
    background-color: var(--active-bg);
    border-color: color-mix(in srgb, var(--accent-color) 60%, transparent);
    color: var(--accent-color);
  }

  .field-toggle:focus-visible,
  .filters-btn:focus-visible,
  .file-row:focus-visible {
    outline: 2px solid var(--accent-color);
    outline-offset: -2px;
  }

  .filters-btn {
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 0 10px 4px;
    padding: 2px 4px;
    border-radius: 4px;
    font-size: 0.72rem;
    color: var(--text-muted);
    text-align: left;
    cursor: pointer;
  }

  .filters-btn:hover {
    color: var(--text-primary);
  }

  .filters-on {
    font-size: 0.5rem;
    color: var(--accent-color);
  }

  .filters {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0 10px 6px;
  }

  .filters label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 0.7rem;
    color: var(--text-muted);
  }

  .filters .field-input {
    padding: 5px 8px;
    font-size: 0.8rem;
  }

  .summary,
  .note {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 1.4em;
    padding: 2px 14px 4px;
    font-size: 0.72rem;
    color: var(--text-muted);
  }

  .summary:empty {
    display: none;
  }

  .summary .error {
    color: #ef4444;
    overflow-wrap: anywhere;
  }

  .note {
    color: var(--text-secondary);
  }

  .results {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 0 6px 6px;
  }

  .file-row {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding: 4px 8px 4px 4px;
    border-radius: 4px;
    font-size: 0.75rem;
    line-height: 1.3;
    text-align: left;
    color: var(--text-primary);
    cursor: pointer;
    white-space: nowrap;
  }

  .file-row:hover,
  .match-row:hover {
    background-color: var(--hover-bg);
  }

  .chevron {
    display: inline-block;
    width: 10px;
    font-size: 0.7rem;
    color: var(--text-muted);
    transition: transform 0.1s;
  }

  .chevron.open {
    transform: rotate(90deg);
  }

  .file-name {
    flex-shrink: 0;
    max-width: 60%;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .file-dir {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: 0.72rem;
    color: var(--text-muted);
  }

  .draft-tag {
    flex-shrink: 0;
    padding: 0 5px;
    border-radius: 999px;
    font-size: 0.65rem;
    color: var(--accent-color);
    border: 1px solid color-mix(in srgb, var(--accent-color) 45%, transparent);
  }

  .count {
    flex-shrink: 0;
    margin-left: auto;
    padding: 0 6px;
    border-radius: 999px;
    font-size: 0.68rem;
    background-color: var(--bg-tertiary);
    color: var(--text-secondary);
  }

  .match-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .match-row {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding: 3px 8px 3px 32px;
    border-radius: 4px;
    font-size: 0.75rem;
    line-height: 1.35;
    cursor: pointer;
    white-space: nowrap;
  }

  .match-row.active {
    background-color: var(--active-bg);
  }

  .preview {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    font-family: var(--font-mono, monospace);
    font-size: 0.72rem;
    color: var(--text-secondary);
    white-space: pre;
  }

  .preview mark {
    padding: 0 1px;
    border-radius: 2px;
    background-color: color-mix(in srgb, var(--accent-color) 30%, transparent);
    color: var(--text-primary);
  }

  .line-no {
    flex-shrink: 0;
    font-size: 0.68rem;
    color: var(--text-muted);
  }

  .state {
    padding: 12px;
    font-size: 0.82rem;
    color: var(--text-secondary);
    line-height: 1.4;
  }
</style>
