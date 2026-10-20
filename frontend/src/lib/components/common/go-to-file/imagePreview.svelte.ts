// Image zoom, as in the media viewer: it opens fitted to the view, and zooms
// with the buttons, + / - / 0, or Ctrl/Cmd + wheel (a trackpad pinch), and
// drags around when larger than the view.

export const MIN_ZOOM = 0.1;
export const MAX_ZOOM = 16;
export const ZOOM_STEP = 1.25;
const IMAGE_PADDING = 20;

export interface ImagePreviewOptions {
  /** The image's data URL, SVG or raster; each new one opens fitted. */
  getSource: () => string;
  /** The SVG data URL, empty when the image is not an SVG. */
  getSvgSource: () => string;
  /** The file's text, read for an SVG's viewBox. */
  getText: () => string;
  /** The scrolling view the image sits in. */
  getScroller: () => HTMLElement | undefined;
  /** Whether the image is showing. */
  isShown: () => boolean;
}

/** Must be called during component initialisation, as it sets up effects. */
export function createImagePreview(options: ImagePreviewOptions) {
  let imgZoom = $state(1);
  // Fitted images follow the view's size until zoomed.
  let imgFitted = $state(true);
  let imgEl: HTMLImageElement | undefined = $state();
  let imgNatural = { width: 0, height: 0 };
  let imgDrag: { x: number; y: number; left: number; top: number; id: number } | null = null;
  let imgDragging = $state(false);

  // The image could not be decoded: a corrupt file, or a format this
  // webview lacks.
  let imgFailed = $state(false);

  // Each image opens fitted.
  $effect(() => {
    void options.getSource();
    imgFitted = true;
    imgFailed = false;
  });

  // An SVG without width and height has no natural size of its own; its
  // viewBox gives one.
  function imageSize(img: HTMLImageElement): { width: number; height: number } {
    if (img.naturalWidth > 0 && img.naturalHeight > 0) return { width: img.naturalWidth, height: img.naturalHeight };
    if (!options.getSvgSource()) return { width: 300, height: 150 };
    const box = new DOMParser().parseFromString(options.getText(), 'image/svg+xml').documentElement.getAttribute('viewBox');
    const [, , w, h] = (box ?? '').split(/[\s,]+/).map(Number);
    return w > 0 && h > 0 ? { width: w, height: h } : { width: 300, height: 150 };
  }

  function onImageLoad(e: Event) {
    imgNatural = imageSize(e.currentTarget as HTMLImageElement);
    if (imgFitted) fitImage();
    else applyImageSize();
  }

  function applyImageSize() {
    if (!imgEl || !imgNatural.width) return;
    imgEl.style.width = `${imgNatural.width * imgZoom}px`;
    imgEl.style.height = `${imgNatural.height * imgZoom}px`;
  }

  function fitImage() {
    const view = options.getScroller();
    if (!view || !imgNatural.width) return;
    const width = view.clientWidth - 2 * IMAGE_PADDING;
    const height = view.clientHeight - 2 * IMAGE_PADDING;
    // A photo or icon smaller than the view shows at its own size, not
    // blown up and blurred; an SVG scales cleanly, so grows to fill it.
    const most = options.getSvgSource() ? MAX_ZOOM : 1;
    imgZoom = Math.min(most, Math.max(MIN_ZOOM, Math.min(width / imgNatural.width, height / imgNatural.height)));
    imgFitted = true;
    applyImageSize();
  }

  // Keeps the point at (x, y) in the view still, the centre by default.
  function zoomImage(next: number, x?: number, y?: number) {
    const view = options.getScroller();
    if (!view || !imgNatural.width) return;
    const ax = x ?? view.clientWidth / 2;
    const ay = y ?? view.clientHeight / 2;
    const px = (view.scrollLeft + ax) / imgZoom;
    const py = (view.scrollTop + ay) / imgZoom;
    imgZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, next));
    imgFitted = false;
    applyImageSize();
    view.scrollLeft = px * imgZoom - ax;
    view.scrollTop = py * imgZoom - ay;
  }

  // Refits as the view resizes, and zooms on Ctrl/Cmd + wheel at the
  // pointer; that listener can't be passive.
  $effect(() => {
    const view = options.getScroller();
    if (!view || !options.isShown()) return;
    const observer = new ResizeObserver(() => {
      if (imgFitted) fitImage();
    });
    observer.observe(view);
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      const rect = view.getBoundingClientRect();
      zoomImage(imgZoom * Math.exp(-e.deltaY * 0.002), e.clientX - rect.left, e.clientY - rect.top);
    };
    view.addEventListener('wheel', onWheel, { passive: false });
    return () => {
      observer.disconnect();
      view.removeEventListener('wheel', onWheel);
    };
  });

  function onImagePointerDown(e: PointerEvent) {
    const view = options.getScroller();
    if (e.button !== 0 || !view) return;
    e.preventDefault();
    imgDrag = { x: e.clientX, y: e.clientY, left: view.scrollLeft, top: view.scrollTop, id: e.pointerId };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    imgDragging = true;
  }

  function onImagePointerMove(e: PointerEvent) {
    const view = options.getScroller();
    if (!imgDrag || !view || e.pointerId !== imgDrag.id) return;
    view.scrollLeft = imgDrag.left - (e.clientX - imgDrag.x);
    view.scrollTop = imgDrag.top - (e.clientY - imgDrag.y);
  }

  function onImagePointerUp(e: PointerEvent) {
    if (!imgDrag || e.pointerId !== imgDrag.id) return;
    imgDrag = null;
    imgDragging = false;
  }

  /** Plain + / - / 0 zoom the image, unless typing in a field. */
  function handleImageZoomKey(e: KeyboardEvent): boolean {
    if (!options.isShown() || e.ctrlKey || e.metaKey || e.altKey) return false;
    const target = e.target as HTMLElement | null;
    if (target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))) return false;
    if (e.key === '+' || e.key === '=') zoomImage(imgZoom * ZOOM_STEP);
    else if (e.key === '-') zoomImage(imgZoom / ZOOM_STEP);
    else if (e.key === '0') fitImage();
    else return false;
    e.preventDefault();
    return true;
  }

  return {
    get imgEl() {
      return imgEl;
    },
    set imgEl(el: HTMLImageElement | undefined) {
      imgEl = el;
    },
    get imgZoom() {
      return imgZoom;
    },
    get imgFitted() {
      return imgFitted;
    },
    get imgFailed() {
      return imgFailed;
    },
    set imgFailed(value: boolean) {
      imgFailed = value;
    },
    get imgDragging() {
      return imgDragging;
    },
    MIN_ZOOM,
    MAX_ZOOM,
    ZOOM_STEP,
    onImageLoad,
    fitImage,
    zoomImage,
    onImagePointerDown,
    onImagePointerMove,
    onImagePointerUp,
    handleImageZoomKey,
  };
}

export type ImagePreview = ReturnType<typeof createImagePreview>;
