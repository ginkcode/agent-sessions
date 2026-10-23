<script lang="ts">
  import { onDestroy, tick, untrack, type Snippet } from 'svelte';
  import { projectSourceDiagnostics } from '../../../sourceDiagnostics';
  import { codeDiagnostics } from '../../../stores/codeDiagnostics.svelte';
  import type { CodeDiagnostic, CodeDiagnosticRange } from '../../../codeDiagnostics';
  import { problemPath, problemLocation, rangeOffsets, revealSourceRange, matchesProblemNavigation, type ProblemNavigationRequest } from '../../../problemNavigation';
  import type { FilePosition } from '../../../filePosition';
  import { selectFileLine, type FileLineSelection } from '../../../fileSelection';
  import type { BlameLine } from '../../../types';
  import { formatAbsoluteTime, formatAgo } from '../../../date';
  import { preferences } from '../../../stores/preferences.svelte';
  import { files } from '../../../stores/files.svelte';
  import type { FileTab } from '../../../stores/files.svelte';
  import { highlightFile, splitHighlightedLines, splitLines } from '../../../codeHighlight';
  import { copyToClipboard } from '../../../portable';
  import { printFile } from '../../../print';
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
  import { canEditContent, readOnlyText } from '../../../fileEditing';
  import { externalLabel } from '../../../codeNavigation';
  import type { FileContent } from '../../../types';
  import LoadingSpinner from '../LoadingSpinner.svelte';
  import MarkdownDoc from '../MarkdownDoc.svelte';
  import SourceView from './SourceView.svelte';
  import FileEditor from './FileEditor.svelte';
  import { keyHandled } from '../../../fullscreen';
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
  const lineSelections = new WeakMap<object, FileLineSelection>();
  let selectionVersion = $state(0);
  let selectedLines = $derived.by(() => { selectionVersion; return activeTab ? lineSelections.get(activeTab) : undefined; });
  let blame = $state<BlameLine | null>(null);
  let blameError = $state('');
  let blameLoading = $state(false);
  // The commit details card under the blame line; the line itself shows whenever one is selected.
  let blameOpen = $state(false);
  let blameRequest = 0;

  function selectLine(line: number, extend: boolean) {
    if (!activeTab) return;
    lineSelections.set(activeTab, selectFileLine(selectedLines, line, extend));
    selectionVersion++;
  }
  function clearSelection() {
    if (!activeTab) return;
    lineSelections.delete(activeTab);
    selectionVersion++;
  }
  function closeBlame() {
    blameRequest++;
    blameOpen = false;
    blameLoading = false;
    blame = null;
    blameError = '';
  }
  async function loadBlame(dir: string, path: string, line: number) {
    const request = ++blameRequest;
    blameLoading = true;
    try {
      const result = await api.blameFile(dir, path, line);
      if (request === blameRequest) blame = result;
    } catch (error) {
      if (request === blameRequest) blameError = errorText(error, 'Could not load blame for this line.');
    } finally {
      if (request === blameRequest) blameLoading = false;
    }
  }
  const blameTime = (b: BlameLine) => new Date(b.authorTime * 1000).toISOString();

  // Files larger than this are shown without highlighting, which is slow on
  // very long text.
  const HIGHLIGHT_LIMIT = 500_000;

  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);

  let previewOn = $state(false);
  let navigation = $state.raw<ProblemNavigationRequest<FileTab> | null>(null);
  let navigationSequence = 0;
  let navigationNotice = $state('');
  // Problems follow the code-check context too: a finding from an older
  // check must not be drawn over newer text.
  function navigationIdentity(kind: ProblemNavigationRequest['kind'] = 'problem') {
    return { dir: files.dir, host: link.dataHost, version: files.contextVersion,
      context: kind === 'search' ? 'search' : JSON.stringify([codeDiagnostics.project, codeDiagnostics.snapshot?.context, codeDiagnostics.status]) };
  }
  function navigationLocation(diagnostic: CodeDiagnostic, tab: NonNullable<typeof files.activeTab>) {
    if (codeDiagnostics.status !== 'ready' || codeDiagnostics.revoking || codeDiagnostics.directory !== files.dir || blocked) return 'Code checks are reconnecting or updating; the position is shown once results are current.';
    return problemLocation(diagnostic, { dirty: Boolean(tab.edit?.dirty || tab.edit?.uncertain || tab.edit?.conflict), revision: tab.edit?.revision ?? tab.content?.revision, live: codeDiagnostics.liveBasis(tab.path) });
  }
  /** Opens path (in a kept tab, or the preview tab) and starts the wait to reveal its target. */
  function startNavigation(path: string, kind: ProblemNavigationRequest['kind'], range: CodeDiagnosticRange | undefined, locate: ProblemNavigationRequest<FileTab>['locate'], keep = true) {
    navigation = null;
    if (!problemPath(path) || !files.dir || frozen || blocked) {
      navigationNotice = kind === 'search' ? 'Cannot open this search result in the current project.' : 'Cannot open this problem target in the current project.';
      return;
    }
    files.openFile(path, { keep, read: fileStatus(path) !== 'D' });
    const tab = files.activeTab;
    if (!tab || tab.path !== path) return;
    pendingAnchor = null;
    navigationNotice = '';
    navigation = { id: ++navigationSequence, identity: navigationIdentity(kind), tab, path, kind, range, locate };
  }
  export function navigateProblem(diagnostic: CodeDiagnostic) {
    startNavigation(diagnostic.path, 'problem', diagnostic.range, tab => navigationLocation(diagnostic, tab));
  }
  /**
   * Opens a search match and selects it. The match must still read the same
   * in the text shown; otherwise the file changed since the search. As in
   * the file list, it opens in the preview tab unless keep is set.
   */
  export function navigateMatch(path: string, range: CodeDiagnosticRange, matched: string, keep = false) {
    startNavigation(path, 'search', range, (_, source) => {
      const at = rangeOffsets(source, range);
      return at && source.slice(at.anchor, at.head) === matched ? '' : 'This file changed since the search; search again to update the result.';
    }, keep);
  }
  // Wait for the matching load and a source render. Recheck identity after every
  // async boundary; a late read/tick must never select another tab or host.
  $effect(() => {
    const request = navigation;
    if (!request) return;
    const identity = navigationIdentity(request.kind);
    const valid = () => matchesProblemNavigation(request, navigation, navigationIdentity(request.kind), files.activeTab, files.selected);
    if (!matchesProblemNavigation(request, navigation, identity, files.activeTab, files.selected)) { navigation = null; return; }
    if (files.loadingFile || (!editing && highlightPending)) return;
    if (files.fileError || fileStatus(request.path) === 'D') {
      navigationNotice = files.fileError ?? (request.kind === 'search' ? 'This file was deleted since the search.' : 'This problem target was deleted; the finding remains in Problems.');
      navigation = null; return;
    }
    const tab = files.activeTab!;
    const shownSource = () => (tab.edit?.editing ? tab.edit.draft : text);
    const source = shownSource();
    const reason = request.locate(tab, source);
    if (reason) { navigationNotice = reason; navigation = null; return; }
    const range = request.range!;
    if (!isTextContent(tab.content) || !rangeOffsets(source, range)) {
      navigationNotice = request.kind === 'search' ? 'This search result cannot be shown in the current source.' : 'This saved-file range cannot be shown in the current source.';
      navigation = null; return;
    }
    previewOn = false;
    if (files.diff) files.toggleDiff();
    presentation = false;
    closeBlame();
    untrack(() => { void tick().then(() => {
      if (!valid() || showPreview || showDiff || frozen || (!editing && highlightPending)) return;
      // A draft may have changed while the source/editor was mounting.
      if (request.locate(tab, shownSource())) {
        navigationNotice = 'Stale: the file changed while it was opening; this position no longer matches the text.';
        navigation = null; return;
      }
      const editorValid = () => valid() && !frozen && !request.locate(tab, shownSource());
      const shown = editing ? editor?.revealRange(range, editorValid) : codeScroll && revealSourceRange(codeScroll, range);
      if (shown) navigation = null;
    }); });
  });

  // Editing. A tab in edit mode shows the editor in place of every viewer
  // state, so a draft stays reachable even when the listing says deleted.
  let activeTab = $derived(files.activeTab);
  // A file outside the project, opened by going to a definition: shown, never
  // edited, and without the project's git or code checks.
  let external = $derived(Boolean(activeTab?.external));
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
      : external ? 'Read-only: outside the project'
      : files.content?.edit?.eligible === false ? readOnlyText(files.content.edit.reason)
      : !canSaveHost && files.content?.edit?.eligible ? 'Read-only: this host cannot save files'
      : ''
  );
  // Save also finishes editing, so it stays available without changes.
  let canSave = $derived(
    Boolean(edit?.editing) && !edit?.saving && !edit?.uncertain && !frozen && canSaveHost && !blocked
  );

  function isTextContent(content: FileContent | null): boolean {
    return !!content && typeof content.text === 'string' && !content.binary && !content.tooLarge && !content.image;
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
      if (!showPreview && codeScroll) rememberSourcePosition(codeScroll);
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

  // Save button: write the changes and return to the viewer.
  function save() {
    const tab = files.activeTab;
    if (tab?.edit && canSave) void fileActions.save(tab);
  }

  // Cmd/Ctrl+S: save without leaving the editor, for saving as you go.
  function saveInPlace() {
    const tab = files.activeTab;
    if (tab?.edit?.dirty && canSave) void fileActions.saveInPlace(tab);
  }

  function resolveConflict() {
    const tab = files.activeTab;
    if (tab?.edit?.conflict && !frozen && !tab.edit.saving) void fileActions.resolveConflict(tab);
  }

  // Discard button: revert every change and return to the viewer.
  function discard() {
    const tab = files.activeTab;
    if (tab?.edit) void fileActions.discard(tab);
  }

  // Reload in the error bar: take the disk contents and keep editing.
  function reload() {
    const tab = files.activeTab;
    if (tab?.edit) void fileActions.reload(tab);
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
  // Only git listings can blame; older hosts say so instead of failing per line.
  let blameShown = $derived(Boolean(selectedLines && !showPreview && files.listing?.git && !external));
  let canBlame = $derived(connectionStore.capabilities.blame === true && !blocked);
  // The selected line's blame loads by itself; another line, file or text starts over.
  $effect(() => {
    const line = selectedLines?.head, dir = files.dir, path = files.selected;
    void text; void editing;
    closeBlame();
    if (!blameShown || !canBlame || editing || !line || !dir || !path) return;
    untrack(() => void loadBlame(dir, path, line));
  });
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
  let highlightPending = $state(false);
  // One entry per line, so a wrapped line keeps its number beside it.
  // Highlighted lines are HTML; plain ones are text.
  let htmlLines = $derived(html !== null ? splitHighlightedLines(html) : null);
  let plainLines = $derived(textContent ? splitLines(text) : []);
  let changes = $derived(
    files.selected && !external ? fileChanges(status?.[files.selected], files.content?.changes, plainLines.length) : undefined
  );
  let marks = $derived(lineMarks(changes, plainLines.length));
  // The diff view shows when it is on and the file has changes to show.
  let hasChanges = $derived(Boolean(changes?.length));
  // Git markers describe the saved file, so the diff never shows over a draft.
  let showDiff = $derived(files.diff && hasChanges && !editing);
  let sourceDiagnostics = $derived(projectSourceDiagnostics(text,
    codeDiagnostics.project && codeDiagnostics.directory === files.dir && !external && !blocked && !files.loadingFile && !showDiff && !showPreview && !blameOpen
      ? codeDiagnostics.findings.filter(f => f.diagnostic.path === files.selected) : [],
    { dirty: Boolean(edit?.dirty || edit?.uncertain || edit?.conflict), revision: edit?.revision ?? files.content?.revision, live: files.selected ? codeDiagnostics.liveBasis(files.selected) : undefined },
    codeDiagnostics.status === 'ready' && !codeDiagnostics.revoking && !highlightPending));
  // Explicit recovery for results that predate the displayed text. Both paths reuse the
  // store's existing reconciliation: nothing is saved and freshness is never assumed.
  let sourceRecovery = $derived.by(() => {
    const failed = (codeDiagnostics.liveDocuments ?? []).find(d => d.path === files.selected && d.failure && d.canReopen);
    const disabled = Boolean(blocked) || !!codeDiagnostics.pending;
    if (failed) return { label: 'Reopen live sync', disabled,
      description: 'Live sync for this file stopped. Reopen sends the displayed text to the language server again under a new document identity; nothing is saved.',
      run: () => codeDiagnostics.reopenDocument(failed.path, failed.documentId) };
    if (codeDiagnostics.status !== 'ready' && codeDiagnostics.status !== 'error') return undefined;
    return { label: 'Refresh results', disabled: disabled || codeDiagnostics.status === 'ready' && codeDiagnostics.revoking,
      description: 'Some positions are from an older version of this file. Refresh re-reads saved files and fetches the latest results; nothing is saved or re-run.',
      run: () => { void codeDiagnostics.refresh().catch(() => {}); } };
  });
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
    highlightPending = false;
    if (!code || !lang || code.length > HIGHLIGHT_LIMIT) return;
    highlightPending = true;
    let current = true;
    highlightFile(code, lang).then(
      (out) => {
        if (current) { html = out; highlightPending = false; }
      },
      () => { if (current) highlightPending = false; }
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

  // A file link may carry a heading fragment (guide.md#setup); after the
  // target opens and renders, the viewer scrolls to that heading.
  let pendingAnchor = $state<{ path: string; id: string } | null>(null);

  function followFileLink(href: string) {
    if (!files.selected) return;
    if (external) {
      toast.show({ title: 'Cannot open this link', body: 'Links in files outside the project are not followed.', tone: 'error' });
      return;
    }
    const hashIndex = href.indexOf('#');
    const fragment = hashIndex >= 0 ? href.slice(hashIndex + 1) : '';
    const target = markdownFileTarget(files.selected, hashIndex >= 0 ? href.slice(0, hashIndex) : href);
    if (!target) {
      toast.show({ title: 'Cannot open this link', body: 'The link must point to a file inside this project.', tone: 'error' });
      return;
    }
    // Keep the referring document so the reader can return to it via its tab.
    files.keepTab(files.selected);
    // Only a markdown target can resolve the heading; a fragment on anything
    // else is just a file link.
    if (fragment && filePreview(target) === 'markdown') pendingAnchor = { path: target, id: fragment };
    pick(target, true);
  }

  // Fragment-only links never reach followFileLink: MarkdownDoc scrolls to the
  // heading in place. Cross-file fragments land here once the target renders.
  let pendingScrolled = new WeakSet<{ path: string; id: string }>();
  $effect(() => {
    const pending = pendingAnchor;
    if (!pending || pendingScrolled.has(pending)) return;
    if (files.selected !== pending.path) return;
    const el = codeScroll;
    if (!el || !showMarkdown || !text) return;
    // The scroll-restore effect also runs on this selection change and would
    // undo the scroll; the frame defers past it.
    pendingScrolled.add(pending);
    untrack(() => {
      requestAnimationFrame(() => {
        let id = pending.id;
        try {
          id = decodeURIComponent(id);
        } catch {}
        const heading = el.querySelector<HTMLElement>(`#${CSS.escape(id)}`);
        if (!heading) return;
        el.scrollTop += heading.getBoundingClientRect().top - el.getBoundingClientRect().top - el.clientTop;
      });
    });
  });

  // Each tab keeps its scroll position. A plain map: saving it on every
  // scroll must not rerun the effect that restores it.
  const scrollTops = new Map<string, number>();
  // Share a logical source position across the viewer and the virtualized
  // editor. Pixel scroll offsets alone drift with wrapping and diff rows.
  const sourcePositions = new WeakMap<object, FilePosition>();
  let codeScroll: HTMLElement | undefined = $state();

  function rememberSourcePosition(el: HTMLElement) {
    const tab = files.activeTab;
    if (!tab || !el.isConnected) return;
    const top = el.getBoundingClientRect().top + el.clientTop;
    const lines = el.querySelectorAll<HTMLElement>('[data-source-line]');
    for (const line of lines) {
      const rect = line.getBoundingClientRect();
      if (rect.bottom <= top) continue;
      sourcePositions.set(tab, {
        line: Number(line.dataset.sourceLine),
        offset: rect.top - top,
        left: el.scrollLeft,
      });
      break;
    }
  }

  $effect(() => {
    const el = codeScroll;
    const tab = activeTab;
    const path = files.selected;
    const mode = showPreview ? 'preview' : 'source';
    if (!el || !path || editing) return;
    untrack(() => {
      const position = tab && mode === 'source' ? sourcePositions.get(tab) : undefined;
      const line = position && el.querySelector<HTMLElement>(`[data-source-line="${position.line}"]`);
      if (position && line) {
        el.scrollTop += line.getBoundingClientRect().top - el.getBoundingClientRect().top - el.clientTop - position.offset;
        el.scrollLeft = position.left;
      } else {
        el.scrollTop = scrollTops.get(`${path}:${mode}`) ?? 0;
      }
    });
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
  let pathCopyLabel = $derived(external && pathCopyState === 'idle' ? 'Copy path' : PATH_COPY_LABELS[pathCopyState]);

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

  /** The text selected in the editor or the source view, for a project search. */
  export function selectedText(): string {
    if (editing) return editor?.selectedText() ?? '';
    const selection = typeof document === 'undefined' ? null : document.getSelection();
    if (!selection || !codeScroll || !codeScroll.contains(selection.anchorNode)) return '';
    return selection.toString();
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
      if (keyHandled(e) || !find.findActive) return false;
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
  {#if navigationNotice}<div class="state nav-notice" role="status">{navigationNotice}</div>{/if}
  {#if !files.selected}
    <div class="state center">Pick a file to see its content. Double-click keeps it open in a tab.</div>
  {:else}
    <div class="viewer-header">
      <span class="viewer-path" title={files.selected}>{external ? externalLabel(files.selected, files.dir) : files.selected}</span>
      <button
        type="button"
        class="path-copy"
        class:copied={pathCopyState === 'copied'}
        class:failed={pathCopyState === 'failed'}
        title={pathCopyLabel}
        aria-label={pathCopyLabel}
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
          class="copy-btn icon-btn"
          class:on={files.wrap}
          aria-pressed={files.wrap}
          aria-label="Wrap"
          title="{files.wrap ? 'Stop wrapping' : 'Wrap'} long lines ({wrapShortcut})"
          onclick={() => files.toggleWrap()}
        >
          <span class="btn-icon icon-wrap" aria-hidden="true"></span>
        </button>
        <!-- Both buttons leave edit mode. -->
        <button type="button" class="copy-btn" title="Revert all changes and stop editing" disabled={frozen || edit.saving} onclick={discard}>Discard</button>
        <button
          type="button"
          class="copy-btn primary"
          title={blocked ?? (canSaveHost ? `Save changes and stop editing (${saveShortcut} saves and keeps editing)` : 'This host cannot save files.')}
          disabled={!canSave}
          onclick={save}
        >
          Save
        </button>
      {:else if files.content && !files.loadingFile && !files.fileError}
        {#if canSaveHost && canEditContent(files.content)}
          <button
            type="button"
            class="copy-btn icon-btn"
            aria-label={starting ? 'Opening…' : 'Edit'}
            title={blocked ?? (starting ? 'Opening…' : 'Edit this file')}
            disabled={!canEdit}
            onclick={startEdit}
          >
            <span class="btn-icon icon-edit" aria-hidden="true"></span>
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
        {#if previewKind === 'markdown' && contentShown && textContent}
          <button
            type="button"
            class="copy-btn icon-btn"
            aria-label="Print / Save as PDF"
            title="Print / Save as PDF"
            onclick={() => files.selected && printFile(files.selected, text)}
          >
            <span class="btn-icon icon-print" aria-hidden="true"></span>
          </button>
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
          class="copy-btn icon-btn"
          class:on={find.findActive}
          aria-pressed={find.findActive}
          aria-label="Find"
          title="Find in the file ({findShortcut})"
          onclick={() => (find.findActive ? find.closeFind() : find.openFind())}
        >
          <span class="btn-icon icon-find" aria-hidden="true"></span>
        </button>
        {/if}
        {#if !showPreview}
        <button
          type="button"
          class="copy-btn icon-btn"
          class:on={files.wrap}
          aria-pressed={files.wrap}
          aria-label="Wrap"
          title="{files.wrap ? 'Stop wrapping' : 'Wrap'} long lines ({wrapShortcut})"
          onclick={() => files.toggleWrap()}
        >
          <span class="btn-icon icon-wrap" aria-hidden="true"></span>
        </button>
        {/if}
        <button
          type="button"
          class="copy-btn icon-btn"
          class:copied={copyState === 'copied'}
          class:failed={copyState === 'failed'}
          title={copyState === 'idle' ? 'Copy the file content' : COPY_LABELS[copyState]}
          aria-label={copyState === 'idle' ? 'Copy the file content' : COPY_LABELS[copyState]}
          onclick={copyContent}
        >
          <span class="btn-icon" class:icon-copy={copyState !== 'copied'} class:icon-copied={copyState === 'copied'} aria-hidden="true"></span>
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
            <button type="button" class="copy-btn" title="Discard changes and reload from disk" disabled={frozen || edit.saving} onclick={reload}>Reload</button>
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
          diagnostics={{ tab: activeTab, path: activeTab.path,
            context: JSON.stringify(navigationIdentity()),
            findings: codeDiagnostics.project && codeDiagnostics.directory === files.dir ? codeDiagnostics.findings : [],
            dirty: Boolean(edit.dirty || edit.uncertain || edit.conflict), revision: edit.revision, live: codeDiagnostics.liveBasis(activeTab.path),
            current: codeDiagnostics.status === 'ready' && !codeDiagnostics.revoking && !blocked && !frozen && !files.loadingFile }}
          wrap={files.wrap}
          disabled={frozen}
          selection={selectedLines}
          onselection={((tab) => (selection: FileLineSelection) => { lineSelections.set(tab, selection); selectionVersion++; })(activeTab)}
          position={sourcePositions.get(activeTab)}
          onposition={((tab) => (position: FilePosition) => sourcePositions.set(tab, position))(activeTab)}
          onchange={(next) => {
            if (!fileActions.busy && !fileActions.quitting) files.updateDraft(activeTab, next);
          }}
          onsave={saveInPlace}
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
          <!-- One field: the case toggle sits inside the input's frame. -->
          <div class="find-field">
            <svg class="find-glass" viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
              <circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" stroke-width="1.5" />
              <path d="M10.5 10.5 14 14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            </svg>
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
          </div>
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
          <button type="button" class="find-btn" aria-label="Close find" title="Close (Escape)" onclick={find.closeFind}>
            <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
              <path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
            </svg>
          </button>
        </div>
      {/if}
      {#if blameShown}
        <div class="line-actions" role="status" aria-live="polite">
          <span class="btn-icon icon-git" aria-hidden="true"></span>
          {#if !canBlame}<span>{blocked ? 'Blame unavailable while disconnected' : 'Blame is not supported on this host'}</span>
          {:else if blameLoading}<span>Loading blame…</span>
          {:else if blameError}<span class="blame-error">{blameError}</span>
          {:else if blame?.uncommitted}<span>Not committed yet</span>
          {:else if blame}
            <button type="button" class="blame-summary" aria-expanded={blameOpen} title={`${blame.summary} · click for commit details`}
              onclick={() => (blameOpen = !blameOpen)}>{blame.author} · {formatAgo(blameTime(blame))}</button>
          {/if}
        </div>
        {#if blameOpen && blame && !blame.uncommitted}
          <div class="line-blame" role="group" aria-label="Commit details">
            <strong>{blame.summary}</strong>
            <span>{blame.author} · {formatAbsoluteTime(blameTime(blame), preferences.timeZone)}</span>
            <button type="button" class="copy-btn" title="Copy commit hash" onclick={async () => { if (blame && !(await copyToClipboard(blame.commit))) toast.show({ title: 'Unable to copy commit hash', body: 'Clipboard access failed.', tone: 'error' }); }}>{blame.commit.slice(0, 12)} · Copy hash</button>
          </div>
        {/if}
      {/if}
      <div
        class="code-scroll"
        bind:this={codeScroll}
        onscroll={(e) => {
          if (files.selected) scrollTops.set(`${files.selected}:${showPreview ? 'preview' : 'source'}`, e.currentTarget.scrollTop);
          if (!showPreview) rememberSourcePosition(e.currentTarget);
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
        <SourceView diagnostics={sourceDiagnostics} recovery={sourceRecovery} {rows} {htmlLines} {plainLines} {showDiff} {presentation} {presentationFontSize} selection={selectedLines} onselectline={selectLine} onclearselection={clearSelection} bind:codeEl={find.codeEl} />
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
    border-radius: var(--radius-sm);
    color: var(--text-secondary);
    cursor: pointer;
  }

  .zoom-level:hover {
    background-color: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .presentation-btn,
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }

  /* Doubled so it outranks .copy-btn's padding, which comes later. */
  .copy-btn.presentation-btn,
  .copy-btn.icon-btn {
    width: 26px;
    padding: 0;
  }

  .btn-icon {
    flex-shrink: 0;
    width: 15px;
    height: 15px;
    background-color: currentColor;
  }

  .icon-edit {
    -webkit-mask: url('../../../../../assets/edit.svg') center / contain no-repeat;
    mask: url('../../../../../assets/edit.svg') center / contain no-repeat;
  }

  .icon-find {
    -webkit-mask: url('../../../../../assets/find.svg') center / contain no-repeat;
    mask: url('../../../../../assets/find.svg') center / contain no-repeat;
  }

  .icon-copy {
    -webkit-mask: url('../../../../../assets/copy.svg') center / contain no-repeat;
    mask: url('../../../../../assets/copy.svg') center / contain no-repeat;
  }

  .icon-copied {
    -webkit-mask: url('../../../../../assets/copied.svg') center / contain no-repeat;
    mask: url('../../../../../assets/copied.svg') center / contain no-repeat;
  }

  .icon-print {
    -webkit-mask: url('../../../../../assets/print.svg') center / contain no-repeat;
    mask: url('../../../../../assets/print.svg') center / contain no-repeat;
  }

  .icon-wrap {
    -webkit-mask: url('../../../../../assets/wrap.svg') center / contain no-repeat;
    mask: url('../../../../../assets/wrap.svg') center / contain no-repeat;
  }

  .icon-git {
    -webkit-mask: url('../../../../../assets/git.svg') center / contain no-repeat;
    mask: url('../../../../../assets/git.svg') center / contain no-repeat;
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

  /* Borderless toolbar buttons, matching the session header's actions. */
  .copy-btn {
    flex-shrink: 0;
    height: 24px;
    padding: 0 9px;
    font-family: var(--font-sans);
    font-size: 0.75rem;
    font-weight: 500;
    line-height: 1;
    border-radius: var(--radius-sm);
    color: var(--text-secondary);
    cursor: pointer;
    transition: background-color 0.15s ease, color 0.15s ease;
  }

  .copy-btn:hover:not(:disabled) {
    background-color: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .copy-btn:focus-visible {
    outline: 2px solid var(--accent-color);
    outline-offset: 1px;
  }

  .copy-btn.on,
  .copy-btn.on:hover:not(:disabled) {
    color: var(--accent-color);
    background-color: var(--active-bg);
  }

  .copy-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .copy-btn.primary {
    background-color: var(--accent-color);
    color: #fff;
  }

  .copy-btn.primary:hover:not(:disabled) {
    background-color: var(--accent-hover);
    color: #fff;
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
    color: var(--status-live);
    background-color: color-mix(in srgb, var(--status-live) 14%, transparent);
  }

  .copy-btn.failed {
    color: var(--danger);
    background-color: color-mix(in srgb, var(--danger) 12%, transparent);
  }

  .line-actions {
    position: absolute;
    z-index: 3;
    right: 14px;
    bottom: 10px;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg-secondary);
    color: var(--text-secondary);
    font-size: 12px;
  }
  .line-blame {
    position: absolute;
    z-index: 4;
    right: 14px;
    bottom: 52px;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
    max-width: min(420px, calc(100% - 28px));
    padding: 12px;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg-secondary);
    color: var(--text-primary);
    box-shadow: 0 4px 16px #0002;
    overflow-wrap: anywhere;
    font-size: 12px;
  }
  .blame-error { color: var(--danger); }
  .blame-summary {
    padding: 0;
    border: 0;
    background: none;
    color: var(--text-primary);
    font: inherit;
    cursor: pointer;
  }
  .blame-summary:hover,
  .blame-summary[aria-expanded='true'] { text-decoration: underline; }

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
    padding: 5px;
    font-family: var(--font-sans);
    font-size: 0.75rem;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-md);
    box-shadow: 0 8px 24px rgba(15, 23, 42, 0.12), 0 1px 3px rgba(15, 23, 42, 0.08);
  }

  /* The input, its icon and the case toggle share one rounded field, ringed
     on focus like the session search. */
  .find-field {
    display: flex;
    align-items: center;
    gap: 4px;
    height: 26px;
    padding: 0 2px 0 8px;
    background-color: var(--bg-secondary);
    border-radius: var(--radius-sm);
    transition: box-shadow 0.15s ease, background-color 0.15s ease;
  }

  .find-field:focus-within {
    background-color: var(--bg-primary);
    box-shadow: 0 0 0 1px var(--accent-color), 0 0 0 3px var(--active-bg);
  }

  .find-glass {
    flex-shrink: 0;
    color: var(--text-muted);
  }

  .find-input {
    width: 180px;
    padding: 0;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    border: none;
    outline: none;
    background: transparent;
    color: var(--text-primary);
  }

  .find-input::placeholder {
    color: var(--text-muted);
  }

  .find-count {
    /* Room for "999 of 999", so the input stays put while stepping. */
    min-width: 76px;
    padding: 0 4px;
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
    text-align: center;
    white-space: nowrap;
  }

  .find-count.none {
    color: var(--danger);
  }

  .find-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    min-width: 24px;
    height: 24px;
    padding: 0 4px;
    border-radius: var(--radius-sm);
    color: var(--text-secondary);
    cursor: pointer;
    transition: background-color 0.15s ease, color 0.15s ease;
  }

  .find-btn:hover:not(:disabled) {
    background-color: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .find-btn:focus-visible {
    outline: 2px solid var(--accent-color);
    outline-offset: 1px;
  }

  .find-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }

  .find-btn.case {
    min-width: 22px;
    height: 20px;
    font-size: 0.7rem;
    font-weight: 600;
    color: var(--text-muted);
  }

  .find-btn.on,
  .find-btn.on:hover:not(:disabled) {
    color: var(--accent-color);
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

  .state.nav-notice {
    padding: 4px 12px;
    font-size: 12px;
    border-bottom: 1px solid var(--border-color);
    overflow-wrap: anywhere;
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
