<script lang="ts">
  let { onclick, label = 'Close', title = label, disabled = false, alignEnd = false, class: className = '' }: {
    onclick: (event: MouseEvent) => void | Promise<void>;
    label?: string;
    title?: string;
    disabled?: boolean;
    alignEnd?: boolean;
    class?: string;
  } = $props();
</script>

<button type="button" class="dialog-close-button {className}" class:align-end={alignEnd} aria-label={label} {title} {disabled} {onclick}>
  <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
    <path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" />
  </svg>
</button>

<style>
  button {
    display: inline-grid;
    place-items: center;
    flex-shrink: 0;
    width: 28px;
    height: 28px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text-secondary);
    cursor: pointer;
  }
  button.align-end { margin-left: auto; }
  button:hover:not(:disabled) { background: var(--hover-bg); color: var(--text-primary); }
  button:focus-visible { outline: 2px solid var(--accent-color); outline-offset: 2px; }
  button:disabled { opacity: 0.5; cursor: default; }
  svg { display: block; }
</style>
