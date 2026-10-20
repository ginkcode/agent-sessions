<script lang="ts">
  import { files } from '../../../stores/files.svelte';
  import { TAB_CLOSE_ACTIONS, tabsToClose } from '../../../tabActions';
  import FileIcon from '../FileIcon.svelte';
  import ContextMenu from '../ContextMenu.svelte';

  let { presentation, onContext }: { presentation: boolean; onContext: () => void } = $props();
  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  const closeShortcut = isMac ? '⌘W' : 'Ctrl+W';
  let tabContext = $state<{ path: string; x: number; y: number } | null>(null);
  let tabItems = $derived.by(() => {
    if (!tabContext) return [];
    const { path } = tabContext;
    return TAB_CLOSE_ACTIONS.map(({ action, label }) => {
      const targets = tabsToClose(files.tabs.map((t) => t.path), path, action);
      return { label, disabled: targets.length === 0, onSelect: () => targets.forEach((p) => files.closeTab(p)) };
    });
  });

  $effect(() => {
    if (!files.open) tabContext = null;
  });

  export function closeContext() {
    tabContext = null;
  }

  function fileStatus(path: string) {
    return files.listing?.status?.[path];
  }

  function splitPath(path: string) {
    return { name: path.slice(path.lastIndexOf('/') + 1) };
  }

  function closeTab(e: MouseEvent, path: string) {
    e.stopPropagation();
    files.closeTab(path);
  }
</script>

{#if tabContext}
  <ContextMenu x={tabContext.x} y={tabContext.y} items={tabItems} ariaLabel="File tab actions" onClose={closeContext} />
{/if}
{#if files.tabs.length > 0}
  <div class="tab-bar" class:presentation role="tablist" aria-label="Open files">
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
        oncontextmenu={(e) => {
          e.preventDefault();
          e.stopPropagation();
          onContext();
          tabContext = { path: tab.path, x: e.clientX, y: e.clientY };
        }}
        onauxclick={(e) => e.button === 1 && closeTab(e, tab.path)}
        onkeydown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            files.selectTab(tab.path);
          }
        }}
      >
        <FileIcon path={tab.path} />
        <span class="tab-name git-{st ?? 'none'}">{splitPath(tab.path).name}</span>
        <button
          type="button"
          class="tab-close"
          aria-label="Close {splitPath(tab.path).name}"
          title="Close ({closeShortcut})"
          onclick={(e) => closeTab(e, tab.path)}>×</button
        >
      </div>
    {/each}
  </div>
{/if}

<style>
  .tab-bar.presentation { display: none; }
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
</style>
