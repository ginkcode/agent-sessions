/**
 * Draws the Mermaid blocks that renderMarkdown emits. The mermaid package is
 * large, so it is only loaded once a transcript has a diagram to show.
 */

import type { ResolvedTheme } from './theme';

type Mermaid = (typeof import('mermaid'))['default'];

const CACHE_LIMIT = 100;

let loading: Promise<Mermaid> | null = null;
let configured: ResolvedTheme | null = null;
let nextId = 0;
// Mermaid keeps one global configuration, so diagrams are drawn one at a time.
let queue: Promise<void> = Promise.resolve();
// SVG by theme and source: a remounted message is drawn again without waiting.
const drawn = new Map<string, string>();
let expander: ((source: string) => void) | null = null;

function load(): Promise<Mermaid> {
  loading ??= import('mermaid').then((m) => m.default);
  return loading;
}

async function draw(source: string, theme: ResolvedTheme): Promise<string> {
  const key = `${theme}\n${source}`;
  const cached = drawn.get(key);
  if (cached !== undefined) return cached;
  const mermaid = await load();
  if (configured !== theme) {
    // strict: no script or click handlers in diagrams, and labels are
    // sanitized; transcripts are untrusted.
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      suppressErrorRendering: true,
      theme: theme === 'dark' ? 'dark' : 'default',
    });
    configured = theme;
  }
  const { svg } = await mermaid.render(`mermaid-diagram-${++nextId}`, source);
  if (drawn.size >= CACHE_LIMIT) drawn.delete(drawn.keys().next().value!);
  drawn.set(key, svg);
  return svg;
}

function enqueue<T>(job: () => Promise<T>): Promise<T> {
  const result = queue.then(job);
  queue = result.then(
    () => {},
    () => {}
  );
  return result;
}

function showView(block: HTMLElement, diagram: boolean): void {
  const toggle = block.querySelector<HTMLElement>('.mermaid-toggle-btn');
  block.querySelector<HTMLElement>('.mermaid-diagram')!.hidden = !diagram;
  block.querySelector<HTMLElement>('.mermaid-source')!.hidden = diagram;
  if (toggle) toggle.textContent = diagram ? 'Source' : 'Diagram';
}

/**
 * Draws every Mermaid block under root in the given theme. Blocks already
 * drawn in that theme are left alone; a block that fails to parse keeps
 * showing its source.
 */
export function renderMermaid(root: HTMLElement | undefined, theme: ResolvedTheme): void {
  if (!root) return;
  for (const block of root.querySelectorAll<HTMLElement>('.mermaid-block')) {
    if (block.dataset.theme === theme) continue;
    block.dataset.theme = theme;
    const source = block.querySelector('.copy-code-btn')?.getAttribute('data-code') ?? '';
    enqueue(() => draw(source, theme)).then(
      (svg) => {
        if (!block.isConnected || block.dataset.theme !== theme) return;
        block.querySelector<HTMLElement>('.mermaid-diagram')!.innerHTML = svg;
        for (const button of block.querySelectorAll<HTMLElement>('.mermaid-toggle-btn, .mermaid-expand-btn')) {
          button.hidden = false;
        }
        block.removeAttribute('title');
        showView(block, block.dataset.view !== 'source');
      },
      (err: unknown) => {
        if (!block.isConnected || block.dataset.theme !== theme) return;
        block.title = `Diagram not rendered: ${err instanceof Error ? err.message : String(err)}`;
      }
    );
  }
}

/** Switches a drawn Mermaid block between its diagram and its source. */
export function toggleMermaidView(button: HTMLElement): void {
  const block = button.closest<HTMLElement>('.mermaid-block');
  if (!block) return;
  const diagram = block.dataset.view === 'source';
  block.dataset.view = diagram ? 'diagram' : 'source';
  showView(block, diagram);
}

/** Sets who shows a diagram in full; null when nobody does. */
export function setDiagramExpander(fn: ((source: string) => void) | null): void {
  expander = fn;
}

/** Opens the diagram of the Mermaid block holding el in full. */
export function expandMermaid(el: HTMLElement): void {
  const block = el.closest<HTMLElement>('.mermaid-block');
  const source = block?.querySelector('.copy-code-btn')?.getAttribute('data-code');
  if (source && block?.querySelector('.mermaid-diagram svg')) expander?.(source);
}

/**
 * svg with its id, and every reference to it, renamed to id: a copy of a
 * diagram must not share ids with the original, or the copy's styles and
 * arrowheads would point into the other.
 */
export function renameSvgId(svg: string, id: string): string {
  const current = /^<svg[^>]*?\sid="([^"]+)"/.exec(svg)?.[1];
  if (!current) return svg;
  const escaped = current.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  // Not followed by a digit: renaming mermaid-diagram-3 leaves -30 alone.
  return svg.replace(new RegExp(`${escaped}(?!\\d)`, 'g'), id);
}

/** A diagram's SVG drawn under another id, for showing it a second time. */
export async function diagramSvg(source: string, theme: ResolvedTheme, id: string): Promise<string> {
  return renameSvgId(await enqueue(() => draw(source, theme)), id);
}
