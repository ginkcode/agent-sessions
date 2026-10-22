<script lang="ts">
  import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';
  let { blocked = false }: { blocked?: boolean } = $props();
  let dismissed = $state(false);
  // A new attachment gets its own approval choice. Not now never calls the backend.
  $effect(() => { void checks.project; dismissed = false; });
  const labels = {
    detached: 'Code checks disabled', attaching: 'Discovering code check configurations…',
    unsupported: 'Code checks unsupported on this connection', resyncing: 'Reconciling code checks; retained results are stale',
    disconnected: 'Code checks disconnected; retained results are stale', error: 'Code checks unavailable; status uncertain', ready: 'Code checks',
  };
  const availability = { approvalRequired: 'Approval required', available: 'Available', missingTool: 'Tool missing', unsupported: 'Unsupported', disabled: 'Disabled' };
  function perform(work: () => Promise<unknown>) { void work().catch(() => {}); } // Store owns bounded, textual errors.
</script>

<section class="code-checks" aria-label="Project code checks" aria-busy={!!checks.pending || checks.status === 'attaching'}>
  <p role="status" aria-live="polite">{labels[checks.status]}{checks.pending ? ` · ${checks.pending} request pending` : ''}</p>
  {#if checks.error}
    <p>{checks.error.message}</p>
    {#if checks.snapshot && checks.status === 'error'}
      <button type="button" disabled={blocked || !!checks.pending} onclick={() => checks.refresh()}>Refresh check status</button>
      {#if checks.revoking}
        <p>Revocation is uncertain. Retry revoking before reconnecting; remembered permission may remain.</p>
        <button type="button" disabled={blocked || !!checks.pending} onclick={() => perform(() => checks.revoke())}>Retry revocation</button>
      {/if}
    {/if}
  {/if}
  {#if checks.project}
    <p class="scope">Host: {checks.project.hostId} · Root: {checks.project.root}</p>
  {/if}
  {#if checks.status === 'ready' && checks.snapshot}
    {#if checks.snapshot.approval.state !== 'approved'}
      {#if dismissed}
        <p>Code checks disabled for now. No tools have been started.</p>
        <button type="button" onclick={() => (dismissed = false)}>Enable code checks for this project</button>
      {:else}
        <p>Enable code checks for this project</p>
        <p>Code checks run the listed tools on this host with your account's permissions. Project configuration and plugins can execute code. Tools may read files and dependencies, access the network, and write caches or other files. This is not a sandbox or a read-only guarantee. Only approve projects and tools you trust.</p>
        <p>Only saved files are checked; drafts are never saved or checked automatically. Session approval lasts until this connection or app session ends. Always allow remembers this host, project and tool/config identity; relevant changes require approval again. Approval does not start a check.</p>
        <p>Proposal identity: {checks.snapshot.proposal?.fingerprint ?? 'unavailable'}</p>
        {#if checks.snapshot.approval.state === 'revoked'}<p>Permission revoked. Pending and running checks are being canceled; previous findings have been cleared.</p>{/if}
        {#each checks.snapshot.proposal?.tools ?? [] as tool}
          <p>{tool.providerId}: {tool.executable} · Configuration: {tool.configPaths.join(', ') || 'none discovered'}</p>
        {/each}
        <div class="actions">
          <button type="button" disabled={blocked || !!checks.pending || !checks.snapshot.proposal?.fingerprint} onclick={() => perform(() => checks.approve('session'))}>Enable for this session</button>
          <button type="button" disabled={blocked || !!checks.pending || !checks.snapshot.proposal?.fingerprint} onclick={() => perform(() => checks.approve('persistent'))}>Always allow this project</button>
          <button type="button" disabled={!!checks.pending} onclick={() => (dismissed = true)}>Not now</button>
        </div>
      {/if}
    {:else}
      <p>{checks.revoking ? 'Revocation pending; checks disabled' : `Approved for ${checks.snapshot.approval.mode === 'persistent' ? 'this project persistently' : 'this session'}`}. Checks run only when requested.</p>
      <div class="actions">
        {#if checks.approved && checks.providers.some(p => p.availability === 'available' && p.coverage.kind === 'savedProject')}
          <button type="button" disabled={blocked || !checks.canRun} onclick={() => perform(() => checks.run())}>Check Project</button>
        {/if}
        {#if checks.approved && checks.running}
          <button type="button" disabled={blocked || !!checks.pending} onclick={() => perform(() => checks.cancel())}>Cancel check</button>
        {/if}
        <button type="button" disabled={blocked || checks.pending === 'revoke' || checks.revoking} onclick={() => perform(() => checks.revoke())}>Revoke approval</button>
      </div>
    {/if}
  {/if}
  {#if checks.snapshot}
    <p>{checks.counts.error} errors · {checks.counts.warning} warnings · reported findings{checks.partial ? ' · partial coverage' : ''}{checks.truncated ? ' · truncated' : ''}</p>
    {#if checks.counts.error === 0 && checks.counts.warning === 0}<p>No reported problems — this does not establish complete coverage.</p>{/if}
    {#if checks.providers.length === 0}<p>No supported checker discovered.</p>{/if}
    {#if checks.truncated}<p>Results were shortened or omitted to stay within limits; no complete-coverage claim.</p>{/if}
    {#each checks.liveDocuments ?? [] as document (document.documentId)}
      {#if document.failure}
        <p role="status">{document.path}: Live document synchronization failed ({document.failure}); retained findings are stale. Browsing and drafts remain available.</p>
        {#if document.canReopen}
          <button type="button" disabled={blocked || !!checks.pending} onclick={() => checks.reopenDocument(document.path, document.documentId)}>Reopen live synchronization for {document.path}</button>
          <p>Resends displayed text with a fresh document identity under current approval, at most twice. This is not a provider restart; same-URI results may remain uncertain until a new isolated session.</p>
        {/if}
      {/if}
    {/each}
    <ul>
      {#each checks.providers as provider (provider.providerId)}
        <li>
          <span>{provider.providerId}: {availability[provider.availability]} · {provider.phase} · {provider.freshness} · {provider.coverage.kind === 'savedProject' ? 'saved project files' : 'opened documents'} · roots: {provider.coverage.roots.join(', ') || '(project root)'}{provider.coverage.partial ? ' · partial' : ''}{provider.truncated ? ' · truncated' : ''}</span>
          {#if provider.coverage.externalChanges === 'unwatched'}<span> · external changes unwatched; unopened-file coverage stale — reopen, refresh or rerun to reconcile saved context (not continuous sync)</span>{/if}
          {#if provider.notice}<p>{provider.notice}</p>{/if}
          {#if provider.failure}<p>{provider.failure.message} (retained findings are not a successful check)</p>{/if}
          {#if provider.phase === 'canceled'}<p>Check canceled; retained findings may be stale.</p>{/if}
          {#if checks.approved && provider.phase === 'failed' && provider.availability === 'available'}
            {#if provider.coverage.kind === 'savedProject'}
              <button type="button" disabled={blocked || !checks.canRetry(provider.providerId)} onclick={() => perform(() => checks.retry(provider.providerId))}>Retry {provider.providerId}</button>
            {:else}
              <p>No provider restart action is supported here. A failed session can be recreated by a subsequent document open; reopening in a surviving session may remain uncertain until a new isolated session. Refresh check status only reads status.</p>
            {/if}
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  .code-checks { padding: 6px 18px; border-bottom: 1px solid var(--border-color); font-size: 0.78rem; max-height: 28vh; overflow: auto; }
  p { margin: 4px 0; }
  .scope { overflow-wrap: anywhere; font-family: var(--font-mono); }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }
  button { padding: 4px 8px; border: 1px solid var(--border-color); border-radius: 4px; cursor: pointer; }
  button:disabled { opacity: 0.5; cursor: default; }
  ul { padding-left: 18px; margin: 4px 0; }
</style>
