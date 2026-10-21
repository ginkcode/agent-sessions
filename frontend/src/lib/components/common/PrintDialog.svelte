<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { modalFocus } from '../../modalFocus';
  import type { LoadedImage } from '../../images';
  import type { FileRef, SessionRef } from '../../types';
  import { printDocument, printMarkdown, setPrintEditor } from '../../print';
  import { theme } from '../../stores/theme.svelte';
  import PrintPage from './PrintPage.svelte';
  import { loadAttachments } from './PrintSheet.svelte';

  // Edits a message before printing it: the title and the Markdown, with a
  // preview of the page beside it. Images stay in the Markdown as
  // ![name](attachment:N) lines, which can be moved or deleted.
  const PREVIEW_DELAY = 250;

  let open = $state(false);
  let sessionRef = $state<SessionRef | null>(null);
  let title = $state('');
  let showTitle = $state(true);
  let contentInput = $state<HTMLTextAreaElement>();
  let markdown = $state('');
  let attachments: FileRef[] = [];
  let images = $state<Record<number, LoadedImage | string>>({});
  let preview = $state('');
  let titleInput = $state<HTMLInputElement>();
  let returnFocus: HTMLElement | null = null;
  let generation = 0;

  onMount(() => {
    setPrintEditor((job) => {
      const id = ++generation;
      const content = 'parts' in job ? printMarkdown(job.parts) : { markdown: job.markdown, attachments: [] };
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      sessionRef = job.sessionRef ?? null;
      title = job.title;
      showTitle = 'parts' in job;
      markdown = content.markdown;
      preview = content.markdown;
      attachments = content.attachments;
      images = {};
      open = true;
      void tick().then(() => showTitle ? titleInput?.select() : contentInput?.focus());
      void loadAttachments(job.sessionRef ?? null, content.attachments).then((loaded) => {
        if (id === generation) images = loaded;
      });
    });
    return () => setPrintEditor(null);
  });

  // The preview follows the Markdown once typing pauses.
  $effect(() => {
    const next = markdown;
    const timer = setTimeout(() => (preview = next), PREVIEW_DELAY);
    return () => clearTimeout(timer);
  });

  async function close(): Promise<void> {
    open = false;
    generation++;
    await tick();
    returnFocus?.focus();
    returnFocus = null;
  }

  function print(): void {
    if (!markdown.trim()) return;
    printDocument({ sessionRef, title: title.trim(), showTitle, markdown, attachments });
    void close();
  }

  function onKeydown(e: KeyboardEvent): void {
    // Keep shortcuts from reaching the file viewer underneath this dialog.
    e.stopPropagation();
    if (e.key === 'Escape') {
      e.preventDefault();
      void close();
    } else if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      print();
    }
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="dialog-backdrop" role="presentation" onkeydown={onKeydown}>
    <div class="dialog" use:modalFocus role="dialog" aria-modal="true" aria-labelledby="print-dialog-title">
      <header class="dialog-header">
        <h2 id="print-dialog-title">Print / Save as PDF</h2>
      </header>
      <div class="dialog-body">
        {#if showTitle}
        <label class="field">
          <span class="field-label">Title</span>
          <input
            bind:this={titleInput}
            bind:value={title}
            type="text"
            class="text-input"
            placeholder="No title"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="off"
            spellcheck="false"
          />
        </label>
        {/if}
        <div class="panes">
          <label class="pane">
            <span class="field-label">Content (Markdown)</span>
            <!-- Svelte's types leave autocorrect off textarea; WebKit reads it there too. -->
            <textarea bind:this={contentInput} bind:value={markdown} class="editor" autocomplete="off" {...{ autocorrect: 'off' }} autocapitalize="off" spellcheck="false"></textarea>
          </label>
          <div class="pane">
            <span class="field-label">Preview</span>
            <div class="preview">
              <PrintPage title={showTitle ? title : ''} markdown={preview} {images} shade={theme.resolved} idPrefix="print-preview" />
            </div>
          </div>
        </div>
        <p class="hint">
          Images are the <code>![name](attachment:N)</code> lines: move or delete them as you like. The page prints in the
          light theme.
        </p>
      </div>
      <footer class="dialog-footer">
        <button type="button" class="btn" onclick={() => void close()}>Cancel</button>
        <button
          type="button"
          class="btn primary-btn"
          disabled={!markdown.trim()}
          title="Open the print dialog (Ctrl+Enter)"
          onclick={print}
        >Print…</button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    background-color: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1011;
    padding: 16px;
  }

  .dialog {
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.35);
    width: min(1180px, 96vw);
    height: min(820px, 92vh);
    display: flex;
    flex-direction: column;
    color: var(--text-primary);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .dialog-header h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }

  .dialog-body {
    flex: 1;
    min-height: 0;
    padding: 14px 18px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .field-label {
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .text-input {
    width: 100%;
    padding: 7px 10px;
    font-size: 0.85rem;
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
  }

  .panes {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  .pane {
    min-height: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .editor,
  .preview {
    flex: 1;
    min-height: 0;
    border: 1px solid var(--border-color);
    border-radius: 6px;
  }

  .editor {
    width: 100%;
    padding: 10px 12px;
    resize: none;
    font-family: var(--font-mono);
    font-size: 13px;
    line-height: 1.5;
    background-color: var(--bg-secondary);
    color: var(--text-primary);
  }

  .preview {
    overflow: auto;
    padding: 16px 20px;
    background-color: var(--bg-primary);
  }

  .text-input:focus,
  .editor:focus {
    outline: none;
    border-color: var(--accent-color, #3b82f6);
  }

  .hint {
    margin: 0;
    font-size: 0.78rem;
    color: var(--text-secondary);
  }

  .hint code {
    font-family: var(--font-mono);
    font-size: 0.95em;
  }

  .dialog-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 18px;
    border-top: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .btn {
    height: 34px;
    padding: 0 16px;
    font-size: 0.82rem;
    font-weight: 500;
    border-radius: 4px;
    border: 1px solid var(--border-color);
    background-color: var(--bg-primary);
    color: var(--text-primary);
    cursor: pointer;
  }

  .btn:hover:not(:disabled) {
    background-color: var(--hover-bg);
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .primary-btn {
    background-color: var(--accent-color, #3b82f6);
    border-color: var(--accent-color, #3b82f6);
    color: #fff;
  }

  .primary-btn:hover:not(:disabled) {
    background-color: #2563eb;
    border-color: #2563eb;
  }
</style>
