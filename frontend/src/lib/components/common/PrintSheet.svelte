<script module lang="ts">
  import type { LoadedImage } from '../../images';
  import type { FileRef, SessionRef } from '../../types';
  import { loadImage } from '../transcript/parts/ImagePart.svelte';

  /** A document's images by attachment number, or why each couldn't be loaded. */
  export async function loadAttachments(
    sessionRef: SessionRef | null,
    files: FileRef[]
  ): Promise<Record<number, LoadedImage | string>> {
    if (!sessionRef) return {};
    const loaded = await Promise.all(
      files.map((file) => loadImage(sessionRef, file).catch((err: unknown) => (err instanceof Error ? err.message : String(err))))
    );
    return Object.fromEntries(loaded.map((image, i) => [i + 1, image]));
  }
</script>

<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../../api';
  import { printTitle, setPrinter, type PrintDocument } from '../../print';
  import { toast } from '../../stores/toast.svelte';
  import PrintPage from './PrintPage.svelte';

  // The document being printed. It is hidden on screen and is the only thing
  // printed: its images are loaded and its diagrams drawn in the light theme
  // before the print dialog opens.
  let doc = $state<PrintDocument | null>(null);
  let images = $state<Record<number, LoadedImage | string>>({});
  let sheet = $state<HTMLDivElement>();
  let page = $state<PrintPage>();
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

  async function run(next: PrintDocument): Promise<void> {
    const id = ++generation;
    restoreTitle();
    const loaded = await loadAttachments(next.sessionRef, next.attachments);
    if (id !== generation) return;
    doc = next;
    images = loaded;
    await tick();
    if (!sheet) return;
    await Promise.all([page?.drawn(), ...[...sheet.querySelectorAll('img')].map((img) => img.decode().catch(() => {}))]);
    if (id !== generation) return;

    // The dialog offers the title as the PDF's file name. macOS reports no
    // afterprint, so the next click or key puts the title back too.
    savedTitle = document.title;
    document.title = printTitle(next.title);
    try {
      await api.printWindow();
      window.addEventListener('pointerdown', restoreTitle, { capture: true, once: true });
      window.addEventListener('keydown', restoreTitle, { capture: true, once: true });
    } catch (err) {
      restoreTitle();
      toast.show({
        title: "Couldn't open the print dialog",
        body: err instanceof Error ? err.message : String(err),
        tone: 'error',
      });
    }
  }

  function restoreTitle(): void {
    window.removeEventListener('pointerdown', restoreTitle, { capture: true });
    window.removeEventListener('keydown', restoreTitle, { capture: true });
    if (savedTitle === null) return;
    document.title = savedTitle;
    savedTitle = null;
  }
</script>

<div class="print-sheet" bind:this={sheet}>
  {#if doc}
    {#key doc}
      <PrintPage bind:this={page} title={doc.showTitle === false ? '' : doc.title} markdown={doc.markdown} {images} shade="light" idPrefix="print-diagram" />
    {/key}
  {/if}
</div>

<style>
  .print-sheet {
    display: none;
  }

  @media print {
    .print-sheet {
      display: block;
      print-color-adjust: exact;
      -webkit-print-color-adjust: exact;
    }
  }
</style>
