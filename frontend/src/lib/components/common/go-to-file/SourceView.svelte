<script lang="ts">
  import { files } from '../../../stores/files.svelte';
  import type { DiffRow } from '../../../files';

  let {
    rows,
    htmlLines,
    plainLines,
    showDiff,
    presentation,
    presentationFontSize,
    codeEl = $bindable<HTMLElement | undefined>(),
  }: {
    rows: DiffRow[];
    htmlLines: string[] | null;
    plainLines: string[];
    showDiff: boolean;
    presentation: boolean;
    presentationFontSize: number;
    codeEl?: HTMLElement;
  } = $props();

  const MARK_LABELS = {
    added: 'Added line',
    modified: 'Changed line',
    'deleted-above': 'Lines removed above',
    'deleted-below': 'Lines removed below',
  };
</script>

<div class="code source file-source" bind:this={codeEl} class:wrap={files.wrap} class:diff={showDiff} style:font-size={presentation ? `${presentationFontSize}px` : undefined} style:--digits={String(htmlLines?.length ?? plainLines.length).length}>
  {#snippet lineNumber(row: DiffRow)}
    {#if row.kind === 'removed'}
      <span aria-hidden="true" class="ln sign removed">−</span>
    {:else if showDiff}
      <span aria-hidden="true" class="ln" class:sign={row.mark} class:added={row.mark}>{row.mark ? '+' : row.index + 1}</span>
    {:else}
      <span aria-hidden="true" class="ln mark-{row.mark ?? 'none'}" title={row.mark && MARK_LABELS[row.mark]}>{row.index + 1}</span>
    {/if}
  {/snippet}
  {#snippet lineText(row: DiffRow)}
    {#if row.kind === 'removed'}
      <span class="line removed">{row.text}</span>
    {:else if htmlLines}
      <!-- highlight.js escapes the file's text -->
      <span class="line" data-source-line={row.index + 1} class:added={showDiff && row.mark}>{@html htmlLines[row.index]}</span>
    {:else}
      <span class="line" data-source-line={row.index + 1} class:added={showDiff && row.mark}>{plainLines[row.index]}</span>
    {/if}
  {/snippet}
  {#if files.wrap}
    {#each rows as row, r (r)}
      {@render lineNumber(row)}
      {@render lineText(row)}
    {/each}
  {:else}
    <div class="gutter">
      {#each rows as row, r (r)}
        {@render lineNumber(row)}
      {/each}
    </div>
    <div class="lines">
      {#each rows as row, r (r)}
        {@render lineText(row)}
      {/each}
    </div>
  {/if}
</div>

<style>
  /* Changed lines: a bar at the gutter's right edge, as in VS Code. A
     triangle-ish marker on the line border shows where lines were removed. */
  .ln.mark-added {
    box-shadow: inset -3px 0 0 #3fb950;
  }
  .ln.mark-modified {
    box-shadow: inset -3px 0 0 #2f81f7;
  }
  .ln.mark-deleted-above {
    background-image: linear-gradient(#f85149, #f85149);
    background-size: 100% 2px;
    background-position: top;
    background-repeat: no-repeat;
  }
  .ln.mark-deleted-below {
    background-image: linear-gradient(#f85149, #f85149);
    background-size: 100% 2px;
    background-position: bottom;
    background-repeat: no-repeat;
  }

  /* Diff view: removed lines on red, changed and added ones on green,
     with − and + in the gutter. */
  .line.removed {
    background-color: rgba(248, 81, 73, 0.15);
    color: var(--text-secondary);
  }
  .line.added {
    background-color: rgba(46, 160, 67, 0.15);
  }
  .ln.sign {
    color: var(--text-secondary);
    font-weight: 600;
  }
  /* Tints over the gutter's color, so lines scrolled under it stay hidden. */
  .ln.removed {
    background-image: linear-gradient(rgba(248, 81, 73, 0.22), rgba(248, 81, 73, 0.22));
  }
  .ln.added {
    background-image: linear-gradient(rgba(46, 160, 67, 0.22), rgba(46, 160, 67, 0.22));
  }

  /* Line numbers and lines side by side. Wrapped, one grid row per line, so
     a wrapped line keeps its number at its first row. Unwrapped, nothing
     wraps, so the numbers stack in one gutter column that sticks as the
     lines scroll sideways; each row is one line high so the two columns
     stay aligned. WebKitGTK scrolled at half speed with a sticky number on
     every line, and stalls on a subgrid of thousands of rows. The gutter
     runs the full height of the view, past a short file's last line. */
  .code {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    align-content: start;
    min-width: max-content;
    min-height: 100%;
    padding: 8px 0;
    font-family: var(--font-mono);
    font-size: 0.78rem;
    line-height: 1.5;
    color: var(--text-primary);
    tab-size: 4;
  }

  /* Wrapped, the numbers are grid items row by row, so nothing spans the
     column below the last one. It gets a width from the digits of the
     last number, its padding and border, and the view paints that strip
     top to bottom. */
  .code.wrap {
    --gutter: calc(var(--digits, 1) * 1ch + 25px);
    grid-template-columns: var(--gutter) minmax(0, 1fr);
    min-width: 0;
    background-image: linear-gradient(
      to right,
      var(--bg-secondary) calc(var(--gutter) - 1px),
      var(--border-color) 0 var(--gutter),
      transparent 0
    );
  }

  /* Unwrapped, the one row of gutter and lines stretches to the full
     height, the padding moved inside so the gutter covers it too. */
  .code:not(.wrap) {
    align-content: stretch;
    padding: 0;
  }

  .gutter,
  .lines {
    padding: 8px 0;
  }

  .gutter {
    position: sticky;
    left: 0;
    background-color: var(--bg-secondary);
    border-right: 1px solid var(--border-color);
  }

  .gutter .ln {
    border-right: none;
  }

  .gutter .ln,
  .lines .line {
    display: block;
    height: 1.5em;
  }

  .ln {
    padding: 0 10px 0 14px;
    text-align: right;
    color: var(--text-muted);
    background-color: var(--bg-secondary);
    border-right: 1px solid var(--border-color);
    user-select: none;
  }

  .line {
    padding: 0 14px;
    white-space: pre;
  }

  .code.wrap .line {
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  /* highlight.js tokens, GitHub colors. Scoped to file sources (this view
     and the editor, which emits the same classes) so the transcript's code
     blocks keep their own look. */
  :global(.file-source) :global(.hljs-keyword),
  :global(.file-source) :global(.hljs-doctag),
  :global(.file-source) :global(.hljs-meta .hljs-keyword),
  :global(.file-source) :global(.hljs-template-tag),
  :global(.file-source) :global(.hljs-template-variable),
  :global(.file-source) :global(.hljs-type),
  :global(.file-source) :global(.hljs-variable.language_) {
    color: #cf222e;
  }
  :global(.file-source) :global(.hljs-title),
  :global(.file-source) :global(.hljs-title.class_),
  :global(.file-source) :global(.hljs-title.function_) {
    color: #8250df;
  }
  :global(.file-source) :global(.hljs-attr),
  :global(.file-source) :global(.hljs-attribute),
  :global(.file-source) :global(.hljs-literal),
  :global(.file-source) :global(.hljs-meta),
  :global(.file-source) :global(.hljs-number),
  :global(.file-source) :global(.hljs-operator),
  :global(.file-source) :global(.hljs-variable),
  :global(.file-source) :global(.hljs-selector-attr),
  :global(.file-source) :global(.hljs-selector-class),
  :global(.file-source) :global(.hljs-selector-id),
  :global(.file-source) :global(.hljs-property) {
    color: #0550ae;
  }
  :global(.file-source) :global(.hljs-regexp),
  :global(.file-source) :global(.hljs-string),
  :global(.file-source) :global(.hljs-meta .hljs-string) {
    color: #0a3069;
  }
  :global(.file-source) :global(.hljs-built_in),
  :global(.file-source) :global(.hljs-symbol),
  :global(.file-source) :global(.hljs-params) {
    color: #953800;
  }
  :global(.file-source) :global(.hljs-comment),
  :global(.file-source) :global(.hljs-code),
  :global(.file-source) :global(.hljs-formula) {
    color: #6e7781;
    font-style: italic;
  }
  :global(.file-source) :global(.hljs-name),
  :global(.file-source) :global(.hljs-quote),
  :global(.file-source) :global(.hljs-selector-tag),
  :global(.file-source) :global(.hljs-selector-pseudo),
  :global(.file-source) :global(.hljs-tag) {
    color: #116329;
  }
  :global(.file-source) :global(.hljs-subst) {
    color: inherit;
  }
  :global(.file-source) :global(.hljs-section) {
    color: #0550ae;
    font-weight: bold;
  }
  :global(.file-source) :global(.hljs-bullet) {
    color: #953800;
  }
  :global(.file-source) :global(.hljs-emphasis) {
    font-style: italic;
  }
  :global(.file-source) :global(.hljs-strong) {
    font-weight: bold;
  }
  :global(.file-source) :global(.hljs-addition) {
    color: #116329;
    background-color: #dafbe1;
  }
  :global(.file-source) :global(.hljs-deletion) {
    color: #82071e;
    background-color: #ffebe9;
  }

  :global([data-theme='dark']) :global(.file-source) :global(.hljs-keyword),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-doctag),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-meta .hljs-keyword),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-template-tag),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-template-variable),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-type),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-variable.language_) {
    color: #ff7b72;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-title),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-title.class_),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-title.function_) {
    color: #d2a8ff;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-attr),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-attribute),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-literal),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-meta),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-number),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-operator),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-variable),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-selector-attr),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-selector-class),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-selector-id),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-property),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-section) {
    color: #79c0ff;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-regexp),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-string),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-meta .hljs-string) {
    color: #a5d6ff;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-built_in),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-symbol),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-params),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-bullet) {
    color: #ffa657;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-comment),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-code),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-formula) {
    color: #8b949e;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-name),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-quote),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-selector-tag),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-selector-pseudo),
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-tag) {
    color: #7ee787;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-addition) {
    color: #aff5b4;
    background-color: #033a16;
  }
  :global([data-theme='dark']) :global(.file-source) :global(.hljs-deletion) {
    color: #ffdcd7;
    background-color: #67060c;
  }
</style>
