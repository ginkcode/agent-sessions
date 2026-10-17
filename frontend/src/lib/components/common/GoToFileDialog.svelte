<script lang="ts">
  import { files } from '../../stores/files.svelte';
  import { link } from '../../stores/link.svelte';
  import { hostLabel } from '../../hosts';
  import { highlightFile, splitHighlightedLines, splitLines } from '../../codeHighlight';
  import { copyToClipboard } from '../../portable';
  import {
    STATUS_LABELS,
    buildFileTree,
    diffRows,
    fileChanges,
    fileLanguage,
    searchFiles,
    folderStatus,
    lineMarks,
    parentFolders,
    withDeleted,
    type FileTreeNode,
  } from '../../files';
  import type { GitStatus } from '../../types';
  import { directoryName } from '../../tree';
  import LoadingSpinner from './LoadingSpinner.svelte';

  // Files larger than this are shown without highlighting, which is slow on
  // very long text.
  const HIGHLIGHT_LIMIT = 500_000;

  let query = $state('');
  let activeIndex = $state(0);
  let input: HTMLInputElement | undefined = $state();
  let resultsEl: HTMLElement | undefined = $state();
  let restoreFocus: HTMLElement | null = null;

  // Git status, when the directory is a work tree.
  let status = $derived(files.listing?.status);
  let folders = $derived(folderStatus(status));
  let changedCount = $derived(status ? Object.keys(status).length : 0);
  // Deleted files stay listed, struck through, while git status shows.
  let paths = $derived(withDeleted(files.listing?.files ?? [], status));
  let tree = $derived(buildFileTree(paths));
  // Ignored files (such as .env) stay out of the tree but can be searched.
  let ignored = $derived(new Set(files.listing?.ignored ?? []));
  let matches = $derived(query.trim() ? searchFiles(paths, files.listing?.ignored, query) : []);

  let language = $derived(files.selected ? fileLanguage(files.selected) : null);
  let text = $derived(files.content?.text ?? '');
  // The highlighted file. Until highlight.js has loaded and run, the text
  // shows plain, so a file never waits on the highlighter.
  let html = $state<string | null>(null);
  // One entry per line, so a wrapped line keeps its number beside it.
  // Highlighted lines are HTML; plain ones are text.
  let htmlLines = $derived(html !== null ? splitHighlightedLines(html) : null);
  let plainLines = $derived(text ? splitLines(text) : []);
  let changes = $derived(
    files.selected ? fileChanges(status?.[files.selected], files.content?.changes, plainLines.length) : undefined
  );
  let marks = $derived(lineMarks(changes, plainLines.length));
  // The diff view shows when it is on and the file has changes to show.
  let hasChanges = $derived(Boolean(changes?.length));
  let showDiff = $derived(files.diff && hasChanges);
  let rows = $derived(showDiff ? diffRows(changes, plainLines.length) : []);
  const MARK_LABELS = {
    added: 'Added line',
    modified: 'Changed line',
    'deleted-above': 'Lines removed above',
    'deleted-below': 'Lines removed below',
  };

  function fileStatus(path: string): GitStatus | undefined {
    return status?.[path];
  }
  $effect(() => {
    const code = text;
    const lang = language;
    html = null;
    if (!code || !lang || code.length > HIGHLIGHT_LIMIT) return;
    let current = true;
    highlightFile(code, lang).then(
      (out) => {
        if (current) html = out;
      },
      () => {}
    );
    return () => {
      current = false;
    };
  });

  // Opening the dialog: start empty, focus the search box, and remember what
  // had focus to give it back on close.
  let wasOpen = false;
  $effect(() => {
    const open = files.open;
    if (open && !wasOpen) {
      restoreFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      query = '';
      activeIndex = 0;
      queueMicrotask(() => input?.focus());
    } else if (!open && wasOpen) {
      restoreFocus?.focus();
      restoreFocus = null;
    }
    wasOpen = open;
  });

  function close() {
    files.close();
  }

  // One click shows the file in the preview tab, which the next file
  // replaces; a double click (or Enter in the search) keeps it in a tab.
  function pick(path: string, keep = false) {
    // Show where the file is in the tree when the search is cleared.
    const missing = parentFolders(path).filter((dir) => !files.expanded.has(dir));
    if (missing.length) files.setExpanded(new Set([...files.expanded, ...missing]));
    // A deleted file has nothing on disk to read.
    files.openFile(path, { keep, read: fileStatus(path) !== 'D' });
  }

  function toggle(dir: string) {
    const next = new Set(files.expanded);
    if (next.has(dir)) next.delete(dir);
    else next.add(dir);
    files.setExpanded(next);
  }

  // Each tab keeps its scroll position. A plain map: saving it on every
  // scroll must not rerun the effect that restores it.
  const scrollTops = new Map<string, number>();
  let codeScroll: HTMLElement | undefined = $state();
  $effect(() => {
    const el = codeScroll;
    const path = files.selected;
    if (el && path) el.scrollTop = scrollTops.get(path) ?? 0;
  });

  function closeTab(e: MouseEvent, path: string) {
    e.stopPropagation();
    files.closeTab(path);
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
      if (path) pick(path, true);
    }
  }

  // The button says how the copy went for a moment afterwards.
  let copyState = $state<'idle' | 'copied' | 'failed'>('idle');
  let copyTimer: ReturnType<typeof setTimeout> | null = null;

  async function copyContent() {
    if (!text) return;
    copyState = (await copyToClipboard(text)) ? 'copied' : 'failed';
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => (copyState = 'idle'), 1500);
  }

  let pathCopyState = $state<'idle' | 'copied' | 'failed'>('idle');
  let pathCopyTimer: ReturnType<typeof setTimeout> | null = null;

  async function copyPath() {
    if (!files.selected) return;
    pathCopyState = (await copyToClipboard(files.selected)) ? 'copied' : 'failed';
    if (pathCopyTimer) clearTimeout(pathCopyTimer);
    pathCopyTimer = setTimeout(() => (pathCopyState = 'idle'), 1500);
  }

  const PATH_COPY_LABELS = { idle: 'Copy relative path', copied: 'Path copied', failed: 'Copy failed' };

  // Another file opened: the buttons start over.
  $effect(() => {
    void files.selected;
    copyState = 'idle';
    pathCopyState = 'idle';
  });

  const COPY_LABELS = { idle: 'Copy', copied: 'Copied', failed: 'Copy failed' };

  // Alt+Z (Option+Z), as in VS Code. Read by key position, since Option+Z
  // types Ω on a Mac.
  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  const wrapShortcut = isMac ? '⌥Z' : 'Alt+Z';
  function isWrapShortcut(e: KeyboardEvent): boolean {
    return e.altKey && !e.ctrlKey && !e.metaKey && !e.shiftKey && e.code === 'KeyZ' && !e.repeat;
  }

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  }

  function splitPath(path: string): { name: string; dir: string } {
    const at = path.lastIndexOf('/');
    return { name: path.slice(at + 1), dir: at >= 0 ? path.slice(0, at) : '' };
  }
</script>

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
        >
          <span class="chevron" class:open={files.expanded.has(node.path)}>▸</span>
          <span class="tree-name git-{folders.get(node.path) ?? 'none'}">{node.name}</span>
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
          class:unsupported={fileLanguage(node.path) === null}
          style:padding-left="{8 + depth * 14 + 14}px"
          title={st ? `${node.path} · ${STATUS_LABELS[st]}` : node.path}
          onclick={() => pick(node.path)}
          ondblclick={() => pick(node.path, true)}
        >
          <span class="tree-name git-{st ?? 'none'}">{node.name}</span>
          {#if st}<span class="git-letter git-{st}">{st}</span>{/if}
        </button>
      </li>
    {/if}
  {/each}
{/snippet}

<svelte:window
  onkeydown={(e) => {
    // On the window, so Escape still closes the dialog after a click
    // outside it moved the focus away.
    if (files.open && e.key === 'Escape' && !e.defaultPrevented) {
      e.preventDefault();
      e.stopPropagation();
      close();
    } else if (files.open && isWrapShortcut(e)) {
      e.preventDefault();
      files.toggleWrap();
    }
  }}
/>

{#if files.open}
  <!-- Clicking outside leaves the dialog open; Escape or × closes it. -->
  <div class="dialog-backdrop" role="presentation">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="goto-file-title">
      <header class="dialog-header">
        <h2 id="goto-file-title">Go to File</h2>
        <span class="root" title={files.dir}>{directoryName(files.dir ?? '')}</span>
        {#if link.dataHost}
          <span class="host-pill">{hostLabel(link.dataHost)}</span>
        {/if}
        <button type="button" class="close-button" aria-label="Close" title="Close" onclick={close}>×</button>
      </header>

      <div class="body">
        <aside class="picker">
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
            spellcheck="false"
          />

          <div class="picker-list" bind:this={resultsEl}>
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
                      class:unsupported={fileLanguage(path) === null}
                      class:ignored={ignored.has(path)}
                      title={ignored.has(path) ? `${path} · Ignored by .gitignore` : path}
                      onclick={() => pick(path)}
                      ondblclick={() => pick(path, true)}
                      onmousemove={() => (activeIndex = i)}
                      onkeydown={() => {}}
                    >
                      <span class="match-name git-{st ?? 'none'}">{parts.name}</span>
                      {#if parts.dir}<span class="match-dir">{parts.dir}</span>{/if}
                      {#if st}<span class="git-letter git-{st}" title={STATUS_LABELS[st]}>{st}</span>{/if}
                      {#if ignored.has(path)}<span class="ignored-tag">ignored</span>{/if}
                    </li>
                  {/each}
                </ul>
              {/if}
            {:else if paths.length === 0}
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

        <section class="viewer" aria-label="File content">
          {#if files.tabs.length > 0}
            <div class="tab-bar" role="tablist" aria-label="Open files">
              {#each files.tabs as tab (tab.path)}
                {@const st = fileStatus(tab.path)}
                <div
                  class="file-tab"
                  class:active={tab.path === files.selected}
                  class:preview={tab.preview}
                  role="tab"
                  tabindex="0"
                  aria-selected={tab.path === files.selected}
                  title={tab.preview ? `${tab.path} · Preview: double-click to keep it open` : tab.path}
                  onclick={() => files.selectTab(tab.path)}
                  ondblclick={() => files.keepTab(tab.path)}
                  onauxclick={(e) => e.button === 1 && closeTab(e, tab.path)}
                  onkeydown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      files.selectTab(tab.path);
                    }
                  }}
                >
                  <span class="tab-name git-{st ?? 'none'}">{splitPath(tab.path).name}</span>
                  <button
                    type="button"
                    class="tab-close"
                    aria-label="Close {splitPath(tab.path).name}"
                    title="Close"
                    onclick={(e) => closeTab(e, tab.path)}>×</button
                  >
                </div>
              {/each}
            </div>
          {/if}
          {#if !files.selected}
            <div class="state center">Pick a file to see its content. Double-click keeps it open in a tab.</div>
          {:else}
            <div class="viewer-header">
              <span class="viewer-path" title={files.selected}>{files.selected}</span>
              <button
                type="button"
                class="path-copy"
                class:copied={pathCopyState === 'copied'}
                class:failed={pathCopyState === 'failed'}
                title={PATH_COPY_LABELS[pathCopyState]}
                aria-label={PATH_COPY_LABELS[pathCopyState]}
                onclick={copyPath}
              >
                {#if pathCopyState === 'copied'}
                  <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
                    <path d="M3 8.5l3 3 7-7" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" />
                  </svg>
                {:else}
                  <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
                    <rect x="5.5" y="5.5" width="8" height="8.5" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.3" />
                    <path d="M10.5 3.5V3a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v6.5A1.5 1.5 0 0 0 4 11h.5" fill="none" stroke="currentColor" stroke-width="1.3" />
                  </svg>
                {/if}
              </button>
              {#if files.content}<span class="viewer-size">{formatSize(files.content.size)}</span>{/if}
              {#if text && !files.loadingFile && language !== null}
                {#if hasChanges}
                  <button
                    type="button"
                    class="copy-btn"
                    class:on={files.diff}
                    aria-pressed={files.diff}
                    title={files.diff ? 'Show the file without the removed lines' : 'Show the changes since the last commit, with removed lines'}
                    onclick={() => files.toggleDiff()}
                  >
                    Git diff
                  </button>
                {/if}
                <button
                  type="button"
                  class="copy-btn"
                  class:on={files.wrap}
                  aria-pressed={files.wrap}
                  title="{files.wrap ? 'Stop wrapping' : 'Wrap'} long lines ({wrapShortcut})"
                  onclick={() => files.toggleWrap()}
                >
                  Wrap
                </button>
                <button
                  type="button"
                  class="copy-btn"
                  class:copied={copyState === 'copied'}
                  class:failed={copyState === 'failed'}
                  title="Copy the file content"
                  aria-label={copyState === 'idle' ? 'Copy the file content' : COPY_LABELS[copyState]}
                  onclick={copyContent}
                >
                  {COPY_LABELS[copyState]}
                </button>
              {/if}
            </div>
            {#if fileStatus(files.selected) === 'D'}
              <div class="state center">This file was deleted since the last commit.</div>
            {:else if files.loadingFile}
              <div class="state center"><LoadingSpinner size={16} label="Loading file…" /></div>
            {:else if files.fileError}
              <div class="state center error" role="alert">{files.fileError}</div>
            {:else if language === null}
              <div class="state center">This file type is not supported yet. Only source code and text files can be shown.</div>
            {:else if files.content?.binary}
              <div class="state center">This is a binary file, so it can't be shown.</div>
            {:else if files.content?.tooLarge}
              <div class="state center">This file is too large to show ({formatSize(files.content.size)}).</div>
            {:else if files.content && !text}
              <div class="state center">This file is empty.</div>
            {:else if files.content}
              <div
                class="code-scroll"
                bind:this={codeScroll}
                onscroll={(e) => {
                  if (files.selected) scrollTops.set(files.selected, e.currentTarget.scrollTop);
                }}
              >
                <div class="code source" class:wrap={files.wrap} class:diff={showDiff}>
                  {#if showDiff}
                    {#each rows as row, r (r)}
                      {#if row.kind === 'removed'}
                        <span aria-hidden="true" class="ln sign">−</span>
                        <span class="line removed">{row.text}</span>
                      {:else}
                        <span aria-hidden="true" class="ln" class:sign={row.mark}>{row.mark ? '+' : row.index + 1}</span>
                        {#if htmlLines}
                          <!-- highlight.js escapes the file's text -->
                          <span class="line" class:added={row.mark}>{@html htmlLines[row.index]}</span>
                        {:else}
                          <span class="line" class:added={row.mark}>{plainLines[row.index]}</span>
                        {/if}
                      {/if}
                    {/each}
                  {:else if htmlLines}
                    {#each htmlLines as line, i (i)}
                      <span aria-hidden="true" class="ln mark-{marks[i] ?? 'none'}" title={marks[i] && MARK_LABELS[marks[i]]}>{i + 1}</span>
                      <!-- highlight.js escapes the file's text -->
                      <span class="line">{@html line}</span>
                    {/each}
                  {:else}
                    {#each plainLines as line, i (i)}
                      <span aria-hidden="true" class="ln mark-{marks[i] ?? 'none'}" title={marks[i] && MARK_LABELS[marks[i]]}>{i + 1}</span>
                      <span class="line">{line}</span>
                    {/each}
                  {/if}
                </div>
              </div>
            {/if}
          {/if}
        </section>
      </div>
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    background-color: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1010;
    padding: 16px;
  }

  .dialog {
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.35);
    /* Nearly the whole window: code needs the room. */
    width: 100%;
    height: 100%;
    display: flex;
    flex-direction: column;
    color: var(--text-primary);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 10px 14px 10px 18px;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .dialog-header h2 {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
  }

  .root {
    font-family: var(--font-mono);
    font-size: 0.8rem;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .host-pill {
    font-size: 0.72rem;
    padding: 2px 8px;
    border-radius: 10px;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
  }

  .close-button {
    margin-left: auto;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    font-size: 1.2rem;
    line-height: 1;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .close-button:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .body {
    flex: 1;
    min-height: 0;
    display: flex;
  }

  .picker {
    width: clamp(280px, 26%, 400px);
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    border-right: 1px solid var(--border-color);
    min-height: 0;
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
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .unsupported {
    color: var(--text-muted);
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

  /* Changed lines: a bar at the gutter's right edge, as in VS Code. A
     triangle-ish marker on the line border shows where lines were removed. */
  .ln.mark-added {
    box-shadow: inset -3px 0 0 #3fb950;
  }
  .ln.mark-modified {
    box-shadow: inset -3px 0 0 #2f81f7;
  }
  .ln.mark-deleted-above {
    background-image: linear-gradient(#f85149, #f85149);
    background-size: 100% 2px;
    background-position: top;
    background-repeat: no-repeat;
  }
  .ln.mark-deleted-below {
    background-image: linear-gradient(#f85149, #f85149);
    background-size: 100% 2px;
    background-position: bottom;
    background-repeat: no-repeat;
  }

  /* Diff view: removed lines on red, changed and added ones on green,
     with − and + in the gutter. */
  .line.removed {
    background-color: rgba(248, 81, 73, 0.15);
    color: var(--text-secondary);
  }
  .line.added {
    background-color: rgba(46, 160, 67, 0.15);
  }
  .ln.sign {
    color: var(--text-secondary);
    font-weight: 600;
  }
  .code.diff .ln:has(+ .line.removed) {
    background-color: rgba(248, 81, 73, 0.22);
  }
  .code.diff .ln:has(+ .line.added) {
    background-color: rgba(46, 160, 67, 0.22);
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
    align-items: baseline;
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
    flex-shrink: 0;
  }

  .match-dir {
    font-size: 0.72rem;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .viewer {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
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

  /* Open files, as VS Code's editor tabs. A preview tab is in italics. */
  .tab-bar {
    display: flex;
    flex-shrink: 0;
    overflow-x: auto;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
    scrollbar-width: thin;
  }

  .file-tab {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
    max-width: 220px;
    padding: 6px 6px 6px 12px;
    border-right: 1px solid var(--border-color);
    border-top: 2px solid transparent;
    font-size: 0.75rem;
    color: var(--text-secondary);
    cursor: pointer;
    user-select: none;
  }

  .file-tab:hover {
    background-color: var(--hover-bg);
  }

  .file-tab.active {
    background-color: var(--bg-primary);
    color: var(--text-primary);
    border-top-color: var(--accent-color);
  }

  .file-tab.preview .tab-name {
    font-style: italic;
  }

  .tab-name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tab-close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 18px;
    height: 18px;
    border-radius: 4px;
    font-size: 0.9rem;
    line-height: 1;
    color: var(--text-muted);
    visibility: hidden;
    cursor: pointer;
  }

  .file-tab.active .tab-close,
  .file-tab:hover .tab-close,
  .tab-close:focus-visible {
    visibility: visible;
  }

  .tab-close:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .viewer-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 14px;
    border-bottom: 1px solid var(--border-color);
    font-family: var(--font-mono);
    font-size: 0.78rem;
  }

  .viewer-path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .path-copy {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 22px;
    height: 22px;
    margin-left: -4px;
    border-radius: 4px;
    color: var(--text-muted);
    cursor: pointer;
  }

  .path-copy:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .path-copy.copied {
    color: #22c55e;
  }

  .path-copy.failed {
    color: #ef4444;
  }

  .viewer-size {
    margin-left: auto;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .copy-btn {
    flex-shrink: 0;
    padding: 2px 10px;
    font-family: var(--font-sans);
    font-size: 0.75rem;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .copy-btn:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .copy-btn.on {
    color: var(--accent-color);
    border-color: var(--accent-color);
    background-color: var(--active-bg);
  }

  .copy-btn.copied {
    color: #22c55e;
    border-color: #22c55e;
  }

  .copy-btn.failed {
    color: #ef4444;
    border-color: #ef4444;
  }

  .code-scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }

  /* Line numbers and lines side by side, one grid row per line, so a
     wrapped line keeps its number at its first row. */
  .code {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    align-content: start;
    min-width: max-content;
    padding: 8px 0;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    line-height: 1.5;
    color: var(--text-primary);
    tab-size: 4;
  }

  .code.wrap {
    min-width: 0;
  }

  .ln {
    position: sticky;
    left: 0;
    padding: 0 10px 0 14px;
    text-align: right;
    color: var(--text-muted);
    background-color: var(--bg-secondary);
    border-right: 1px solid var(--border-color);
    user-select: none;
  }

  .line {
    padding: 0 14px;
    white-space: pre;
  }

  .code.wrap .line {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  /* highlight.js tokens, GitHub colors. Scoped to the viewer so the
     transcript's code blocks keep their own look. */
  .source :global(.hljs-keyword),
  .source :global(.hljs-doctag),
  .source :global(.hljs-meta .hljs-keyword),
  .source :global(.hljs-template-tag),
  .source :global(.hljs-template-variable),
  .source :global(.hljs-type),
  .source :global(.hljs-variable.language_) {
    color: #cf222e;
  }
  .source :global(.hljs-title),
  .source :global(.hljs-title.class_),
  .source :global(.hljs-title.function_) {
    color: #8250df;
  }
  .source :global(.hljs-attr),
  .source :global(.hljs-attribute),
  .source :global(.hljs-literal),
  .source :global(.hljs-meta),
  .source :global(.hljs-number),
  .source :global(.hljs-operator),
  .source :global(.hljs-variable),
  .source :global(.hljs-selector-attr),
  .source :global(.hljs-selector-class),
  .source :global(.hljs-selector-id),
  .source :global(.hljs-property) {
    color: #0550ae;
  }
  .source :global(.hljs-regexp),
  .source :global(.hljs-string),
  .source :global(.hljs-meta .hljs-string) {
    color: #0a3069;
  }
  .source :global(.hljs-built_in),
  .source :global(.hljs-symbol),
  .source :global(.hljs-params) {
    color: #953800;
  }
  .source :global(.hljs-comment),
  .source :global(.hljs-code),
  .source :global(.hljs-formula) {
    color: #6e7781;
    font-style: italic;
  }
  .source :global(.hljs-name),
  .source :global(.hljs-quote),
  .source :global(.hljs-selector-tag),
  .source :global(.hljs-selector-pseudo),
  .source :global(.hljs-tag) {
    color: #116329;
  }
  .source :global(.hljs-subst) {
    color: inherit;
  }
  .source :global(.hljs-section) {
    color: #0550ae;
    font-weight: bold;
  }
  .source :global(.hljs-bullet) {
    color: #953800;
  }
  .source :global(.hljs-emphasis) {
    font-style: italic;
  }
  .source :global(.hljs-strong) {
    font-weight: bold;
  }
  .source :global(.hljs-addition) {
    color: #116329;
    background-color: #dafbe1;
  }
  .source :global(.hljs-deletion) {
    color: #82071e;
    background-color: #ffebe9;
  }

  :global([data-theme='dark']) .source :global(.hljs-keyword),
  :global([data-theme='dark']) .source :global(.hljs-doctag),
  :global([data-theme='dark']) .source :global(.hljs-meta .hljs-keyword),
  :global([data-theme='dark']) .source :global(.hljs-template-tag),
  :global([data-theme='dark']) .source :global(.hljs-template-variable),
  :global([data-theme='dark']) .source :global(.hljs-type),
  :global([data-theme='dark']) .source :global(.hljs-variable.language_) {
    color: #ff7b72;
  }
  :global([data-theme='dark']) .source :global(.hljs-title),
  :global([data-theme='dark']) .source :global(.hljs-title.class_),
  :global([data-theme='dark']) .source :global(.hljs-title.function_) {
    color: #d2a8ff;
  }
  :global([data-theme='dark']) .source :global(.hljs-attr),
  :global([data-theme='dark']) .source :global(.hljs-attribute),
  :global([data-theme='dark']) .source :global(.hljs-literal),
  :global([data-theme='dark']) .source :global(.hljs-meta),
  :global([data-theme='dark']) .source :global(.hljs-number),
  :global([data-theme='dark']) .source :global(.hljs-operator),
  :global([data-theme='dark']) .source :global(.hljs-variable),
  :global([data-theme='dark']) .source :global(.hljs-selector-attr),
  :global([data-theme='dark']) .source :global(.hljs-selector-class),
  :global([data-theme='dark']) .source :global(.hljs-selector-id),
  :global([data-theme='dark']) .source :global(.hljs-property),
  :global([data-theme='dark']) .source :global(.hljs-section) {
    color: #79c0ff;
  }
  :global([data-theme='dark']) .source :global(.hljs-regexp),
  :global([data-theme='dark']) .source :global(.hljs-string),
  :global([data-theme='dark']) .source :global(.hljs-meta .hljs-string) {
    color: #a5d6ff;
  }
  :global([data-theme='dark']) .source :global(.hljs-built_in),
  :global([data-theme='dark']) .source :global(.hljs-symbol),
  :global([data-theme='dark']) .source :global(.hljs-params),
  :global([data-theme='dark']) .source :global(.hljs-bullet) {
    color: #ffa657;
  }
  :global([data-theme='dark']) .source :global(.hljs-comment),
  :global([data-theme='dark']) .source :global(.hljs-code),
  :global([data-theme='dark']) .source :global(.hljs-formula) {
    color: #8b949e;
  }
  :global([data-theme='dark']) .source :global(.hljs-name),
  :global([data-theme='dark']) .source :global(.hljs-quote),
  :global([data-theme='dark']) .source :global(.hljs-selector-tag),
  :global([data-theme='dark']) .source :global(.hljs-selector-pseudo),
  :global([data-theme='dark']) .source :global(.hljs-tag) {
    color: #7ee787;
  }
  :global([data-theme='dark']) .source :global(.hljs-addition) {
    color: #aff5b4;
    background-color: #033a16;
  }
  :global([data-theme='dark']) .source :global(.hljs-deletion) {
    color: #ffdcd7;
    background-color: #67060c;
  }

  .state {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px;
    font-size: 0.82rem;
    color: var(--text-secondary);
  }

  .state.center {
    flex: 1;
    justify-content: center;
    text-align: center;
  }

  .state.error {
    color: #ef4444;
  }
</style>
