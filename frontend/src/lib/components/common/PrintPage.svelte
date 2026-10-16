<script lang="ts">
  import type { LoadedImage } from '../../images';
  import { renderMarkdown } from '../../markdown';
  import { diagramSvg } from '../../mermaid';
  import { printBlocks } from '../../print';
  import type { ResolvedTheme } from '../../theme';

  // A print document laid out as on paper: the title, then the Markdown with
  // its attachment images and its diagrams drawn. The editor previews it and
  // the print sheet prints it.
  interface Props {
    title: string;
    markdown: string;
    /** By attachment number: the loaded image, or why it couldn't be loaded. */
    images: Record<number, LoadedImage | string>;
    shade: ResolvedTheme;
    /** Keeps the ids of drawn diagrams apart from other copies. */
    idPrefix: string;
  }

  let { title, markdown, images, shade, idPrefix }: Props = $props();

  let blocks = $derived(printBlocks(markdown));
  let root = $state<HTMLDivElement>();
  let drawing: Promise<unknown> = Promise.resolve();
  let drawCount = 0;

  /** Settles once the diagrams of the current Markdown are drawn. */
  export function drawn(): Promise<unknown> {
    return drawing;
  }

  $effect(() => {
    void blocks;
    if (root) drawing = drawDiagrams(root, shade);
  });

  // Diagrams that fail to draw keep showing their source.
  function drawDiagrams(el: HTMLElement, theme: ResolvedTheme): Promise<unknown> {
    const diagrams = [...el.querySelectorAll<HTMLElement>('.mermaid-block')];
    return Promise.all(
      diagrams.map(async (block) => {
        // Blocks the Markdown didn't change are drawn already.
        if (block.dataset.printTheme === theme) return;
        block.dataset.printTheme = theme;
        const source = block.querySelector('.copy-code-btn')?.getAttribute('data-code') ?? '';
        try {
          const svg = await diagramSvg(source, theme, `${idPrefix}-${++drawCount}`);
          const diagram = block.querySelector<HTMLElement>('.mermaid-diagram');
          const code = block.querySelector<HTMLElement>('.mermaid-source');
          if (!block.isConnected || block.dataset.printTheme !== theme || !diagram || !code) return;
          diagram.innerHTML = svg;
          diagram.hidden = false;
          code.hidden = true;
          block.classList.add('drawn');
        } catch {
          // The source is shown instead.
        }
      })
    );
  }
</script>

<div class="print-page" bind:this={root}>
  {#if title.trim()}
    <h1 class="print-title">{title.trim()}</h1>
  {/if}
  {#each blocks as block, idx (idx)}
    {#if block.kind === 'markdown'}
      <div class="text-part-container">{@html renderMarkdown(block.text)}</div>
    {:else}
      {@const image = images[block.index]}
      {#if image && typeof image === 'object'}
        <figure class="print-image">
          <img src={image.url} alt={block.alt} />
        </figure>
      {:else}
        <p class="print-note">
          Image not shown: {block.alt || `attachment ${block.index}`}.{typeof image === 'string' ? ` ${image}` : ''}
        </p>
      {/if}
    {/if}
  {/each}
</div>

<style>
  /* Paper reads smaller than the screen: the transcript's sizes at 90%. */
  .print-page {
    font-size: 12.6px;
    line-height: 1.6;
    color: var(--text-primary);
  }

  .print-title {
    font-size: 19.8px;
    line-height: 1.3;
    margin-bottom: 12px;
  }

  .print-page :global(h1) {
    font-size: 18px;
  }

  .print-page :global(h2) {
    font-size: 15.8px;
  }

  .print-page :global(h3) {
    font-size: 13.7px;
  }

  .print-page :global(table),
  .print-page :global(code.hljs) {
    font-size: 11.5px;
  }

  .print-page :global(.code-lang-tag) {
    font-size: 9.8px;
  }

  .print-page > :global(* + *) {
    margin-top: 10px;
  }

  .print-image {
    break-inside: avoid;
  }

  .print-image img {
    display: block;
    max-width: 100%;
    max-height: 230mm;
    border: 1px solid var(--border-color);
    border-radius: 6px;
  }

  .print-note {
    font-size: 10.8px;
    color: var(--text-secondary);
  }

  /* Paper has no scrolling: code wraps and wide tables use the width. */
  .print-page :global(pre) {
    overflow: visible;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .print-page :global(.table-container) {
    overflow: visible;
  }

  .print-page :global(.code-block-actions),
  .print-page :global(.copy-code-btn) {
    display: none;
  }

  .print-page :global(.mermaid-block.drawn) {
    border: none;
    background: none;
    break-inside: avoid;
  }

  .print-page :global(.mermaid-block.drawn .code-block-header) {
    display: none;
  }

  .print-page :global(.mermaid-diagram) {
    overflow: visible;
    cursor: auto;
  }
</style>
