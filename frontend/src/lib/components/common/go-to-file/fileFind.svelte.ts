// Find in the file (⌘F, Ctrl+F), in source or rendered Markdown. The CSS Custom
// Highlight API paints the matches without touching the lines' DOM, and
// only those near the screen, so a common letter in a long file stays
// cheap. Without it, finding still scrolls to each match.
import { onDestroy, tick, untrack } from 'svelte';
import { findInLines, firstMatchFrom, textRange, textNodesRange, type FindMatch, type FindResult } from '../../../fileFind';
import { previewText, type PreviewText } from '../../../previewFind';
import type { DiffRow } from '../../../files';

const PAINT_MARGIN = 30;
const PAINT_LIMIT = 2000;

export interface FileFindOptions {
  /** The scrolling view the file sits in. */
  getScroller: () => HTMLElement | undefined;
  /** Whether the file's text is showing, so there is something to find. */
  isContentShown: () => boolean;
  /** A previewed SVG is an image, with no text to find. */
  isSvgShown: () => boolean;
  /** Whether rendered Markdown shows instead of the source. */
  isMarkdownShown: () => boolean;
  getRows: () => DiffRow[];
  getPlainLines: () => string[];
  getHtmlLines: () => string[] | null;
  getWrap: () => boolean;
  getPresentationFontSize: () => number;
}

/** Must be called during component initialisation, as it sets up effects. */
export function createFileFind(options: FileFindOptions) {
  const canPaint = typeof CSS !== 'undefined' && 'highlights' in CSS && typeof Highlight !== 'undefined';

  let findOpen = $state(false);
  let findQuery = $state('');
  let findCase = $state(false);
  let findCurrent = $state(-1);
  let findInput: HTMLInputElement | undefined = $state();
  let codeEl: HTMLElement | undefined = $state();
  let findActive = $derived(findOpen && options.isContentShown() && !options.isSvgShown());
  let previewEl: HTMLElement | undefined = $state();
  let previewRows: PreviewText[] = $state.raw([]);
  $effect(() => {
    const el = previewEl;
    if (!findActive || !options.isMarkdownShown() || !el) {
      previewRows = [];
      return;
    }
    const update = () => {
      previewRows = previewText(el);
    };
    update();
    // Markdown and asynchronous diagrams can replace their rendered text.
    const observer = new MutationObserver(update);
    observer.observe(el, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['class', 'style', 'hidden'] });
    return () => observer.disconnect();
  });
  let rowTexts = $derived(
    options.isMarkdownShown()
      ? previewRows.map((row) => row.text)
      : options.getRows().map((row) => (row.kind === 'removed' ? row.text : (options.getPlainLines()[row.index] ?? '')))
  );
  let found: FindResult = $derived(findActive ? findInLines(rowTexts, findQuery, findCase) : { matches: [], capped: false });

  // One .line per row, gathered again whenever the rows render anew.
  let lineEls: HTMLElement[] = [];
  $effect(() => {
    void options.getRows();
    void options.getHtmlLines();
    void options.getWrap();
    void options.getPresentationFontSize();
    lineEls = findActive && codeEl ? [...codeEl.querySelectorAll<HTMLElement>('.line')] : [];
    schedulePaint();
  });

  // A new search goes to the first match from the one shown before, or from
  // the top of the screen in a file just opened.
  let lastMatch: FindMatch | null = null;
  let lastRows: string[] | null = null;
  $effect(() => {
    const { matches } = found;
    untrack(() => {
      const from =
        rowTexts === lastRows && lastMatch ? lastMatch : { row: rowAt(options.getScroller()?.getBoundingClientRect().top ?? 0), start: 0 };
      lastRows = rowTexts;
      if (matches.length === 0) {
        findCurrent = -1;
        lastMatch = null;
        schedulePaint();
        return;
      }
      const i = firstMatchFrom(matches, from.row, from.start);
      goToMatch(i < matches.length ? i : 0);
    });
  });

  function goToMatch(i: number) {
    const match = found.matches[i];
    findCurrent = i;
    lastMatch = match;
    reveal(match);
    schedulePaint();
  }

  function stepMatch(delta: number) {
    const total = found.matches.length;
    if (total > 0) goToMatch(findCurrent < 0 ? 0 : (findCurrent + delta + total) % total);
  }

  function openFind() {
    // Text picked in the file is what to find, as in editors.
    const selection = window.getSelection();
    const picked = selection?.toString() ?? '';
    if (picked && !picked.includes('\n') && selection?.anchorNode && options.getScroller()?.contains(selection.anchorNode)) findQuery = picked;
    findOpen = true;
    void tick().then(() => {
      findInput?.focus();
      findInput?.select();
    });
  }

  function closeFind() {
    findOpen = false;
    lastMatch = null;
  }

  function handleFindKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      stepMatch(e.shiftKey ? -1 : 1);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeFind();
    }
  }

  // The first row whose bottom is below y on the screen.
  function rowAt(y: number): number {
    if (options.isMarkdownShown()) {
      const index = previewRows.findIndex((row) => row.element.getBoundingClientRect().bottom > y);
      return index < 0 ? previewRows.length : index;
    }
    let lo = 0;
    let hi = lineEls.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (lineEls[mid].getBoundingClientRect().bottom <= y) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }

  function matchRange(match: FindMatch): Range | null {
    if (options.isMarkdownShown()) {
      const row = previewRows[match.row];
      return row ? textNodesRange(row.nodes, match.start, match.end) : null;
    }
    const el = lineEls[match.row];
    return el ? textRange(el, match.start, match.end) : null;
  }

  // Scrolls a match into view: to the middle when it is above or below the
  // screen, and sideways clear of the line numbers.
  function reveal(match: FindMatch) {
    const scroller = options.getScroller();
    const range = matchRange(match);
    if (!scroller || !range) return;
    const box = scroller.getBoundingClientRect();
    const r = range.getBoundingClientRect();
    if (r.top < box.top || r.bottom > box.top + scroller.clientHeight) {
      scroller.scrollTop += r.top - box.top - (scroller.clientHeight - r.height) / 2;
    }
    const left =
      box.left + (options.isMarkdownShown() || options.getWrap() ? 0 : (codeEl?.querySelector('.gutter')?.getBoundingClientRect().width ?? 0));
    const right = box.left + scroller.clientWidth;
    if (r.left < left || r.right > right) scroller.scrollLeft += r.left - left - (right - left) / 3;
  }

  let paintFrame = 0;
  function schedulePaint() {
    if (canPaint && !paintFrame) paintFrame = requestAnimationFrame(paint);
  }

  function paint() {
    paintFrame = 0;
    const others = new Highlight();
    const current = new Highlight();
    const { matches } = found;
    const scroller = options.getScroller();
    if (findActive && matches.length > 0 && scroller && (options.isMarkdownShown() ? previewRows.length > 0 : lineEls.length > 0)) {
      const box = scroller.getBoundingClientRect();
      const first = Math.max(0, rowAt(box.top) - PAINT_MARGIN);
      const last = rowAt(box.bottom) + PAINT_MARGIN;
      for (let i = firstMatchFrom(matches, first), n = 0; i < matches.length && matches[i].row <= last && n < PAINT_LIMIT; i++, n++) {
        const range = i === findCurrent ? null : matchRange(matches[i]);
        if (range) others.add(range);
      }
      const range = findCurrent >= 0 ? matchRange(matches[findCurrent]) : null;
      if (range) current.add(range);
    }
    CSS.highlights.set('file-find', others);
    CSS.highlights.set('file-find-current', current);
  }

  onDestroy(() => {
    cancelAnimationFrame(paintFrame);
    if (canPaint) {
      CSS.highlights.delete('file-find');
      CSS.highlights.delete('file-find-current');
    }
  });

  return {
    get findInput() {
      return findInput;
    },
    set findInput(el: HTMLInputElement | undefined) {
      findInput = el;
    },
    get codeEl() {
      return codeEl;
    },
    set codeEl(el: HTMLElement | undefined) {
      codeEl = el;
    },
    get previewEl() {
      return previewEl;
    },
    set previewEl(el: HTMLElement | undefined) {
      previewEl = el;
    },
    get findQuery() {
      return findQuery;
    },
    set findQuery(value: string) {
      findQuery = value;
    },
    get findCase() {
      return findCase;
    },
    set findCase(value: boolean) {
      findCase = value;
    },
    get findActive() {
      return findActive;
    },
    get found() {
      return found;
    },
    get findCurrent() {
      return findCurrent;
    },
    openFind,
    closeFind,
    stepMatch,
    schedulePaint,
    handleFindKey,
  };
}

export type FileFind = ReturnType<typeof createFileFind>;
