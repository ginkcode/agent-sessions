<script lang="ts">
  import { renderMarkdown } from '../../../markdown';
  import { handleCopyCodeClick } from '../../../copycode';

  interface Props {
    text: string;
  }

  let { text }: Props = $props();

  let renderedHtml = $derived(renderMarkdown(text));
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="text-part-container" onclick={handleCopyCodeClick}>
  {@html renderedHtml}
</div>

<style>
  .text-part-container {
    font-size: 0.875rem;
    line-height: 1.6;
    color: var(--text-primary);
    word-break: break-word;
  }

  :global(.text-part-container p) {
    margin: 0.5em 0;
  }

  :global(.text-part-container p:first-child) {
    margin-top: 0;
  }

  :global(.text-part-container p:last-child) {
    margin-bottom: 0;
  }

  :global(.text-part-container h1, .text-part-container h2, .text-part-container h3, .text-part-container h4) {
    margin: 1em 0 0.4em 0;
    color: var(--text-primary);
  }

  :global(.text-part-container h1) { font-size: 1.25rem; }
  :global(.text-part-container h2) { font-size: 1.1rem; }
  :global(.text-part-container h3) { font-size: 0.95rem; }

  :global(.text-part-container ul, .text-part-container ol) {
    margin: 0.5em 0;
    padding-left: 1.5em;
  }

  :global(.text-part-container li) {
    margin: 0.25em 0;
  }

  :global(.text-part-container blockquote) {
    margin: 0.8em 0;
    padding: 6px 12px;
    border-left: 3px solid var(--accent-color);
    background-color: var(--bg-secondary);
    color: var(--text-secondary);
    border-radius: 0 4px 4px 0;
  }

  :global(.text-part-container .inline-code) {
    font-family: var(--font-mono);
    font-size: 0.825em;
    padding: 1px 4px;
    border-radius: 4px;
    background-color: var(--bg-tertiary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
  }

  :global(.text-part-container a) {
    color: var(--accent-color);
    text-decoration: underline;
  }

  :global(.text-part-container hr.markdown-divider) {
    border: none;
    border-top: 1px solid var(--border-color);
    margin: 1.2em 0;
  }

  :global(.text-part-container .table-container) {
    overflow-x: auto;
    margin: 0.8em 0;
  }

  :global(.text-part-container table) {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.8rem;
  }

  :global(.text-part-container th, .text-part-container td) {
    border: 1px solid var(--border-color);
    padding: 6px 10px;
    text-align: left;
  }

  :global(.text-part-container th) {
    background-color: var(--bg-secondary);
    font-weight: 600;
  }

  :global(.text-part-container .code-block-container) {
    margin: 0.8em 0;
    border-radius: 6px;
    border: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
    overflow: hidden;
  }

  :global(.text-part-container .code-block-header) {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 4px 10px;
    background-color: var(--bg-tertiary);
    border-bottom: 1px solid var(--border-color);
  }

  :global(.text-part-container .code-lang-tag) {
    font-size: 0.68rem;
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
  }

  :global(.text-part-container .copy-code-btn) {
    font-size: 0.68rem;
    padding: 2px 6px;
    border-radius: 3px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    cursor: pointer;
    transition: all 0.15s ease;
  }

  :global(.text-part-container .copy-code-btn:hover) {
    color: var(--text-primary);
  }

  :global(.text-part-container .copy-code-btn.copied) {
    background-color: rgba(16, 185, 129, 0.15);
    color: var(--status-live);
    border-color: var(--status-live);
  }

  :global(.text-part-container pre) {
    margin: 0;
    padding: 10px 12px;
    overflow-x: auto;
    background-color: var(--bg-primary);
  }

  :global(.text-part-container code.hljs) {
    font-family: var(--font-mono);
    font-size: 0.8rem;
    line-height: 1.45;
  }
</style>
