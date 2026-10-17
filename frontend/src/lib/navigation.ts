import type { BackendAPI } from './api';

/**
 * Intercepts external link clicks and routes them through the native desktop
 * browser opener, preventing WebKitGTK from navigating away from the app.
 * All non-fragment, non-internal hrefs are treated as external: the WebView
 * must never navigate, so failing closed is the safe default.
 */
export function setupLinkInterceptor(api: BackendAPI): void {
  if (typeof document === 'undefined') return;

  document.addEventListener('click', (e: MouseEvent) => {
    if (e.defaultPrevented) return;

    const target = (e.target as HTMLElement).closest('a');
    if (!target) return;

    const href = target.getAttribute('href');
    if (!href) return;

    // Internal app anchors (#), copy-code buttons, and empty hrefs are inert.
    const trimmed = href.trim();
    if (!trimmed || trimmed.startsWith('#')) return;

    // Case-insensitive scheme check so HTTPS:// and JAVASCRIPT: can't bypass.
    const lower = trimmed.toLowerCase();
    if (lower.startsWith('http://') || lower.startsWith('https://')) {
      e.preventDefault();
      api.openURL(trimmed).catch((err) => {
        console.error('Failed to open external URL:', err);
      });
      return;
    }

    // Any other absolute scheme (javascript:, file:, data:, …) must not
    // navigate the WebView. Only same-document anchors are allowed through.
    e.preventDefault();
  });
}

/**
 * Stops files, links or text dragged in from other apps from navigating the
 * WebView to them when dropped. WebKitGTK opens a dropped file unless the
 * page cancels the drag, and the Wails runtime cancels only drags it
 * recognizes as files. The paths of dropped files still reach the app:
 * the desktop shell reads them from the native drop.
 */
export function setupDropGuard(target: Window = window): void {
  // A drag that started in the page, like selected text, keeps its default.
  let internal = false;
  target.addEventListener('dragstart', () => (internal = true), true);
  target.addEventListener('dragend', () => (internal = false), true);
  const guard = (e: DragEvent) => {
    if (internal) return;
    e.preventDefault();
    if (e.type === 'dragover' && e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
  };
  for (const type of ['dragenter', 'dragover', 'drop']) {
    target.addEventListener(type, guard as EventListener, true);
  }
}
