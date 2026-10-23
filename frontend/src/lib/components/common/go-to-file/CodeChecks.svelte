<script lang="ts">
  // Code check settings: approval, runs and optional setup. Mounted only inside the
  // disclosed bottom details; the compact status button owns the summary.
  import { codeDiagnostics as checks } from '../../../stores/codeDiagnostics.svelte';
  import { liveLanguages, providerMode, providerStatus, remoteHost, syncFailureReason, toolName } from '../../../codeCheckLabels';
  import { MANAGED_TOOL_FOR_PROVIDER, type ManagedToolName } from '../../../managedTools';
  import type { CodeDiagnosticsProvider } from '../../../codeDiagnostics';
  let { blocked = false }: { blocked?: boolean } = $props();
  let dismissed = $state(false);
  // A new attachment gets its own approval choice. Not now never calls the backend.
  $effect(() => { void checks.project; dismissed = false; });
  const labels = {
    detached: 'Code checks disabled', attaching: 'Discovering code check configurations…',
    unsupported: 'Code checks unsupported on this connection', resyncing: 'Reconciling code checks; retained results are stale',
    disconnected: 'Code checks disconnected; retained results are stale', error: 'Code checks unavailable; status uncertain', ready: 'Code checks',
  };
  function perform(work: () => Promise<unknown>) { void work().catch(() => {}); } // Store owns bounded, textual errors.
  let remote = $derived(remoteHost(checks.project?.hostId));
  let watching = $derived(checks.providers.some(p => p.availability === 'available' && p.coverage.externalChanges === 'watching'));
  let failedDocuments = $derived((checks.liveDocuments ?? []).filter(d => d.failure));
  // Live languages offered for approval, and those running once approved.
  let proposedLive = $derived(liveLanguages(checks.providers, ['available', 'approvalRequired']));
  let activeLive = $derived(liveLanguages(checks.providers));
  let where = $derived(remote ? 'the remote host' : 'this computer');
  // A status that is unavailable without a tool means this host offers no managed installs at all.
  let canOffer = $derived(!!checks.managed && !(checks.managed.state === 'unavailable' && !checks.managed.tool));
  /** The app-managed install a missing language server can get here, if any. */
  function offerFor(provider: CodeDiagnosticsProvider): ManagedToolName | undefined {
    const tool = MANAGED_TOOL_FOR_PROVIDER[provider.providerId];
    if (!tool || !canOffer || provider.availability !== 'missingTool') return undefined;
    // An app copy that exists (verified or not) is not offered again; "missingTool" means none.
    const install = checks.managedInstalls?.[tool];
    return !install || install === 'missingTool' ? tool : undefined;
  }
  /** The tool whose review card is open; its Install button is replaced by the card. */
  let reviewing = $derived(checks.managed?.state === 'proposed' && !checks.managedDismissed && !checks.autoPending ? checks.managed.proposal?.tool : undefined);
  // With nothing approvable, the approval card leads with the installs that would change that.
  let installFirst = $derived(checks.snapshot?.proposal?.fingerprint ? [] : checks.providers.flatMap(p => offerFor(p) ?? []).filter(t => t !== reviewing));
  /** Why a server is missing and how to get it, when no Install button says so; Svelte has no managed install. */
  function setupNote(provider: CodeDiagnosticsProvider): string {
    const tool = MANAGED_TOOL_FOR_PROVIDER[provider.providerId];
    if (tool) return provider.availability === 'missingTool' && checks.managed && !canOffer ? 'No app-managed install is offered on this host: install it yourself, then refresh.' : '';
    if (provider.providerId === 'svelte' || provider.providerId === 'svelte-check') return 'Setup not supported here: install it on the host yourself, then refresh.';
    return '';
  }
  /** Language servers this project uses, with their app-managed install where one exists. */
  let setupRows = $derived(checks.providers.flatMap(p => {
    const tool = MANAGED_TOOL_FOR_PROVIDER[p.providerId];
    if (!tool || !['available', 'missingTool'].includes(p.availability)) return [];
    const install = checks.managedInstalls[tool];
    const text = p.availability === 'available' ? (install === 'available' ? 'Ready · app copy installed' : 'Ready')
      : install === 'available' ? 'App copy installed · refresh check status'
      : install === 'unavailable' ? 'App copy failed verification and is not used'
      : 'Not found';
    return [{ providerId: p.providerId, text }];
  }));
  /** Brings a card that appears after a click into view in the scrolled panel. */
  function reveal(node: HTMLElement) { node.scrollIntoView({ block: 'nearest' }); }
  const installWork: Record<ManagedToolName, string> = {
    gopls: "Downloads gopls source and its dependencies through the Go module proxy, verified by the Go checksum database, and builds it with this host's Go. Go itself is never downloaded or upgraded.",
    ruff: 'Downloads one release archive and checks its SHA-256 before unpacking anything.',
    vtsls: 'Runs npm ci against a lockfile shipped with this app, with install scripts disabled; every package is checked against its recorded hash.',
    pyright: 'Runs npm ci against a lockfile shipped with this app, with install scripts disabled; uses the host Node.js or downloads an app-managed copy.',
  };
</script>

{#snippet installButton(tool: ManagedToolName)}
  <button type="button" title="Shows what would be downloaded; nothing is installed until you confirm" disabled={blocked || checks.managedPending || checks.managed?.state === 'installing'} onclick={() => perform(() => checks.managedAction('propose', tool))}>Install {tool}…</button>
{/snippet}

<section class="code-checks" aria-label="Code check settings" aria-busy={!!checks.pending || checks.status === 'attaching'}>
  {#if checks.status !== 'ready' || checks.pending}
    <p role="status" class="muted">{labels[checks.status]}{checks.pending ? ` · ${checks.pending} request pending` : ''}</p>
  {/if}
  {#if checks.error}
    <p class="wrapped">{checks.error.message}</p>
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
        <div class="card">
          <p><strong>Enable code checks for this project?</strong></p>
          {#if checks.snapshot.proposal && !checks.snapshot.proposal.tools.length}
            <p>Only built-in checks are available on this host. They run no program: the app reads saved project files and watches them for changes. No external checker was found, so type and vet errors are not reported.</p>
          {:else}
            <p>Code checks run the listed tools on this host with your account's permissions. Project configuration and plugins can execute code. Tools may read files and dependencies, access the network, and write caches or other files. This is not a sandbox or a read-only guarantee. Only approve projects and tools you trust.</p>
          {/if}
          <p class="muted">{checks.liveEnabled && proposedLive ? `Check Project runs on saved files. After approval, opened ${proposedLive} files are also sent unsaved to the listed language servers for live diagnostics; drafts are never saved.` : 'Only saved files are checked; drafts are never saved or checked automatically.'} Session approval lasts until this connection or app session ends. Always allow remembers this host, project and tool/config identity; relevant changes require approval again. Approval does not start a check.</p>
          {#if checks.snapshot.approval.state === 'revoked'}<p>Permission revoked. Pending and running checks are being canceled; previous findings have been cleared.</p>{/if}
          <ul class="rows">
            {#each checks.snapshot.proposal?.analyzers ?? [] as analyzer}
              <li><span class="name">{toolName(analyzer.providerId)}</span><span class="pill">built-in</span><span class="detail">{analyzer.description}</span></li>
            {/each}
            {#each checks.snapshot.proposal?.tools ?? [] as tool}
              <li><span class="name">{toolName(tool.providerId)}</span><span class="detail mono">{tool.providerId}: {tool.executable} · Configuration: {tool.configPaths.join(', ') || 'none discovered'}</span></li>
            {/each}
          </ul>
          {#if checks.approvalBlocker}<p role="status" class="muted">{checks.approvalBlocker}</p>{/if}
          {#if installFirst.length}<p>Install a language server on {where} first, or install one yourself and refresh. Installing asks for confirmation and approves nothing.</p>{/if}
          <div class="actions">
            {#each installFirst as tool (tool)}{@render installButton(tool)}{/each}
            <button type="button" disabled={blocked || !!checks.approvalBlocker || !checks.snapshot.proposal?.fingerprint} onclick={() => perform(() => checks.approve('session'))}>Enable for this session</button>
            <button type="button" disabled={blocked || !!checks.approvalBlocker || !checks.snapshot.proposal?.fingerprint} onclick={() => perform(() => checks.approve('persistent'))}>Always allow this project</button>
            <button type="button" disabled={!!checks.pending} onclick={() => (dismissed = true)}>Not now</button>
          </div>
        </div>
      {/if}
    {:else}
      <div class="actions">
        <span class="muted">{checks.revoking ? 'Revocation pending; checks disabled' : `Approved for ${checks.snapshot.approval.mode === 'persistent' ? 'this project persistently' : 'this session'}`}.</span>
        {#if checks.approved && checks.providers.some(p => p.availability === 'available' && p.coverage.kind === 'savedProject')}
          <button type="button" disabled={blocked || !checks.canRun} onclick={() => perform(() => checks.run())}>Check Project</button>
        {/if}
        {#if checks.approved && checks.running}
          <button type="button" disabled={blocked || !!checks.pending} onclick={() => perform(() => checks.cancel())}>Cancel check</button>
        {/if}
        <button type="button" disabled={blocked || checks.pending === 'revoke' || checks.revoking} onclick={() => perform(() => checks.revoke())}>Revoke approval</button>
      </div>
      <p class="muted">{watching ? 'Saved Go files are checked automatically when they change on disk.' : ''}{checks.liveEnabled && activeLive ? ` Opened ${activeLive} files are checked live as you type.` : ''} Check Project runs the on-demand checkers on saved files.</p>
    {/if}
  {/if}
  {#if checks.project && checks.status === 'ready'}
    {#if checks.managedNotice}<p role="status" class="wrapped">{checks.managedNotice}</p>{/if}
    {#if checks.managed?.message}<p role="status" class="wrapped">{checks.managed.message}</p>{/if}
    {#if reviewing && checks.managed?.proposal}
      {@const proposal = checks.managed.proposal}
      <div class="card" role="group" aria-label="Installation review" use:reveal>
        <p><strong>Install {proposal.tool} {proposal.version} on {where}?</strong></p>
        <p class="wrapped">Source: {proposal.source}</p>
        <p class="wrapped">Integrity: {proposal.integrity}</p>
        {#if proposal.note}<p class="wrapped">{proposal.note}</p>{/if}
        <p>{installWork[proposal.tool]} The download needs network access. Nothing is installed globally or into the project, and no project configuration is used.</p>
        <p class="wrapped">App-private destination: {proposal.destination}</p>
        <p>This offer expires: {proposal.expires}. Installation does not approve running it; code checks are rediscovered afterwards and ask for approval separately.</p>
        <p class="mono">Target host identity: {proposal.hostId}</p>
        <div class="actions">
          <button type="button" disabled={blocked || checks.managedPending} onclick={() => perform(() => checks.managedAction('install'))}>Install</button>
          <button type="button" disabled={checks.managedPending} onclick={() => perform(() => checks.managedAction('dismiss'))}>Not now</button>
        </div>
      </div>
    {:else if checks.managed?.state === 'installing' || checks.autoPending}
      <div class="card" role="group" aria-label="Installation progress">
        <p role="status">Downloading {checks.managed?.tool ?? checks.autoPending ?? 'the tool'} into this app's folder on {where}… Progress is coarse; no percentage is available. Disconnecting cancels it. Code checks are rediscovered when it finishes; running it still needs your approval.</p>
        {#if checks.autoInstall}<p class="muted">Missing language servers are downloaded automatically. Turn this off under Tools setup.</p>{/if}
        <button type="button" disabled={blocked || checks.managedPending || checks.managed?.state !== 'installing'} onclick={() => perform(() => checks.managedAction('cancel'))}>Cancel download</button>
      </div>
    {:else if checks.managed?.state === 'installed' && checks.managed.tool}
      <p role="status">{checks.managed.tool} is installed for this app on {where}. Code checks were rediscovered; approve them to use it. Setup started no server.</p>
    {:else if checks.managedDismissed}
      <p>Setup dismissed. No installation requested.</p>
    {/if}
  {/if}
  {#if checks.snapshot}
    {#if checks.providers.length === 0}<p class="muted">No supported checker discovered.</p>{/if}
    {#if checks.truncated}<p class="muted">Results were shortened or omitted to stay within limits; no complete-coverage claim.</p>{/if}
    {#if failedDocuments.length}
      <div class="card warn" role="group" aria-label="Live sync problems">
        <p><strong>Live sync stopped for {failedDocuments.length === 1 ? '1 file' : `${failedDocuments.length} files`}.</strong> Their findings are stale. Browsing and drafts still work.</p>
        <ul class="rows">
          {#each failedDocuments as document (document.documentId)}
            <li>
              <span class="name mono" title={document.path}>{document.path}</span>
              <span class="detail" role="status">Live document synchronization failed ({document.failure}): {syncFailureReason(document.failure ?? '')}</span>
              {#if document.canReopen}
                <button type="button" disabled={blocked || !!checks.pending} aria-label={`Reopen live synchronization for ${document.path}`} onclick={() => checks.reopenDocument(document.path, document.documentId)}>Reopen</button>
              {/if}
            </li>
          {/each}
        </ul>
        {#if failedDocuments.some(d => d.canReopen)}
          <p class="muted">Reopen sends the displayed text again with a fresh document identity under the current approval, at most twice. Nothing is saved. It is not a provider restart; same-URI results may remain uncertain until a new isolated session.</p>
        {/if}
      </div>
    {/if}
    {#if checks.providers.length}
      <h4>Checkers{checks.partial ? ' · partial coverage' : ''}{checks.truncated ? ' · truncated' : ''}</h4>
      <ul class="rows">
        {#each checks.providers as provider (provider.providerId)}
          {@const status = providerStatus(provider)}
          {@const note = setupNote(provider)}
          {@const offer = checks.status === 'ready' ? offerFor(provider) : undefined}
          <li>
            <span class="name">{toolName(provider.providerId)}</span>
            <span class="pill tone-{status.tone}">{status.text}</span>
            <span class="detail">{providerMode(provider)}{provider.freshness !== 'current' && provider.availability === 'available' ? ` · ${provider.freshness}` : ''}</span>
            {#if offer && offer !== reviewing}{@render installButton(offer)}{/if}
            {#if checks.approved && provider.phase === 'failed' && provider.availability === 'available' && provider.coverage.kind === 'savedProject'}
              <button type="button" disabled={blocked || !checks.canRetry(provider.providerId)} onclick={() => perform(() => checks.retry(provider.providerId))}>Retry {provider.providerId}</button>
            {/if}
            {#if provider.failure}<p class="sub wrapped">{provider.failure.message} (retained findings are not a successful check)</p>{/if}
            {#if provider.phase === 'canceled'}<p class="sub">Check canceled; retained findings may be stale.</p>{/if}
            {#if checks.approved && provider.phase === 'failed' && provider.availability === 'available' && provider.coverage.kind !== 'savedProject'}
              <p class="sub">No provider restart action is supported here. A failed session can be recreated by a subsequent document open; reopening in a surviving session may remain uncertain until a new isolated session. Refresh check status only reads status.</p>
            {/if}
            {#if provider.availability !== 'available' && note}<p class="sub">{note}</p>{/if}
            {#if provider.notice}<p class="sub wrapped">{provider.notice}</p>{/if}
          </li>
        {/each}
      </ul>
      <details>
        <summary>Coverage details</summary>
        <ul class="tech">
          {#each checks.providers as provider (provider.providerId)}
            <li class="mono">{provider.providerId}: {provider.availability} · {provider.phase} · {provider.freshness} · {provider.coverage.kind === 'savedProject' ? 'saved project files' : 'opened documents'} · roots: {provider.coverage.roots.join(', ') || '(project root)'}{provider.coverage.partial ? ' · partial' : ''}{provider.truncated ? ' · truncated' : ''}
              {#if provider.coverage.externalChanges === 'watching'} · watching saved project files on disk; updates after external changes (open drafts are not analyzed){/if}
              {#if provider.coverage.externalChanges === 'unwatched'} · external changes unwatched; unopened-file coverage stale — reopen, refresh or rerun to reconcile saved context (not continuous sync){/if}
            </li>
          {/each}
        </ul>
      </details>
    {/if}
  {/if}
  {#if checks.project && checks.status === 'ready'}
    <details aria-label="Language-server setup">
      <summary>Tools setup · language servers</summary>
      <p class="muted">Like Zed, each language server is looked up in the project first (node_modules/.bin, .venv), then on your login shell's PATH, then in this app's own install folder on {where}. {checks.liveEnabled ? 'Live diagnostics (experimental) use them for opened files only, after project approval: gopls for Go, vtsls for TypeScript/JavaScript and Ruff plus Pyright for Python (lint/syntax and type errors, respectively). Svelte live diagnostics are not enabled.' : 'Live diagnostics are not available on this connection.'} Like Zed, a missing server that this project's files need is downloaded into this app's folder automatically, at most once per app session; turn that off below to review each download first. Installing is separate from trusting this project: it never approves or starts a server. Revoking project approval also abandons a pending installation offer and cancels an owned installation.</p>
      <label class="toggle"><input type="checkbox" checked={checks.autoInstall} onchange={e => checks.setAutoInstall(e.currentTarget.checked)} /> Download missing language servers automatically</label>
      {#if setupRows.length}
        <ul class="rows">
          {#each setupRows as row (row.providerId)}
            <li><span class="name">{toolName(row.providerId)}</span><span class="detail">{row.text}</span></li>
          {/each}
        </ul>
      {/if}
      {#if !checks.managed && !checks.managedNotice}<p class="muted">Setup status not loaded yet.</p>{/if}
      <div class="actions">
        <button type="button" disabled={blocked || checks.managedPending} onclick={() => perform(() => checks.refreshManaged())}>Refresh setup status</button>
      </div>
      <p class="muted">Svelte setup is not offered. To install a server yourself instead, add it to the project or put it on your PATH: gopls with your Go toolchain, <span class="mono">npm install -g @vtsls/language-server@0.3.0</span> <span class="mono">uv tool install ruff==0.15.20</span> or <span class="mono">npm install -g pyright</span>. Then refresh check status.</p>
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
  .code-checks { padding: 6px 10px; font-size: 12px; line-height: 1.45; }
  p { margin: 4px 0; }
  h4 { margin: 8px 0 2px; font-size: 12px; font-weight: 600; color: var(--text-secondary); }
  .muted { color: var(--text-secondary); }
  .mono { overflow-wrap: anywhere; font-family: var(--font-mono); font-size: 11px; }
  .wrapped { overflow-wrap: anywhere; white-space: pre-wrap; }
  .card { border: 1px solid var(--border-color); border-radius: 6px; padding: 6px 8px; margin: 6px 0; background: var(--bg-secondary); }
  .card.warn { border-color: rgba(210, 153, 34, 0.5); }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin: 6px 0; }
  .rows { list-style: none; margin: 4px 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .rows li { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; padding: 3px 6px; border-radius: 4px; }
  .rows li:hover { background: var(--hover-bg); }
  .name { font-weight: 600; min-width: 12ch; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .detail { color: var(--text-secondary); min-width: 0; overflow-wrap: anywhere; }
  .rows li > button { margin-left: auto; }
  .sub { flex-basis: 100%; margin: 0; padding-left: 2px; font-size: 11px; color: var(--text-secondary); }
  .pill { padding: 0 6px; border-radius: 8px; font-size: 11px; line-height: 16px; white-space: nowrap; border: 1px solid var(--border-color); color: var(--text-secondary); }
  .tone-ok { color: #3fb950; border-color: rgba(63, 185, 80, 0.5); }
  .tone-busy { color: var(--accent-color); border-color: var(--accent-color); }
  .tone-bad { color: #f85149; border-color: rgba(248, 81, 73, 0.5); }
  .tone-warn { color: #d29922; border-color: rgba(210, 153, 34, 0.5); }
  :global(:root:not([data-theme='dark'])) .tone-ok { color: #1a7f37; }
  :global(:root:not([data-theme='dark'])) .tone-bad { color: #cf222e; }
  :global(:root:not([data-theme='dark'])) .tone-warn { color: #9a6700; }
  .tech { margin: 4px 0; padding-left: 16px; }
  .toggle { display: flex; align-items: center; gap: 6px; margin: 6px 0; }
  button { padding: 3px 8px; border: 1px solid var(--border-color); border-radius: 4px; cursor: pointer; font-size: 12px; }
  button:disabled { opacity: 0.5; cursor: default; }
  details { margin: 6px 0; }
  summary { cursor: pointer; color: var(--text-secondary); }
</style>
