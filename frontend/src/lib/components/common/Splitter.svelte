<script lang="ts">
  import { endSplitterDrag, startSplitterDrag } from '../../splitterDrag';

  interface Props {
    direction?: 'horizontal' | 'vertical';
    ariaLabel?: string;
    /** Resizes by delta pixels and returns how much of it applied, which is
     *  less at a size limit. Returning nothing means all of it applied. */
    onResize: (delta: number) => number | void;
  }

  let { direction = 'horizontal', ariaLabel = 'Resize panel', onResize }: Props = $props();

  let isDragging = $state(false);

  function handlePointerDown(e: PointerEvent) {
    if (e.button !== 0) return; // Only left click
    // The press would otherwise start a text selection across the panes.
    e.preventDefault();
    window.getSelection()?.removeAllRanges();
    isDragging = true;
    startSplitterDrag(direction === 'horizontal' ? 'col-resize' : 'row-resize');
    // Where the edge would be with no limits. Moves past a limit are owed,
    // so the edge stays put until the pointer comes back to it.
    let edgePos = direction === 'horizontal' ? e.clientX : e.clientY;
    let pointerPos = edgePos;
    let frame = 0;

    const target = e.currentTarget as HTMLElement;
    target.setPointerCapture(e.pointerId);

    // One resize per frame, however often the pointer reports.
    function apply() {
      frame = 0;
      const delta = pointerPos - edgePos;
      if (delta === 0) return;
      const applied = onResize(delta);
      edgePos += typeof applied === 'number' ? applied : delta;
    }

    function onPointerMove(moveEvent: PointerEvent) {
      pointerPos = direction === 'horizontal' ? moveEvent.clientX : moveEvent.clientY;
      if (!frame) frame = requestAnimationFrame(apply);
    }

    function onPointerUp(upEvent: PointerEvent) {
      if (frame) cancelAnimationFrame(frame);
      apply();
      isDragging = false;
      endSplitterDrag();
      if (target.hasPointerCapture(upEvent.pointerId)) target.releasePointerCapture(upEvent.pointerId);
      window.removeEventListener('pointermove', onPointerMove);
      window.removeEventListener('pointerup', onPointerUp);
      window.removeEventListener('pointercancel', onPointerUp);
    }

    window.addEventListener('pointermove', onPointerMove);
    window.addEventListener('pointerup', onPointerUp);
    window.addEventListener('pointercancel', onPointerUp);
  }

  function handleKeyDown(e: KeyboardEvent) {
    const step = e.shiftKey ? 50 : 10;
    if (direction === 'horizontal') {
      if (e.key === 'ArrowLeft') {
        e.preventDefault();
        onResize(-step);
      } else if (e.key === 'ArrowRight') {
        e.preventDefault();
        onResize(step);
      }
    } else {
      if (e.key === 'ArrowUp') {
        e.preventDefault();
        onResize(-step);
      } else if (e.key === 'ArrowDown') {
        e.preventDefault();
        onResize(step);
      }
    }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<div
  class="splitter {direction}"
  class:dragging={isDragging}
  role="separator"
  tabindex="0"
  aria-label={ariaLabel}
  aria-orientation={direction === 'horizontal' ? 'vertical' : 'horizontal'}
  onpointerdown={handlePointerDown}
  onkeydown={handleKeyDown}
>
  <div class="line"></div>
</div>

<style>
  .splitter {
    position: relative;
    user-select: none;
    touch-action: none;
    flex-shrink: 0;
    z-index: 10;
    background: transparent;
    transition: background-color 0.15s ease;
  }

  .splitter.horizontal {
    width: 6px;
    cursor: col-resize;
    height: 100%;
    margin: 0 -3px;
  }

  .splitter.vertical {
    height: 6px;
    cursor: row-resize;
    width: 100%;
    margin: -3px 0;
  }

  .line {
    position: absolute;
    background-color: var(--border-color, #e2e8f0);
    transition: background-color 0.15s ease, width 0.15s ease, height 0.15s ease;
  }

  .splitter.horizontal .line {
    top: 0;
    bottom: 0;
    left: 2px;
    width: 1px;
  }

  .splitter.vertical .line {
    left: 0;
    right: 0;
    top: 2px;
    height: 1px;
  }

  .splitter:hover .line,
  .splitter.dragging .line,
  .splitter:focus-visible .line {
    background-color: var(--accent-color);
  }

  .splitter.horizontal:hover .line,
  .splitter.horizontal.dragging .line {
    width: 2px;
  }

  .splitter.vertical:hover .line,
  .splitter.vertical.dragging .line {
    height: 2px;
  }

  .splitter:focus-visible {
    outline: none;
  }
</style>
