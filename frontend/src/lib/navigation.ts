import type { BackendAPI } from './api';

/**
 * Intercepts external link clicks and routes them through the native desktop
 * browser opener, preventing WebKitGTK from navigating away from the app.
 */
export function setupLinkInterceptor(api: BackendAPI): void {
  if (typeof document === 'undefined') return;

  document.addEventListener('click', (e: MouseEvent) => {
    const target = (e.target as HTMLElement).closest('a');
    if (!target) return;

    const href = target.getAttribute('href');
    if (href && (href.startsWith('http://') || href.startsWith('https://'))) {
      e.preventDefault();
      api.openURL(href).catch((err) => {
        console.error('Failed to open external URL:', err);
      });
    }
  });
}
