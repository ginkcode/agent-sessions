<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { diagramSvg, setDiagramExpander } from '../../mermaid';
  import { theme } from '../../stores/theme.svelte';

  // A Mermaid diagram in the whole window. It opens fitted to the window and
  // can be zoomed with the buttons, + / - / 0, or Ctrl/Cmd + wheel (a
  // trackpad pinch), and dragged around when larger than the window.
  const MIN_ZOOM = 0.1;
  const MAX_ZOOM = 8;
  const STEP = 1.25;
  const PADDING = 24;

  let source = $state<string | null>(null);
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

  onMount(() => {
    setDiagramExpander((next) => {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      source = next;
      svg = '';
      error = null;
      fitted = true;
    });
    return () => setDiagramExpander(null);
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
    if (source !== null) dialog?.focus();
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
    const el = canvas?.querySelector('svg');
    if (!el || !natural.width) return;
    el.setAttribute('width', String(natural.width * zoom));
    el.setAttribute('height', String(natural.height * zoom));
  }

  function fit(): void {
    if (!viewport || !natural.width) return;
    const width = viewport.clientWidth - 2 * PADDING;
    const height = viewport.clientHeight - 2 * PADDING;
    zoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.min(width / natural.width, height / natural.height)));
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

  async function close(): Promise<void> {
    source = null;
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

{#if source !== null}
  <div
    class="diagram-viewer"
    role="dialog"
    aria-modal="true"
    aria-label="Diagram"
    tabindex="-1"
    bind:this={dialog}
    onkeydown={onKeydown}
  >
    <header class="viewer-toolbar">
      <span class="viewer-title">Diagram</span>
      <div class="viewer-actions">
        <button type="button" class="viewer-btn" title="Zoom out (-)" aria-label="Zoom out" onclick={() => setZoom(zoom / STEP)}>−</button>
        <span class="zoom-level" aria-live="polite">{Math.round(zoom * 100)}%</span>
        <button type="button" class="viewer-btn" title="Zoom in (+)" aria-label="Zoom in" onclick={() => setZoom(zoom * STEP)}>+</button>
        <button type="button" class="viewer-btn" class:active={fitted} title="Fit to window (0)" onclick={fit}>Fit</button>
        <button type="button" class="viewer-btn" title="Close (Esc)" aria-label="Close" onclick={() => void close()}>✕</button>
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
        {#if error}
          <p class="viewer-message" role="alert">Diagram not rendered: {error}</p>
        {:else if svg}
          {@html svg}
        {:else}
          <p class="viewer-message">Drawing…</p>
        {/if}
      </div>
    </div>
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

  .viewer-canvas :global(svg) {
    flex: none;
  }

  .viewer-message {
    color: var(--text-secondary);
    font-size: 0.85rem;
  }
</style>
