import { Annotation, Compartment, EditorState, RangeSet, StateField, type Extension, type TransactionSpec } from '@codemirror/state';
import { forEachDiagnostic, lintKeymap, setDiagnostics, setDiagnosticsEffect, type Diagnostic } from '@codemirror/lint';
import { projectSourceDiagnostics } from './sourceDiagnostics';
import type { AttributedCodeDiagnostic, LiveDocumentBasis } from './codeDiagnostics';
import { EditorView, GutterMarker, closeHoverTooltips, drawSelection, hasHoverTooltips, highlightActiveLine, hoverTooltip, keymap, lineNumberMarkers, lineNumbers, tooltips, type KeyBinding, type Tooltip } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab, isolateHistory } from '@codemirror/commands';
import { LanguageDescription, indentOnInput, indentUnit } from '@codemirror/language';
import { Decoration, type DecorationSet } from '@codemirror/view';
import { loadFileHighlighter } from './codeHighlight';
import type { HLJSApi } from 'highlight.js';
import { keyHandled } from './fullscreen';
import type { FilePosition } from './filePosition';
import type { CodeDiagnosticRange } from './codeDiagnostics';
import { rangeOffsets } from './problemNavigation';
import type { FileLineSelection } from './fileSelection';
import { fileLanguage } from './files';
import { textChanges } from './searchReplace';
import { closeSearchPanel, openSearchPanel, search, searchKeymap, searchPanelOpen } from '@codemirror/search';
import type { CodeDiagnosticPosition } from './codeDiagnostics';
import { HOVER_DELAY, linkModifier, wordRange, type ViewportPoint } from './sourceNavigation';
import { handleCopyCodeClick } from './copycode';

/**
 * Code navigation from the draft. `current` says whether the editor still
 * shows the text the position was taken from; an answer is wanted only
 * while it does.
 */
export interface EditorSymbols {
  /** Go to definition (Cmd/Ctrl+click, F12) or find references (Shift+F12). */
  ask(op: 'definition' | 'references', position: CodeDiagnosticPosition, at: ViewportPoint, current: () => boolean): void;
  /** The hover for the word at position, as HTML; undefined shows nothing. */
  hover(position: CodeDiagnosticPosition, current: () => boolean): Promise<{ html: string; truncated: boolean } | undefined>;
}

export interface FileEditorOptions {
  id: object;
  text: string;
  path: string;
  version: number;
  wrap: boolean;
  disabled?: boolean;
  /** Context-bound snapshot supplied anew by the viewer, never cached as an owner. */
  diagnostics?: { tab: object; path: string; context: string; findings: AttributedCodeDiagnostic[]; dirty: boolean; revision?: string; live?: LiveDocumentBasis; current: boolean };
  position?: FilePosition;
  selection?: FileLineSelection;
  onselection?: (selection: FileLineSelection) => void;
  onposition?: (position: FilePosition) => void;
  onchange: (text: string) => void;
  onsave: () => void;
  /** Read through owners when asked, like every callback here. */
  symbols?: EditorSymbols;
}

interface CachedEditor {
  state: EditorState;
  version: number;
  top: number;
  left: number;
}

// Only immutable editor state and scroll coordinates survive a tab's unmount.
// In particular, cached extensions must never close over a view or its props.
const tabs = new WeakMap<object, CachedEditor>();
const owners = new WeakMap<EditorView, FileEditorOptions>();
const wrapping = new Compartment();
const editing = new Compartment();
const language = new Compartment();
const coloring = new Compartment();
const editable = (disabled = false): Extension => [
  EditorView.editable.of(!disabled),
  EditorState.readOnly.of(disabled),
];

// Matches the viewer's SourceView (font, line height, gutter and line
// padding), so switching between viewing and editing does not move the text.
const theme = EditorView.theme({
  '&': { height: '100%', color: 'var(--text-primary)', backgroundColor: 'var(--bg-primary)', fontSize: '0.78rem' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--font-mono)', lineHeight: '1.5', tabSize: '4' },
  '.cm-content': { padding: '8px 0', caretColor: 'var(--text-primary)' },
  '.cm-line': { padding: '0 14px' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--text-primary)' },
  '.cm-gutters': { backgroundColor: 'var(--bg-secondary)', color: 'var(--text-muted)', border: 'none', borderRight: '1px solid var(--border-color)' },
  '.cm-gutter': { paddingTop: '0', paddingBottom: '0' },
  '.cm-lineNumbers .cm-gutterElement': { padding: '0 10px 0 14px', minWidth: '0' },
  '.cm-lineNumbers .cm-has-diagnostics': { textDecoration: 'underline dotted', fontWeight: 'bold' },
  // The selection layer sits behind the lines, so the active line's tint must
  // stay translucent or it hides a selection on the cursor's line.
  '.cm-activeLine': { backgroundColor: 'var(--hover-bg)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--bg-secondary)' },
  // Selectors as specific as CodeMirror's base theme, which would otherwise
  // paint its own (light-only) selection colours.
  '& > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'color-mix(in srgb, var(--accent-color) 16%, transparent)',
  },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, &.cm-focused .cm-content ::selection': {
    backgroundColor: 'color-mix(in srgb, var(--accent-color) 30%, transparent)',
  },
  // Scrolls when shortened to fit the editor (see fitTooltips).
  '.cm-tooltip': { backgroundColor: 'var(--bg-secondary)', color: 'var(--text-primary)', border: '1px solid var(--border-color)', overflowY: 'auto' },
  // A symbol's hover reads as the viewer's hover card (HoverCard.svelte).
  '.cm-tooltip.cm-symbol-hover': {
    width: 'max-content', maxWidth: 'min(560px, calc(100vw - 8px))', padding: '6px 10px', borderRadius: '6px',
    backgroundColor: 'var(--bg-primary)', boxShadow: '0 4px 16px rgba(0, 0, 0, 0.25)',
    fontFamily: 'var(--font-sans)', fontSize: '12.5px', lineHeight: '1.5', wordBreak: 'break-word', overflow: 'auto',
  },
  '.cm-symbol-hover p, .cm-symbol-hover .code-block-container, .cm-symbol-hover ul, .cm-symbol-hover ol': { margin: '0.4em 0' },
  '.cm-symbol-hover .hover-body > :first-child': { marginTop: '0' },
  '.cm-symbol-hover .hover-body > :last-child': { marginBottom: '0' },
  '.cm-symbol-hover ul, .cm-symbol-hover ol': { paddingLeft: '1.4em' },
  '.cm-symbol-hover .inline-code': { fontFamily: 'var(--font-mono)', fontSize: '0.9em', padding: '0 3px', borderRadius: '3px', backgroundColor: 'var(--bg-tertiary)' },
  '.cm-symbol-hover blockquote': { margin: '0.4em 0', paddingLeft: '0.8em', borderLeft: '2px solid var(--border-color)' },
  '.cm-symbol-hover a': { color: 'var(--accent-color)' },
  '.cm-symbol-hover .hover-plain': { margin: '0', fontFamily: 'var(--font-mono)', whiteSpace: 'pre-wrap' },
  '.cm-symbol-hover .hover-truncated': { marginTop: '4px', fontSize: '11px', color: 'var(--text-secondary)' },
  '.cm-panels': { backgroundColor: 'var(--bg-secondary)', color: 'var(--text-primary)' },
  '& .cm-panels-bottom': { borderTop: '1px solid var(--border-color)' },
  '& .cm-panels-top': { borderBottom: '1px solid var(--border-color)' },
  '.cm-textfield, .cm-button': { background: 'var(--bg-primary)', color: 'var(--text-primary)', border: '1px solid var(--border-color)' },
});

// Tooltips (diagnostic messages) fit in the editor's own box. Measured
// against the window, one above the first lines went under the file
// header; now it flips below, or shortens, when there is no room above.
const fitTooltips = tooltips({ tooltipSpace: view => view.scrollDOM.getBoundingClientRect() });

// Use exactly the viewer's tokenizer, not a best-effort mapping of Lezer
// tags: the two parsers disagree on properties, builtins, parameters, etc.
// Language extensions below still provide indentation and editing support.
function fileHighlight(hljs: HLJSApi, path: string): Extension {
  const language = fileLanguage(path);
  const lang = language && hljs.getLanguage(language) ? language : 'plaintext';
  function decorate(state: EditorState): DecorationSet {
    const html = hljs.highlight(state.doc.toString(), { language: lang, ignoreIllegals: true }).value;
    const marks = [];
    const open: { from: number; class: string }[] = [];
    let offset = 0;
    // highlight.js emits only escaped text and span tags. Count decoded UTF-16
    // units so entities and non-ASCII text retain the document's exact offsets.
    for (const token of html.matchAll(/<span class="([^"]*)">|<\/span>|&(?:#\d+|#x[\da-f]+|\w+);|[^<&]+/gi)) {
      if (token[1] !== undefined) open.push({ from: offset, class: token[1] });
      else if (token[0] === '</span>') {
        const mark = open.pop();
        if (mark && mark.from < offset) marks.push(Decoration.mark({ class: mark.class }).range(mark.from, offset));
      } else offset += token[0].startsWith('&') ? 1 : token[0].length;
    }
    return Decoration.set(marks, true);
  }
  return StateField.define<DecorationSet>({
    create: decorate,
    update: (value, transaction) => transaction.docChanged ? decorate(transaction.state) : value,
    provide: field => EditorView.decorations.from(field),
  });
}

// Infer the unit once per document load, not on each keystroke (which could
// change the setting halfway through typing). Prefer repeated indent steps
// over absolute columns, so nested blocks and continuation alignment don't
// make a four-space file look like an eight-space file.
function detectIndent(text: string): string {
  let tabs = 0;
  let spaces = 0;
  let previous = 0;
  const widths = new Map<number, number>();
  for (const line of text.split('\n').slice(0, 10000)) {
    if (!line.trim()) continue;
    const prefix = /^[\t ]*/.exec(line)![0];
    if (prefix.includes('\t')) {
      tabs++;
      previous = 0;
      continue;
    }
    const width = prefix.length;
    if (width) spaces++;
    const step = Math.abs(width - previous);
    // Ignore single-column alignment and unusually deep continuation indents.
    if (step >= 2 && step <= 8) widths.set(step, (widths.get(step) ?? 0) + 1);
    previous = width;
  }
  if (tabs > spaces) return '\t';
  let best = 2;
  let count = 0;
  for (const [width, votes] of widths) {
    if (votes > count || (votes === count && width < best)) {
      best = width;
      count = votes;
    }
  }
  return ' '.repeat(best);
}

function lineSelection(text: string, selection: FileLineSelection) {
  const doc = EditorState.create({ doc: text }).doc;
  const line = (number: number) => doc.line(Math.max(1, Math.min(number, doc.lines)));
  return { anchor: line(selection.anchor).from, head: line(selection.head).from };
}

/** Convert against the actual LF-normalized EditorState document, sharing the
 * source viewer's provider fallback, freshness and strict UTF-16 validation. */
export function editorDiagnostics(state: EditorState, options: FileEditorOptions): Diagnostic[] {
  const input = options.diagnostics;
  if (!input || input.tab !== options.id || input.path !== options.path || !input.context || options.text.replace(/\r\n/g, '\n') !== state.doc.toString()) return [];
  return projectSourceDiagnostics(state.doc.toString(), input.findings.filter(f => f.diagnostic.path === options.path), input, input.current)
    .messages.flatMap(({ finding, notice }) => {
      if (notice) return [];
      const d = finding.diagnostic;
      const offsets = rangeOffsets(state.doc.toString(), d.range!);
      return offsets ? [{ from: offsets.anchor, to: offsets.head,
        severity: d.severity === 'information' ? 'info' as const : d.severity,
        source: [d.source || finding.entries.map(e => e.providerId).join(', '), d.code].filter(Boolean).join(' '),
        message: d.message }] : [];
    });
}

// Lines with diagnostics are marked on their numbers, as in the viewer. A
// separate lint gutter would widen the gutter and move the text.
const diagnosticLine = new class extends GutterMarker { elementClass = 'cm-has-diagnostics'; }();
const diagnosticLines = StateField.define<RangeSet<GutterMarker>>({
  create: () => RangeSet.empty,
  update(value, tr) {
    if (!tr.docChanged && !tr.effects.some(e => e.is(setDiagnosticsEffect))) return value;
    const { doc } = tr.state;
    const starts = new Set<number>();
    forEachDiagnostic(tr.state, (_, from, to) => {
      // The end is exclusive: a range ending at a line's start does not reach that line.
      for (let line = doc.lineAt(from).number, last = doc.lineAt(Math.max(from, to - 1)).number; line <= last; line++) starts.add(doc.line(line).from);
    });
    return RangeSet.of([...starts].map(from => diagnosticLine.range(from)), true);
  },
  provide: field => lineNumberMarkers.from(field),
});

// Clearing in the same transaction prevents even one frame of automatically
// mapped saved markers. Undo is a document change too, not freshness evidence.
const clearSavedMarkers = EditorState.transactionExtender.of(tr => tr.docChanged
  ? { effects: setDiagnosticsEffect.of([]) } : null);
function withDiagnostics(state: EditorState, options: FileEditorOptions): EditorState {
  return state.update(setDiagnostics(state, editorDiagnostics(state, options))).state;
}

// F8 still steps through diagnostics. Mod-Shift-m would open CodeMirror's own
// lint panel; Ctrl+Shift+M toggles the dialog's problems panel instead.
const editorLintKeymap = lintKeymap.filter(binding => binding.key !== 'Mod-Shift-m');

// Marks a draft the store changed (a replace in files), already in the draft.
const external = Annotation.define<boolean>();

// Code navigation: Cmd/Ctrl+click, F12, Shift+F12 and hover. Positions are
// LSP's (0-based line, UTF-16 column), taken from the document shown, which
// counts UTF-16 units as LSP does. Like every handler here, these find the
// view's current options through owners, never through a closure.

/** The 0-based line and UTF-16 column of a document offset. */
export function editorPosition(state: EditorState, pos: number): CodeDiagnosticPosition {
  const line = state.doc.lineAt(pos);
  return { line: line.number - 1, character: pos - line.from };
}

/** The word (identifier) the pointer is over: the character after pos when side > 0, else before it. */
export function wordAtSide(state: EditorState, pos: number, side: number): { from: number; to: number } | undefined {
  const line = state.doc.lineAt(pos);
  const at = side < 0 ? pos - 1 : pos;
  if (at < line.from || at >= line.to) return undefined;
  const word = wordRange(line.text, at - line.from);
  // wordRange falls back to the character before; under the pointer only.
  if (!word || at - line.from < word.start || at - line.from >= word.end) return undefined;
  return { from: line.from + word.start, to: line.from + word.end };
}

/**
 * Whether the editor still shows the text it showed now, in the same tab and
 * path. While it does, a synced live document holds exactly this text: every
 * edit goes to the draft (and its document) as it is made.
 */
function askedText(view: EditorView): () => boolean {
  const owner = owners.get(view), doc = view.state.doc;
  return () => {
    const now = owners.get(view);
    return Boolean(owner && now && now.id === owner.id && now.path === owner.path && now.version === owner.version
      && view.state.doc.eq(doc) && lf(now.text) === doc.toString());
  };
}

/** F12 and Shift+F12 at the caret: the word around it, else the caret itself. */
function navigateAtCaret(op: 'definition' | 'references') {
  return (view: EditorView): boolean => {
    const symbols = owners.get(view)?.symbols;
    if (!symbols) return false;
    const head = view.state.selection.main.head;
    const line = view.state.doc.lineAt(head);
    const word = wordRange(line.text, head - line.from);
    const coords = view.coordsAtPos(head);
    symbols.ask(op, editorPosition(view.state, word ? line.from + word.start : head), { x: coords?.left ?? 0, y: coords?.bottom ?? 0 }, askedText(view));
    return true;
  };
}

export const navigationKeymap: readonly KeyBinding[] = [
  { key: 'F12', run: navigateAtCaret('definition'), preventDefault: true },
  { key: 'Shift-F12', run: navigateAtCaret('references'), preventDefault: true },
];

/** The word under viewport coordinates, judged as CodeMirror judges a hover. */
function wordAtCoords(view: EditorView, x: number, y: number): { from: number; to: number } | undefined {
  const pos = view.posAtCoords({ x, y });
  if (pos === null) return undefined;
  const coords = view.coordsAtPos(pos);
  if (!coords || y < coords.top || y > coords.bottom
    || x < coords.left - view.defaultCharacterWidth || x > coords.right + view.defaultCharacterWidth) return undefined;
  return wordAtSide(view.state, pos, x < coords.left ? -1 : 1);
}

/** Cmd (macOS) or Ctrl+click on a word goes to its definition instead of placing the caret. */
export function symbolMouseDown(event: MouseEvent, view: EditorView): boolean {
  const symbols = owners.get(view)?.symbols;
  if (!symbols || event.button !== 0 || !linkModifier(event, /Mac|iPhone|iPad/.test(navigator.platform))) return false;
  const word = wordAtCoords(view, event.clientX, event.clientY);
  if (!word) return false;
  event.preventDefault();
  symbols.ask('definition', editorPosition(view.state, word.from), { x: event.clientX, y: event.clientY }, askedText(view));
  return true;
}

/** The hover tooltip of the word under the pointer, from the language server. */
export async function symbolHover(view: EditorView, pos: number, side: -1 | 1): Promise<Tooltip | null> {
  const symbols = owners.get(view)?.symbols;
  const word = symbols && wordAtSide(view.state, pos, side);
  if (!symbols || !word) return null;
  const sameText = askedText(view), top = view.scrollDOM.scrollTop, left = view.scrollDOM.scrollLeft;
  const current = () => sameText() && view.scrollDOM.scrollTop === top && view.scrollDOM.scrollLeft === left;
  const hover = await symbols.hover(editorPosition(view.state, word.from), current);
  if (!hover || !current()) return null;
  return {
    pos: word.from, end: word.to, above: false,
    create() {
      const dom = document.createElement('div');
      dom.className = 'cm-symbol-hover';
      const body = dom.appendChild(document.createElement('div'));
      body.className = 'hover-body';
      // Markdown through the transcript renderer, or escaped plain text (hoverHtml).
      body.innerHTML = hover.html;
      if (hover.truncated) {
        const note = dom.appendChild(document.createElement('div'));
        note.className = 'hover-truncated';
        note.textContent = 'The description was cut short.';
      }
      dom.addEventListener('click', handleCopyCodeClick);
      return {
        dom,
        // At most 60% of the editor's height, as in the viewer; it scrolls.
        mount(view) { dom.style.maxHeight = `${Math.max(80, view.scrollDOM.clientHeight * 0.6)}px`; },
      };
    },
  };
}

const navigation: Extension = [
  keymap.of(navigationKeymap),
  EditorView.domEventHandlers({
    mousedown(event, view) {
      if (hasHoverTooltips(view.state)) view.dispatch({ effects: closeHoverTooltips });
      return symbolMouseDown(event, view);
    },
    scroll(_event, view) {
      if (hasHoverTooltips(view.state)) view.dispatch({ effects: closeHoverTooltips });
      return false;
    },
  }),
  hoverTooltip(symbolHover, { hoverTime: HOVER_DELAY, hideOnChange: true }),
];

function freshState(options: FileEditorOptions): EditorState {
  return EditorState.create({
    doc: options.text,
    selection: options.selection ? lineSelection(options.text, options.selection) : undefined,
    extensions: [
      lineNumbers(), history(), drawSelection(), highlightActiveLine(), indentOnInput(),
      diagnosticLines, clearSavedMarkers, navigation,
      search(), theme, fitTooltips, indentUnit.of(detectIndent(options.text)), EditorState.tabSize.of(4),
      wrapping.of(options.wrap ? EditorView.lineWrapping : []),
      editing.of(editable(options.disabled)), language.of([]), coloring.of([]),
      keymap.of([indentWithTab, ...defaultKeymap, ...historyKeymap, ...searchKeymap, ...editorLintKeymap]),
      EditorView.updateListener.of(update => {
        if (update.docChanged && !update.transactions.every(tr => tr.annotation(external)))
          owners.get(update.view)?.onchange(update.state.doc.toString());
      }),
    ],
  });
}

/**
 * The state with its document changed to text as one undoable step of its
 * own, so a replace in files keeps the history before it and undo takes
 * back exactly the replace.
 */
function adoption(state: EditorState, text: string): TransactionSpec {
  return { changes: textChanges(state.doc.toString(), lf(text)), annotations: [external.of(true), isolateHistory.of('full')] };
}

function adopt(state: EditorState, text: string): EditorState {
  return state.doc.toString() === lf(text) ? state : state.update(adoption(state, text)).state;
}

// The document as CodeMirror holds it: every line break is \n.
const lf = (text: string) => text.replace(/\r\n?/g, '\n');

function handleKey(view: EditorView, event: KeyboardEvent): boolean {
  // A parent window handler can see an already-consumed event.
  if (keyHandled(event)) return true;
  const mod = /Mac|iPhone|iPad/.test(navigator.platform) ? event.metaKey : event.ctrlKey;
  const key = event.key.toLowerCase();
  let consumed = false;
  if (mod && !event.altKey && !event.shiftKey && key === 's') {
    owners.get(view)?.onsave();
    consumed = true;
  } else if (mod && !event.altKey && !event.shiftKey && key === 'f') {
    openSearchPanel(view);
    consumed = true;
  } else if (event.key === 'Escape' && hasHoverTooltips(view.state)) {
    // A hover (a symbol's, or a diagnostic's) closes before the search panel or the dialog.
    view.dispatch({ effects: closeHoverTooltips });
    consumed = true;
  } else if (event.key === 'Escape' && searchPanelOpen(view.state)) {
    closeSearchPanel(view);
    consumed = true;
  }
  if (consumed) {
    event.preventDefault();
    event.stopPropagation();
  }
  return consumed;
}

// Picks the editor's language by file name from @codemirror/language-data,
// which loads each language only when a file needs it. Svelte is not in that
// list, so it has its own package. Names the list does not know (.zshrc,
// Brewfile) fall back to the viewer's highlight.js language, looked up by name.
async function loadLanguage(path: string): Promise<Extension> {
  const name = path.slice(path.lastIndexOf('/') + 1);
  if (/\.svelte$/i.test(name)) return (await import('@replit/codemirror-lang-svelte')).svelte();
  const { languages } = await import('@codemirror/language-data');
  const hljsName = fileLanguage(path);
  const description =
    LanguageDescription.matchFilename(languages, name) ??
    (hljsName ? LanguageDescription.matchLanguageName(languages, hljsName, false) : null);
  return description ? await description.load() : [];
}

export function createFileEditor(parent: HTMLElement, initial: FileEditorOptions) {
  let options = initial;
  let destroyed = false;
  let languageRequest = 0;
  let scrollRequest = 0;
  const cached = tabs.get(options.id);
  // The same version is the same history: a draft changed meanwhile (a
  // replace in files) is taken in as one undoable change.
  const reuse = cached?.version === options.version;
  const view = new EditorView({ parent, state: withDiagnostics(reuse ? adopt(cached.state, options.text) : freshState(options), options) });
  owners.set(view, options);
  // Capture also covers the search panel: its own keymap otherwise closes the
  // panel before a bubbling parent handler can recognize and consume Escape.
  const onKeyDown = (event: KeyboardEvent) => { handleKey(view, event); };
  view.dom.addEventListener('keydown', onKeyDown, true);

  function configure() {
    view.dispatch({ effects: [
      wrapping.reconfigure(options.wrap ? EditorView.lineWrapping : []),
      editing.reconfigure(editable(options.disabled)),
    ] });
  }
  function setLanguage() {
    const request = ++languageRequest;
    const path = options.path;
    void loadFileHighlighter().then(hljs => {
      if (!destroyed && request === languageRequest) {
        view.dispatch({ effects: coloring.reconfigure(fileHighlight(hljs, path)) });
      }
    }).catch(() => { /* Keep plain text if the lazy highlighter cannot load. */ });
    void loadLanguage(path).catch((): Extension => []).then(extension => {
      if (!destroyed && request === languageRequest) view.dispatch({ effects: language.reconfigure(extension) });
    });
  }
  function restore(top: number, left: number) {
    const request = ++scrollRequest;
    view.scrollDOM.scrollTop = top;
    view.scrollDOM.scrollLeft = left;
    view.requestMeasure({ read: () => null, write() {
      if (!destroyed && request === scrollRequest) {
        view.scrollDOM.scrollTop = top;
        view.scrollDOM.scrollLeft = left;
      }
    } });
  }
  function position(): FilePosition {
    const top = view.scrollDOM.scrollTop;
    const block = view.lineBlockAtHeight(Math.max(0, top - view.documentPadding.top));
    return {
      line: view.state.doc.lineAt(block.from).number,
      offset: block.top + view.documentPadding.top - top,
      left: view.scrollDOM.scrollLeft,
    };
  }
  function restorePosition(target: FilePosition) {
    const request = ++scrollRequest;
    const line = view.state.doc.line(Math.max(1, Math.min(target.line, view.state.doc.lines)));
    // Scroll to the logical line first so CodeMirror renders and measures it,
    // including wrapped lines outside the initial virtual viewport.
    view.dispatch({ effects: EditorView.scrollIntoView(line.from, { y: 'start', yMargin: 0 }) });
    // Flush CodeMirror's pending scroll before applying the exact viewer
    // offset. It processes scrollIntoView AFTER requestMeasure writes, so
    // scheduling both together lets its scroll overwrite our restoration.
    view.lineBlockAtHeight(0);
    view.requestMeasure({
      read: () => view.lineBlockAt(line.from).top + view.documentPadding.top - target.offset,
      write(top) {
        if (!destroyed && request === scrollRequest) {
          view.scrollDOM.scrollTop = top;
          view.scrollDOM.scrollLeft = target.left;
        }
      },
    });
  }
  function reportPosition() { options.onposition?.(position()); }
  view.scrollDOM.addEventListener('scroll', reportPosition);
  function remember() {
    reportPosition();
    const range = view.state.selection.main;
    options.onselection?.({ anchor: view.state.doc.lineAt(range.anchor).number, head: view.state.doc.lineAt(range.head).number });
    tabs.set(options.id, { state: view.state, version: options.version, top: view.scrollDOM.scrollTop, left: view.scrollDOM.scrollLeft });
  }
  if (reuse && options.selection) view.dispatch({ selection: lineSelection(options.text, options.selection) });
  configure();
  setLanguage();
  if (options.position) restorePosition(options.position);
  else if (reuse) restore(cached.top, cached.left);

  return {
    update(next: FileEditorOptions) {
      if (destroyed) return;
      const changedTab = next.id !== options.id;
      const reset = changedTab || next.version !== options.version;
      const adopted = !reset && lf(next.text) !== view.state.doc.toString();
      const changedPath = next.path !== options.path;
      if (changedTab) remember();
      options = next;
      owners.set(view, options);
      if (reset) {
        const saved = changedTab ? tabs.get(options.id) : undefined;
        const valid = saved?.version === options.version;
        view.setState(withDiagnostics(valid ? adopt(saved.state, options.text) : freshState(options), options));
        if (options.position) restorePosition(options.position);
        else restore(valid ? saved.top : 0, valid ? saved.left : 0);
      } else if (adopted) view.dispatch(adoption(view.state, options.text));
      configure();
      view.dispatch(setDiagnostics(view.state, editorDiagnostics(view.state, options)));
      if (reset || changedPath) setLanguage();
    },
    revealRange(range: CodeDiagnosticRange): boolean {
      if (destroyed) return false;
      const selection = rangeOffsets(view.state.doc.toString(), range);
      if (!selection) return false;
      ++scrollRequest; // Supersede pending scroll restoration, not document/history.
      view.dispatch({ selection, effects: EditorView.scrollIntoView(selection.anchor, { y: 'center' }) });
      return true;
    },
    focus() { if (!destroyed) view.focus(); },
    /** Where the selection starts, for navigation history. */
    caret(): CodeDiagnosticPosition | undefined {
      return destroyed ? undefined : editorPosition(view.state, view.state.selection.main.from);
    },
    selectedText() {
      if (destroyed) return '';
      const { from, to } = view.state.selection.main;
      return view.state.sliceDoc(from, to);
    },
    find() { return !destroyed && openSearchPanel(view); },
    closeSearch() { return !destroyed && closeSearchPanel(view); },
    handleKey(event: KeyboardEvent) { return !destroyed && handleKey(view, event); },
    destroy() {
      if (destroyed) return;
      remember();
      destroyed = true;
      languageRequest++;
      owners.delete(view);
      view.dom.removeEventListener('keydown', onKeyDown, true);
      view.scrollDOM.removeEventListener('scroll', reportPosition);
      view.destroy();
    },
  };
}

export type FileEditorController = ReturnType<typeof createFileEditor>;
