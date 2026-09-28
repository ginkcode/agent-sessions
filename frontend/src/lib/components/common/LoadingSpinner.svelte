<script lang="ts">
  interface Props {
    size?: number;
    label?: string;
  }

  let { size = 20, label = 'Loading…' }: Props = $props();
</script>

<div class="spinner-container" role="status" aria-label={label}>
  <div
    class="spinner"
    style="width: {size}px; height: {size}px;"
  ></div>
  {#if label}
    <span class="spinner-label">{label}</span>
  {/if}
</div>

<style>
  .spinner-container {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    color: var(--text-muted);
    font-size: 0.8rem;
  }

  .spinner {
    border: 2px solid var(--border-color);
    border-top-color: var(--accent-color);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
    flex-shrink: 0;
  }

  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  .spinner-label {
    user-select: none;
  }
</style>
