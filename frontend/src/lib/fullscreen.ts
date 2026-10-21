export function isFullscreenShortcut(
  e: Pick<KeyboardEvent, 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): boolean {
  if (e.altKey) return false;
  return mac
    ? e.code === 'KeyF' && e.ctrlKey && e.metaKey && !e.shiftKey
    : e.code === 'F11' && !e.ctrlKey && !e.metaKey && !e.shiftKey;
}

// WebKit can consume Escape before NSWindow.cancelOperation is reached.
// Run in the capture phase so dialogs stopping propagation cannot bypass this.
// Cancel its default action, but do not stop propagation: dialogs and xterm
// must still receive Escape. Browser fullscreen deliberately keeps its exit key.
export function preventDesktopEscapeFullscreen(e: Pick<KeyboardEvent, 'key' | 'preventDefault'>): void {
  if (e.key !== 'Escape' || !window.go?.app?.App) return;
  const native = e.preventDefault;
  native.call(e);
  safeguarded.add(e);
  // defaultPrevented is now always true, so record whether a later handler
  // (a find bar, menu, editor search…) really used the key.
  e.preventDefault = function () {
    used.add(e);
    native.call(e);
  };
}

const safeguarded = new WeakSet<object>();
const used = new WeakSet<object>();

/**
 * Whether an earlier handler already used this key. Use instead of
 * `defaultPrevented`, which the desktop Escape safeguard always sets.
 */
export function keyHandled(e: Pick<KeyboardEvent, 'defaultPrevented'>): boolean {
  return used.has(e) || (e.defaultPrevented && !safeguarded.has(e));
}

let toggling = false;

export async function toggleFullscreen(): Promise<void> {
  if (toggling) return;
  toggling = true;
  try {
    const runtime = window.runtime;
    if (runtime?.WindowIsFullscreen && runtime.WindowFullscreen && runtime.WindowUnfullscreen) {
      if (await runtime.WindowIsFullscreen()) runtime.WindowUnfullscreen();
      else runtime.WindowFullscreen();
    } else if (document.fullscreenElement) {
      await document.exitFullscreen();
    } else {
      await document.documentElement.requestFullscreen();
    }
  } finally {
    toggling = false;
  }
}
