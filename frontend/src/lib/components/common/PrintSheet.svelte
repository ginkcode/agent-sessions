<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../../api';
  import { isImageFile, type LoadedImage } from '../../images';
  import { renderMarkdown } from '../../markdown';
  import { diagramSvg } from '../../mermaid';
  import { printTitle, setPrinter, type PrintJob } from '../../print';
  import { toast } from '../../stores/toast.svelte';
  import { loadImage } from '../transcript/parts/ImagePart.svelte';

  // A message laid out for paper. It is hidden on screen and is the only
  // thing printed: its images are loaded and its diagrams drawn in the light
  // theme before the print dialog opens.
  let job = $state<PrintJob | null>(null);
  // Per part: the loaded image, or why it couldn't be loaded.
  let images = $state<(LoadedImage | string | null)[]>([]);
  let sheet = $state<HTMLDivElement>();
  let generation = 0;
  let savedTitle: string | null = null;

  onMount(() => {
    setPrinter((next) => void run(next));
    // The print sheet sits beside #app so print styles can hide the rest.
    if (sheet) document.body.appendChild(sheet);
    const restore = () => restoreTitle();
    window.addEventListener('afterprint', restore);
    return () => {
      setPrinter(null);
      window.removeEventListener('afterprint', restore);
      sheet?.remove();
    };
  });

  function fileName(part: PrintJob['parts'][number]): string {
    const file = part.file;
    return file?.name || file?.path?.split(/[\\/]/).pop() || (file && isImageFile(file) ? 'Image' : 'Attachment');
  }

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  async function run(next: PrintJob): Promise<void> {
    const id = ++generation;
    restoreTitle();
    const loaded = await Promise.all(
      next.parts.map((part) =>
        part.kind === 'file' && part.file && isImageFile(part.file)
          ? loadImage(next.sessionRef, part.file).catch(message)
          : Promise.resolve(null)
      )
    );
    if (id !== generation) return;
    job = next;
    images = loaded;
    await tick();
    if (!sheet) return;
    await Promise.all([drawDiagrams(sheet), ...[...sheet.querySelectorAll('img')].map((img) => img.decode().catch(() => {}))]);
    if (id !== generation) return;

    // The dialog offers the title as the PDF's file name. macOS reports no
    // afterprint, so the next click or key puts the title back too.
    savedTitle = document.title;
    document.title = printTitle(next.title, next.role);
    try {
      await api.printWindow();
      window.addEventListener('pointerdown', restoreTitle, { capture: true, once: true });
      window.addEventListener('keydown', restoreTitle, { capture: true, once: true });
    } catch (err) {
      restoreTitle();
      toast.show({ title: "Couldn't open the print dialog", body: message(err), tone: 'error' });
    }
  }

  function restoreTitle(): void {
    window.removeEventListener('pointerdown', restoreTitle, { capture: true });
    window.removeEventListener('keydown', restoreTitle, { capture: true });
    if (savedTitle === null) return;
    document.title = savedTitle;
    savedTitle = null;
  }

  // Diagrams that fail to draw keep showing their source.
  function drawDiagrams(root: HTMLElement): Promise<unknown> {
    const blocks = [...root.querySelectorAll<HTMLElement>('.mermaid-block')];
    return Promise.all(
      blocks.map(async (block, n) => {
        const source = block.querySelector('.copy-code-btn')?.getAttribute('data-code') ?? '';
        try {
          const svg = await diagramSvg(source, 'light', `print-diagram-${n}`);
          const diagram = block.querySelector<HTMLElement>('.mermaid-diagram');
          const code = block.querySelector<HTMLElement>('.mermaid-source');
          if (!diagram || !code) return;
          diagram.innerHTML = svg;
          diagram.hidden = false;
          code.hidden = true;
          block.classList.add('drawn');
        } catch {
          // The source is printed instead.
        }
      })
    );
  }
</script>

<div class="print-sheet" bind:this={sheet}>
  {#if job}
    <header class="print-header">
      <h1>{job.title}</h1>
      <p>
        {job.role === 'user' ? 'User' : 'Assistant'}{job.model ? ` · ${job.model}` : ''}{job.time ? ` · ${job.time}` : ''}
      </p>
    </header>
    {#each job.parts as part, idx (idx)}
      {#if part.kind === 'text' && part.text}
        <div class="text-part-container">{@html renderMarkdown(part.text)}</div>
      {:else if part.kind === 'file' && part.file}
        {@const image = images[idx]}
        {#if image && typeof image === 'object'}
          <figure class="print-image">
            <img src={image.url} alt={fileName(part)} />
          </figure>
        {:else if typeof image === 'string'}
          <p class="print-note">Image not shown: {fileName(part)}. {image}</p>
        {:else}
          <p class="print-note">📎 {fileName(part)}</p>
        {/if}
      {:else if part.kind === 'notice' && part.text}
        <p class="print-note">ℹ️ {part.text}</p>
      {/if}
    {/each}
  {/if}
</div>

<style>
  .print-sheet {
    display: none;
  }

  @media print {
    .print-sheet {
      display: block;
      font-size: 14px;
      line-height: 1.6;
      color: var(--text-primary);
      print-color-adjust: exact;
      -webkit-print-color-adjust: exact;
    }

    .print-header {
      margin-bottom: 16px;
      padding-bottom: 8px;
      border-bottom: 1px solid var(--border-color);
    }

    .print-header h1 {
      font-size: 18px;
      line-height: 1.3;
    }

    .print-header p {
      font-size: 12px;
      color: var(--text-muted);
    }

    .print-sheet > :global(* + *) {
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
      font-size: 12px;
      color: var(--text-secondary);
    }

    /* Paper has no scrolling: code wraps and wide tables use the width. */
    .print-sheet :global(pre) {
      overflow: visible;
      white-space: pre-wrap;
      overflow-wrap: anywhere;
    }

    .print-sheet :global(.table-container) {
      overflow: visible;
    }

    .print-sheet :global(.code-block-actions),
    .print-sheet :global(.copy-code-btn) {
      display: none;
    }

    .print-sheet :global(.mermaid-block.drawn) {
      border: none;
      background: none;
      break-inside: avoid;
    }

    .print-sheet :global(.mermaid-block.drawn .code-block-header) {
      display: none;
    }

    .print-sheet :global(.mermaid-diagram) {
      overflow: visible;
      cursor: auto;
    }
  }
</style>
