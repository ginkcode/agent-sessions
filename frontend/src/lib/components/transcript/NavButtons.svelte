<script lang="ts">
  import { appState } from '../../stores/appState.svelte';
  import { isMac } from '../../../main';

  const backTitle = isMac ? 'Back (⌘[)' : 'Back (Alt+←)';
  const forwardTitle = isMac ? 'Forward (⌘])' : 'Forward (Alt+→)';
</script>

<div class="nav-buttons">
  <button
    type="button"
    class="nav-btn"
    title={backTitle}
    aria-label="Back"
    disabled={!appState.canGoBack}
    onclick={() => void appState.goBack()}
  >
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M15 18l-6-6 6-6" />
    </svg>
  </button>
  <button
    type="button"
    class="nav-btn"
    title={forwardTitle}
    aria-label="Forward"
    disabled={!appState.canGoForward}
    onclick={() => void appState.goForward()}
  >
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M9 18l6-6-6-6" />
    </svg>
  </button>
</div>

<style>
  .nav-buttons {
    display: flex;
    align-items: center;
    gap: 2px;
    margin-right: 8px;
  }

  .nav-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: none;
    border-radius: 4px;
    background: transparent;
    color: var(--text-secondary);
    cursor: pointer;
  }

  .nav-btn:hover:not(:disabled) {
    color: var(--text-primary);
    background-color: var(--hover-bg);
  }

  .nav-btn:disabled {
    opacity: 0.35;
    cursor: default;
  }
</style>
