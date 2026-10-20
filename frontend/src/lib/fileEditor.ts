import { Compartment, EditorState, type Extension } from '@codemirror/state';
import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { defaultHighlightStyle, indentOnInput, syntaxHighlighting } from '@codemirror/language';
import { keyHandled } from './fullscreen';
import { closeSearchPanel, openSearchPanel, search, searchKeymap, searchPanelOpen } from '@codemirror/search';

export interface FileEditorOptions {
  id: object;
  text: string;
  path: string;
  version: number;
  wrap: boolean;
  disabled?: boolean;
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
const editable = (disabled = false): Extension => [
  EditorView.editable.of(!disabled),
  EditorState.readOnly.of(disabled),
];

const theme = EditorView.theme({
  '&': { height: '100%', color: 'var(--text-primary)', backgroundColor: 'var(--bg-primary)', fontSize: '0.78rem' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { overflow: 'auto', fontFamily: 'var(--font-mono)', lineHeight: '1.5' },
  '.cm-content': { padding: '8px 0', caretColor: 'var(--text-primary)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--text-primary)' },
  '.cm-gutters': { backgroundColor: 'var(--bg-secondary)', color: 'var(--text-muted)', borderRight: '1px solid var(--border-color)' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'var(--bg-secondary)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': { backgroundColor: 'var(--bg-tertiary)' },
  '.cm-panels': { backgroundColor: 'var(--bg-secondary)', color: 'var(--text-primary)' },
  '.cm-textfield, .cm-button': { background: 'var(--bg-primary)', color: 'var(--text-primary)', border: '1px solid var(--border-color)' },
});

function freshState(options: FileEditorOptions): EditorState {
  return EditorState.create({
    doc: options.text,
    extensions: [
      lineNumbers(), history(), drawSelection(), highlightActiveLine(), indentOnInput(),
      syntaxHighlighting(defaultHighlightStyle, { fallback: true }), search(), theme,
      wrapping.of(options.wrap ? EditorView.lineWrapping : []),
      editing.of(editable(options.disabled)), language.of([]),
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

async function loadLanguage(path: string): Promise<Extension> {
  const ext = path.split('.').pop()?.toLowerCase();
  switch (ext) {
    case 'js': case 'mjs': case 'cjs': case 'jsx': case 'ts': case 'mts': case 'cts': case 'tsx': {
      const { javascript } = await import('@codemirror/lang-javascript');
      return javascript({ typescript: ['ts', 'mts', 'cts', 'tsx'].includes(ext), jsx: ext === 'jsx' || ext === 'tsx' });
    }
    case 'json': return (await import('@codemirror/lang-json')).json();
    case 'html': case 'htm': return (await import('@codemirror/lang-html')).html();
    case 'css': return (await import('@codemirror/lang-css')).css();
    case 'md': case 'markdown': return (await import('@codemirror/lang-markdown')).markdown();
    case 'py': case 'pyw': return (await import('@codemirror/lang-python')).python();
    case 'go': return (await import('@codemirror/lang-go')).go();
    case 'yml': case 'yaml': return (await import('@codemirror/lang-yaml')).yaml();
    default: return [];
  }
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
    void loadLanguage(options.path).catch((): Extension => []).then(extension => {
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
  function remember() {
    tabs.set(options.id, { state: view.state, version: options.version, top: view.scrollDOM.scrollTop, left: view.scrollDOM.scrollLeft });
  }
  configure();
  setLanguage();
  if (reuse) restore(cached.top, cached.left);

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
        restore(valid ? saved.top : 0, valid ? saved.left : 0);
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
      view.destroy();
    },
  };
}

export type FileEditorController = ReturnType<typeof createFileEditor>;
