<script lang="ts">
  interface Props {
    text: string;
  }

  let { text }: Props = $props();
</script>

<div class="notice-pill">
  <span class="notice-icon">ℹ️</span>
  <span class="notice-text">{text}</span>
</div>

<style>
  .notice-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    margin: 4px 0;
    border-radius: 4px;
    background-color: var(--bg-tertiary);
    border: 1px solid var(--border-color);
    font-size: 0.72rem;
    color: var(--text-muted);
  }

  .notice-icon {
    font-size: 0.75rem;
    opacity: 0.7;
  }

  .notice-text {
    font-family: var(--font-mono);
  }
</style>
