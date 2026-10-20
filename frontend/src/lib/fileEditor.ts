import { Compartment, EditorState, StateField, type Extension } from '@codemirror/state';
import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { LanguageDescription, indentOnInput } from '@codemirror/language';
import { Decoration, type DecorationSet } from '@codemirror/view';
import { loadFileHighlighter } from './codeHighlight';
import type { HLJSApi } from 'highlight.js';
import { keyHandled } from './fullscreen';
import type { FilePosition } from './filePosition';
import { fileLanguage } from './files';
import { closeSearchPanel, openSearchPanel, search, searchKeymap, searchPanelOpen } from '@codemirror/search';

export interface FileEditorOptions {
  id: object;
  text: string;
  path: string;
  version: number;
  wrap: boolean;
  disabled?: boolean;
  position?: FilePosition;
  onposition?: (position: FilePosition) => void;
  onchange: (text: string) => void;
  onsave: () => void;
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
  '.cm-panels': { backgroundColor: 'var(--bg-secondary)', color: 'var(--text-primary)' },
  '.cm-textfield, .cm-button': { background: 'var(--bg-primary)', color: 'var(--text-primary)', border: '1px solid var(--border-color)' },
});

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

function freshState(options: FileEditorOptions): EditorState {
  return EditorState.create({
    doc: options.text,
    extensions: [
      lineNumbers(), history(), drawSelection(), highlightActiveLine(), indentOnInput(),
      search(), theme,
      wrapping.of(options.wrap ? EditorView.lineWrapping : []),
      editing.of(editable(options.disabled)), language.of([]), coloring.of([]),
      keymap.of([indentWithTab, ...defaultKeymap, ...historyKeymap, ...searchKeymap]),
      EditorView.updateListener.of(update => {
        if (update.docChanged) owners.get(update.view)?.onchange(update.state.doc.toString());
      }),
    ],
  });
}

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
  const reuse = cached?.version === options.version && cached.state.doc.toString() === options.text;
  const view = new EditorView({ parent, state: reuse ? cached.state : freshState(options) });
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
    tabs.set(options.id, { state: view.state, version: options.version, top: view.scrollDOM.scrollTop, left: view.scrollDOM.scrollLeft });
  }
  configure();
  setLanguage();
  if (options.position) restorePosition(options.position);
  else if (reuse) restore(cached.top, cached.left);

  return {
    update(next: FileEditorOptions) {
      if (destroyed) return;
      const changedTab = next.id !== options.id;
      const reset = changedTab || next.version !== options.version || next.text !== view.state.doc.toString();
      const changedPath = next.path !== options.path;
      if (changedTab) remember();
      options = next;
      owners.set(view, options);
      if (reset) {
        const saved = changedTab ? tabs.get(options.id) : undefined;
        const valid = saved?.version === options.version && saved.state.doc.toString() === options.text;
        view.setState(valid ? saved.state : freshState(options));
        if (options.position) restorePosition(options.position);
        else restore(valid ? saved.top : 0, valid ? saved.left : 0);
      }
      configure();
      if (reset || changedPath) setLanguage();
    },
    focus() { if (!destroyed) view.focus(); },
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
