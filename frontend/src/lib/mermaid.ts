/**
 * Draws the Mermaid blocks that renderMarkdown emits. The mermaid package is
 * large, so it is only loaded once a transcript has a diagram to show.
 */

import type { ResolvedTheme } from './theme';
import { rasterPng } from './images';
import { openViewer } from './viewer';

type Mermaid = (typeof import('mermaid'))['default'];

const CACHE_LIMIT = 100;

let loading: Promise<Mermaid> | null = null;
// The theme and label kind mermaid is set up for, as in draw's cache key.
let configured: string | null = null;
let nextId = 0;
// Mermaid keeps one global configuration, so diagrams are drawn one at a time.
let queue: Promise<void> = Promise.resolve();
// SVG by theme and source: a remounted message is drawn again without waiting.
const drawn = new Map<string, string>();

function load(): Promise<Mermaid> {
  loading ??= import('mermaid').then((m) => m.default);
  return loading;
}

// Labels are HTML by default, which wraps text best on screen. An image is
// drawn with SVG text instead: a canvas won't take HTML labels (WebKit
// taints it), and other programs show them poorly.
async function draw(source: string, theme: ResolvedTheme, svgLabels = false): Promise<string> {
  const setup = `${theme}${svgLabels ? ' svg' : ''}`;
  const key = `${setup}\n${source}`;
  const cached = drawn.get(key);
  if (cached !== undefined) return cached;
  const mermaid = await load();
  if (configured !== setup) {
    // strict: no script or click handlers in diagrams, and labels are
    // sanitized; transcripts are untrusted.
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      suppressErrorRendering: true,
      theme: theme === 'dark' ? 'dark' : 'default',
      htmlLabels: !svgLabels,
    });
    configured = setup;
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

/** Opens the diagram of the Mermaid block holding el in full. */
export function expandMermaid(el: HTMLElement): void {
  const block = el.closest<HTMLElement>('.mermaid-block');
  const source = block?.querySelector('.copy-code-btn')?.getAttribute('data-code');
  if (source && block?.querySelector('.mermaid-diagram svg')) openViewer({ kind: 'diagram', source });
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

/** A diagram saved as an image, with its size in CSS pixels. */
export interface DiagramImage {
  svg: string;
  width: number;
  height: number;
}

/**
 * A diagram as a standalone SVG file in the theme, on a background of the
 * given CSS colour, at the size Mermaid drew it.
 */
export async function diagramImage(source: string, theme: ResolvedTheme, background: string): Promise<DiagramImage> {
  const drawnSvg = renameSvgId(await enqueue(() => draw(source, theme, true)), 'mermaid-diagram-image');
  // The HTML parser takes whatever Mermaid wrote; XMLSerializer then writes
  // well-formed XML with the namespaces a file needs.
  const svg = new DOMParser().parseFromString(drawnSvg, 'text/html').querySelector('svg');
  if (!svg) throw new Error('Mermaid did not draw an SVG');
  const box = svg.viewBox.baseVal;
  if (!box || !(box.width > 0) || !(box.height > 0)) throw new Error('the diagram has no size');
  const width = Math.ceil(box.width);
  const height = Math.ceil(box.height);
  svg.setAttribute('width', String(width));
  svg.setAttribute('height', String(height));
  svg.style.removeProperty('max-width');
  if (!svg.getAttribute('style')) svg.removeAttribute('style');
  const fill = document.createElementNS('http://www.w3.org/2000/svg', 'rect');
  fill.setAttribute('x', String(box.x));
  fill.setAttribute('y', String(box.y));
  fill.setAttribute('width', String(box.width));
  fill.setAttribute('height', String(box.height));
  fill.setAttribute('fill', background);
  svg.insertBefore(fill, svg.firstChild);
  const text = `<?xml version="1.0" encoding="UTF-8"?>\n${new XMLSerializer().serializeToString(svg)}\n`;
  return { svg: text, width, height };
}

/** image as a PNG at scale times its size, or smaller to fit a canvas. */
export async function diagramPng(image: DiagramImage, scale = 2): Promise<Blob> {
  const url = URL.createObjectURL(new Blob([image.svg], { type: 'image/svg+xml' }));
  try {
    return await rasterPng(url, image.width, image.height, scale);
  } finally {
    URL.revokeObjectURL(url);
  }
}
