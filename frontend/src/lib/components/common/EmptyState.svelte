<script lang="ts">
  interface Props {
    icon?: string;
    title: string;
    description?: string;
    actionText?: string;
    onAction?: () => void;
  }

  let {
    icon = '💬',
    title,
    description,
    actionText,
    onAction,
  }: Props = $props();
</script>

<div class="empty-state">
  <div class="empty-icon">{icon}</div>
  <h3 class="empty-title">{title}</h3>
  {#if description}
    <p class="empty-desc">{description}</p>
  {/if}
  {#if actionText && onAction}
    <button type="button" class="empty-action-btn" onclick={onAction}>
      {actionText}
    </button>
  {/if}
</div>

<style>
  .empty-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 32px 20px;
    height: 100%;
    color: var(--text-muted);
    user-select: none;
    gap: 8px;
  }

  .empty-icon {
    font-size: 2.5rem;
    margin-bottom: 4px;
    opacity: 0.6;
  }

  .empty-title {
    font-size: 1rem;
    font-weight: 600;
    color: var(--text-secondary);
    margin: 0;
  }

  .empty-desc {
    font-size: 0.825rem;
    max-width: 360px;
    line-height: 1.45;
    margin: 0;
  }

  .empty-action-btn {
    margin-top: 10px;
    padding: 6px 14px;
    border-radius: 6px;
    background-color: var(--accent-color);
    color: white;
    font-size: 0.8rem;
    font-weight: 500;
    cursor: pointer;
    transition: opacity 0.15s ease;
  }

  .empty-action-btn:hover {
    opacity: 0.9;
  }
</style>
