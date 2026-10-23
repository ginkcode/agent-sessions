<script lang="ts">
  import { paintSourceDiagnostics, type SourceDiagnosticProjection } from '../../../sourceDiagnostics';
  import { locationLabel, longMessage, messagePreview, severityLabels, staleKind, toolName } from '../../../codeCheckLabels';
  import { files } from '../../../stores/files.svelte';
  import type { DiffRow } from '../../../files';
  import { includesFileLine, type FileLineSelection } from '../../../fileSelection';
  import { HOVER_DELAY, caretPoint, linkModifier, sourcePosition, sourceRange, wordRange, type SourceSymbols } from '../../../sourceNavigation';

  let {
    rows,
    htmlLines,
    plainLines,
    showDiff,
    presentation,
    presentationFontSize,
    codeEl = $bindable<HTMLElement | undefined>(),
    selection,
    onselectline,
    onclearselection,
    diagnostics = { messages: [], segments: [] },
    recovery,
    symbols,
  }: {
    /** Code navigation on words: underline, Cmd/Ctrl+click, hover and menu. */
    symbols?: SourceSymbols;
    diagnostics?: SourceDiagnosticProjection;
    /** Explicit, described recovery for results that are actually stale (never shown for file-level findings). */
    recovery?: { label: string; description: string; disabled?: boolean; run: () => void };
    rows: DiffRow[];
    htmlLines: string[] | null;
    plainLines: string[];
    showDiff: boolean;
    presentation: boolean;
    presentationFontSize: number;
    codeEl?: HTMLElement;
    selection?: FileLineSelection;
    onselectline?: (line: number, extend: boolean) => void;
    /** A click on empty space, past the last line. */
    onclearselection?: () => void;
  } = $props();

  const messageId = $props.id();
  let messagesEl: HTMLDetailsElement | undefined = $state();
  function selectDiagnosticLine(line: number, extend: boolean) {
    onselectline?.(line, extend);
    if (lineMessages.has(line - 1) && messagesEl) messagesEl.open = true;
  }
  let activeDiagnostics = $derived(showDiff ? { messages: [], segments: [] } : diagnostics);
  let severityCounts = $derived((['error', 'warning', 'information', 'hint'] as const)
    .map(severity => [severity, activeDiagnostics.messages.filter(m => m.finding.diagnostic.severity === severity).length] as const)
    .filter(([, count]) => count > 0));
  let staleCount = $derived(activeDiagnostics.messages.filter(m => staleKind(m.kind)).length);
  let lineMessages = $derived.by(() => {
    const map = new Map<number, number[]>();
    for (const s of activeDiagnostics.segments) {
      const indexes = map.get(s.line) ?? [];
      if (!indexes.includes(s.finding)) indexes.push(s.finding);
      map.set(s.line, indexes);
    }
    return map;
  });
  $effect(() => {
    htmlLines; plainLines; rows;
    if (codeEl) return paintSourceDiagnostics(codeEl, activeDiagnostics, 'source-diagnostic');
  });

  const isMac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform);

  // Words as links: under Cmd (macOS) or Ctrl the word under the pointer is
  // underlined and a click goes to its definition; resting on a word asks
  // for its hover. The pointer's column comes from the text, so highlighting
  // markup never shifts it.
  function symbolPointer(node: HTMLElement, initial: SourceSymbols | undefined) {
    let symbols = initial;
    const doc = node.ownerDocument;
    const view = doc.defaultView as (Window & { Highlight?: new (...ranges: Range[]) => unknown; CSS?: { highlights?: Map<string, unknown> } }) | null;
    let x = 0, y = 0, inside = false, linking = false, buttons = 0;
    let hovered = '';
    let timer: ReturnType<typeof setTimeout> | undefined;

    function wordAt() {
      const point = caretPoint(doc, x, y);
      const position = point && sourcePosition(node, point.node, point.offset);
      const line = position && plainLines[position.line];
      const word = position && line !== undefined ? wordRange(line, position.character) : undefined;
      if (!position || !word) return undefined;
      // The caret snaps to the nearest text, so past a line's end it would
      // find the last word: the pointer must be on the word itself.
      const range = sourceRange(node, position.line, word.start, word.end);
      if (!range || ![...range.getClientRects()].some(r => x >= r.left && x <= r.right && y >= r.top && y <= r.bottom)) return undefined;
      return { position: { line: position.line, character: word.start }, text: line!.slice(word.start, word.end), range };
    }
    function paint(range?: Range) {
      const registry = view?.CSS?.highlights;
      node.classList.toggle('linking', Boolean(range));
      if (!registry || !view?.Highlight) return;
      if (range) registry.set('source-symbol', new view.Highlight(range));
      else registry.delete('source-symbol');
    }
    function endHover() {
      clearTimeout(timer);
      if (hovered) { hovered = ''; symbols?.hoverEnd(); }
    }
    function update() {
      const word = symbols && inside ? wordAt() : undefined;
      paint(word && linking && symbols?.linked ? word.range : undefined);
      const key = word && !buttons ? `${word.position.line}:${word.position.character}` : '';
      if (key === hovered) return;
      endHover();
      if (!word || !key) return;
      hovered = key;
      const at = { x, y };
      timer = setTimeout(() => symbols?.hover(word.position, at), HOVER_DELAY);
    }
    function move(event: PointerEvent) {
      x = event.clientX; y = event.clientY; inside = true; buttons = event.buttons;
      linking = linkModifier(event, isMac);
      update();
    }
    function leave() { inside = false; paint(); endHover(); }
    function down() { buttons = 1; endHover(); }
    function key(event: KeyboardEvent) {
      if (!inside || (event.key !== 'Meta' && event.key !== 'Control')) return;
      linking = event.type === 'keydown' && linkModifier(event, isMac);
      update();
    }
    function click(event: MouseEvent) {
      if (event.button !== 0 || !linkModifier(event, isMac) || !symbols) return;
      x = event.clientX; y = event.clientY;
      const word = wordAt();
      if (!word) return;
      event.preventDefault();
      symbols.definition(word.position, { x, y });
    }
    function menu(event: MouseEvent) {
      x = event.clientX; y = event.clientY;
      const word = symbols && wordAt();
      if (!word) return;
      event.preventDefault();
      endHover();
      symbols!.menu(word.position, word.text, { x, y });
    }
    // Only the view moving under the pointer: a scrolling hover card is not.
    function scroll(event: Event) {
      if (event.target instanceof Node && event.target.contains(node)) { paint(); endHover(); }
    }
    node.addEventListener('pointermove', move);
    node.addEventListener('pointerleave', leave);
    node.addEventListener('pointerdown', down);
    node.addEventListener('click', click);
    node.addEventListener('contextmenu', menu);
    doc.addEventListener('keydown', key);
    doc.addEventListener('keyup', key);
    doc.addEventListener('scroll', scroll, true);
    return {
      update(next: SourceSymbols | undefined) { symbols = next; if (!symbols) { paint(); endHover(); } },
      destroy() {
        paint(); clearTimeout(timer);
        node.removeEventListener('pointermove', move);
        node.removeEventListener('pointerleave', leave);
        node.removeEventListener('pointerdown', down);
        node.removeEventListener('click', click);
        node.removeEventListener('contextmenu', menu);
        doc.removeEventListener('keydown', key);
        doc.removeEventListener('keyup', key);
        doc.removeEventListener('scroll', scroll, true);
      },
    };
  }

  // Delegate text clicks without turning code into buttons or interfering
  // with native drag-to-select. Gutter buttons remain keyboard accessible.
  function lineClicks(node: HTMLElement) {
    let startX = 0;
    let startY = 0;
    function down(event: PointerEvent) { startX = event.clientX; startY = event.clientY; }
    function click(event: MouseEvent) {
      // Cmd/Ctrl+click is go to definition, not a line selection.
      if (event.button !== 0 || (symbols && linkModifier(event, isMac)) || Math.abs(event.clientX - startX) > 4 || Math.abs(event.clientY - startY) > 4) return;
      if (node.ownerDocument.getSelection()?.isCollapsed === false) return;
      // Gutter buttons select on their own; removed diff lines are code but have no line to select.
      const hit = event.target instanceof Element ? event.target.closest<HTMLElement>('.line, .ln') : null;
      if (!hit || !node.contains(hit)) onclearselection?.();
      else if (hit.dataset.sourceLine) onselectline?.(Number(hit.dataset.sourceLine), event.shiftKey);
    }
    node.addEventListener('pointerdown', down);
    node.addEventListener('click', click);
    return { destroy() { node.removeEventListener('pointerdown', down); node.removeEventListener('click', click); } };
  }

  const MARK_LABELS = {
    added: 'Added line',
    modified: 'Changed line',
    'deleted-above': 'Lines removed above',
    'deleted-below': 'Lines removed below',
  };
</script>

{#if activeDiagnostics.messages.length}
  <details class="diagnostic-messages" bind:this={messagesEl}>
    <summary>
      <span class="title">Source diagnostics</span>
      {#each severityCounts as [severity, count] (severity)}<span class="sev sev-{severity}">{count} {severityLabels[severity]}</span>{/each}
      {#if staleCount}<span class="tag stale">{staleCount} not current</span>{/if}
    </summary>
    {#if staleCount && recovery}
      <div class="recovery" role="group" aria-label="Recover stale positions">
        <span>{recovery.description}</span>
        <button type="button" disabled={recovery.disabled} onclick={recovery.run}>{recovery.label}</button>
      </div>
    {/if}
    <ul>
      {#each activeDiagnostics.messages as message, i}
        {@const d = message.finding.diagnostic}
        <li id={`${messageId}-${i}`} class:stale={staleKind(message.kind)}>
          <span class="sev sev-{d.severity}">{severityLabels[d.severity]}</span>
          <span class="loc">{locationLabel(d.range, message.kind)}</span>
          {#if longMessage(d.message)}
            <details class="msg long"><summary>{messagePreview(d.message)}</summary><div class="full">{d.message}</div></details>
          {:else}
            <span class="msg">{d.message}</span>
          {/if}
          <span class="src" title={message.finding.entries.map(e => `${e.diagnostic.source} (${toolName(e.providerId)}) · ${e.diagnostic.freshness} · ${e.diagnostic.basis.kind}`).join('; ')}>{[...new Set(message.finding.entries.map(e => e.diagnostic.source))].join(', ')}{d.code ? ` · ${d.code}` : ''}</span>
          {#if message.notice}<span class="notice" class:warn={staleKind(message.kind)}>{message.notice}</span>{/if}
        </li>
      {/each}
    </ul>
  </details>
{/if}
<div class="code source file-source" use:lineClicks use:symbolPointer={symbols} bind:this={codeEl} class:wrap={files.wrap} class:diff={showDiff} style:font-size={presentation ? `${presentationFontSize}px` : undefined} style:--digits={String(htmlLines?.length ?? plainLines.length).length}>
  {#snippet lineNumber(row: DiffRow)}
    {#if row.kind === 'removed'}
      <span aria-hidden="true" class="ln sign removed">−</span>
    {:else}
      <button type="button" class="ln line-select mark-{showDiff ? 'none' : row.mark ?? 'none'}"
        class:sign={showDiff && row.mark} class:added={showDiff && row.mark}
        class:selected={includesFileLine(selection, row.index + 1)}
        class:has-diagnostics={lineMessages.has(row.index)}
        aria-describedby={lineMessages.get(row.index)?.map(i => `${messageId}-${i}`).join(' ')}
        aria-label={`Select line ${row.index + 1}${lineMessages.has(row.index) ? '; diagnostics: ' + lineMessages.get(row.index)!.map(i => activeDiagnostics.messages[i].finding.diagnostic.severity).join(', ') + '; activate to show messages' : ''}`} aria-pressed={includesFileLine(selection, row.index + 1)}
        title={`${row.mark ? MARK_LABELS[row.mark] + ' · ' : ''}Click to select line; Shift-click to select range`}
        onclick={(event) => selectDiagnosticLine(row.index + 1, event.shiftKey)}
      >{showDiff && row.mark ? '+' : row.index + 1}</button>
    {/if}
  {/snippet}
  {#snippet lineText(row: DiffRow)}
    {#if row.kind === 'removed'}
      <span class="line removed">{row.text}</span>
    {:else if htmlLines}
      <!-- highlight.js escapes the file's text -->
      <span class="line" data-source-line={row.index + 1} class:selected={includesFileLine(selection, row.index + 1)} class:added={showDiff && row.mark}>{@html htmlLines[row.index]}</span>
    {:else}
      <span class="line" data-source-line={row.index + 1} class:selected={includesFileLine(selection, row.index + 1)} class:added={showDiff && row.mark}>{plainLines[row.index]}</span>
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
  /* Compact, bounded list above the source: never grows to push the code out of view. */
  .diagnostic-messages { flex-shrink: 0; padding: 4px 12px; font-family: var(--font-sans); font-size: 12px; line-height: 1.4; border-bottom: 1px solid var(--border-color); background: var(--bg-secondary); }
  .diagnostic-messages > summary { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; cursor: pointer; color: var(--text-secondary); }
  .diagnostic-messages > summary .title { color: var(--text-primary); font-weight: 600; }
  .diagnostic-messages ul { list-style: none; margin: 4px 0 2px; padding: 0; max-height: min(30vh, 220px); overflow: auto; }
  .diagnostic-messages li { display: grid; grid-template-columns: auto auto minmax(0, 1fr) auto; column-gap: 8px; align-items: baseline; padding: 2px 4px; border-radius: 4px; }
  .diagnostic-messages li:hover { background: var(--hover-bg); }
  .diagnostic-messages li.stale .msg { color: var(--text-secondary); }
  .sev { display: inline-block; padding: 0 6px; border-radius: 8px; font-size: 11px; font-weight: 600; line-height: 16px; white-space: nowrap; background: var(--bg-tertiary, rgba(127, 127, 127, 0.15)); }
  .sev-error { color: #f85149; background: rgba(248, 81, 73, 0.12); }
  .sev-warning { color: #d29922; background: rgba(210, 153, 34, 0.12); }
  .sev-information { color: #2f81f7; background: rgba(47, 129, 247, 0.12); }
  .sev-hint { color: #8250df; background: rgba(130, 80, 223, 0.12); }
  :global(:root:not([data-theme='dark'])) .sev-error { color: #cf222e; }
  :global(:root:not([data-theme='dark'])) .sev-warning { color: #9a6700; }
  .loc { font-family: var(--font-mono); font-size: 11px; color: var(--text-secondary); white-space: nowrap; }
  .msg { min-width: 0; overflow-wrap: anywhere; white-space: pre-wrap; }
  .msg.long > summary { cursor: pointer; overflow-wrap: anywhere; }
  .msg.long .full { max-height: 8em; overflow: auto; margin-top: 2px; padding: 4px 6px; border-left: 2px solid var(--border-color); white-space: pre-wrap; font-family: var(--font-mono); font-size: 11px; }
  .src { font-size: 11px; color: var(--text-muted, var(--text-secondary)); white-space: nowrap; max-width: 16ch; overflow: hidden; text-overflow: ellipsis; }
  .notice { grid-column: 2 / -1; font-size: 11px; color: var(--text-secondary); }
  .notice.warn { color: #d29922; }
  .tag { padding: 0 6px; border-radius: 8px; font-size: 11px; line-height: 16px; }
  .tag.stale { color: #d29922; border: 1px solid rgba(210, 153, 34, 0.5); }
  .recovery { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin: 4px 0; color: var(--text-secondary); font-size: 11px; }
  .recovery button { padding: 2px 8px; border: 1px solid var(--border-color); border-radius: 4px; font-size: 12px; color: var(--text-primary); cursor: pointer; }
  .recovery button:disabled { opacity: 0.5; cursor: default; }
  .ln.has-diagnostics { text-decoration: underline dotted; font-weight: bold; }
  :global(::highlight(source-symbol)) { text-decoration: underline; color: var(--accent-color); }
  .code:global(.linking) .line { cursor: pointer; }
  :global(::highlight(source-diagnostic-error)) { text-decoration: underline wavy #d1242f; }
  :global(::highlight(source-diagnostic-warning)) { text-decoration: underline wavy #9a6700; }
  :global(::highlight(source-diagnostic-information)) { text-decoration: underline dotted #2f81f7; }
  :global(::highlight(source-diagnostic-hint)) { text-decoration: underline dotted #8250df; }
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

  /* A button only fills the gutter when told to. Narrower, a short number's
     highlight stops short of the edge and it no longer aligns right. */
  .line-select {
    width: 100%;
    appearance: none;
    border: 0;
    border-radius: 0;
    border-right: 1px solid var(--border-color);
    font: inherit;
    line-height: inherit;
    cursor: pointer;
  }
  .line-select:hover,
  .line-select:focus-visible {
    color: var(--text-primary);
    outline: 1px solid var(--accent-color);
    outline-offset: -1px;
  }
  .line.selected,
  .ln.selected {
    background-image: linear-gradient(color-mix(in srgb, var(--accent-color) 14%, transparent), color-mix(in srgb, var(--accent-color) 14%, transparent));
  }
  /* Empty wrapped lines need height, not generated text (which can leak
     into the display and text selection). */
  .code.wrap .line {
    min-height: 1.5em;
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
