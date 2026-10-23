<script lang="ts">
  import { onMount } from 'svelte';
  import type { CodeDiagnosticPosition, CodeDiagnosticRange } from '../../../codeDiagnostics';
  let pending: { range: CodeDiagnosticRange; valid: () => boolean } | undefined;
  export function revealRange(range: CodeDiagnosticRange, valid: () => boolean): boolean {
    if (editor) return valid() && editor.revealRange(range);
    pending = { range, valid };
    return false;
  }
  import type { FileEditorController, FileEditorOptions } from '../../../fileEditor';
  import { filePrompt } from '../../../stores/filePrompt.svelte';

  let { id, text, path, version, wrap, disabled = false, diagnostics, position, onposition, selection, onselection, onchange, onsave, symbols }: FileEditorOptions = $props();
  let host: HTMLDivElement;
  let editor = $state<FileEditorController>();
  let error = $state('');

  function options(): FileEditorOptions {
    return { id, text, path, version, wrap, disabled, diagnostics, position, onposition, selection, onselection, onchange, onsave, symbols };
  }

  onMount(() => {
    let disposed = false;
    void import('../../../fileEditor').then(({ createFileEditor }) => {
      if (!disposed) {
        editor = createFileEditor(host, options());
        if (pending?.valid()) editor.revealRange(pending.range);
        pending = undefined;
        // The runtime loads lazily: by now a prompt may hold focus.
        if (!filePrompt.request) editor.focus();
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
  export function selectedText(): string { return editor?.selectedText() ?? ''; }
  export function caret(): CodeDiagnosticPosition | undefined { return editor?.caret(); }
  export function find(): boolean { return editor?.find() ?? false; }
  export function closeSearch(): boolean { return editor?.closeSearch() ?? false; }
  export function handleKey(event: KeyboardEvent): boolean { return editor?.handleKey(event) ?? false; }
</script>

<!-- file-source: the viewer's token colours (see SourceView). -->
<!-- These attributes inherit into CodeMirror's dynamically created search
     and replace inputs as well as its editable content. -->
<div class="file-editor file-source" {...{ autocorrect: 'off' }} autocapitalize="off" spellcheck="false" bind:this={host}></div>
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
