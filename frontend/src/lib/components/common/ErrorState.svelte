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
  <div class="error-card">
    <div class="error-icon-wrap">
      <svg class="error-icon" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
        <circle cx="10" cy="10" r="10" fill="color-mix(in srgb, var(--danger) 14%, transparent)" />
        <path
          d="M10 5.5a.75.75 0 0 1 .75.75v4.5a.75.75 0 0 1-1.5 0v-4.5A.75.75 0 0 1 10 5.5Zm0 8a.75.75 0 1 0 0-1.5.75.75 0 0 0 0 1.5Z"
          fill="var(--danger)"
        />
      </svg>
    </div>
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
</div>

<style>
  .error-state {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    padding: 32px 20px;
  }

  .error-card {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    max-width: 380px;
    padding: 28px 24px;
    background: var(--bg-secondary);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-card);
    gap: 8px;
  }

  .error-icon-wrap {
    width: 36px;
    height: 36px;
    margin-bottom: 4px;
  }

  .error-icon {
    width: 100%;
    height: 100%;
    color: var(--danger);
  }

  .error-title {
    font-size: 0.95rem;
    font-weight: 600;
    color: var(--danger);
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
    border-radius: var(--radius-sm);
    background-color: var(--accent-color);
    color: white;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: background-color 0.15s ease;
  }

  .retry-btn:hover {
    background-color: var(--accent-hover);
  }
</style>
