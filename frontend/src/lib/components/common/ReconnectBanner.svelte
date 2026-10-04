<script lang="ts">
  import { connectionStore } from '../../stores/connection.svelte';
  import { connectionBanner } from '../../guidance';

  // Generation of the last attempt that reached the host. A connect attempt
  // keeps its generation across automatic reconnects, so a later failure in
  // the same generation is a lost session, not a failed first connect.
  let connectedGeneration = $state<number | undefined>(undefined);
  $effect(() => {
    if (connectionStore.phase === 'connected') {
      connectedGeneration = connectionStore.generation;
    }
  });

  let message = $derived(
    connectionBanner({
      phase: connectionStore.phase,
      host: connectionStore.host,
      error: connectionStore.error,
      wasConnected:
        connectedGeneration !== undefined && connectedGeneration === connectionStore.generation,
    }),
  );
  let isDisconnected = $derived(connectionStore.phase === 'disconnected');

  function handleSwitchLocal() {
    void connectionStore.disconnect();
  }

  function handleRetry() {
    if (connectionStore.host) {
      void connectionStore.connect(connectionStore.host);
    }
  }
</script>

{#if message}
  <div class="reconnect-banner" role="alert" class:reconnecting={message.retrying}>
    <div class="banner-content">
      <span class="banner-icon" aria-hidden="true">
        {#if message.retrying}
          <span class="spinner"></span>
        {:else}
          ⚠️
        {/if}
      </span>
      <div class="banner-text">
        <p class="banner-summary">{message.before}<strong>{message.host}</strong>{message.after}</p>
        {#if message.detail}
          <div class="banner-detail">
            {#if message.detailLabel}
              <span class="detail-label">{message.detailLabel}</span>
            {/if}
            <pre class="detail-text">{message.detail}</pre>
          </div>
        {/if}
      </div>
    </div>
    <div class="banner-actions">
      {#if isDisconnected && connectionStore.host}
        <button type="button" class="banner-btn retry-btn" onclick={handleRetry}>
          Retry
        </button>
      {/if}
      <button type="button" class="banner-btn local-btn" onclick={handleSwitchLocal}>
        Switch to Local
      </button>
    </div>
  </div>
{/if}

<style>
  .reconnect-banner {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    padding: 8px 16px;
    background: #7f1d1d;
    color: #fef2f2;
    border-bottom: 1px solid rgba(255, 255, 255, 0.15);
    font-size: 0.8rem;
    z-index: 100;
  }

  .reconnect-banner.reconnecting {
    background: #78350f;
    color: #fffbeb;
  }

  .banner-content {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    flex: 1;
    min-width: 0;
  }

  .banner-icon {
    display: flex;
    align-items: center;
    min-height: 1.4em;
    font-size: 1rem;
    flex-shrink: 0;
  }

  .spinner {
    width: 14px;
    height: 14px;
    border: 2px solid rgba(255, 255, 255, 0.3);
    border-top-color: #ffffff;
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .banner-text {
    flex: 1;
    min-width: 0;
  }

  .banner-summary {
    margin: 0;
    line-height: 1.4;
    overflow-wrap: anywhere;
  }

  /* Raw SSH errors can be long and multi-line: wrap them, keep them
     selectable for copying, and scroll rather than grow the banner. */
  .banner-detail {
    margin-top: 4px;
  }

  .detail-label {
    display: block;
    font-size: 0.72rem;
    opacity: 0.85;
    margin-bottom: 2px;
  }

  .detail-text {
    margin: 0;
    max-height: 7.5em;
    overflow-y: auto;
    padding: 4px 8px;
    border-radius: 4px;
    background: rgba(0, 0, 0, 0.2);
    font-family: var(--font-mono);
    font-size: 0.72rem;
    line-height: 1.4;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    user-select: text;
    -webkit-user-select: text;
    cursor: text;
  }

  .banner-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }

  .banner-btn {
    padding: 4px 10px;
    font-size: 0.75rem;
    font-weight: 500;
    border-radius: 4px;
    cursor: pointer;
    border: 1px solid rgba(255, 255, 255, 0.25);
    background: rgba(255, 255, 255, 0.1);
    color: inherit;
    transition: background-color 0.15s ease;
  }

  .banner-btn:hover {
    background: rgba(255, 255, 255, 0.2);
  }

  .retry-btn {
    background: rgba(255, 255, 255, 0.2);
    font-weight: 600;
  }
</style>
