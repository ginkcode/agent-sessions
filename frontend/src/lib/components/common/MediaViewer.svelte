<script lang="ts">
  import DialogCloseButton from './DialogCloseButton.svelte';
  import { modalFocus } from '../../modalFocus';
  import { onMount, tick, untrack } from 'svelte';
  import { api } from '../../api';
  import { copyFailed } from '../../guidance';
  import { base64Blob, rasterPng } from '../../images';
  import { diagramImage, diagramPng, diagramSvg } from '../../mermaid';
  import { setViewer, type ViewerContent } from '../../viewer';
  import { theme } from '../../stores/theme.svelte';
  import { toast } from '../../stores/toast.svelte';
  import ContextMenu, { type ContextMenuItem } from './ContextMenu.svelte';

  // A Mermaid diagram or an attached image in the whole window. It opens
  // fitted to the window and can be zoomed with the buttons, + / - / 0, or
  // Ctrl/Cmd + wheel (a trackpad pinch), and dragged around when larger than
  // the window. Export saves a diagram as an SVG or PNG image on this
  // computer, in the current theme, and an image as it is.
  const MIN_ZOOM = 0.1;
  const MAX_ZOOM = 8;
  const STEP = 1.25;
  const PADDING = 24;

  let content = $state<ViewerContent | null>(null);
  let source = $derived(content?.kind === 'diagram' ? content.source : null);
  let attached = $derived(content?.kind === 'image' ? content : null);
  let svg = $state('');
  let error = $state<string | null>(null);
  let zoom = $state(1);
  // Fitted diagrams follow the window size until zoomed.
  let fitted = $state(true);
  let dialog = $state<HTMLDivElement>();
  let viewport = $state<HTMLDivElement>();
  let canvas = $state<HTMLDivElement>();
  let natural = { width: 0, height: 0 };
  let drag: { x: number; y: number; left: number; top: number; id: number } | null = null;
  let dragging = $state(false);
  let returnFocus: HTMLElement | null = null;
  let exportMenu = $state<{ x: number; y: number } | null>(null);
  let exportButton = $state<HTMLButtonElement>();
  let exporting = $state(false);
  // Images can go to the clipboard only where the webview supports it.
  const canCopyImage = typeof ClipboardItem !== 'undefined' && typeof navigator.clipboard?.write === 'function';

  onMount(() => {
    setViewer((next) => {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      content = next;
      svg = '';
      error = null;
      fitted = true;
    });
    return () => setViewer(null);
  });

  // Drawn again when the theme changes while open.
  $effect(() => {
    if (source === null) return;
    const wanted = source;
    const shade = theme.resolved;
    diagramSvg(wanted, shade, 'mermaid-diagram-viewer').then(
      (drawn) => {
        if (source === wanted && theme.resolved === shade) svg = drawn;
      },
      (err: unknown) => {
        if (source === wanted) error = err instanceof Error ? err.message : String(err);
      }
    );
  });

  $effect(() => {
    if (content !== null) dialog?.focus();
  });

  // Takes the size Mermaid drew the diagram at, then fits or zooms it.
  $effect(() => {
    if (!svg || !canvas) return;
    const el = canvas.querySelector('svg');
    if (!el) return;
    const box = el.viewBox.baseVal;
    natural = box && box.width > 0 ? { width: box.width, height: box.height } : el.getBBox();
    el.style.maxWidth = 'none';
    untrack(() => (fitted ? fit() : applySize()));
  });

  function onImageLoad(e: Event): void {
    const img = e.currentTarget as HTMLImageElement;
    natural = { width: img.naturalWidth, height: img.naturalHeight };
    if (fitted) fit();
    else applySize();
  }

  $effect(() => {
    if (!viewport) return;
    const observer = new ResizeObserver(() => {
      if (fitted) fit();
    });
    observer.observe(viewport);
    return () => observer.disconnect();
  });

  // Ctrl/Cmd + wheel zooms at the pointer; the listener can't be passive.
  $effect(() => {
    const el = viewport;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      setZoom(zoom * Math.exp(-e.deltaY * 0.002), e.clientX - rect.left, e.clientY - rect.top);
    };
    el.addEventListener('wheel', onWheel, { passive: false });
    return () => el.removeEventListener('wheel', onWheel);
  });

  function applySize(): void {
    const el = canvas?.querySelector<HTMLElement | SVGSVGElement>('svg, img');
    if (!el || !natural.width) return;
    el.style.width = `${natural.width * zoom}px`;
    el.style.height = `${natural.height * zoom}px`;
  }

  function fit(): void {
    if (!viewport || !natural.width) return;
    const width = viewport.clientWidth - 2 * PADDING;
    const height = viewport.clientHeight - 2 * PADDING;
    // An image smaller than the window shows at its own size; a diagram
    // grows to fill the window.
    const most = attached ? 1 : MAX_ZOOM;
    zoom = Math.min(most, Math.max(MIN_ZOOM, Math.min(width / natural.width, height / natural.height)));
    fitted = true;
    applySize();
  }

  // Keeps the point at (x, y) in the viewport still, the centre by default.
  function setZoom(next: number, x?: number, y?: number): void {
    if (!viewport) return;
    const clamped = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, next));
    const ax = x ?? viewport.clientWidth / 2;
    const ay = y ?? viewport.clientHeight / 2;
    const px = (viewport.scrollLeft + ax) / zoom;
    const py = (viewport.scrollTop + ay) / zoom;
    zoom = clamped;
    fitted = false;
    applySize();
    viewport.scrollLeft = px * zoom - ax;
    viewport.scrollTop = py * zoom - ay;
  }

  // The image as the viewer shows it: theme and background included.
  function image() {
    const background = dialog ? getComputedStyle(dialog).backgroundColor : 'white';
    return diagramImage(source!, theme.resolved, background);
  }

  function blobBase64(blob: Blob): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result).replace(/^data:[^,]*,/, ''));
      reader.onerror = () => reject(reader.error ?? new Error('the image could not be read'));
      reader.readAsDataURL(blob);
    });
  }

  async function saveImage(format: 'svg' | 'png'): Promise<void> {
    if (source === null || exporting) return;
    exporting = true;
    try {
      const drawn = await image();
      const data = format === 'svg' ? drawn.svg : await blobBase64(await diagramPng(drawn));
      const path = await api.saveImage({ name: 'diagram', format, data });
      if (path) toast.show({ title: 'Image saved', body: path });
    } catch (err) {
      toast.show({
        title: `Couldn't save the ${format.toUpperCase()} image`,
        body: err instanceof Error ? err.message : String(err),
        tone: 'error',
      });
    } finally {
      exporting = false;
    }
  }

  // An attached image is saved as it is, in its own format.
  async function saveAttached(): Promise<void> {
    if (!attached || exporting) return;
    exporting = true;
    try {
      const { name, format, data } = attached;
      const path = await api.saveImage({ name, format, data });
      if (path) toast.show({ title: 'Image saved', body: path });
    } catch (err) {
      toast.show({
        title: "Couldn't save the image",
        body: err instanceof Error ? err.message : String(err),
        tone: 'error',
      });
    } finally {
      exporting = false;
    }
  }

  // The image to copy: a diagram drawn at 2x, an attached PNG as it is, and
  // other attached images as a PNG.
  async function pngToCopy(): Promise<Blob> {
    if (attached) {
      if (attached.format === 'png') return base64Blob(attached.data, 'image/png');
      return rasterPng(attached.url, natural.width, natural.height);
    }
    return diagramPng(await image());
  }

  async function copyImage(): Promise<void> {
    if (content === null || exporting) return;
    exporting = true;
    try {
      // The item takes a promise so the write starts within the click, as
      // Safari requires.
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': pngToCopy() })]);
      toast.show({ title: 'Image copied', body: `The ${attached ? 'image' : 'diagram'} is on the clipboard as a PNG image.` });
    } catch (err) {
      toast.show(copyFailed('the image', err));
    } finally {
      exporting = false;
    }
  }

  let exportItems = $derived.by((): ContextMenuItem[] => {
    const items: ContextMenuItem[] = attached
      ? [{ label: 'Save Image…', onSelect: () => void saveAttached() }]
      : [
          { label: 'Save as SVG…', onSelect: () => void saveImage('svg') },
          { label: 'Save as PNG…', onSelect: () => void saveImage('png') },
        ];
    if (canCopyImage) items.push({ label: 'Copy as PNG', onSelect: () => void copyImage() });
    return items;
  });

  function toggleExportMenu(): void {
    if (exportMenu || !exportButton) {
      exportMenu = null;
      return;
    }
    const rect = exportButton.getBoundingClientRect();
    exportMenu = { x: rect.left, y: rect.bottom + 4 };
  }

  async function close(): Promise<void> {
    exportMenu = null;
    content = null;
    svg = '';
    error = null;
    dragging = false;
    drag = null;
    await tick();
    returnFocus?.focus();
    returnFocus = null;
  }

  function onKeydown(e: KeyboardEvent): void {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    let handled = true;
    if (e.key === 'Escape') void close();
    else if (e.key === '+' || e.key === '=') setZoom(zoom * STEP);
    else if (e.key === '-') setZoom(zoom / STEP);
    else if (e.key === '0') fit();
    else handled = false;
    if (handled) {
      e.preventDefault();
      e.stopPropagation();
    }
  }

  function onPointerDown(e: PointerEvent): void {
    if (e.button !== 0 || !viewport) return;
    // A press on a scrollbar scrolls by itself.
    if (e.target === viewport && (e.offsetX >= viewport.clientWidth || e.offsetY >= viewport.clientHeight)) return;
    drag = { x: e.clientX, y: e.clientY, left: viewport.scrollLeft, top: viewport.scrollTop, id: e.pointerId };
    viewport.setPointerCapture(e.pointerId);
    dragging = true;
  }

  function onPointerMove(e: PointerEvent): void {
    if (!drag || !viewport || e.pointerId !== drag.id) return;
    viewport.scrollLeft = drag.left - (e.clientX - drag.x);
    viewport.scrollTop = drag.top - (e.clientY - drag.y);
  }

  function onPointerUp(e: PointerEvent): void {
    if (!drag || e.pointerId !== drag.id) return;
    drag = null;
    dragging = false;
  }
</script>

{#if content !== null}
  <div
    class="diagram-viewer"
    use:modalFocus role="dialog"
    aria-modal="true"
    aria-label={attached ? attached.name : 'Diagram'}
    tabindex="-1"
    bind:this={dialog}
    onkeydown={onKeydown}
  >
    <header class="viewer-toolbar">
      <span class="viewer-title">{attached ? attached.name : 'Diagram'}</span>
      <div class="viewer-actions">
        <button type="button" class="viewer-btn" title="Zoom out (-)" aria-label="Zoom out" onclick={() => setZoom(zoom / STEP)}>−</button>
        <span class="zoom-level" aria-live="polite">{Math.round(zoom * 100)}%</span>
        <button type="button" class="viewer-btn" title="Zoom in (+)" aria-label="Zoom in" onclick={() => setZoom(zoom * STEP)}>+</button>
        <button type="button" class="viewer-btn" class:active={fitted} title="Fit to window (0)" onclick={fit}>Fit</button>
        <button
          type="button"
          class="viewer-btn"
          title={attached ? 'Save or copy the image' : 'Save or copy the diagram as an image'}
          aria-haspopup="menu"
          aria-expanded={exportMenu !== null}
          disabled={!attached && !svg}
          aria-busy={exporting}
          bind:this={exportButton}
          onpointerdown={(e) => exportMenu && e.stopPropagation()}
          onclick={toggleExportMenu}
        >{exporting ? 'Exporting…' : 'Export'}</button>
        <DialogCloseButton title="Close (Esc)" onclick={() => void close()} />
      </div>
    </header>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="viewer-viewport"
      class:dragging
      bind:this={viewport}
      onpointerdown={onPointerDown}
      onpointermove={onPointerMove}
      onpointerup={onPointerUp}
      onpointercancel={onPointerUp}
    >
      <div class="viewer-canvas" bind:this={canvas}>
        {#if attached}
          <img src={attached.url} alt={attached.name} draggable="false" onload={onImageLoad} />
        {:else if error}
          <p class="viewer-message" role="alert">Diagram not rendered: {error}</p>
        {:else if svg}
          {@html svg}
        {:else}
          <p class="viewer-message">Drawing…</p>
        {/if}
      </div>
    </div>
    {#if exportMenu}
      <ContextMenu
        x={exportMenu.x}
        y={exportMenu.y}
        items={exportItems}
        ariaLabel={attached ? 'Export the image' : 'Export the diagram'}
        onClose={() => (exportMenu = null)}
      />
    {/if}
  </div>
{/if}

<style>
  .diagram-viewer {
    position: fixed;
    inset: 0;
    z-index: 1020;
    display: flex;
    flex-direction: column;
    background-color: var(--bg-primary);
    outline: none;
  }

  .viewer-toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 40px;
    padding: 0 8px 0 14px;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .viewer-title {
    font-size: 0.82rem;
    font-weight: 600;
    color: var(--text-secondary);
  }

  .viewer-actions {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .viewer-btn {
    min-width: 28px;
    height: 28px;
    padding: 0 8px;
    border-radius: 4px;
    border: 1px solid var(--border-color);
    background-color: var(--bg-primary);
    color: var(--text-secondary);
    font-size: 0.8rem;
    cursor: pointer;
  }

  .viewer-btn:hover {
    color: var(--text-primary);
    background-color: var(--hover-bg);
  }

  .viewer-btn:disabled {
    opacity: 0.5;
    cursor: default;
  }

  .viewer-btn:disabled:hover {
    color: var(--text-secondary);
    background-color: var(--bg-primary);
  }

  .viewer-btn.active {
    color: var(--accent-color);
    border-color: var(--accent-color);
  }

  .zoom-level {
    min-width: 48px;
    text-align: center;
    font-size: 0.78rem;
    font-variant-numeric: tabular-nums;
    color: var(--text-secondary);
  }

  .viewer-viewport {
    flex: 1;
    min-height: 0;
    overflow: auto;
    cursor: grab;
  }

  .viewer-viewport.dragging {
    cursor: grabbing;
    user-select: none;
  }

  /* Centres a diagram smaller than the window; a larger one widens the
     canvas and scrolls from its top-left corner. */
  .viewer-canvas {
    display: flex;
    align-items: center;
    justify-content: center;
    min-width: 100%;
    min-height: 100%;
    width: max-content;
    padding: 24px;
    box-sizing: border-box;
  }

  .viewer-canvas :global(svg),
  .viewer-canvas img {
    flex: none;
  }

  .viewer-canvas img {
    max-width: none;
    user-select: none;
  }

  .viewer-message {
    color: var(--text-secondary);
    font-size: 0.85rem;
  }
</style>
