<script lang="ts">
  import { connectionStore } from '../../stores/connection.svelte';

  let isReconnecting = $derived(connectionStore.phase === 'reconnecting');
  let isDisconnected = $derived(connectionStore.phase === 'disconnected');
  let visible = $derived(isReconnecting || isDisconnected);

  function handleSwitchLocal() {
    void connectionStore.disconnect();
  }

  function handleRetry() {
    if (connectionStore.host) {
      void connectionStore.connect(connectionStore.host);
    }
  }
</script>

{#if visible}
  <div class="reconnect-banner" role="alert" class:reconnecting={isReconnecting}>
    <div class="banner-content">
      <span class="banner-icon" aria-hidden="true">
        {#if isReconnecting}
          <span class="spinner"></span>
        {:else}
          ⚠️
        {/if}
      </span>
      <div class="banner-text">
        {#if isReconnecting}
          <span>
            Connection to <strong>{connectionStore.host}</strong> lost. Automatically reconnecting…
          </span>
        {:else}
          <span>
            Disconnected from <strong>{connectionStore.host}</strong>
            {#if connectionStore.error}
              — {connectionStore.error}
            {/if}
          </span>
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
    align-items: center;
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
    align-items: center;
    gap: 10px;
    overflow: hidden;
  }

  .banner-icon {
    display: flex;
    align-items: center;
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
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
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
