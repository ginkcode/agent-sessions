<script lang="ts">
  // Themed replacement for <select>. WebKitGTK draws native select popups
  // with the GTK theme, which ignores the app's colors and fonts.
  interface Option {
    value: string;
    label: string;
  }

  interface Props {
    options: Option[];
    value: string;
    onChange: (value: string) => void;
    ariaLabel: string;
    title?: string;
    align?: 'left' | 'right';
  }

  let { options, value, onChange, ariaLabel, title, align = 'left' }: Props = $props();

  let open = $state(false);
  let activeIndex = $state(0);
  let root: HTMLDivElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();
  let list: HTMLUListElement | undefined = $state();

  const id = `dropdown-${Math.random().toString(36).slice(2, 9)}`;
  let selected = $derived(options.find((o) => o.value === value));

  function show() {
    activeIndex = Math.max(0, options.findIndex((o) => o.value === value));
    open = true;
    queueMicrotask(() => list?.focus());
  }

  function hide(refocus = true) {
    open = false;
    if (refocus) trigger?.focus();
  }

  function choose(index: number) {
    const opt = options[index];
    hide();
    if (opt && opt.value !== value) onChange(opt.value);
  }

  function handleTriggerKeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      e.stopPropagation();
      show();
    }
  }

  // Stop propagation so parent keyboard navigation (e.g. the session list's
  // arrow keys) does not also react while the menu is open.
  function handleListKeydown(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      activeIndex = Math.min(options.length - 1, activeIndex + 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      activeIndex = Math.max(0, activeIndex - 1);
    } else if (e.key === 'Home') {
      e.preventDefault();
      activeIndex = 0;
    } else if (e.key === 'End') {
      e.preventDefault();
      activeIndex = options.length - 1;
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      choose(activeIndex);
    } else if (e.key === 'Escape' || e.key === 'Tab') {
      if (e.key === 'Escape') e.preventDefault();
      hide(e.key === 'Escape');
    }
  }

  function handleWindowPointerDown(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) hide(false);
  }
</script>

<svelte:window onpointerdown={handleWindowPointerDown} />

<div class="dropdown" bind:this={root}>
  <button
    bind:this={trigger}
    type="button"
    class="trigger"
    class:open
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-label={ariaLabel}
    {title}
    onclick={() => (open ? hide() : show())}
    onkeydown={handleTriggerKeydown}
  >
    <span class="trigger-label">{selected?.label ?? ''}</span>
    <span class="caret" aria-hidden="true"></span>
  </button>

  {#if open}
    <ul
      bind:this={list}
      class="menu"
      class:right={align === 'right'}
      role="listbox"
      tabindex="-1"
      aria-label={ariaLabel}
      aria-activedescendant="{id}-{activeIndex}"
      onkeydown={handleListKeydown}
    >
      {#each options as opt, i (opt.value)}
        <!-- Keyboard selection is handled on the listbox (aria-activedescendant). -->
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <li
          id="{id}-{i}"
          role="option"
          aria-selected={opt.value === value}
          class:active={i === activeIndex}
          class:selected={opt.value === value}
          onpointerenter={() => (activeIndex = i)}
          onclick={() => choose(i)}
        >
          <span class="check" aria-hidden="true">{opt.value === value ? '✓' : ''}</span>
          {opt.label}
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .dropdown {
    position: relative;
    display: inline-flex;
  }

  .trigger {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: var(--control-height, 24px);
    padding: 0 7px;
    border-radius: 4px;
    font-size: 0.72rem;
    font-weight: 500;
    white-space: nowrap;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .trigger:hover,
  .trigger.open,
  .trigger:focus-visible {
    color: var(--text-primary);
    border-color: var(--text-muted);
    outline: none;
  }

  .caret {
    width: 0;
    height: 0;
    border-left: 3.5px solid transparent;
    border-right: 3.5px solid transparent;
    border-top: 4px solid currentColor;
    opacity: 0.7;
  }

  .menu {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    z-index: 50;
    min-width: 100%;
    margin: 0;
    padding: 4px;
    list-style: none;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    box-shadow: 0 6px 20px rgba(0, 0, 0, 0.18);
    outline: none;
  }

  .menu.right {
    left: auto;
    right: 0;
  }

  .menu li {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 8px 4px 4px;
    border-radius: 4px;
    font-size: 0.75rem;
    white-space: nowrap;
    color: var(--text-primary);
    cursor: pointer;
  }

  .menu li.active {
    background-color: var(--bg-tertiary);
  }

  .menu li.selected {
    color: var(--accent-color);
    font-weight: 600;
  }

  .check {
    width: 12px;
    font-size: 0.7rem;
    text-align: center;
  }
</style>
