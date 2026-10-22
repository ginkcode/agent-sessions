<script lang="ts">
  import { files } from '../../../stores/files.svelte';
  import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';
  import DiagnosticBadge from './DiagnosticBadge.svelte';
  import {
    STATUS_LABELS,
    buildFileTree,
    searchFiles,
    folderStatus,
    withDeleted,
    type FileTreeNode,
  } from '../../../files';
  import type { GitStatus } from '../../../types';
  import LoadingSpinner from '../LoadingSpinner.svelte';
  import FileIcon from '../FileIcon.svelte';
  import { issueCounts } from '../../../codeCheckSummary';

  interface Props {
    input?: HTMLInputElement | undefined;
    presentation?: boolean;
    onPick: (path: string, keep?: boolean) => void;
    onContext: (event: MouseEvent, path: string, directory: boolean) => void;
  }

  let { input = $bindable(), presentation = false, onPick, onContext }: Props = $props();

  let query = $state('');
  let diagnosticContext = $derived([
    checks.status !== 'ready' ? 'status uncertain; retained results are not a successful check' : '',
    checks.providers.some(p => p.freshness !== 'current' || p.coverage.externalChanges === 'unwatched') ? 'provider freshness stale or uncertain' : '',
    checks.partial ? 'partial coverage' : '',
    checks.truncated ? 'diagnostics truncated' : '',
  ].filter(Boolean).join(' · '));
  // Any reported severity marks a file name red; folders get a subtle tint.
  let issues = $derived(issueCounts(checks.findings ?? []));
  let activeIndex = $state(0);
  let resultsEl: HTMLElement | undefined = $state();

  // Git status, when the directory is a work tree.
  let status = $derived(files.listing?.status);
  let folders = $derived(folderStatus(status));
  let changedCount = $derived(status ? Object.keys(status).length : 0);
  // Deleted files stay listed, struck through, while git status shows.
  let paths = $derived(withDeleted(files.listing?.files ?? [], status));
  let tree = $derived(buildFileTree(paths, files.listing?.directories));
  // Ignored files (such as .env) stay out of the tree but can be searched.
  let ignored = $derived(new Set(files.listing?.ignored ?? []));
  let matches = $derived(query.trim() ? searchFiles(paths, files.listing?.ignored, query) : []);

  /** Starts over with an empty search, as when the dialog opens. */
  export function reset() {
    query = '';
    activeIndex = 0;
  }

  function fileStatus(path: string): GitStatus | undefined {
    return status?.[path];
  }

  function toggle(dir: string) {
    const next = new Set(files.expanded);
    if (next.has(dir)) next.delete(dir);
    else next.add(dir);
    files.setExpanded(next);
  }

  function setQuery(value: string) {
    query = value;
    activeIndex = 0;
    if (resultsEl) resultsEl.scrollTop = 0;
  }

  function move(delta: number) {
    if (matches.length === 0) return;
    activeIndex = (activeIndex + delta + matches.length) % matches.length;
    resultsEl?.querySelector(`#goto-file-${activeIndex}`)?.scrollIntoView({ block: 'nearest' });
  }

  function handleInputKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      move(1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      move(-1);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const path = matches[activeIndex];
      if (path) onPick(path, true);
    }
  }

  function rowTitle(path: string, st?: GitStatus, isIgnored = false) {
    const n = issues.files.get(path);
    return [path, st ? STATUS_LABELS[st] : '', isIgnored ? 'Ignored by .gitignore' : '', n ? `${n} reported code ${n === 1 ? 'issue' : 'issues'}` : ''].filter(Boolean).join(' · ');
  }

  function splitPath(path: string): { name: string; dir: string } {
    const at = path.lastIndexOf('/');
    return { name: path.slice(at + 1), dir: at >= 0 ? path.slice(0, at) : '' };
  }
</script>

<!-- Error/warning badges carry their own label; other severities need text too. -->
{#snippet issueNote(path: string)}
  {@const c = checks.pathCounts.files.get(path)}
  {#if issues.files.has(path) && !(c && (c.error || c.warning))}<span class="sr-only">, has reported code issues</span>{/if}
{/snippet}

{#snippet treeRows(nodes: FileTreeNode[], depth: number)}
  {#each nodes as node (node.path)}
    {#if node.children}
      <li>
        <button
          type="button"
          class="tree-row"
          style:padding-left="{8 + depth * 14}px"
          aria-expanded={files.expanded.has(node.path)}
          onclick={() => toggle(node.path)}
          oncontextmenu={(e) => onContext(e, node.path, true)}
        >
          <span class="chevron" class:open={files.expanded.has(node.path)}>▸</span>
          <FileIcon path={node.path} folder expanded={files.expanded.has(node.path)} />
          <span class="tree-name git-{folders.get(node.path) ?? 'none'}" class:folder-issue={issues.folders.has(node.path)}>{node.name}</span>
          <DiagnosticBadge counts={checks.pathCounts.folders.get(node.path)} context={diagnosticContext} />
          {#if folders.has(node.path)}
            <span class="git-dot git-{folders.get(node.path)}" title="Holds changed files">●</span>
          {/if}
        </button>
        {#if files.expanded.has(node.path)}
          <ul class="tree-list">{@render treeRows(node.children, depth + 1)}</ul>
        {/if}
      </li>
    {:else}
      {@const st = fileStatus(node.path)}
      <li>
        <button
          type="button"
          class="tree-row file"
          class:selected={files.selected === node.path}
          style:padding-left="{8 + depth * 14 + 14}px"
          title={rowTitle(node.path, st)}
          onclick={() => onPick(node.path)}
          ondblclick={() => onPick(node.path, true)}
          oncontextmenu={(e) => onContext(e, node.path, false)}
        >
          <FileIcon path={node.path} />
          <span class="tree-name git-{st ?? 'none'}" class:has-issue={issues.files.has(node.path)}>{node.name}</span>
          {@render issueNote(node.path)}
          <DiagnosticBadge counts={checks.pathCounts.files.get(node.path)} context={diagnosticContext} />
          {#if st}<span class="git-letter git-{st}">{st}</span>{/if}
        </button>
      </li>
    {/if}
  {/each}
{/snippet}

<aside class="picker" class:presentation>
  <input
    bind:this={input}
    class="search-input"
    type="search"
    placeholder="Type a file name…"
    value={query}
    oninput={(e) => setQuery(e.currentTarget.value)}
    onkeydown={handleInputKey}
    aria-label="Find a file"
    aria-controls="goto-file-results"
    aria-activedescendant={matches.length > 0 ? `goto-file-${activeIndex}` : undefined}
    autocomplete="off"
    autocorrect="off"
    autocapitalize="off"
    spellcheck="false"
  />

  <div class="picker-list" role="region" aria-label="Project file browser" bind:this={resultsEl} oncontextmenu={(e) => { if (!query.trim()) onContext(e, '', true); }}>
    {#if files.loadingList}
      <div class="state"><LoadingSpinner size={16} label="Listing files…" /> Listing files…</div>
    {:else if files.listError}
      <div class="state error" role="alert">{files.listError}</div>
    {:else if query.trim()}
      {#if matches.length === 0}
        <div class="state">No files match.</div>
      {:else}
        <ul id="goto-file-results" class="match-list" role="listbox" aria-label="Matching files">
          {#each matches as path, i (path)}
            {@const parts = splitPath(path)}
            {@const st = fileStatus(path)}
            <li
              id="goto-file-{i}"
              role="option"
              aria-selected={i === activeIndex}
              class="match"
              class:active={i === activeIndex}
              class:ignored={ignored.has(path)}
              title={rowTitle(path, undefined, ignored.has(path))}
              onclick={() => onPick(path)}
              ondblclick={() => onPick(path, true)}
              oncontextmenu={(e) => onContext(e, path, false)}
              onmousemove={() => (activeIndex = i)}
              onkeydown={() => {}}
            >
              <FileIcon {path} />
              <span class="match-name git-{st ?? 'none'}" class:has-issue={issues.files.has(path)}>{parts.name}</span>
              {@render issueNote(path)}
              {#if parts.dir}<span class="match-dir">{parts.dir}</span>{/if}
              <DiagnosticBadge counts={checks.pathCounts.files.get(path)} context={diagnosticContext} />
              {#if st}<span class="git-letter git-{st}" title={STATUS_LABELS[st]}>{st}</span>{/if}
              {#if ignored.has(path)}<span class="ignored-tag">ignored</span>{/if}
            </li>
          {/each}
        </ul>
      {/if}
    {:else if tree.length === 0}
      <div class="state">This directory has no files.</div>
    {:else}
      <ul class="tree-list" role="tree" aria-label="Project files">{@render treeRows(tree, 0)}</ul>
    {/if}
  </div>

  {#if files.listing || files.listError}
    <footer class="picker-footer">
      <span class="footer-text">
        {#if files.listing}
          {files.listing.files.length.toLocaleString()} files{files.listing.truncated ? ' (list cut short)' : ''}
          {#if files.listing.git}· .gitignore applied{/if}
          {#if changedCount > 0}· {changedCount} changed{/if}
          {#if checks.counts.error || checks.counts.warning}· Diagnostic counts include findings outside this listing and search.{/if}
        {/if}
      </span>
      <button
        type="button"
        class="refresh-btn"
        class:spinning={files.refreshing}
        disabled={files.loadingList || files.refreshing}
        title="Refresh: list the files again and reload the open ones"
        aria-label="Refresh the file list"
        onclick={() => void files.refresh()}
      >
        <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
          <path d="M13.5 8a5.5 5.5 0 1 1-1.6-3.9" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
          <path d="M12.5 1.8v2.7H9.8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    </footer>
  {/if}
</aside>

<style>
  .picker {
    width: clamp(280px, 26%, 400px);
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    border-right: 1px solid var(--border-color);
    min-height: 0;
  }

  .picker.presentation {
    display: none;
  }

  .search-input {
    margin: 10px;
    padding: 7px 10px;
    font-size: 0.85rem;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
  }

  .search-input:focus {
    outline: none;
    border-color: var(--accent-color, #3b82f6);
  }

  .picker-list {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 0 6px 6px;
  }

  .picker-footer {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 6px 2px 12px;
    font-size: 0.72rem;
    color: var(--text-muted);
    border-top: 1px solid var(--border-color);
  }

  .tree-list,
  .match-list {
    list-style: none;
    margin: 0;
    padding: 0;
  }

  .tree-row {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding-top: 5px;
    padding-bottom: 5px;
    padding-right: 8px;
    border-radius: 4px;
    font-size: 0.75rem;
    line-height: 1.3;
    text-align: left;
    color: var(--text-primary);
    cursor: pointer;
  }

  .tree-row:hover,
  .match.active {
    background-color: var(--hover-bg);
  }

  .tree-row.selected {
    background-color: var(--active-bg);
    color: var(--accent-color);
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

  .tree-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* Git status, VS Code's colours. */
  .git-M,
  .git-R {
    color: #d29922;
  }
  .git-A,
  .git-U {
    color: #3fb950;
  }
  .git-D {
    color: #f85149;
  }
  .git-C {
    color: #db61a2;
  }
  /* Any reported code issue: red file name, above Git colours (the letter keeps Git). */
  .tree-name.has-issue,
  .match-name.has-issue,
  .tree-row.selected .tree-name.has-issue {
    color: #f85149;
  }
  .tree-name.folder-issue {
    text-decoration: underline dotted color-mix(in srgb, #f85149 60%, transparent);
    text-underline-offset: 3px;
  }
  :global(:root:not([data-theme='dark'])) .tree-name.has-issue,
  :global(:root:not([data-theme='dark'])) .match-name.has-issue {
    color: #cf222e;
  }
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
  /* Only the deleted file is struck through, not the folders holding it. */
  .tree-row.file .tree-name.git-D,
  .match-name.git-D {
    text-decoration: line-through;
  }
  :global(:root:not([data-theme='dark'])) .git-M,
  :global(:root:not([data-theme='dark'])) .git-R {
    color: #9a6700;
  }
  :global(:root:not([data-theme='dark'])) .git-A,
  :global(:root:not([data-theme='dark'])) .git-U {
    color: #1a7f37;
  }
  :global(:root:not([data-theme='dark'])) .git-D {
    color: #cf222e;
  }
  :global(:root:not([data-theme='dark'])) .git-C {
    color: #bf3989;
  }

  .git-letter {
    margin-left: auto;
    padding-left: 8px;
    flex-shrink: 0;
    font-size: 0.72rem;
    font-weight: 600;
  }

  .git-dot {
    margin-left: auto;
    padding-left: 8px;
    flex-shrink: 0;
    font-size: 0.5rem;
  }

  /* Files .gitignore leaves out, found by search: dimmed, as VS Code does. */
  .match.ignored .match-name,
  .match.ignored .match-dir {
    opacity: 0.6;
  }
  .ignored-tag {
    margin-left: auto;
    padding-left: 8px;
    flex-shrink: 0;
    font-size: 0.7rem;
    color: var(--text-secondary);
  }
  .git-letter + .ignored-tag {
    margin-left: 0;
  }

  .match {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px;
    border-radius: 4px;
    font-size: 0.75rem;
    line-height: 1.3;
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
  }

  .match-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .match-dir {
    font-size: 0.72rem;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* Wraps rather than hiding the counts when the list is narrow. */
  .footer-text {
    flex: 1;
    min-width: 0;
    padding: 4px 0;
  }

  .refresh-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 24px;
    height: 24px;
    border-radius: 4px;
    color: var(--text-muted);
    cursor: pointer;
  }

  .refresh-btn:hover:not(:disabled) {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .refresh-btn:disabled {
    cursor: default;
  }

  .refresh-btn.spinning svg {
    animation: refresh-spin 0.8s linear infinite;
  }

  @keyframes refresh-spin {
    to {
      transform: rotate(360deg);
    }
  }

  .state {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px;
    font-size: 0.82rem;
    color: var(--text-secondary);
  }

  .state.error {
    color: #ef4444;
  }
</style>
