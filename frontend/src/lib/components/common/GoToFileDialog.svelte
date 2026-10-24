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
  import { onMount, untrack } from 'svelte';
  import { codeDiagnostics } from '../../stores/codeDiagnostics.svelte';
  import { connectionStore } from '../../stores/connection.svelte';
  import CodeChecks from './go-to-file/CodeChecks.svelte';
  import ProblemsPanel from './go-to-file/ProblemsPanel.svelte';
  import { contentSearch } from '../../stores/contentSearch.svelte';
  import { gitChanges } from '../../stores/gitChanges.svelte';
  import { isChangesShortcut } from '../../gitChanges';
  import { isReferenceMatch } from '../../referenceResults';
  import { isSearchShortcut, matchRange, matchedText, searchSeed } from '../../contentSearch';
  import type { AgentID, SearchMatch } from '../../types';
  import { newSession } from '../../stores/newSession.svelte';
  import { newSessionMenuItems } from '../../newSession';
  import { isNewSessionShortcut } from '../../terminal';

  $effect(() => {
    const open = files.open;
    void files.dir; void files.listing; void link.dataHost; void connectionStore.generation;
    untrack(() => { if (open) void gitChanges.refresh(); });
  });
  onMount(() => {
    const refresh = () => { if (files.open) void gitChanges.refresh(); };
    window.addEventListener('focus', refresh);
    return () => window.removeEventListener('focus', refresh);
  });

  // Track routing, not diagnostics mutations. Discovery never delays picker rendering.
  $effect(() => {
    const open = files.open, dir = files.dir;
    void link.dataHost; void link.host; void link.phase; void connectionStore.generation;
    untrack(() => {
      if (open && dir && !link.blockedReason) void codeDiagnostics.show(dir);
      else void codeDiagnostics.hide();
    });
    return () => { untrack(() => { void codeDiagnostics.hide(); }); };
  });
  import FilePrompt from './go-to-file/FilePrompt.svelte';
  import RenamePrompt from './go-to-file/RenamePrompt.svelte';
  let renamePath = $state<string | null>(null);
  import NewFolderPrompt from './go-to-file/NewFolderPrompt.svelte';
  let newEntry = $state<{ path: string; kind: 'folder' | 'file' } | null>(null);
  let checksBlocked = $derived(!!link.blockedReason || fileActions.busy || fileActions.quitting || renamePath !== null || newEntry !== null);
  import { fileActions } from '../../stores/fileActions.svelte';
  import { keyHandled } from '../../fullscreen';
  import { tabShortcut } from '../../tabActions';

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
  let problems: ProblemsPanel | undefined = $state();
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
      // The Search pane focuses its own field.
      if (untrack(() => contentSearch.dialogOpened()) === 'files') queueMicrotask(() => input?.focus());
    } else if (!open && wasOpen) {
      untrack(() => contentSearch.dialogClosed());
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
    if (directory && !path) void newSession.refresh();
  }

  /**
   * Starts a new agent session in the project. The panel it opens in sits
   * behind this dialog, so the dialog closes for it.
   */
  async function startNewSession(agent: AgentID) {
    const dir = files.dir;
    if (!dir) return;
    const inPanel = newSession.where === 'panel';
    if ((await newSession.start(agent, dir)) && inPanel && files.dir === dir) close();
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
        await api.revealFile(dir, path || '.');
      } else if (action === 'copy') {
        await api.copyFile(dir, path || '.');
        toast.show({ title: 'Copied', body: `${path || dir} is ready to paste in your file manager.` });
      } else {
        const saved = await api.downloadFile(dir, path || '.');
        if (saved) toast.show({ title: 'Downloaded', body: saved });
      }
    } catch (err) {
      toast.show({ title: 'File action failed', body: err instanceof Error ? err.message : String(err), tone: 'error' });
    } finally {
      actionBusy = false;
    }
  }

  async function addFile(path: string) {
    const dir = files.dir;
    const host = link.dataHost;
    if (!dir || actionBusy || link.locked || link.blockedReason) return;
    actionBusy = true;
    try {
      if (!await api.addFile(dir, path, host ?? '')) return;
      toast.show({ title: host ? 'File uploaded' : 'File added', body: path || dir });
      // A native picker or remote transfer can outlive the current project.
      if (path && files.dir === dir && link.dataHost === host) files.setExpanded(new Set([...files.expanded, path]));
      await files.listingsChanged(dir, host);
    } catch (err) {
      toast.show({ title: host ? 'Upload failed' : 'Could not add file', body: err instanceof Error ? err.message : String(err), tone: 'error' });
      void files.listingsChanged(dir, host); // A failed transfer may have written part of a file.
    } finally {
      actionBusy = false;
    }
  }

  async function deleteItem(path: string, directory: boolean) {
    try {
      if (await fileActions.delete(path, directory)) toast.show({ title: 'Deleted permanently', body: path });
    } catch (err) {
      toast.show({ title: 'Could not delete item', body: err instanceof Error ? err.message : String(err), tone: 'error' });
    }
  }

  let contextItems = $derived.by((): ContextMenuItem[] => {
    if (!context) return [];
    const { path, directory } = context;
    const remote = Boolean(link.dataHost);
    const disabled = actionBusy || fileActions.busy || fileActions.quitting || link.locked || fileStatus(path) === 'D';
    const unsafe = Boolean(link.blockedReason || files.renameUncertain || files.deleteUncertain);
    const why = files.deleteUncertain || files.renameUncertain || link.blockedReason;
    // Create, then the file manager, then paths; renaming and deleting come last.
    const create: ContextMenuItem[] = [], open: ContextMenuItem[] = [], copy: ContextMenuItem[] = [], change: ContextMenuItem[] = [];
    // The project itself can start agent sessions; its subdirectories are
    // not projects of their own.
    const start = directory && !path && newSession.available
      ? newSessionMenuItems(newSession.choices, (agent) => void startNewSession(agent), { disabled: newSession.starting ? 'Starting a session…' : undefined })
      : [];
    if (directory) create.push({
      label: 'New File…',
      disabled: disabled || unsafe,
      title: why || 'Create an empty file in this directory',
      onSelect: () => { newEntry = { path, kind: 'file' }; },
    });
    if (directory) create.push({
      label: 'New Folder…',
      disabled: disabled || unsafe,
      title: why || 'Create a folder in this directory',
      onSelect: () => { newEntry = { path, kind: 'folder' }; },
    });
    if (directory) create.push({
      label: remote ? 'Upload File…' : 'Add File…',
      disabled: disabled || Boolean(link.blockedReason),
      title: link.blockedReason || (remote ? 'Choose a local file to upload into this directory' : 'Choose a local file to copy into this directory'),
      onSelect: () => void addFile(path),
    });
    if (!remote) open.push({ label: 'Reveal', disabled, onSelect: () => void fileAction('reveal', path) });
    open.push({
      label: remote ? 'Download' : 'Copy', disabled,
      title: remote && directory ? 'Download this directory as a ZIP archive' : undefined,
      onSelect: () => void fileAction(remote ? 'download' : 'copy', path),
    });
    copy.push({ label: 'Copy Path', disabled: actionBusy, onSelect: () => void fileAction('absolute', path) });
    if (path) copy.push({ label: 'Copy Relative Path', disabled: actionBusy, onSelect: () => void fileAction('relative', path) });
    if (path) change.push({
      label: 'Rename…',
      disabled: disabled || unsafe,
      title: why || 'Rename without losing open drafts',
      onSelect: () => { renamePath = path; },
    });
    if (path) change.push({
      label: 'Delete…',
      disabled: disabled || unsafe,
      title: why || 'Permanently delete this item',
      onSelect: () => void deleteItem(path, directory),
    });
    return [start, create, open, copy, change].filter(group => group.length)
      .flatMap((group, i) => group.map((item, j) => (i && !j ? { ...item, separator: true } : item)));
  });

  function close() {
    if (fileActions.busy || fileActions.quitting || renamePath !== null || newEntry !== null) return;
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

  function openMatch(path: string, match: SearchMatch, keep = false) {
    if (isReferenceMatch(match)) viewer?.navigateReference(match.location, keep);
    else viewer?.navigateMatch(path, matchRange(match), matchedText(match), keep);
  }

  /** F4 and Shift+F4: the next and previous search match, in any file. */
  function stepMatch(direction: 1 | -1) {
    const ref = contentSearch.step(direction);
    const match = contentSearch.match(ref);
    if (ref && match) openMatch(ref.path, match);
  }

  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);
  function isCloseShortcut(e: KeyboardEvent) {
    return (isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey && !e.shiftKey && e.code === 'KeyW';
  }
</script>

<svelte:window onkeydown={(e) => {
  if (!files.open || fileActions.busy || fileActions.quitting || renamePath !== null || newEntry !== null) return;
  if (e.ctrlKey && e.shiftKey && !e.metaKey && !e.altKey && e.code === 'KeyL') {
    e.preventDefault();
    e.stopPropagation();
    if (!e.repeat) locked = !locked;
    return;
  }
  // Ctrl+Shift+M shows or hides the problems, like Ctrl+` for the terminal.
  if (e.ctrlKey && e.shiftKey && !e.metaKey && !e.altKey && e.code === 'KeyM' && problems) {
    e.preventDefault();
    e.stopPropagation();
    if (!e.repeat) problems.toggle();
    return;
  }
  if (isChangesShortcut(e, isMac)) {
    e.preventDefault(); e.stopPropagation();
    if (!e.repeat) { contentSearch.setMode('changes'); void gitChanges.refresh(); }
    return;
  }
  // Cmd+Shift+F on macOS, Ctrl+Shift+F elsewhere: searches the project's
  // files, starting from the selected text.
  if (isSearchShortcut(e, isMac)) {
    e.preventDefault();
    e.stopPropagation();
    if (!e.repeat) contentSearch.open(searchSeed(viewer?.selectedText() ?? ''));
    return;
  }
  // Cmd+Shift+A on macOS, Ctrl+Shift+A elsewhere: a new session of the
  // agent last started, in this project.
  if (isNewSessionShortcut(e, isMac) && newSession.available) {
    e.preventDefault();
    e.stopPropagation();
    if (!e.repeat) void startNewSession(newSession.defaultAgent);
    return;
  }
  if (e.key === 'F4' && !e.metaKey && !e.ctrlKey && !e.altKey && contentSearch.result?.matches) {
    e.preventDefault();
    stepMatch(e.shiftKey ? -1 : 1);
    return;
  }
  if (viewer?.handleKey(e)) return;
  // Cmd+1–9 on macOS, Ctrl+1–9 elsewhere: selects an open file tab.
  const tabNumber = tabShortcut(e, isMac);
  if (tabNumber !== null) {
    if (files.selectNth(tabNumber)) e.preventDefault();
    return;
  }
  if (e.key === 'Escape' && !keyHandled(e)) {
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
{#if renamePath !== null}
  <RenamePrompt path={renamePath} onClose={() => (renamePath = null)} />
{/if}
{#if newEntry !== null}
  <NewFolderPrompt path={newEntry.path} kind={newEntry.kind} onClose={() => (newEntry = null)} />
{/if}

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
      <button
        type="button"
        class="root"
        title={files.dir}
        aria-label="Project directory actions"
        aria-haspopup="menu"
        oncontextmenu={(e) => openContext(e, '', true)}
        onclick={(e) => openContext(e, '', true)}
      >{directoryName(files.dir ?? '')}</button>
      {#if link.dataHost}
        <span class="host-pill">{hostLabel(link.dataHost)}</span>
      {/if}
      <button type="button" class="close-button" aria-label="Close" title="Close" onclick={close}>×</button>
    </header>
    <div class="body">
      <FilePicker bind:this={picker} bind:input {presentation} onPick={pick} onContext={openContext} onOpenMatch={openMatch} />
      <FileViewer bind:this={viewer} bind:presentation bind:presentationFontSize>
        {#snippet tabs()}
          <FileTabs bind:this={fileTabs} {presentation} onContext={() => (context = null)} />
        {/snippet}
      </FileViewer>
    </div>
    {#if files.open && !presentation}
      <!-- Compact status only; details, approval and setup stay behind disclosure. -->
      <ProblemsPanel bind:this={problems} onnavigate={(diagnostic) => viewer?.navigateProblem(diagnostic)} blocked={checksBlocked}>
        {#snippet settings()}
          <CodeChecks blocked={checksBlocked} />
        {/snippet}
      </ProblemsPanel>
    {/if}
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
    cursor: pointer;
    text-align: left;
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
