export function isFullscreenShortcut(
  e: Pick<KeyboardEvent, 'code' | 'metaKey' | 'ctrlKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): boolean {
  if (e.ctrlKey || e.altKey) return false;
  return mac
    ? e.code === 'KeyF' && e.metaKey && e.shiftKey
    : e.code === 'F11' && !e.metaKey && !e.shiftKey;
}

// WebKit can consume Escape before NSWindow.cancelOperation is reached.
// Cancel its default action, but do not stop propagation: dialogs and xterm
// must still receive Escape. Browser fullscreen deliberately keeps its exit key.
export function preventDesktopEscapeFullscreen(e: Pick<KeyboardEvent, 'key' | 'preventDefault'>): void {
  if (e.key === 'Escape' && window.go?.app?.App) e.preventDefault();
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
