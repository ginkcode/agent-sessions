<script lang="ts">
  import { renderMarkdown } from '../../../markdown';
  import { handleCopyCodeClick } from '../../../copycode';

  interface Props {
    text: string;
    defaultExpanded?: boolean;
  }

  let { text, defaultExpanded = false }: Props = $props();

  let renderedHtml = $derived(renderMarkdown(text));
</script>

<details class="reasoning-block" open={defaultExpanded}>
  <summary class="reasoning-summary">
    <span class="reasoning-icon">🧠</span>
    <span class="reasoning-title">Thinking process…</span>
  </summary>
  <div class="reasoning-body">
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="markdown-content text-part-container" onclick={handleCopyCodeClick}>
      {@html renderedHtml}
    </div>
  </div>
</details>

<style>
  .reasoning-block {
    margin: 8px 0;
    border-radius: 6px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    overflow: hidden;
  }

  .reasoning-summary {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    cursor: pointer;
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--text-secondary);
    user-select: none;
    background-color: var(--bg-secondary);
  }

  .reasoning-summary:hover {
    color: var(--text-primary);
  }

  .reasoning-icon {
    font-size: 0.85rem;
  }

  .reasoning-body {
    padding: 10px 14px;
    border-top: 1px solid var(--border-color);
    font-size: 0.825rem;
    color: var(--text-secondary);
    line-height: 1.5;
    background-color: var(--bg-primary);
  }
</style>
