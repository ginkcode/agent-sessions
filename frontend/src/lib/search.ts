import type { FTSProgress, SearchFilter } from './types';

/**
 * Debounces requests and exposes a generation token so late responses from
 * superseded queries can be discarded.
 */
export class LatestRequestGate {
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private readonly delayMs: number;

  constructor(delayMs: number) {
    this.delayMs = delayMs;
  }

  schedule(run: () => void): void {
    this.cancel();
    this.timer = setTimeout(() => {
      this.timer = null;
      run();
    }, this.delayMs);
  }

  /** Starts an immediate request and invalidates any older request. */
  begin(): number {
    this.clearTimer();
    return ++this.generation;
  }

  isCurrent(generation: number): boolean {
    return generation === this.generation;
  }

  cancel(): void {
    this.clearTimer();
    this.generation++;
  }

  private clearTimer(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }
}

/** Return true when a global shortcut may safely replace the current focus. */
export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  return Boolean(target.closest('input, textarea, select, [contenteditable]:not([contenteditable="false"])'));
}

/** Any modal owns keyboard input while it is open, not while kept mounted but hidden. */
export function hasOpenModal(root: ParentNode = document): boolean {
  return Array.from(root.querySelectorAll('[role="dialog"][aria-modal="true"]'))
    .some((dialog) => !dialog.closest('[hidden]'));
}

export function progressIncomplete(progress: FTSProgress | null): boolean {
  return Boolean(progress && (progress.running || progress.pending > 0));
}

/**
 * The session list's path filter is partial, case-insensitive text. Hosts
 * that support it match search the same way; older ones only take an absolute
 * directory, so other text cannot scope their search (see searchPathScoped).
 */
export function searchFilterFromApp(
  agent?: string,
  path?: string,
  limit = 30,
  pathText = true
): SearchFilter {
  const filter: SearchFilter = { limit };
  if (agent) filter.agents = [agent];
  const text = path?.trim();
  if (text && pathText) filter.path = text;
  else if (text && isAbsolutePath(text)) filter.dir = text;
  return filter;
}

/** Whether searchFilterFromApp can apply this path filter on the host. */
export function searchPathScoped(path: string | undefined, pathText: boolean): boolean {
  const text = path?.trim();
  return !text || pathText || isAbsolutePath(text);
}

function isAbsolutePath(path: string): boolean {
  return path.startsWith('/') || /^[A-Za-z]:[\\/]/.test(path) || path.startsWith('\\\\');
}

export function jumpOffset(messageIndex: number): number {
  return Math.max(0, messageIndex - 20);
}

export function searchTerms(query: string): string[] {
  const terms: string[] = [];
  const pattern = /"([^"]+)"|([^\s]+)/g;
  let match: RegExpExecArray | null;
  while ((match = pattern.exec(query)) !== null) {
    const value = (match[1] || match[2] || '').trim();
    if (!value || /^(agent|dir):/i.test(value)) continue;
    terms.push(value);
  }
  return terms;
}

/**
 * Mark matching text nodes without ever interpreting transcript text as HTML.
 * Returns a disposer used when a new jump replaces the old one.
 */
export function highlightTextNodes(
  root: HTMLElement,
  query: string
): () => void {
  const terms = searchTerms(query).sort((a, b) => b.length - a.length);
  if (!terms.length) return () => {};
  const escaped = terms.map((term) => term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'));
  const re = new RegExp(escaped.join('|'), 'giu');
  const replacements: { original: Text; inserted: Node[] }[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = node.parentElement;
      if (!node.nodeValue?.trim() || !parent || parent.closest('mark, script, style, button, summary, svg')) {
        return NodeFilter.FILTER_REJECT;
      }
      re.lastIndex = 0;
      return re.test(node.nodeValue) ? NodeFilter.FILTER_ACCEPT : NodeFilter.FILTER_REJECT;
    },
  });
  const nodes: Text[] = [];
  let node: Node | null;
  while ((node = walker.nextNode())) nodes.push(node as Text);

  for (const textNode of nodes) {
    const text = textNode.nodeValue || '';
    const fragment = document.createDocumentFragment();
    let cursor = 0;
    re.lastIndex = 0;
    for (const match of text.matchAll(re)) {
      const at = match.index;
      if (at > cursor) fragment.append(text.slice(cursor, at));
      const mark = document.createElement('mark');
      mark.className = 'search-jump-match';
      mark.textContent = match[0];
      fragment.append(mark);
      cursor = at + match[0].length;
    }
    if (cursor < text.length) fragment.append(text.slice(cursor));
    replacements.push({ original: textNode, inserted: [...fragment.childNodes] });
    textNode.replaceWith(fragment);
  }

  // Put the original nodes back: Svelte may still hold references to them.
  return () => {
    for (const { original, inserted } of replacements) {
      const first = inserted[0];
      if (!first?.parentNode) continue;
      first.parentNode.insertBefore(original, first);
      for (const node of inserted) node.parentNode?.removeChild(node);
    }
    replacements.length = 0;
  };
}

/**
 * Defense in depth for backend snippets, which are already escaped: any
 * markup other than a bare <mark> or </mark> is neutralized as text.
 */
export function safeSnippetHTML(snippet: string): string {
  return snippet.replace(/<(?!\/?mark>)/gi, '&lt;');
}
