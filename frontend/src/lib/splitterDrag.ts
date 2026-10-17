// A splitter drag in progress. Views too costly to lay out at every frame
// wait for its end: CSS through the class on <html>, code through
// onSplitterDragEnd.

const DRAGGING_CLASS = 'splitter-dragging';
const endListeners = new Set<() => void>();

/** Starts a drag; cursor is the one the whole page shows meanwhile. */
export function startSplitterDrag(cursor: 'col-resize' | 'row-resize'): void {
  document.documentElement.classList.add(DRAGGING_CLASS);
  document.documentElement.dataset.splitterCursor = cursor;
}

export function endSplitterDrag(): void {
  document.documentElement.classList.remove(DRAGGING_CLASS);
  delete document.documentElement.dataset.splitterCursor;
  for (const listener of [...endListeners]) listener();
}

export function splitterDragging(): boolean {
  return typeof document !== 'undefined' && document.documentElement.classList.contains(DRAGGING_CLASS);
}

/** Calls listener after each drag; returns what stops it. */
export function onSplitterDragEnd(listener: () => void): () => void {
  endListeners.add(listener);
  return () => endListeners.delete(listener);
}
