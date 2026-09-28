<script lang="ts">
  interface Props {
    title?: string;
    message: string;
    details?: string;
    onRetry?: () => void;
  }

  let {
    title = 'An error occurred',
    message,
    details,
    onRetry,
  }: Props = $props();

  let showDetails = $state(false);
</script>

<div class="error-state" role="alert">
  <div class="error-icon">⚠️</div>
  <h3 class="error-title">{title}</h3>
  <p class="error-msg">{message}</p>
  {#if details}
    <button
      type="button"
      class="toggle-details-btn"
      onclick={() => (showDetails = !showDetails)}
    >
      {showDetails ? 'Hide technical details' : 'Show technical details'}
    </button>
    {#if showDetails}
      <pre class="error-details">{details}</pre>
    {/if}
  {/if}
  {#if onRetry}
    <button type="button" class="retry-btn" onclick={onRetry}>
      Retry
    </button>
  {/if}
</div>

<style>
  .error-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 32px 20px;
    height: 100%;
    color: var(--text-muted);
    gap: 8px;
  }

  .error-icon {
    font-size: 2.2rem;
  }

  .error-title {
    font-size: 1rem;
    font-weight: 600;
    color: #ef4444;
    margin: 0;
  }

  .error-msg {
    font-size: 0.85rem;
    color: var(--text-secondary);
    max-width: 420px;
    line-height: 1.4;
    margin: 0;
  }

  .toggle-details-btn {
    font-size: 0.75rem;
    color: var(--accent-color);
    background: none;
    cursor: pointer;
    text-decoration: underline;
    margin-top: 4px;
  }

  .error-details {
    max-width: 500px;
    max-height: 160px;
    overflow-y: auto;
    font-family: var(--font-mono);
    font-size: 0.72rem;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    padding: 8px 12px;
    border-radius: 6px;
    text-align: left;
    white-space: pre-wrap;
    word-break: break-all;
  }

  .retry-btn {
    margin-top: 10px;
    padding: 6px 14px;
    border-radius: 6px;
    background-color: var(--accent-color);
    color: white;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
  }
</style>
