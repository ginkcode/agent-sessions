<script lang="ts">
  import { tick } from 'svelte';
  import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';
  import { diagnosticKey, type CodeDiagnostic } from '../../../codeDiagnostics';
  import { orderedProblems, problemSeverities, problemKey, stepProblem } from '../../../problemNavigation';
  let { onnavigate, blocked = false, expanded = $bindable(false) }: { onnavigate: (diagnostic: CodeDiagnostic) => void; blocked?: boolean; expanded?: boolean } = $props();
  let enabled = $state<string[]>([...problemSeverities]);
  let collapsed = $state<string[]>([]);
  let selected = $state<string>();
  let region = $state<HTMLElement>();
  let toggle: HTMLButtonElement;
  let rows = $derived(orderedProblems(checks.findings, enabled));
  let paths = $derived([...new Set(rows.map(f => f.diagnostic.path))]);
  let visible = $derived(rows.filter(f => !collapsed.includes(f.diagnostic.path)));
  let ownedFocus = false;
  function activate(d: CodeDiagnostic) { selected = diagnosticKey(d); if (!blocked) onnavigate(d); }
  async function step(direction: number) {
    const next = stepProblem(rows, selected, direction);
    if (!next) return;
    collapsed = collapsed.filter(p => p !== next.diagnostic.path);
    activate(next.diagnostic);
    await tick();
    Array.from(region?.querySelectorAll<HTMLButtonElement>('[data-problem-key]') ?? []).find(el => el.dataset.problemKey === selected)?.focus();
  }
  // A removed/filtered row gives focus to the surviving first row or panel toggle.
  $effect(() => {
    if (selected && !visible.some(f => diagnosticKey(f.diagnostic) === selected)) {
      selected = visible[0] ? diagnosticKey(visible[0].diagnostic) : undefined;
      if (ownedFocus) void tick().then(() => { (region?.querySelector<HTMLButtonElement>('[data-problem-key]') ?? toggle)?.focus(); });
    }
  });
</script>

<section class="problems" aria-label="Problems">
  <button bind:this={toggle} type="button" aria-expanded={expanded} onclick={() => expanded = !expanded}>Problems ({checks.findings.length})</button>
  {#if expanded}
    <div class="filters">
      {#each problemSeverities as severity}
        <label><input type="checkbox" checked={enabled.includes(severity)} onchange={() => enabled = enabled.includes(severity) ? enabled.filter(s => s !== severity) : [...enabled, severity]} />{severity}</label>
      {/each}
      <button type="button" disabled={!rows.length || blocked} onclick={() => step(-1)}>Previous problem</button>
      <button type="button" disabled={!rows.length || blocked} onclick={() => step(1)}>Next problem</button>
    </div>
    <p>Saved-file locations · Next/previous wrap through the filtered set, including collapsed groups. ↑/↓ in the list navigate.</p>
    <!-- Native buttons own activation; this group only delegates arrow navigation. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="results" bind:this={region} role="group" aria-label="Problems by file"
      onfocusin={() => ownedFocus = true} onfocusout={() => ownedFocus = false}
      onkeydown={(event) => { if (!blocked) problemKey(event, step); }}>
      {#if !rows.length}<p>{checks.findings.length ? 'No problems match the severity filters.' : 'No reported problems.'}</p>{/if}
      {#each paths as path (path)}
        <button type="button" aria-expanded={!collapsed.includes(path)} onclick={() => collapsed = collapsed.includes(path) ? collapsed.filter(p => p !== path) : [...collapsed, path]}>{path}</button>
        {#if !collapsed.includes(path)}
          {#each rows.filter(f => f.diagnostic.path === path) as finding (diagnosticKey(finding.diagnostic))}
            {@const d = finding.diagnostic}
            <button type="button" class="problem" data-problem-key={diagnosticKey(d)} aria-pressed={selected === diagnosticKey(d)} disabled={blocked} onclick={() => activate(d)}>
              <strong>{d.severity}</strong> {d.message} {d.code ?? ''}
              <span>{d.range ? `${d.range.start.line + 1}:${d.range.start.character + 1}–${d.range.end.line + 1}:${d.range.end.character + 1}` : 'File-level'} · {finding.entries.map(e => `${e.diagnostic.source} (${e.providerId}) · ${e.diagnostic.freshness}`).sort().join(', ')}</span>
            </button>
          {/each}
        {/if}
      {/each}
    </div>
  {/if}
</section>
<style>
  .problems { border-top: 1px solid var(--border-color); padding: 6px 12px; font-size: 12px; }
  .filters { display: flex; flex-wrap: wrap; gap: 12px; margin: 6px 0; }
  .results { overflow: auto; max-height: 220px; }
  button { text-align: left; padding: 4px; color: var(--text-primary); cursor: pointer; }
  button:focus-visible { outline: 2px solid var(--accent-color); }
  button:disabled { opacity: .5; }
  .problem { display: block; width: 100%; padding-left: 18px; overflow-wrap: anywhere; }
  .problem[aria-pressed='true'] { background: var(--active-bg); }
  span, p { color: var(--text-secondary); } span { display: block; }
</style>
