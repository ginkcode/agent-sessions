<script lang="ts">
  import { onDestroy } from 'svelte';
  import { appState } from '../../stores/appState.svelte';

  let timer: ReturnType<typeof setTimeout> | undefined;

  // Debounce so fast typing doesn't trigger a backend query per keystroke.
  function handleInput(e: Event) {
    const val = (e.target as HTMLInputElement).value;
    clearTimeout(timer);
    timer = setTimeout(() => {
      appState.setFilter({ path: val });
    }, 250);
  }

  onDestroy(() => {
    clearTimeout(timer);
  });
</script>

<div class="path-filter">
  <svg
    class="path-icon"
    width="13"
    height="13"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
  </svg>
  <input
    type="search"
    placeholder="Filter by directory…"
    value={appState.filter.path || ''}
    oninput={handleInput}
    aria-label="Filter by directory"
    spellcheck="false"
    autocomplete="off"
  />
</div>

<style>
  .path-filter {
    display: flex;
    align-items: center;
    gap: 6px;
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    padding: 4px 8px;
  }

  .path-filter:focus-within {
    border-color: var(--accent-color);
  }

  .path-icon {
    flex-shrink: 0;
    color: var(--text-muted);
  }

  input {
    border: none;
    outline: none;
    width: 100%;
    font-size: 0.8rem;
    color: var(--text-primary);
  }
</style>
