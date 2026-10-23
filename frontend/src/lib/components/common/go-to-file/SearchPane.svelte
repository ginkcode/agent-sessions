<script lang="ts">
  import { tick } from 'svelte';
  import { isReferenceMatch } from '../../../referenceResults';
  import { contentSearch } from '../../../stores/contentSearch.svelte';
  import { connectionStore } from '../../../stores/connection.svelte';
  import { matchKey, searchRegExp, searchSummary, type MatchRef } from '../../../contentSearch';
  import { previewReplacement, replacementTemplate, stickyRegExp } from '../../../searchReplace';
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

  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  const replaceAllShortcut = isMac ? '⌘⌥Enter' : 'Ctrl+Alt+Enter';

  let supported = $derived(connectionStore.capabilities.searchFiles === true);
  let o = $derived(contentSearch.options);
  let overlaid = $derived(contentSearch.overlaid);
  let result = $derived(contentSearch.result);
  let active = $derived(contentSearch.active);

  // Replacing. The preview follows the search the results answer.
  let references = $derived(contentSearch.references);
  let replaceOpen = $derived(o.replaceOpen && !references);
  let previewing = $derived(replaceOpen && o.replace !== '');
  let searched = $derived(contentSearch.searched);
  let template = $derived(replacementTemplate(o.replace, !!searched?.regex));
  let sticky = $derived.by(() => {
    if (!previewing || !searched?.regex) return null;
    try { return stickyRegExp(searchRegExp(searched)); } catch { return null; }
  });
  let replaceBlocked = $derived(contentSearch.replaceBlocked());
  // Said under the field: what will not pass by waiting for the search.
  let replaceNote = $derived(
    replaceBlocked && !contentSearch.replacing && contentSearch.upToDate ? replaceBlocked : null
  );

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

  function isReplaceAll(e: KeyboardEvent): boolean {
    return e.key === 'Enter' && e.altKey && !e.shiftKey && (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey);
  }

  function handleFieldKey(e: KeyboardEvent) {
    if (isReplaceAll(e)) {
      e.preventDefault();
      if (o.replaceOpen) void contentSearch.replaceAll();
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
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

  /** Why one file's matches cannot be replaced, or null. */
  function fileBlocked(path: string): string | null {
    return replaceBlocked ?? contentSearch.replaceBlocked(path);
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

{#snippet icon(kind: 'replace' | 'replace-all' | 'dismiss')}
  <svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if kind === 'dismiss'}
      <path d="M4 4l8 8M12 4l-8 8" />
    {:else if kind === 'replace'}
      <path d="M3 5.5h7.5a2.5 2.5 0 0 1 0 5H6" /><path d="M7.5 8.5L5.5 10.5l2 2" />
    {:else}
      <path d="M2.5 3.5h6M2.5 6.5h6" /><path d="M3 10h8a2 2 0 0 0 0-4h-.5" /><path d="M5 8l-2 2 2 2" />
    {/if}
  </svg>
{/snippet}

<div class="search-pane">
  {#if !supported && !references}
    <div class="state" role="note">
      This host's agent-sessions server cannot search file contents. Update agent-sessions on the host, then reconnect.
    </div>
  {:else}
    {#if references}
      <div class="reference-heading">
        <strong>References to {references.name}</strong>
        <button type="button" class="filters-btn" onclick={() => contentSearch.backToSearch()}>← Back to search</button>
      </div>
    {:else}
    <div class="fields">
      <button
        type="button"
        class="replace-toggle"
        aria-expanded={o.replaceOpen}
        aria-controls="search-replace"
        aria-label="Toggle replace"
        title="Toggle replace"
        onclick={() => contentSearch.toggle('replaceOpen')}
      ><span class="chevron" class:open={o.replaceOpen}>▸</span></button>
      <div class="field-stack">
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
        {#if replaceOpen}
          <div id="search-replace" class="search-field replace-field">
            <input
              class="field-input"
              type="text"
              placeholder="Replace"
              value={o.replace}
              oninput={(e) => contentSearch.set({ replace: e.currentTarget.value })}
              onkeydown={handleFieldKey}
              aria-label="Replace with"
              autocomplete="off"
              autocorrect="off"
              autocapitalize="off"
              spellcheck="false"
            />
            <button
              type="button"
              class="field-toggle action"
              aria-label="Replace all"
              title={replaceBlocked ?? `Replace All (${replaceAllShortcut})`}
              aria-keyshortcuts={isMac ? 'Meta+Alt+Enter' : 'Control+Alt+Enter'}
              disabled={Boolean(replaceBlocked) || !result || result.matches === 0}
              onclick={() => void contentSearch.replaceAll()}
            >{@render icon('replace-all')}</button>
          </div>
        {/if}
      </div>
    </div>
    {#if o.replaceOpen && replaceNote}
      <div class="note" role="note">{replaceNote}</div>
    {/if}
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

    {/if}
    <div class="summary" role="status" aria-live="polite">
      {#if references?.loading}
        <LoadingSpinner size={12} label="Reading previews…" /> Reading previews…
      {:else if references}
        {references.result.matches} references in {references.result.files.length} files{references.result.truncated ? ' · The answer was cut short' : ''}
      {:else if contentSearch.replacing}
        <LoadingSpinner size={12} label="Replacing…" /> Replacing…
      {:else if contentSearch.searching}
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

    <!-- References have no search field: this focusable region owns their arrow/Enter navigation. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <div id="search-results" class="results" role="region" aria-label="Search results" tabindex={references ? 0 : undefined}
      onkeydown={(e) => {
        if (!references || e.target !== e.currentTarget) return;
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          reveal(contentSearch.move(e.key === 'ArrowDown' ? 1 : -1));
        } else if (e.key === 'Enter') {
          e.preventDefault();
          open(active ?? contentSearch.order[0] ?? null, true);
        }
      }} bind:this={resultsEl}>
      {#if result && !contentSearch.idle && (references || !contentSearch.error)}
        {#each result.files as file (file.path)}
          {@const parts = splitPath(file.path)}
          {@const folded = contentSearch.collapsed.has(file.path)}
          {@const blockedFile = o.replaceOpen ? fileBlocked(file.path) : null}
          <div class="file-group">
            <div class="file-head">
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
                <span class="count" aria-label="{file.matches.length}{file.truncated ? '+' : ''} {file.matches.length === 1 && !file.truncated ? 'match' : 'matches'}">{file.matches.length}{file.truncated ? '+' : ''}</span>
              </button>
              <span class="row-actions">
                {#if replaceOpen}
                  <button type="button" class="row-action" aria-label="Replace all in {file.path}"
                    title={blockedFile ?? 'Replace all in file'} disabled={Boolean(blockedFile)}
                    onclick={() => void contentSearch.replaceFile(file.path)}>{@render icon('replace-all')}</button>
                {/if}
                {#if !references}<button type="button" class="row-action" aria-label="Dismiss {file.path}" title="Dismiss"
                  onclick={() => contentSearch.dismiss(file.path)}>{@render icon('dismiss')}</button>{/if}
              </span>
            </div>
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
                    {#if isReferenceMatch(m) && m.unavailable}
                      <span class="preview">{m.unavailable}</span>
                    {:else if previewing}
                      <span class="preview">{m.preview.slice(0, m.previewStart)}<del>{m.preview.slice(m.previewStart, m.previewEnd)}</del><ins>{previewReplacement(m, sticky, template, !!searched?.regex)}</ins>{m.preview.slice(m.previewEnd)}</span>
                    {:else}
                      <span class="preview">{m.preview.slice(0, m.previewStart)}<mark>{m.preview.slice(m.previewStart, m.previewEnd)}</mark>{m.preview.slice(m.previewEnd)}</span>
                    {/if}
                    <span class="line-no">{m.line}</span>
                    <span class="row-actions">
                      {#if replaceOpen}
                        <button type="button" class="row-action" aria-label="Replace line {m.line} of {file.path}"
                          title={blockedFile ?? 'Replace'} disabled={Boolean(blockedFile)}
                          onclick={(e) => { e.stopPropagation(); void contentSearch.replaceMatch({ path: file.path, index: i }); }}
                          ondblclick={(e) => e.stopPropagation()}>{@render icon('replace')}</button>
                      {/if}
                      {#if !references}<button type="button" class="row-action" aria-label="Dismiss line {m.line} of {file.path}" title="Dismiss"
                        onclick={(e) => { e.stopPropagation(); contentSearch.dismiss(file.path, { path: file.path, index: i }); }}
                        ondblclick={(e) => e.stopPropagation()}>{@render icon('dismiss')}</button>{/if}
                    </span>
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
  .reference-heading { padding: 10px; display: flex; flex-direction: column; gap: 6px; overflow-wrap: anywhere; font-size: 12px; }
  .search-pane {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .fields {
    display: flex;
    align-items: flex-start;
    gap: 2px;
    margin: 10px 10px 6px 4px;
  }

  .field-stack {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .replace-toggle {
    flex-shrink: 0;
    align-self: stretch;
    width: 16px;
    border-radius: 3px;
    color: var(--text-muted);
    cursor: pointer;
  }

  .replace-toggle:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .search-field {
    display: flex;
    align-items: center;
    gap: 2px;
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

  .field-toggle:disabled {
    opacity: 0.4;
    cursor: default;
    background: none;
  }

  .field-toggle.action {
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }

  .field-toggle.on {
    background-color: var(--active-bg);
    border-color: color-mix(in srgb, var(--accent-color) 60%, transparent);
    color: var(--accent-color);
  }

  .field-toggle:focus-visible,
  .filters-btn:focus-visible,
  .replace-toggle:focus-visible,
  .row-action:focus-visible,
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

  .file-head {
    position: relative;
    display: flex;
    align-items: center;
    border-radius: 4px;
  }

  .file-head:hover,
  .match-row:hover {
    background-color: var(--hover-bg);
  }

  /* Shown on hover and keyboard focus, over the count or line number. */
  .row-actions {
    position: absolute;
    right: 4px;
    top: 50%;
    transform: translateY(-50%);
    display: none;
    gap: 1px;
    padding-left: 4px;
    background-color: inherit;
  }

  .file-head:hover .row-actions,
  .file-head:focus-within .row-actions,
  .match-row:hover .row-actions,
  .match-row.active .row-actions,
  .match-row:focus-within .row-actions {
    display: flex;
  }

  .file-head:hover .row-actions,
  .match-row:hover .row-actions {
    background-color: var(--hover-bg);
  }

  .match-row.active .row-actions {
    background-color: var(--active-bg);
  }

  .row-action {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 18px;
    border-radius: 3px;
    color: var(--text-muted);
    cursor: pointer;
  }

  .row-action:hover:not(:disabled) {
    background-color: var(--active-bg);
    color: var(--text-primary);
  }

  .row-action:disabled {
    opacity: 0.4;
    cursor: default;
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
    position: relative;
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

  .preview del {
    text-decoration: line-through;
    background-color: color-mix(in srgb, #ef4444 22%, transparent);
    color: var(--text-primary);
  }

  .preview ins {
    text-decoration: none;
    background-color: color-mix(in srgb, #22c55e 22%, transparent);
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
