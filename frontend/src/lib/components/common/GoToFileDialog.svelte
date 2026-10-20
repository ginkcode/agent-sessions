<script lang="ts">
  import { files } from '../../stores/files.svelte';
  import { link } from '../../stores/link.svelte';
  import { hostLabel } from '../../hosts';
  import { copyToClipboard } from '../../portable';
  import { directoryName } from '../../tree';
  import ContextMenu, { type ContextMenuItem } from './ContextMenu.svelte';
  import { api } from '../../api';
  import { toast } from '../../stores/toast.svelte';
  import { absoluteFilePath } from '../../fileActions';
  import FilePicker from './go-to-file/FilePicker.svelte';
  import FileTabs from './go-to-file/FileTabs.svelte';
  import FileViewer from './go-to-file/FileViewer.svelte';
  import { onMount } from 'svelte';
  import FilePrompt from './go-to-file/FilePrompt.svelte';
  import { fileActions } from '../../stores/fileActions.svelte';

  onMount(() => {
    const off = api.onEvent('files:close-request', (id: number) => {
      if (Number.isSafeInteger(id) && id > 0) void fileActions.nativeClose(id);
    });
    const unload = (e: BeforeUnloadEvent) => {
      if (files.hasUnsaved) { e.preventDefault(); e.returnValue = ''; }
    };
    window.addEventListener('beforeunload', unload);
    return () => { off(); window.removeEventListener('beforeunload', unload); };
  });

  let locked = $state(false);
  let presentation = $state(false);
  let presentationFontSize = $state(20);
  let input: HTMLInputElement | undefined = $state();
  let picker: FilePicker | undefined = $state();
  let fileTabs: FileTabs | undefined = $state();
  let viewer: FileViewer | undefined = $state();
  let restoreFocus: HTMLElement | null = null;

  function fileStatus(path: string) {
    return files.listing?.status?.[path];
  }

  // Opening the dialog: start empty, focus the search box, and remember what
  // had focus to give it back on close.
  let wasOpen = false;
  $effect(() => {
    const open = files.open;
    if (open && !wasOpen) {
      restoreFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      picker?.reset();
      queueMicrotask(() => input?.focus());
    } else if (!open && wasOpen) {
      restoreFocus?.focus();
      restoreFocus = null;
    }
    wasOpen = open;
  });

  let context = $state<{ path: string; directory: boolean; x: number; y: number } | null>(null);
  let actionBusy = $state(false);

  $effect(() => {
    void files.dir;
    void files.open;
    void link.dataHost;
    context = null;
  });

  function openContext(e: MouseEvent, path: string, directory: boolean) {
    e.preventDefault();
    e.stopPropagation();
    fileTabs?.closeContext();
    context = { path, directory, x: e.clientX, y: e.clientY };
  }

  async function fileAction(action: 'reveal' | 'copy' | 'download' | 'absolute' | 'relative', path: string) {
    const dir = files.dir;
    if (!dir || actionBusy) return;
    if (action !== 'absolute' && action !== 'relative' && link.blockedReason) {
      toast.show({ title: 'File action unavailable', body: link.blockedReason, tone: 'error' });
      return;
    }
    actionBusy = true;
    try {
      if (action === 'absolute' || action === 'relative') {
        const value = action === 'absolute' ? absoluteFilePath(dir, path) : path;
        if (!await copyToClipboard(value)) throw new Error('Could not write to the clipboard.');
        toast.show({ title: 'Path copied', body: value });
      } else if (action === 'reveal') {
        await api.revealFile(dir, path);
      } else if (action === 'copy') {
        await api.copyFile(dir, path);
        toast.show({ title: 'Copied', body: `${path} is ready to paste in your file manager.` });
      } else {
        const saved = await api.downloadFile(dir, path);
        if (saved) toast.show({ title: 'Downloaded', body: saved });
      }
    } catch (err) {
      toast.show({ title: 'File action failed', body: err instanceof Error ? err.message : String(err), tone: 'error' });
    } finally {
      actionBusy = false;
    }
  }

  let contextItems = $derived.by((): ContextMenuItem[] => {
    if (!context) return [];
    const { path, directory } = context;
    const remote = Boolean(link.dataHost);
    const disabled = actionBusy || link.locked || fileStatus(path) === 'D';
    const items: ContextMenuItem[] = [];
    if (!remote) items.push({ label: 'Reveal', disabled, onSelect: () => void fileAction('reveal', path) });
    items.push({
      label: remote ? 'Download' : 'Copy', disabled,
      title: remote && directory ? 'Download this directory as a ZIP archive' : undefined,
      onSelect: () => void fileAction(remote ? 'download' : 'copy', path),
    });
    items.push(
      { label: 'Copy Path', disabled: actionBusy, onSelect: () => void fileAction('absolute', path) },
      { label: 'Copy Relative Path', disabled: actionBusy, onSelect: () => void fileAction('relative', path) },
    );
    return items;
  });

  function close() {
    if (fileActions.busy || fileActions.quitting) return;
    presentation = false;
    fileTabs?.closeContext();
    context = null;
    files.close();
  }

  // One click shows the file in the preview tab, which the next file
  // replaces; a double click (or Enter in the search) keeps it in a tab.
  function pick(path: string, keep = false) {
    // A deleted file has nothing on disk to read.
    files.openFile(path, { keep, read: fileStatus(path) !== 'D' });
  }

  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  function isCloseShortcut(e: KeyboardEvent) {
    return (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey && !e.shiftKey && e.code === 'KeyW';
  }
</script>

<svelte:window onkeydown={(e) => {
  if (!files.open || fileActions.busy || fileActions.quitting) return;
  if (e.ctrlKey && e.shiftKey && !e.metaKey && !e.altKey && e.code === 'KeyL') {
    e.preventDefault();
    e.stopPropagation();
    if (!e.repeat) locked = !locked;
    return;
  }
  if (viewer?.handleKey(e)) return;
  if (e.key === 'Escape' && !e.defaultPrevented) {
    e.preventDefault();
    e.stopPropagation();
    if (presentation) presentation = false;
    else if (!locked) close();
  } else if (isCloseShortcut(e)) {
    e.preventDefault();
    if (files.selected) void fileActions.closeTabs([files.selected]);
  }
}} />

<FilePrompt />

<!-- Keep the viewer mounted so preview preferences and tab scroll positions survive closing. -->
<div class="dialog-backdrop" hidden={!files.open} role="presentation">
  {#if context}
    <ContextMenu x={context.x} y={context.y} items={contextItems} ariaLabel="File actions" onClose={() => (context = null)} />
  {/if}
  <div class="dialog" class:presentation role="dialog" aria-modal="true" aria-labelledby="goto-file-title">
    <header class="dialog-header">
      <h2 id="goto-file-title">Go to File</h2>
      <button
        type="button"
        class="lock-button"
        class:locked
        aria-label="Lock dialog against Escape"
        aria-pressed={locked}
        aria-keyshortcuts="Control+Shift+L"
        title={locked ? 'Unlock dialog (Ctrl+Shift+L; Escape can close it)' : 'Lock dialog (Ctrl+Shift+L; prevent Escape from closing it)'}
        onclick={() => (locked = !locked)}
      >
        <svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">
          <rect x="3.5" y="7" width="9" height="7" rx="1.5" />
          {#if locked}
            <path d="M5 7V4a3 3 0 0 1 6 0v3" />
          {:else}
            <path d="M5 7V4a3 3 0 0 1 6 0" />
          {/if}
          <path d="M8 10v1" />
        </svg>
      </button>
      <span class="root" title={files.dir}>{directoryName(files.dir ?? '')}</span>
      {#if link.dataHost}
        <span class="host-pill">{hostLabel(link.dataHost)}</span>
      {/if}
      <button type="button" class="close-button" aria-label="Close" title="Close" onclick={close}>×</button>
    </header>
    <div class="body">
      <FilePicker bind:this={picker} bind:input {presentation} onPick={pick} onContext={openContext} />
      <FileViewer bind:this={viewer} bind:presentation bind:presentationFontSize>
        {#snippet tabs()}
          <FileTabs bind:this={fileTabs} {presentation} onContext={() => (context = null)} />
        {/snippet}
      </FileViewer>
    </div>
  </div>
</div>

<style>
  .dialog-backdrop[hidden] {
    display: none;
  }

  .presentation .dialog-header {
    display: none;
  }

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

  .lock-button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .lock-button:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .lock-button.locked {
    color: var(--accent-color);
    background-color: var(--active-bg);
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
</style>
