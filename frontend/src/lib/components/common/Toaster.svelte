<script lang="ts">
  import { toast } from '../../stores/toast.svelte';
</script>

<!-- Bottom right, above every dialog (they top out at z-index 1200). -->
<div class="toaster" role="status" aria-live="polite">
  {#if toast.current}
    {#key toast.current.id}
      <div
        class="toast"
        class:error={toast.current.tone === 'error'}
        role="presentation"
        onmouseenter={() => toast.pause()}
        onmouseleave={() => toast.resume()}
      >
        <div class="toast-text">
          <strong class="toast-title">{toast.current.title}</strong>
          <p class="toast-body">{toast.current.body}</p>
          {#if toast.current.code}
            <code class="toast-code">{toast.current.code}</code>
          {/if}
        </div>
        <button type="button" class="toast-close" aria-label="Dismiss" onclick={() => toast.dismiss()}>
          ×
        </button>
      </div>
    {/key}
  {/if}
</div>

<style>
  .toaster {
    position: fixed;
    right: 16px;
    bottom: 16px;
    z-index: 1300;
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    width: min(380px, calc(100vw - 32px));
    padding: 10px 12px;
    border: 1px solid var(--border-color);
    border-left: 3px solid var(--accent-color);
    border-radius: var(--radius-md);
    background: var(--bg-tertiary);
    color: var(--text-primary);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.25);
    pointer-events: auto;
    animation: toast-in 0.15s ease-out;
  }

  .toast.error {
    border-left-color: var(--danger);
  }

  .toast-text {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .toast-title {
    font-size: 0.8rem;
  }

  .toast-body {
    margin: 0;
    font-size: 0.75rem;
    line-height: 1.4;
    color: var(--text-secondary);
    overflow-wrap: anywhere;
  }

  .toast-code {
    align-self: flex-start;
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    background: var(--bg-secondary);
    font-family: var(--font-mono, monospace);
    font-size: 0.75rem;
    user-select: all;
  }

  .toast-close {
    padding: 0 4px;
    font-size: 1rem;
    line-height: 1;
    color: var(--text-muted);
  }

  .toast-close:hover {
    color: var(--text-primary);
  }

  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
  }
</style>
