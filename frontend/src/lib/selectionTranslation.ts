import type { ContextMenuItem } from './components/common/ContextMenu.svelte';

export const MAX_SELECTION_CONTEXT = 8192;

export function selectionMenuItems(configured: boolean, language: string, copy: () => void, translate: () => void, setup: () => void): ContextMenuItem[] {
  return [
    { label: 'Copy', onSelect: copy },
    ...(configured ? [{ label: `Translate to ${language}`, onSelect: translate }] : [
      { label: 'Translate', onSelect: () => {}, disabled: true, title: 'Set up translation in Settings' },
      { label: 'Set up translation…', onSelect: setup },
    ]),
  ];
}

/** The owner must invalidate this request on replacement or teardown. */
export function requestSelectionTranslation(request: () => Promise<string>, done: (text: string) => void, failed: (error: unknown) => void): () => void {
  let current = true;
  void Promise.resolve().then(request).then(text => { if (current) done(text); }, error => { if (current) failed(error); });
  return () => { current = false; };
}

export interface SelectedText {
  text: string;
  context: string;
  rect: { left: number; right: number; top: number; bottom: number };
  /** A copy of the selected range, held while the menu or popover has focus. */
  range: Range;
}

const HELD = 'held-selection';

/**
 * Focus moving into the menu or popover clears the page selection. Paint the
 * captured range meanwhile; release selects it again unless it left the page or
 * another selection replaced it.
 */
export function holdSelection(range: Range): () => void {
  const highlights = typeof CSS !== 'undefined' && typeof Highlight !== 'undefined' ? CSS.highlights : undefined;
  const highlight = highlights ? new Highlight(range) : undefined;
  if (highlight) highlights!.set(HELD, highlight);
  return () => {
    if (highlight && highlights!.get(HELD) === highlight) highlights!.delete(HELD);
    const selection = window.getSelection();
    if (!selection || !range.startContainer.isConnected || !range.endContainer.isConnected) return;
    if (selection.rangeCount > 0 && !selection.isCollapsed) return;
    selection.removeAllRanges();
    selection.addRange(range);
  };
}

const encoder = new TextEncoder();

/** Keep sentence context near the selected range, bounded in UTF-8 bytes. */
export function sentenceContext(text: string, start: number, end: number): string {
  const Segmenter = Intl.Segmenter;
  const spans = Segmenter
    ? Array.from(new Segmenter(undefined, { granularity: 'sentence' }).segment(text), s => ({ start: s.index, end: s.index + s.segment.length }))
    : Array.from(text.matchAll(/[^.!?。！？\n]+[.!?。！？\n]*|[.!?。！？\n]+/g), s => ({ start: s.index!, end: s.index! + s[0].length }));
  const first = spans.find(s => s.end > start);
  const last = spans.find(s => s.end >= end);
  let left = first?.start ?? 0;
  let right = last?.end ?? text.length;
  // A pathological sentence can be as long as the message. Narrow around the
  // selection first so measuring/copying it remains bounded.
  left = Math.max(left, start - MAX_SELECTION_CONTEXT);
  right = Math.min(right, Math.max(end, start + MAX_SELECTION_CONTEXT));
  // UTF-16 range offsets can put a crop boundary between surrogate halves.
  if (left > 0 && /[\uDC00-\uDFFF]/.test(text[left] ?? '')) left--;
  if (right < text.length && /[\uDC00-\uDFFF]/.test(text[right] ?? '')) right++;
  const points = Array.from(text.slice(left, right));
  let offset = left;
  let bytes = 0;
  const selected = points.findIndex(p => { const at = offset; offset += p.length; return at >= start; });
  const pivot = Math.max(0, selected);
  let lo = pivot;
  let hi = pivot;
  while (lo > 0 || hi < points.length) {
    const forward = hi < points.length && (lo === 0 || hi - pivot <= pivot - lo);
    const point = forward ? points[hi] : points[lo - 1];
    const size = encoder.encode(point).length;
    if (bytes + size > MAX_SELECTION_CONTEXT) break;
    bytes += size;
    if (forward) hi++; else lo--;
  }
  return points.slice(lo, hi).join('').trim();
}

/** Capture before the menu takes focus. Cross-message and control ranges stay native. */
export function captureSelection(root: HTMLElement, selection: Selection | null): SelectedText | null {
  if (!selection || selection.isCollapsed || selection.rangeCount !== 1) return null;
  const range = selection.getRangeAt(0);
  if (!root.contains(range.startContainer) || !root.contains(range.endContainer)) return null;
  const positions = new Map<Node, number[]>();
  let text = '';
  let invalid = false;
  const block = /^(P|DIV|LI|PRE|BLOCKQUOTE|H[1-6]|TR|SECTION|DETAILS)$/;
  function walk(node: Node) {
    if (node.nodeType === 3) {
      positions.set(node, [text.length]);
      text += node.textContent ?? '';
      return;
    }
    if (node.nodeType !== 1) return;
    const el = node as HTMLElement;
    const style = getComputedStyle(el);
    if (el.matches('button,input,textarea,select,[hidden],[aria-hidden="true"]') || style.display === 'none' || style.visibility === 'hidden') {
      if (range.intersectsNode(el)) invalid = true;
      return;
    }
    const newline = () => { if (text && !text.endsWith('\n')) text += '\n'; };
    if (block.test(el.tagName) || el.tagName === 'BR') newline();
    const offsets = [text.length];
    for (const child of el.childNodes) {
      walk(child);
      offsets.push(text.length);
    }
    positions.set(node, offsets);
    if (block.test(el.tagName)) newline();
  }
  walk(root);
  const offset = (node: Node, at: number) => node.nodeType === 3
    ? (positions.get(node)?.[0] ?? NaN) + at : positions.get(node)?.[at] ?? NaN;
  const start = offset(range.startContainer, range.startOffset);
  const end = offset(range.endContainer, range.endOffset);
  const selected = range.toString();
  if (invalid || !Number.isFinite(start) || !Number.isFinite(end) || !selected.trim()) return null;
  const rect = range.getBoundingClientRect();
  return { text: selected, context: sentenceContext(text, start, end), rect: { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom }, range: range.cloneRange() };
}
