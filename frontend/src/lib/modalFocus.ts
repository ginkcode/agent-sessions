/**
 * Keep keyboard events inside a modal after clicking non-focusable chrome.
 * In particular, Escape must still bubble through the dialog, not the page
 * underneath it. Do not prevent pointer defaults: previews remain selectable.
 */
export function modalFocus(node: HTMLElement): { destroy(): void } {
  const doc = node.ownerDocument;
  const previousTabindex = node.getAttribute('tabindex');
  if (previousTabindex === null) node.setAttribute('tabindex', '-1');
  let destroyed = false;
  const focus = () => {
    if (!destroyed && node.isConnected) node.focus({ preventScroll: true });
  };
  // Existing input autofocus (including Svelte's tick callbacks) takes priority.
  queueMicrotask(() => {
    if (!node.contains(doc.activeElement)) focus();
  });
  let restoreTimer: ReturnType<typeof setTimeout> | undefined;
  const restore = () => {
    clearTimeout(restoreTimer);
    // focusout/pointerup may run before the browser's default focus action.
    // A microtask here can focus the container mid-transition and prevent
    // an input or textarea from receiving focus. Wait for the next task.
    restoreTimer = setTimeout(() => {
      if (doc.activeElement === doc.body) focus();
    }, 0);
  };
  node.addEventListener('focusout', restore);
  node.addEventListener('pointerup', restore);
  return {
    destroy() {
      destroyed = true;
      clearTimeout(restoreTimer);
      node.removeEventListener('focusout', restore);
      node.removeEventListener('pointerup', restore);
      if (previousTabindex === null) node.removeAttribute('tabindex');
    },
  };
}
