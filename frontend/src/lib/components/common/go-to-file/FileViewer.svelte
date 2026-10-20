<script lang="ts">
  import { onDestroy, type Snippet } from 'svelte';
  import { files } from '../../../stores/files.svelte';
  import { highlightFile, splitHighlightedLines, splitLines } from '../../../codeHighlight';
  import { copyToClipboard } from '../../../portable';
  import { findCount } from '../../../fileFind';
  import { diffRows, fileChanges, fileLanguage, filePreview, isImageFile, lineMarks, type DiffRow } from '../../../files';
  import type { GitStatus } from '../../../types';
  import { toast } from '../../../stores/toast.svelte';
  import { markdownFileTarget } from '../../../fileActions';
  import { api } from '../../../api';
  import { epoch } from '../../../link';
  import { errorText } from '../../../manage';
  import { link } from '../../../stores/link.svelte';
  import { connectionStore } from '../../../stores/connection.svelte';
  import { fileActions } from '../../../stores/fileActions.svelte';
  import { canEditContent } from '../../../fileEditing';
  import type { FileContent, FileEditReason } from '../../../types';
  import LoadingSpinner from '../LoadingSpinner.svelte';
  import MarkdownDoc from '../MarkdownDoc.svelte';
  import SourceView from './SourceView.svelte';
  import FileEditor from './FileEditor.svelte';
  import { createImagePreview } from './imagePreview.svelte';
  import { createFileFind } from './fileFind.svelte';

  let {
    tabs,
    presentation = $bindable(false),
    presentationFontSize = $bindable(20),
  }: {
    tabs?: Snippet;
    presentation?: boolean;
    presentationFontSize?: number;
  } = $props();

  let status = $derived(files.listing?.status);


  // Files larger than this are shown without highlighting, which is slow on
  // very long text.
  const HIGHLIGHT_LIMIT = 500_000;

  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);

  let previewOn = $state(false);

  // Editing. A tab in edit mode shows the editor in place of every viewer
  // state, so a draft stays reachable even when the listing says deleted.
  let activeTab = $derived(files.activeTab);
  let edit = $derived(activeTab?.edit);
  let editing = $derived(Boolean(edit?.editing));
  let editor: FileEditor | undefined = $state();
  let starting = $state(false);
  let frozen = $derived(fileActions.busy || fileActions.quitting);
  let canSaveHost = $derived(connectionStore.capabilities.saveFile === true);
  let blocked = $derived(link.blockedReason);
  // Text the viewer can show as text; only then does a read-only reason matter.
  let textContent = $derived(isTextContent(files.content));
  let canEdit = $derived(
    !editing && !starting && !frozen && canSaveHost && !blocked
      && !files.loadingFile && !files.fileError && canEditContent(files.content)
  );
  let readOnlyReason = $derived(
    editing || !textContent ? ''
      : files.content?.edit?.eligible === false ? readOnlyText(files.content.edit.reason)
      : !canSaveHost && files.content?.edit?.eligible ? 'Read-only: this host cannot save files'
      : ''
  );
  let canSave = $derived(
    Boolean(edit?.editing && edit.dirty) && !edit?.saving && !edit?.uncertain && !frozen && canSaveHost && !blocked
  );

  function isTextContent(content: FileContent | null): boolean {
    return !!content && typeof content.text === 'string' && !content.binary && !content.tooLarge && !content.image;
  }

  function readOnlyText(reason: FileEditReason | undefined): string {
    switch (reason) {
      case 'too_large': return 'Read-only: too large to edit';
      case 'unsupported_newlines': return 'Read-only: mixed line endings';
      case 'not_checked': return 'Read-only: editing not available';
      case 'protected': return 'Read-only: protected file';
      case 'linked': return 'Read-only: linked file';
      case 'read_only': return 'Read-only file';
      default: return 'Read-only';
    }
  }

  async function startEdit() {
    const tab = files.activeTab;
    if (!tab || !canEdit) return;
    const dir = files.dir;
    const host = link.dataHost;
    const started = epoch();
    starting = true;
    try {
      // The native close guard must be armed before an editable document exists.
      if (!(await api.enableFileEditing())) {
        toast.show({ title: 'Cannot edit now', body: 'The window is closing.', tone: 'error' });
        return;
      }
      if (files.activeTab !== tab || files.dir !== dir || link.dataHost !== host || epoch() !== started) return;
      if (fileActions.busy || fileActions.quitting) return;
      previewOn = false;
      presentation = false;
      find.closeFind();
      if (!files.startEditing(tab.path)) {
        toast.show({ title: 'Cannot edit this file', body: link.blockedReason ?? 'This file cannot be edited.', tone: 'error' });
      }
    } catch (err) {
      toast.show({ title: 'Cannot edit this file', body: errorText(err, 'Could not start editing.'), tone: 'error' });
    } finally {
      starting = false;
    }
  }

  function save() {
    const tab = files.activeTab;
    if (tab?.edit && canSave) void fileActions.save(tab);
  }

  function resolveConflict() {
    const tab = files.activeTab;
    if (tab?.edit?.conflict && !frozen && !tab.edit.saving) void fileActions.resolveConflict(tab);
  }

  function discard() {
    const tab = files.activeTab;
    if (tab?.edit) void fileActions.discard(tab);
  }

  function done() {
    const tab = files.activeTab;
    if (tab?.edit) void fileActions.done(tab);
  }

  // Presentation is read-only.
  $effect(() => {
    if (editing && presentation) presentation = false;
  });

  let language = $derived(files.selected ? fileLanguage(files.selected) : null);
  let previewKind = $derived(files.selected ? filePreview(files.selected) : null);
  let showMarkdown = $derived(previewOn && !editing && previewKind === 'markdown');
  let showSvg = $derived(previewOn && !editing && previewKind === 'svg');
  // Rendered rather than source: the source-only tools hide.
  let showPreview = $derived(showMarkdown || showSvg);
  let text = $derived(files.content?.text ?? '');
  // Drawn as an image, so scripts in the SVG never run.
  let svgSrc = $derived(showSvg && text ? `data:image/svg+xml;charset=utf-8,${encodeURIComponent(text)}` : '');
  // A PNG, JPEG and the like, which the backend sends as data.
  let rasterSrc = $derived(
    files.content?.image && files.content.data && files.selected && isImageFile(files.selected)
      ? `data:${files.content.image};base64,${files.content.data}`
      : ''
  );
  let imageSrc = $derived(svgSrc || rasterSrc);
  let showImage = $derived(Boolean(imageSrc) && !files.loadingFile && fileStatus(files.selected ?? '') !== 'D');

  const image = createImagePreview({
    getSource: () => imageSrc,
    getSvgSource: () => svgSrc,
    getText: () => text,
    getScroller: () => codeScroll,
    isShown: () => showImage,
  });

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
  // Git markers describe the saved file, so the diff never shows over a draft.
  let showDiff = $derived(files.diff && hasChanges && !editing);
  let rows: DiffRow[] = $derived(
    showDiff
      ? diffRows(changes, plainLines.length)
      : Array.from({ length: htmlLines?.length ?? plainLines.length }, (_, index) => ({ kind: 'line', index, mark: marks[index] }))
  );

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

  // One click shows the file in the preview tab, which the next file
  // replaces; a double click (or Enter in the search) keeps it in a tab.
  function pick(path: string, keep = false) {
    // A deleted file has nothing on disk to read.
    files.openFile(path, { keep, read: fileStatus(path) !== 'D' });
  }

  function followFileLink(href: string) {
    if (!files.selected) return;
    const target = markdownFileTarget(files.selected, href);
    if (!target) {
      toast.show({ title: 'Cannot open this link', body: 'The link must point to a file inside this project.', tone: 'error' });
      return;
    }
    // Keep the referring document so the reader can return to it via its tab.
    files.keepTab(files.selected);
    pick(target, true);
  }

  // Each tab keeps its scroll position. A plain map: saving it on every
  // scroll must not rerun the effect that restores it.
  const scrollTops = new Map<string, number>();
  let codeScroll: HTMLElement | undefined = $state();
  $effect(() => {
    const el = codeScroll;
    const path = files.selected;
    const mode = showPreview ? 'preview' : 'source';
    if (el && path) el.scrollTop = scrollTops.get(`${path}:${mode}`) ?? 0;
  });

  const findShortcut = isMac ? '⌘F' : 'Ctrl+F';
  const saveShortcut = isMac ? '⌘S' : 'Ctrl+S';
  let contentShown = $derived(
    files.open && !editing && !files.loadingFile && !files.fileError && Boolean(text) && fileStatus(files.selected ?? '') !== 'D'
  );
  const find = createFileFind({
    getScroller: () => codeScroll,
    isContentShown: () => contentShown,
    isSvgShown: () => showSvg,
    isMarkdownShown: () => showMarkdown,
    getRows: () => rows,
    getPlainLines: () => plainLines,
    getHtmlLines: () => htmlLines,
    getWrap: () => files.wrap,
    getPresentationFontSize: () => presentationFontSize,
  });

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

  /** Called by the parent only while the file dialog is open. */
  export function handleKey(e: KeyboardEvent): boolean {
    if (editing) {
      // Editor shortcuts (save, its own find, closing its search) come first;
      // the viewer's find never opens over the editor. An unconsumed Escape
      // falls through to the dialog, which keeps the draft in memory.
      if (editor?.handleKey(e)) return true;
      if (isWrapShortcut(e)) {
        e.preventDefault();
        files.toggleWrap();
        return true;
      }
      return false;
    }
    if (e.key === 'Escape') {
      if (e.defaultPrevented || !find.findActive) return false;
      e.preventDefault();
      e.stopPropagation();
      find.closeFind();
      return true;
    }
    const command = isMac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
    if (contentShown && !showSvg && command && !e.altKey && !e.shiftKey && e.code === 'KeyF') {
      e.preventDefault();
      find.openFind();
      return true;
    }
    if (isWrapShortcut(e)) {
      e.preventDefault();
      files.toggleWrap();
      return true;
    }
    return image.handleImageZoomKey(e);
  }

  onDestroy(() => {
    if (copyTimer) clearTimeout(copyTimer);
    if (pathCopyTimer) clearTimeout(pathCopyTimer);
  });
</script>

{#snippet zoomControls()}
  <button type="button" class="copy-btn" aria-label="Zoom out" title="Zoom out (-)" disabled={image.imgZoom <= image.MIN_ZOOM} onclick={() => image.zoomImage(image.imgZoom / image.ZOOM_STEP)}>−</button>
  <button type="button" class="zoom-level" title="Actual size" aria-label="Zoom {Math.round(image.imgZoom * 100)}%, show at actual size" onclick={() => image.zoomImage(1)}>{Math.round(image.imgZoom * 100)}%</button>
  <button type="button" class="copy-btn" aria-label="Zoom in" title="Zoom in (+)" disabled={image.imgZoom >= image.MAX_ZOOM} onclick={() => image.zoomImage(image.imgZoom * image.ZOOM_STEP)}>+</button>
  <button type="button" class="copy-btn" class:on={image.imgFitted} aria-pressed={image.imgFitted} title="Fit to view (0)" onclick={image.fitImage}>Fit</button>
{/snippet}

{#snippet imageView()}
  <div
    class="image-preview"
    class:dragging={image.imgDragging}
    role="presentation"
    onpointerdown={image.onImagePointerDown}
    onpointermove={image.onImagePointerMove}
    onpointerup={image.onImagePointerUp}
    onpointercancel={image.onImagePointerUp}
  >
    {#if image.imgFailed}
      <div class="state center">This image can't be shown.</div>
    {:else}
      <img bind:this={image.imgEl} src={imageSrc} alt={splitPath(files.selected ?? '').name} draggable="false" onload={image.onImageLoad} onerror={() => (image.imgFailed = true)} />
    {/if}
  </div>
{/snippet}

<section class="viewer" class:presentation aria-label="File content">
  {@render tabs?.()}
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
      {#if editing && edit}
        {#if edit.saving}
          <span class="edit-status" aria-live="polite">Saving…</span>
        {:else if edit.dirty}
          <span class="edit-status" aria-live="polite">Unsaved</span>
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
          class="copy-btn primary"
          title={blocked ?? (canSaveHost ? `Save (${saveShortcut})` : 'This host cannot save files.')}
          disabled={!canSave}
          onclick={save}
        >
          Save
        </button>
        <button type="button" class="copy-btn" title="Discard changes and reload from disk" disabled={frozen || edit.saving} onclick={discard}>Discard</button>
        <button type="button" class="copy-btn" title="Stop editing" disabled={frozen || edit.saving} onclick={done}>Done</button>
      {:else if files.content && !files.loadingFile && !files.fileError}
        {#if canSaveHost && canEditContent(files.content)}
          <button
            type="button"
            class="copy-btn"
            title={blocked ?? 'Edit this file'}
            disabled={!canEdit}
            onclick={startEdit}
          >
            {starting ? 'Opening…' : 'Edit'}
          </button>
        {:else if readOnlyReason}
          <span class="read-only" title={readOnlyReason}>{readOnlyReason}</span>
        {/if}
      {/if}
      {#if editing}
        <!-- Read-only tools hide while editing. -->
      {:else if text && !files.loadingFile}
        {#if previewKind}
          {@const kindLabel = previewKind === 'svg' ? 'SVG' : 'Markdown'}
          <button type="button" class="copy-btn" class:on={showPreview} aria-pressed={showPreview} title={showPreview ? `Show ${kindLabel} source` : `Preview ${kindLabel}`} onclick={() => (previewOn = !previewOn)}>Preview</button>
        {/if}
        {#if !showPreview}
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
        {/if}
        {#if showSvg}
          {@render zoomControls()}
        {:else}
        <button
          type="button"
          class="copy-btn"
          class:on={find.findActive}
          aria-pressed={find.findActive}
          title="Find in the file ({findShortcut})"
          onclick={() => (find.findActive ? find.closeFind() : find.openFind())}
        >
          Find
        </button>
        {/if}
        {#if !showPreview}
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
        {/if}
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
      {:else if showImage}
        {@render zoomControls()}
      {/if}
      {#if editing}
        <!-- Presentation is read-only. -->
      {:else}
      {#if presentation}
        <button type="button" class="copy-btn" aria-label="Decrease presentation font size" title="Decrease font size" disabled={presentationFontSize <= 12} onclick={() => (presentationFontSize -= 2)}>−</button>
        <span class="font-size" aria-live="polite">{presentationFontSize}px</span>
        <button type="button" class="copy-btn" aria-label="Increase presentation font size" title="Increase font size" disabled={presentationFontSize >= 48} onclick={() => (presentationFontSize += 2)}>+</button>
      {/if}
      <button
        type="button"
        class="copy-btn presentation-btn"
        class:on={presentation}
        aria-pressed={presentation}
        aria-label={presentation ? 'Exit presentation mode' : 'Presentation mode'}
        title={presentation ? 'Exit presentation mode (Escape)' : 'Presentation mode'}
        onclick={() => (presentation = !presentation)}
      >
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
          {#if presentation}
            <path d="M1 5h4V1m10 4h-4V1M1 11h4v4m10-4h-4v4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
          {:else}
            <path d="M6 2H2v4m8-4h4v4M2 10v4h4m8-4v4h-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
          {/if}
        </svg>
      </button>
      {/if}
    </div>
    {#if editing && edit && activeTab}
      {#if edit.saveError}
        <div class="edit-error" role="alert">
          <span>{edit.saveError}</span>
          {#if edit.conflict}
            <button type="button" class="copy-btn" title="Choose how to resolve the conflict" disabled={frozen || edit.saving || Boolean(blocked) || !canSaveHost} onclick={resolveConflict}>Resolve…</button>
          {/if}
          {#if edit.conflict || edit.uncertain}
            <button type="button" class="copy-btn" title="Discard changes and reload from disk" disabled={frozen || edit.saving} onclick={discard}>Reload</button>
          {/if}
        </div>
      {/if}
      <div class="code-area">
        <FileEditor
          bind:this={editor}
          id={activeTab}
          text={edit.draft}
          path={activeTab.path}
          version={edit.version}
          wrap={files.wrap}
          disabled={frozen}
          onchange={(next) => {
            if (!fileActions.busy && !fileActions.quitting) files.updateDraft(activeTab, next);
          }}
          onsave={save}
        />
      </div>
    {:else if fileStatus(files.selected) === 'D'}
      <div class="state center">This file was deleted since the last commit.</div>
    {:else if files.loadingFile}
      <div class="state center"><LoadingSpinner size={16} label="Loading file…" /></div>
    {:else if files.fileError}
      <div class="state center error" role="alert">{files.fileError}</div>
    {:else if rasterSrc}
      <div class="code-area">
        <div class="code-scroll" bind:this={codeScroll}>
          {@render imageView()}
        </div>
      </div>
    {:else if files.content?.tooLarge}
      <div class="state center">This file is too large to show ({formatSize(files.content.size)}).</div>
    {:else if files.content?.binary}
      <div class="state center">This is a binary file, so it can't be shown.</div>
    {:else if files.content && !text}
      <div class="state center">This file is empty.</div>
    {:else if files.content}
      <div class="code-area">
      {#if find.findActive}
        <div class="find-bar" role="search">
          <input
            bind:this={find.findInput}
            class="find-input"
            type="text"
            placeholder="Find"
            aria-label="Find in the file"
            value={find.findQuery}
            oninput={(e) => (find.findQuery = e.currentTarget.value)}
            onkeydown={find.handleFindKey}
            autocomplete="off"
            autocorrect="off"
            autocapitalize="off"
            spellcheck="false"
          />
          <button
            type="button"
            class="find-btn case"
            class:on={find.findCase}
            aria-pressed={find.findCase}
            aria-label="Match case"
            title="Match case"
            onclick={() => {
              find.findCase = !find.findCase;
              find.findInput?.focus();
            }}>Aa</button
          >
          <span class="find-count" class:none={find.findQuery && find.found.matches.length === 0} aria-live="polite">
            {find.findQuery ? findCount(find.found, find.findCurrent) : ''}
          </span>
          <button type="button" class="find-btn" aria-label="Previous match" title="Previous match (Shift+Enter)" disabled={find.found.matches.length === 0} onclick={() => find.stepMatch(-1)}>
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <path d="M8 13V3M3.5 7.5 8 3l4.5 4.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <button type="button" class="find-btn" aria-label="Next match" title="Next match (Enter)" disabled={find.found.matches.length === 0} onclick={() => find.stepMatch(1)}>
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <path d="M8 3v10M3.5 8.5 8 13l4.5-4.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          <button type="button" class="find-btn" aria-label="Close find" title="Close (Escape)" onclick={find.closeFind}>×</button>
        </div>
      {/if}
      <div
        class="code-scroll"
        bind:this={codeScroll}
        onscroll={(e) => {
          if (files.selected) scrollTops.set(`${files.selected}:${showPreview ? 'preview' : 'source'}`, e.currentTarget.scrollTop);
          if (find.findActive) find.schedulePaint();
        }}
      >
        {#if showMarkdown}
          <div class="markdown-preview" bind:this={find.previewEl} style:--markdown-font-size={presentation ? `${presentationFontSize}px` : '14px'}>
            <MarkdownDoc source={text} onFileLink={followFileLink} />
          </div>
        {:else if showSvg}
          {@render imageView()}
        {:else}
        <SourceView {rows} {htmlLines} {plainLines} {showDiff} {presentation} {presentationFontSize} bind:codeEl={find.codeEl} />
        {/if}
      </div>
      </div>
    {/if}
  {/if}
</section>

<style>
  .markdown-preview {
    padding: 20px 28px;
    max-width: 1100px;
    margin: 0 auto;
  }

  .presentation .markdown-preview {
    max-width: 1600px;
  }

  /* Centred on a checkerboard, so transparent parts show as transparent. */
  /* Grows with the zoomed image so the view scrolls; the image's auto
     margins centre it without clipping its top and left when larger. */
  .image-preview {
    display: flex;
    width: max-content;
    min-width: 100%;
    min-height: 100%;
    padding: 20px;
    box-sizing: border-box;
    cursor: grab;
    user-select: none;
    background-color: var(--bg-primary);
    background-image:
      linear-gradient(45deg, var(--bg-tertiary) 25%, transparent 25%),
      linear-gradient(-45deg, var(--bg-tertiary) 25%, transparent 25%),
      linear-gradient(45deg, transparent 75%, var(--bg-tertiary) 75%),
      linear-gradient(-45deg, transparent 75%, var(--bg-tertiary) 75%);
    background-size: 16px 16px;
    background-position: 0 0, 0 8px, 8px -8px, -8px 0;
  }

  .image-preview.dragging {
    cursor: grabbing;
  }

  /* Sized by the zoom once loaded. */
  .image-preview img {
    display: block;
    flex-shrink: 0;
    margin: auto;
    max-width: none;
  }

  .zoom-level {
    flex-shrink: 0;
    min-width: 44px;
    padding: 2px 4px;
    font-family: var(--font-sans);
    font-size: 0.75rem;
    font-variant-numeric: tabular-nums;
    text-align: center;
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .zoom-level:hover {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .presentation-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }

  .font-size {
    flex-shrink: 0;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .viewer {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
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

  .copy-btn:disabled {
    opacity: 0.45;
    cursor: default;
    background-color: transparent;
    color: var(--text-secondary);
  }

  .copy-btn.primary:not(:disabled) {
    color: var(--accent-color);
    border-color: var(--accent-color);
  }

  .edit-status,
  .read-only {
    flex-shrink: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-family: var(--font-sans);
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .edit-error {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 14px;
    font-size: 0.78rem;
    color: #ef4444;
    border-bottom: 1px solid var(--border-color);
  }

  .edit-error span {
    flex: 1;
    min-width: 0;
  }

  .copy-btn.copied {
    color: #22c55e;
    border-color: #22c55e;
  }

  .copy-btn.failed {
    color: #ef4444;
    border-color: #ef4444;
  }

  .code-area {
    position: relative;
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  .code-scroll {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }

  /* Over the code's top right corner, as in editors. */
  .find-bar {
    position: absolute;
    top: 6px;
    right: 18px;
    z-index: 2;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px;
    font-family: var(--font-sans);
    font-size: 0.75rem;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.18);
  }

  .find-input {
    width: 200px;
    padding: 3px 8px;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
  }

  .find-input:focus {
    outline: none;
    border-color: var(--accent-color, #3b82f6);
  }

  .find-count {
    /* Room for "999 of 999", so the input stays put while stepping. */
    min-width: 84px;
    padding: 0 6px;
    color: var(--text-secondary);
    font-variant-numeric: tabular-nums;
    text-align: center;
    white-space: nowrap;
  }

  .find-count.none {
    color: #ef4444;
  }

  .find-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 24px;
    height: 24px;
    padding: 0 4px;
    border: 1px solid transparent;
    border-radius: 4px;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .find-btn:hover:not(:disabled) {
    background-color: var(--hover-bg);
    color: var(--text-primary);
  }

  .find-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .find-btn.case {
    font-weight: 600;
  }

  .find-btn.on {
    color: var(--accent-color);
    border-color: var(--accent-color);
    background-color: var(--active-bg);
  }

  /* Matches as editors mark them: every one, and stronger the current one. */
  :global(::highlight(file-find)) {
    background-color: rgba(234, 179, 8, 0.3);
  }

  :global(::highlight(file-find-current)) {
    background-color: rgba(249, 115, 22, 0.55);
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
