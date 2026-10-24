<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../api';
  import { copyToClipboard } from '../../portable';
  import { errorText } from '../../manage';
  import { toast } from '../../stores/toast.svelte';
  import { copyFailed } from '../../guidance';
  import { requestSelectionTranslation, type SelectedText } from '../../selectionTranslation';

  let { selection, language, onClose }: {
    selection: SelectedText;
    language: string;
    onClose: () => void;
  } = $props();

  let card: HTMLDivElement | undefined = $state();
  let left = $state(0);
  let top = $state(0);
  let status = $state<'loading' | 'done' | 'error'>('loading');
  let result = $state('');
  let error = $state('');
  let attempt = $state(0);

  // Cleanup invalidates both success and failure when dismissed or superseded.
  $effect(() => {
    const source = selection;
    void language;
    void attempt;
    status = 'loading';
    result = '';
    error = '';
    return requestSelectionTranslation(() => api.translateSelection(source.text, source.context), text => {
      result = text;
      status = 'done';
    }, err => {
      error = errorText(err, 'Translation failed');
      status = 'error';
    });
  });

  $effect(() => {
    void status; void result; void error;
    if (!card) return;
    const { width, height } = card.getBoundingClientRect();
    const anchor = selection.rect;
    left = Math.max(8, Math.min(anchor.left, window.innerWidth - width - 8));
    top = anchor.bottom + 8 + height <= window.innerHeight - 8
      ? anchor.bottom + 8 : Math.max(8, anchor.top - height - 8);
  });

  onMount(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    card?.focus();
    function dismiss(event: Event) {
      if (card?.contains(event.target as Node)) return;
      onClose();
    }
    function key(event: KeyboardEvent) {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopPropagation();
      onClose();
      previous?.focus();
    }
    document.addEventListener('pointerdown', dismiss, true);
    document.addEventListener('scroll', dismiss, true);
    document.addEventListener('keydown', key, true);
    window.addEventListener('resize', onClose);
    return () => {
      document.removeEventListener('pointerdown', dismiss, true);
      document.removeEventListener('scroll', dismiss, true);
      document.removeEventListener('keydown', key, true);
      window.removeEventListener('resize', onClose);
    };
  });

  async function copy() {
    try {
      if (!(await copyToClipboard(result))) throw new Error('The clipboard is not available.');
      toast.show({ title: 'Translation copied', body: 'Copied the selected text’s translation.' });
    } catch (err) {
      toast.show(copyFailed('the translation', err));
    }
  }
</script>

<div class="selection-translation" role="dialog" aria-label="Selection translation" tabindex="-1" bind:this={card} style:left="{left}px" style:top="{top}px">
  <header>
    <strong>Translation · {language}</strong>
    <button type="button" aria-label="Close translation" onclick={onClose}>×</button>
  </header>
  {#if status === 'loading'}
    <p role="status">Translating…</p>
  {:else if status === 'error'}
    <p role="alert">{error}</p>
    <button type="button" class="btn" onclick={() => attempt++}>Retry</button>
  {:else}
    <p class="result" aria-live="polite">{result}</p>
    <button type="button" class="btn" onclick={() => void copy()}>Copy translation</button>
  {/if}
</div>

<style>
  .selection-translation {
    position: fixed;
    z-index: 1000;
    width: 360px;
    max-width: calc(100vw - 16px);
    max-height: calc(100vh - 16px);
    overflow: auto;
    padding: 12px;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background: var(--bg-primary);
    color: var(--text-primary);
    box-shadow: 0 4px 16px rgba(0, 0, 0, 0.25);
    font-size: 13px;
    line-height: 1.5;
    overflow-wrap: anywhere;
  }
  header { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  header button { font-size: 20px; color: var(--text-secondary); }
  p { margin: 10px 0; }
  .result { white-space: pre-wrap; user-select: text; }
</style>
