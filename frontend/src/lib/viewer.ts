/**
 * Opens the full-window viewer. It lives in App.svelte and registers itself
 * here, so markdown and transcript code can open it without importing a
 * component.
 */

export type ViewerContent =
  | { kind: 'diagram'; source: string }
  | {
      kind: 'image';
      /** A data: URL. */
      url: string;
      /** The image's bytes in base64, for saving it as it is. */
      data: string;
      format: 'png' | 'jpeg' | 'gif' | 'webp';
      name: string;
    };

let viewer: ((content: ViewerContent) => void) | null = null;

/** Sets who shows content in full; null when nobody does. */
export function setViewer(fn: ((content: ViewerContent) => void) | null): void {
  viewer = fn;
}

export function openViewer(content: ViewerContent): void {
  viewer?.(content);
}
