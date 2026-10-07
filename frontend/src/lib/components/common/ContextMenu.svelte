<script lang="ts" module>
  export interface ContextMenuItem {
    label: string;
    onSelect: () => void;
    disabled?: boolean;
    /** Shown as a tooltip; explains why a disabled item is unavailable. */
    title?: string;
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';

  interface Props {
    /** Pointer position in viewport coordinates. */
    x: number;
    y: number;
    items: ContextMenuItem[];
    ariaLabel: string;
    onClose: () => void;
  }

  let { x, y, items, ariaLabel, onClose }: Props = $props();

  const MARGIN = 4;

  let menu: HTMLDivElement | undefined = $state();
  let left = $state(0);
  let top = $state(0);
  let activeIndex = $state(0);
  // Where focus returns when the menu is dismissed with Escape.
  let returnFocus: HTMLElement | null = null;

  // Keep the menu inside the viewport, flipping it left or up near an edge.
  function place() {
    if (!menu) return;
    const { width, height } = menu.getBoundingClientRect();
    const maxLeft = window.innerWidth - width - MARGIN;
    const maxTop = window.innerHeight - height - MARGIN;
    left = Math.max(MARGIN, x > maxLeft ? Math.min(x - width, maxLeft) : x);
    top = Math.max(MARGIN, y > maxTop ? Math.min(y - height, maxTop) : y);
  }

  $effect(() => {
    void x;
    void y;
    void items.length;
    place();
  });

  onMount(() => {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    focusItem(0);
  });

  function buttons(): HTMLButtonElement[] {
    return menu ? Array.from(menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')) : [];
  }

  function focusItem(index: number) {
    const list = buttons();
    if (!list.length) return;
    activeIndex = Math.max(0, Math.min(list.length - 1, index));
    list[activeIndex].focus();
  }

  function close(refocus: boolean) {
    onClose();
    if (refocus) returnFocus?.focus();
  }

  function choose(item: ContextMenuItem) {
    if (item.disabled) return;
    close(true);
    item.onSelect();
  }

  // Stop propagation so parent keyboard navigation (e.g. the session list's
  // arrow keys) does not also react while the menu is open.
  function handleKeydown(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      focusItem(activeIndex + 1 >= items.length ? 0 : activeIndex + 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      focusItem(activeIndex - 1 < 0 ? items.length - 1 : activeIndex - 1);
    } else if (e.key === 'Home') {
      e.preventDefault();
      focusItem(0);
    } else if (e.key === 'End') {
      e.preventDefault();
      focusItem(items.length - 1);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      const item = items[activeIndex];
      if (item) choose(item);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close(true);
    } else if (e.key === 'Tab') {
      e.preventDefault();
      close(true);
    }
  }

  function handleWindowPointerDown(e: PointerEvent) {
    if (menu && !menu.contains(e.target as Node)) close(false);
  }

  function handleWindowDismiss() {
    close(false);
  }
</script>

<svelte:window
  onpointerdown={handleWindowPointerDown}
  onresize={handleWindowDismiss}
  onblur={handleWindowDismiss}
/>
<svelte:document onscrollcapture={handleWindowDismiss} />

<div
  bind:this={menu}
  class="context-menu"
  role="menu"
  tabindex="-1"
  aria-label={ariaLabel}
  style:left="{left}px"
  style:top="{top}px"
  onkeydown={handleKeydown}
  oncontextmenu={(e) => e.preventDefault()}
>
  {#each items as item, i (item.label)}
    <button
      type="button"
      class="context-menu-item"
      role="menuitem"
      tabindex={i === activeIndex ? 0 : -1}
      aria-disabled={item.disabled ? 'true' : undefined}
      class:disabled={item.disabled}
      title={item.title}
      onpointerenter={() => focusItem(i)}
      onclick={() => choose(item)}
    >
      <span>{item.label}</span>
    </button>
  {/each}
</div>

<style>
  .context-menu {
    position: fixed;
    z-index: 120;
    min-width: 160px;
    background: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: var(--radius-sm, 6px);
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25);
    padding: 4px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    outline: none;
  }

  .context-menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border: none;
    background: transparent;
    border-radius: var(--radius-sm, 4px);
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--text-secondary);
    cursor: pointer;
    text-align: left;
    width: 100%;
    box-sizing: border-box;
    outline: none;
  }

  .context-menu-item:hover,
  .context-menu-item:focus-visible,
  .context-menu-item:focus {
    background: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .context-menu-item.disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
</style>
