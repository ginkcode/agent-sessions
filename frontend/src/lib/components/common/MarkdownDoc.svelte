<script lang="ts">
  import { renderMarkdown } from '../../markdown';
  import { handleCopyCodeClick } from '../../copycode';

  interface Props {
    source: string;
  }

  let { source }: Props = $props();

  let renderedHtml = $derived(renderMarkdown(source));
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="markdown-doc" onclick={handleCopyCodeClick}>
  {@html renderedHtml}
</div>

<style>
  .markdown-doc {
    font-size: 13px;
    line-height: 1.55;
    color: var(--text-primary);
    word-break: break-word;
  }

  :global(.markdown-doc p) {
    margin: 0.5em 0;
  }

  :global(.markdown-doc p:first-child) {
    margin-top: 0;
  }

  :global(.markdown-doc p:last-child) {
    margin-bottom: 0;
  }

  :global(.markdown-doc h1, .markdown-doc h2, .markdown-doc h3, .markdown-doc h4) {
    margin: 1em 0 0.4em 0;
    color: var(--text-primary);
  }

  :global(.markdown-doc h1) { font-size: 18px; }
  :global(.markdown-doc h2) { font-size: 16px; }
  :global(.markdown-doc h3) { font-size: 14px; }

  :global(.markdown-doc ul, .markdown-doc ol) {
    margin: 0.5em 0;
    padding-left: 1.5em;
  }

  :global(.markdown-doc li) {
    margin: 0.25em 0;
  }

  :global(.markdown-doc blockquote) {
    margin: 0.8em 0;
    padding: 6px 12px;
    border-left: 3px solid var(--accent-color);
    background-color: var(--bg-secondary);
    color: var(--text-secondary);
    border-radius: 0 4px 4px 0;
  }

  :global(.markdown-doc .inline-code) {
    font-family: var(--font-mono);
    font-size: 0.825em;
    padding: 1px 4px;
    border-radius: 4px;
    background-color: var(--bg-tertiary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
  }

  :global(.markdown-doc a) {
    color: var(--accent-color);
    text-decoration: underline;
  }

  :global(.markdown-doc hr.markdown-divider) {
    border: none;
    border-top: 1px solid var(--border-color);
    margin: 1.2em 0;
  }

  :global(.markdown-doc .table-container) {
    overflow-x: auto;
    margin: 0.8em 0;
  }

  :global(.markdown-doc table) {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }

  :global(.markdown-doc th, .markdown-doc td) {
    border: 1px solid var(--border-color);
    padding: 4px 8px;
    text-align: left;
  }

  :global(.markdown-doc th) {
    background-color: var(--bg-secondary);
    font-weight: 600;
  }

  :global(.markdown-doc .code-block-container) {
    margin: 0.8em 0;
    border-radius: 6px;
    border: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
    overflow: hidden;
  }

  :global(.markdown-doc .code-block-header) {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 4px 10px;
    background-color: var(--bg-tertiary);
    border-bottom: 1px solid var(--border-color);
  }

  :global(.markdown-doc .code-lang-tag) {
    font-size: 10.88px;
    font-family: var(--font-mono);
    font-weight: 600;
    color: var(--text-muted);
    text-transform: uppercase;
  }

  :global(.markdown-doc .copy-code-btn) {
    font-size: 10.88px;
    padding: 2px 6px;
    border-radius: 3px;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    cursor: pointer;
    transition: all 0.15s ease;
  }

  :global(.markdown-doc .copy-code-btn:hover) {
    color: var(--text-primary);
  }

  :global(.markdown-doc .copy-code-btn.copied) {
    background-color: rgba(16, 185, 129, 0.15);
    color: var(--status-live);
    border-color: var(--status-live);
  }

  :global(.markdown-doc pre) {
    margin: 0;
    padding: 8px 12px;
    overflow-x: auto;
    background-color: var(--bg-primary);
  }

  :global(.markdown-doc code.hljs) {
    font-family: var(--font-mono);
    font-size: 12px;
    line-height: 1.45;
  }
</style>