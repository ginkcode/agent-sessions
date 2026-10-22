<script lang="ts">
  import { codeDiagnosticCountsLabel, type CodeDiagnosticCounts } from '../../../codeDiagnosticCounts';
  let { counts, context = '' }: { counts?: CodeDiagnosticCounts; context?: string } = $props();
</script>

{#if counts && (counts.error || counts.warning)}
  <span class="diagnostic-badge" aria-label={codeDiagnosticCountsLabel(counts, context)} title={codeDiagnosticCountsLabel(counts, context)}>
    {#if counts.error}<span class="errors" aria-hidden="true">E {counts.error}</span>{/if}
    {#if counts.warning}<span class="warnings" aria-hidden="true">W {counts.warning}</span>{/if}
  </span>
{/if}

<style>
  .diagnostic-badge { display: inline-flex; flex-shrink: 0; gap: 4px; font-size: 0.7rem; font-variant-numeric: tabular-nums; }
  .errors { color: #f85149; }
  .warnings { color: #d29922; }
  :global(:root:not([data-theme='dark'])) .errors { color: #cf222e; }
  :global(:root:not([data-theme='dark'])) .warnings { color: #9a6700; }
</style>
