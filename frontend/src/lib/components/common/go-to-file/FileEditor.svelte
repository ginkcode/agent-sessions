<script lang="ts">
  import { onMount } from 'svelte';
  import type { FileEditorController, FileEditorOptions } from '../../../fileEditor';

  let { id, text, path, version, wrap, disabled = false, onchange, onsave }: FileEditorOptions = $props();
  let host: HTMLDivElement;
  let editor = $state<FileEditorController>();
  let error = $state('');

  function options(): FileEditorOptions {
    return { id, text, path, version, wrap, disabled, onchange, onsave };
  }

  onMount(() => {
    let disposed = false;
    void import('../../../fileEditor').then(({ createFileEditor }) => {
      if (!disposed) {
        editor = createFileEditor(host, options());
        editor.focus();
      }
    }).catch((cause: unknown) => {
      if (!disposed) error = cause instanceof Error ? cause.message : 'Unable to load editor';
    });
    return () => {
      disposed = true;
      editor?.destroy();
      editor = undefined;
    };
  });

  $effect(() => {
    editor?.update(options());
  });

  /** Commands are harmless no-ops until the lazy runtime has mounted. */
  export function focus(): void { editor?.focus(); }
  export function find(): boolean { return editor?.find() ?? false; }
  export function closeSearch(): boolean { return editor?.closeSearch() ?? false; }
  export function handleKey(event: KeyboardEvent): boolean { return editor?.handleKey(event) ?? false; }
</script>

<div class="file-editor" bind:this={host}></div>
{#if error}
  <div class="editor-error" role="alert">{error}</div>
{/if}

<style>
  .file-editor {
    height: 100%;
    min-height: 0;
    min-width: 0;
    overflow: hidden;
  }

  .editor-error {
    padding: 12px;
    color: var(--text-muted);
  }
</style>
