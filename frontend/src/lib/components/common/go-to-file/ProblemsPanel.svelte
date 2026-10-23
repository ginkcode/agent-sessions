<script module lang="ts">
  const HEIGHT_KEY = 'agent-sessions:problems-height';
  // Room the header, status bar and some source lines keep above the details.
  const MIN_HEIGHT = 80, KEPT_ABOVE = 240;

  function clampDetailsHeight(height: number, windowHeight: number): number {
    return Math.round(Math.max(MIN_HEIGHT, Math.min(height, windowHeight - KEPT_ABOVE)));
  }

  function savedHeight(): number | undefined {
    try {
      if (typeof localStorage === 'undefined') return undefined;
      const saved = Number(localStorage.getItem(HEIGHT_KEY));
      return saved > 0 ? saved : undefined;
    } catch {
      return undefined;
    }
  }
</script>

<script lang="ts">
  import { tick, type Snippet } from 'svelte';
  import Splitter from '../Splitter.svelte';
  import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';
  import { diagnosticKey, type CodeDiagnostic } from '../../../codeDiagnostics';
  import { fileLevelFinding, orderedProblems, problemSeverities, problemKey, stepProblem } from '../../../problemNavigation';
  import { codeCheckSummary, toggleDisclosure } from '../../../codeCheckSummary';
  import { locationLabel, longMessage, messagePreview, severityLabels, toolName } from '../../../codeCheckLabels';
  // Every dialog opening mounts this collapsed: details and settings are never
  // remembered. Only the height the details were dragged to is.
  let { onnavigate, blocked = false, expanded = $bindable(false), settingsOpen = $bindable(false), settings }: {
    onnavigate: (diagnostic: CodeDiagnostic) => void; blocked?: boolean; expanded?: boolean; settingsOpen?: boolean;
    /** Approval, setup and run controls, rendered only once disclosed. */
    settings?: Snippet;
  } = $props();
  const uid = $props.id();
  const regionId = `code-check-details-${uid}`, settingsId = `code-check-settings-${uid}`;
  let enabled = $state<string[]>([...problemSeverities]);
  let collapsed = $state<string[]>([]);
  let selected = $state<string>();
  let region = $state<HTMLElement>();
  let details = $state<HTMLElement>();
  let height = $state(savedHeight());
  let statusButton: HTMLButtonElement;
  let summary = $derived(codeCheckSummary(checks));
  let findings = $derived(checks.findings ?? []);
  let rows = $derived(orderedProblems(findings, enabled));
  // Filters, navigation and the empty-result line only mean something once some
  // analysis is approved, or when retained findings are still listed.
  let listing = $derived(!!checks.analyzing || findings.length > 0);
  let paths = $derived([...new Set(rows.map(f => f.diagnostic.path))]);
  let visible = $derived(rows.filter(f => !collapsed.includes(f.diagnostic.path)));
  let ownedFocus = false;
  let section: HTMLElement;
  function toggleDetails() {
    ({ expanded, settingsOpen } = toggleDisclosure({ expanded, settingsOpen }, summary, findings.length));
  }
  /** The Ctrl+Shift+M shortcut. Hiding keeps focus in the panel, on the status. */
  export function toggle() {
    const inside = section?.contains(document.activeElement);
    toggleDetails();
    if (!expanded && inside) void tick().then(() => statusButton?.focus());
  }
  // Dragging the top edge up grows the details; the first drag starts from the shown height.
  function resize(delta: number): number {
    const before = height ?? details?.offsetHeight ?? 0;
    height = clampDetailsHeight(before - delta, window.innerHeight);
    try {
      localStorage.setItem(HEIGHT_KEY, String(height));
    } catch {
      // Storage disabled; the height lasts this opening.
    }
    return before - height;
  }
  function activate(d: CodeDiagnostic) { selected = diagnosticKey(d); if (!blocked) onnavigate(d); }
  async function step(direction: number) {
    const next = stepProblem(rows, selected, direction);
    if (!next) return;
    collapsed = collapsed.filter(p => p !== next.diagnostic.path);
    activate(next.diagnostic);
    await tick();
    Array.from(region?.querySelectorAll<HTMLButtonElement>('[data-problem-key]') ?? []).find(el => el.dataset.problemKey === selected)?.focus();
  }
  // A removed/filtered row gives focus to the surviving first row or the status button.
  $effect(() => {
    if (selected && !visible.some(f => diagnosticKey(f.diagnostic) === selected)) {
      selected = visible[0] ? diagnosticKey(visible[0].diagnostic) : undefined;
      if (ownedFocus) void tick().then(() => { (region?.querySelector<HTMLButtonElement>('[data-problem-key]') ?? statusButton)?.focus(); });
    }
  });
  const span = (d: CodeDiagnostic) => d.range ? `${d.range.start.line + 1}:${d.range.start.character + 1}–${d.range.end.line + 1}:${d.range.end.character + 1}` : 'File-level';
  const attribution = (f: (typeof findings)[number]) => f.entries.map(e => `${e.diagnostic.source} (${toolName(e.providerId)}) · ${e.diagnostic.freshness}`).sort().join(', ');
  const notCurrent = (f: (typeof findings)[number]) => f.entries.every(e => e.diagnostic.freshness !== 'current');
</script>

<section bind:this={section} class="problems" class:open={expanded} aria-label="Code checks">
  {#if expanded}<Splitter direction="vertical" ariaLabel="Resize code check details" onResize={resize} />{/if}
  <div class="status-bar">
    <button bind:this={statusButton} type="button" class="status tone-{summary.tone}" aria-expanded={expanded} aria-controls={regionId}
      title={summary.title} aria-busy={checks.status === 'attaching' || !!checks.running}
      onclick={toggleDetails}>
      <span class="chevron" class:open={expanded} aria-hidden="true">▸</span>
      <span class="dot" aria-hidden="true"></span>
      <span role="status" aria-live="polite">{summary.text}</span>
    </button>
    <button type="button" class="panel-toggle" class:active={expanded} aria-controls={regionId}
      title={expanded ? 'Hide problems (Ctrl+Shift+M)' : 'Show problems (Ctrl+Shift+M)'}
      aria-label={expanded ? 'Hide problems' : 'Show problems'} aria-pressed={expanded} aria-keyshortcuts="Control+Shift+M"
      onclick={toggleDetails}>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <!-- The terminal's hide chevron; it points up while there is something to show. -->
        <path d={expanded ? 'm6 9 6 6 6-6' : 'm6 15 6-6 6 6'} />
      </svg>
    </button>
  </div>
  <div id={regionId} bind:this={details} class="details" class:sized={height !== undefined} style:height={height ? `${height}px` : undefined}
    role="region" aria-label="Code check details" hidden={!expanded}>
    {#if expanded}
      <div class="toolbar">
        {#if listing}
          <div class="filters" role="group" aria-label="Severity filters">
            {#each problemSeverities as severity}
              <label class="filter"><input type="checkbox" checked={enabled.includes(severity)} onchange={() => enabled = enabled.includes(severity) ? enabled.filter(s => s !== severity) : [...enabled, severity]} /><span>{severityLabels[severity]}</span></label>
            {/each}
          </div>
          <div class="nav" role="group" aria-label="Problem navigation">
            <button type="button" disabled={!rows.length || blocked} onclick={() => step(-1)}>Previous problem</button>
            <button type="button" disabled={!rows.length || blocked} onclick={() => step(1)}>Next problem</button>
          </div>
        {/if}
        {#if settings}
          <button type="button" class="settings-toggle" class:attention={summary.needsSettings} aria-expanded={settingsOpen} aria-controls={settingsId}
            onclick={() => settingsOpen = !settingsOpen}>{summary.needsSettings ? 'Set up code checks' : 'Settings'}</button>
        {/if}
      </div>
      {#if settings}
        <div id={settingsId} class="settings" hidden={!settingsOpen}>
          {#if settingsOpen}{@render settings()}{/if}
        </div>
      {/if}
      {#if listing && (checks.partial || checks.truncated)}<p class="note">Partial coverage{checks.truncated ? ' · results truncated' : ''}; retained findings are not a complete check.</p>{/if}
      <!-- Native buttons own activation; this group only delegates arrow navigation. -->
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <div class="results" bind:this={region} role="group" aria-label="Problems by file"
        title="↑/↓ move between problems; next/previous wrap through the filtered set."
        onfocusin={() => ownedFocus = true} onfocusout={() => ownedFocus = false}
        onkeydown={(event) => { if (!blocked) problemKey(event, step); }}>
        {#if !rows.length && listing}<p class="note">{findings.length ? 'No problems match the severity filters.' : 'No problems reported.'}</p>{/if}
        {#each paths as path (path)}
          <button type="button" class="file" aria-expanded={!collapsed.includes(path)} onclick={() => collapsed = collapsed.includes(path) ? collapsed.filter(p => p !== path) : [...collapsed, path]}>{path}</button>
          {#if !collapsed.includes(path)}
            {#each rows.filter(f => f.diagnostic.path === path) as finding (diagnosticKey(finding.diagnostic))}
              {@const d = finding.diagnostic}
              <button type="button" class="problem" data-problem-key={diagnosticKey(d)} aria-pressed={selected === diagnosticKey(d)} disabled={blocked} onclick={() => activate(d)}
                title={`${span(d)} · ${attribution(finding)}`}>
                <span class="sev sev-{d.severity}">{severityLabels[d.severity]}</span>
                <span class="loc">{fileLevelFinding(d) ? 'File-level' : locationLabel(d.range)}</span>
                <span class="msg">{longMessage(d.message) ? messagePreview(d.message) : d.message}{d.code ? ` (${d.code})` : ''}</span>
                <span class="meta">{attribution(finding)}{notCurrent(finding) ? ' · not current' : ''}</span>
              </button>
            {/each}
          {/if}
        {/each}
      </div>
    {/if}
  </div>
</section>
<style>
  .problems { border-top: 1px solid var(--border-color); font-size: 12px; background: var(--bg-secondary); }
  /* The splitter draws the top edge while open. */
  .problems.open { border-top: none; }
  .status-bar { display: flex; align-items: center; padding: 0 8px; min-height: 24px; }
  .status { display: inline-flex; align-items: center; gap: 6px; padding: 2px 6px; border-radius: 4px; color: var(--text-secondary); white-space: nowrap; max-width: 100%; overflow: hidden; text-overflow: ellipsis; }
  .status:hover { background: var(--hover-bg); color: var(--text-primary); }
  .panel-toggle { display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 24px; height: 24px; margin-left: auto; padding: 0; color: var(--text-secondary); }
  .panel-toggle:hover { background: var(--hover-bg); color: var(--text-primary); }
  .panel-toggle.active { color: var(--accent-color); }
  .chevron { display: inline-block; font-size: 0.7rem; transition: transform 0.1s; }
  .chevron.open { transform: rotate(90deg); }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--text-muted); flex-shrink: 0; }
  .tone-error .dot { background: #f85149; } .tone-error { color: #f85149; }
  .tone-warning .dot { background: #d29922; }
  .tone-info .dot { background: var(--accent-color); }
  .tone-attention .dot { background: #d29922; }
  :global(:root:not([data-theme='dark'])) .tone-error, :global(:root:not([data-theme='dark'])) .sev-error { color: #cf222e; }
  /* Bounded so the source area keeps most of the dialog until dragged taller.
     The only scroll container: nothing inside scrolls on its own. */
  .details { border-top: 1px solid var(--border-color); padding: 4px 12px 6px; max-height: min(38vh, 360px); overflow: auto; background: var(--bg-primary); }
  .details.sized { max-height: calc(100vh - 240px); }
  .details[hidden] { display: none; }
  .toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 16px; margin: 2px 0 4px; }
  .filters, .nav { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; }
  .filter { display: inline-flex; align-items: center; gap: 5px; cursor: pointer; white-space: nowrap; }
  .filter input { margin: 0; }
  .nav button { border: 1px solid var(--border-color); padding: 2px 8px; }
  .settings-toggle { margin-left: auto; }
  .settings-toggle.attention { color: var(--accent-color); }
  .settings { border: 1px solid var(--border-color); border-radius: 4px; margin: 4px 0; }
  .settings[hidden] { display: none; }
  button { text-align: left; padding: 4px; color: var(--text-primary); cursor: pointer; border-radius: 4px; }
  .status { color: var(--text-secondary); }
  .tone-error.status { color: #f85149; }
  button:focus-visible { outline: 2px solid var(--accent-color); }
  button:disabled { opacity: .5; }
  .file { font-family: var(--font-mono); }
  .problem { display: grid; grid-template-columns: auto auto minmax(0, 1fr); column-gap: 8px; align-items: baseline; width: 100%; padding: 2px 4px 2px 18px; overflow-wrap: anywhere; }
  .problem .meta { grid-column: 3; font-size: 11px; }
  .sev { padding: 0 6px; border-radius: 8px; font-size: 11px; font-weight: 600; line-height: 16px; white-space: nowrap; background: rgba(127, 127, 127, 0.15); }
  .sev-error { background: rgba(248, 81, 73, 0.12); } .sev-warning { background: rgba(210, 153, 34, 0.12); }
  .sev-information { color: #2f81f7; background: rgba(47, 129, 247, 0.12); } .sev-hint { color: #8250df; background: rgba(130, 80, 223, 0.12); }
  .loc { font-family: var(--font-mono); font-size: 11px; color: var(--text-secondary); white-space: nowrap; }
  .problem[aria-pressed='true'] { background: var(--active-bg); }
  .sev-error { color: #f85149; } .sev-warning { color: #d29922; }
  .problem .meta, .note { color: var(--text-secondary); }
  .note { margin: 4px 0; }
</style>
