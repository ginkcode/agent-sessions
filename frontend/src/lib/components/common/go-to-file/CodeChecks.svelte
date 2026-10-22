<script lang="ts">
  // Code check settings: approval, runs and optional setup. Mounted only inside the
  // disclosed bottom details; the compact status button owns the summary.
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
  let setupOpen = $derived(checks.managed?.state === 'proposed' || checks.managed?.state === 'installing');
</script>

<section class="code-checks" aria-label="Code check settings" aria-busy={!!checks.pending || checks.status === 'attaching'}>
  {#if checks.status !== 'ready' || checks.pending}
    <p role="status">{labels[checks.status]}{checks.pending ? ` · ${checks.pending} request pending` : ''}</p>
  {/if}
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
  {#if checks.status === 'ready' && checks.snapshot}
    {#if checks.snapshot.approval.state !== 'approved'}
      {#if dismissed}
        <p>Code checks disabled for now. No tools have been started.</p>
        <button type="button" onclick={() => (dismissed = false)}>Enable code checks for this project</button>
      {:else}
        <p><strong>Enable code checks for this project?</strong></p>
        <p>Code checks run the listed tools on this host with your account's permissions. Project configuration and plugins can execute code. Tools may read files and dependencies, access the network, and write caches or other files. This is not a sandbox or a read-only guarantee. Only approve projects and tools you trust.</p>
        <p>{checks.liveEnabled ? 'Check Project runs on saved files. After approval, opened Go files are also sent unsaved to the listed gopls for live diagnostics; drafts are never saved.' : 'Only saved files are checked; drafts are never saved or checked automatically.'} Session approval lasts until this connection or app session ends. Always allow remembers this host, project and tool/config identity; relevant changes require approval again. Approval does not start a check.</p>
        {#if checks.snapshot.approval.state === 'revoked'}<p>Permission revoked. Pending and running checks are being canceled; previous findings have been cleared.</p>{/if}
        {#each checks.snapshot.proposal?.tools ?? [] as tool}
          <p class="mono">{tool.providerId}: {tool.executable} · Configuration: {tool.configPaths.join(', ') || 'none discovered'}</p>
        {/each}
        <div class="actions">
          <button type="button" disabled={blocked || !!checks.pending || !checks.snapshot.proposal?.fingerprint} onclick={() => perform(() => checks.approve('session'))}>Enable for this session</button>
          <button type="button" disabled={blocked || !!checks.pending || !checks.snapshot.proposal?.fingerprint} onclick={() => perform(() => checks.approve('persistent'))}>Always allow this project</button>
          <button type="button" disabled={!!checks.pending} onclick={() => (dismissed = true)}>Not now</button>
        </div>
      {/if}
    {:else}
      <div class="actions">
        <span>{checks.revoking ? 'Revocation pending; checks disabled' : `Approved for ${checks.snapshot.approval.mode === 'persistent' ? 'this project persistently' : 'this session'}`}. Checks run only when requested.</span>
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
    {#if checks.providers.length}
      <details>
        <summary>Checkers ({checks.providers.length}){checks.partial ? ' · partial coverage' : ''}{checks.truncated ? ' · truncated' : ''}</summary>
        <ul>
          {#each checks.providers as provider (provider.providerId)}
            <li>
              <span>{provider.providerId}: {availability[provider.availability]} · {provider.phase} · {provider.freshness} · {provider.coverage.kind === 'savedProject' ? 'saved project files' : 'opened documents'} · roots: {provider.coverage.roots.join(', ') || '(project root)'}{provider.coverage.partial ? ' · partial' : ''}{provider.truncated ? ' · truncated' : ''}</span>
              {#if provider.coverage.externalChanges === 'watching'}<span> · watching saved project files on disk; updates after external changes (open drafts are not analyzed)</span>{/if}
              {#if provider.coverage.externalChanges === 'unwatched'}<span> · external changes unwatched; unopened-file coverage stale — reopen, refresh or rerun to reconcile saved context (not continuous sync)</span>{/if}
              {#if provider.notice}<p>{provider.notice}</p>{/if}
            </li>
          {/each}
        </ul>
      </details>
    {/if}
    {#each checks.providers as provider (provider.providerId)}
      {#if provider.failure}<p>{provider.providerId}: {provider.failure.message} (retained findings are not a successful check)</p>{/if}
      {#if provider.phase === 'canceled'}<p>{provider.providerId}: Check canceled; retained findings may be stale.</p>{/if}
      {#if checks.approved && provider.phase === 'failed' && provider.availability === 'available'}
        {#if provider.coverage.kind === 'savedProject'}
          <button type="button" disabled={blocked || !checks.canRetry(provider.providerId)} onclick={() => perform(() => checks.retry(provider.providerId))}>Retry {provider.providerId}</button>
        {:else}
          <p>No provider restart action is supported here. A failed session can be recreated by a subsequent document open; reopening in a surviving session may remain uncertain until a new isolated session. Refresh check status only reads status.</p>
        {/if}
      {/if}
    {/each}
  {/if}
  {#if checks.project && checks.status === 'ready'}
    <details aria-label="Go language-server setup" open={setupOpen}>
      <summary>Go language server (optional setup)</summary>
      <p>Installing a tool is separate from trusting this project's execution. Neither approval installs or starts a server automatically. {checks.liveEnabled ? 'Live diagnostics (experimental) use an installed, supported gopls on this host for opened Go files only, after project approval. A missing or unsupported gopls is shown as its own status; TypeScript and Svelte live diagnostics are not enabled.' : 'Live diagnostics are not available on this connection.'} Revoking project approval also abandons pending setup consent and cancels an owned installation under the backend's conservative policy.</p>
      {#if checks.managedNotice}<p role="status">{checks.managedNotice}</p>{/if}
      {#if checks.managed?.message}<p role="status">{checks.managed.message}</p>{/if}
      {#if checks.managed?.state === 'proposed' && checks.managed.proposal && !checks.managedDismissed}
        {@const proposal = checks.managed.proposal}
        <p>Install {proposal.tool} {proposal.version} on this host?</p>
        <p>Source: {proposal.source} · Integrity: {proposal.integrity}</p>
        <p>App-private destination: {proposal.destination}</p>
        <p>Requires an explicitly reviewed installed Go &gt;=1.27 tree provisioned by the backend operator. No automatic Go download or upgrade. Installation downloads pinned source and dependencies over the network, builds with Go, and writes private staging/build/module caches and the app-private tool directory. It does not use project configuration or install globally.</p>
        <p>Approval expires: {proposal.expires}. Installation does not approve project execution. Reattach and review execution approval separately after installation.</p>
        <p class="mono">Target host identity: {proposal.hostId}</p>
        <button type="button" disabled={blocked || checks.managedPending} onclick={() => perform(() => checks.managedAction('install'))}>Install</button>
        <button type="button" disabled={checks.managedPending} onclick={() => perform(() => checks.managedAction('dismiss'))}>Not now</button>
      {:else if checks.managed?.state === 'installing'}
        <p role="status">Installing gopls on this host… Download/build progress is coarse; no percentage is available. Hiding this panel stops polling, not the approved installation. Disconnecting cancels backend-owned work.</p>
        <button type="button" disabled={blocked || checks.managedPending} onclick={() => perform(() => checks.managedAction('cancel'))}>Cancel installation</button>
      {:else if checks.managed?.state === 'installed' || checks.managed?.state === 'available'}
        <p>Managed gopls is installed. Reopen project diagnostics to rediscover tools and review separate execution approval. No server has been started by setup.</p>
      {:else if checks.managed?.state === 'missingTool' || checks.managed?.state === 'failed' || checks.managed?.state === 'canceled' || checks.managed?.state === 'idle'}
        {#if checks.managedDismissed}
          <p>Setup dismissed. No installation requested.</p>
        {:else}
          <button type="button" disabled={blocked || checks.managedPending} onclick={() => perform(() => checks.managedAction('propose'))}>Review gopls installation</button>
        {/if}
      {:else if !checks.managedNotice}
        <p>Managed setup unavailable. Backend operator review and provisioning of Go &gt;=1.27 is required. Remote setup requires authenticated host identity; older backends may not support setup.</p>
      {/if}
      <button type="button" disabled={blocked || checks.managedPending} onclick={() => perform(() => checks.refreshManaged())}>Refresh setup status</button>
    </details>
  {/if}
  {#if checks.project}
    <details>
      <summary>Technical details</summary>
      <p class="mono">Host: {checks.project.hostId} · Root: {checks.project.root}</p>
      {#if checks.snapshot?.proposal?.fingerprint}<p class="mono">Proposal identity: {checks.snapshot.proposal.fingerprint}</p>{/if}
    </details>
  {/if}
</section>

<style>
  .code-checks { padding: 4px 10px; font-size: 0.78rem; }
  p { margin: 4px 0; }
  .mono { overflow-wrap: anywhere; font-family: var(--font-mono); }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin: 4px 0; }
  button { padding: 3px 8px; border: 1px solid var(--border-color); border-radius: 4px; cursor: pointer; }
  button:disabled { opacity: 0.5; cursor: default; }
  details { margin: 4px 0; }
  summary { cursor: pointer; color: var(--text-secondary); }
  ul { padding-left: 18px; margin: 4px 0; }
</style>
