<script lang="ts">
  import { portForward } from '../../stores/portForward.svelte';
  import { connectionStore } from '../../stores/connection.svelte';
  import { FORWARD_STATUS_LABELS, validatePortForward, type PortForwardRule } from '../../portForward';
  import { hostLabel, wslDistro } from '../../hosts';
  import { copyToClipboard } from '../../portable';
  import { toast } from '../../stores/toast.svelte';
  import { api } from '../../api';

  let editing = $state<string | null>(null);
  let formOpen = $state(false);
  let remoteHost = $state('localhost');
  let remotePort = $state<number | undefined>();
  let localPort = $state<number | undefined>();
  let localEdited = $state(false);
  let autoStart = $state(false);
  let formError = $state('');
  let firstInput: HTMLInputElement | undefined = $state();
  let restoreFocus: HTMLElement | null = null;
  let wasOpen = false;
  let host = $derived(connectionStore.host ?? '');
  let connected = $derived(connectionStore.phase === 'connected' && Boolean(host) && !wslDistro(host));
  let rules = $derived(portForward.data.host === host ? portForward.data.rules : []);

  $effect(() => {
    if (portForward.open && !wasOpen) {
      restoreFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      formOpen = false;
      queueMicrotask(() => document.querySelector<HTMLButtonElement>('.forward-close')?.focus());
    } else if (!portForward.open && wasOpen) restoreFocus?.focus();
    wasOpen = portForward.open;
  });
  $effect(() => { void host; formOpen = false; });

  function edit(rule?: PortForwardRule) {
    editing = rule?.id ?? null;
    remoteHost = rule?.remoteHost ?? 'localhost';
    remotePort = rule?.remotePort;
    localPort = rule?.localPort;
    localEdited = Boolean(rule);
    autoStart = rule?.autoStart ?? false;
    formError = '';
    formOpen = true;
    queueMicrotask(() => firstInput?.focus());
  }

  async function save(start: boolean) {
    const request = { id: editing ?? undefined, remoteHost: remoteHost.trim(), remotePort: Number(remotePort), localPort: Number(localPort), autoStart };
    formError = validatePortForward(request) ?? '';
    if (formError) return;
    const previousIds = new Set(rules.map(r => r.id));
    if (!await portForward.action('save', request)) return;
    formOpen = false;
    if (start) {
      const rule = editing ? rules.find(r => r.id === editing) : rules.find(r => !previousIds.has(r.id));
      if (rule) await portForward.action('start', rule.id);
    }
  }

  async function copyAddress(port: number) {
    const address = `127.0.0.1:${port}`;
    const ok = await copyToClipboard(address);
    toast.show({ title: ok ? 'Address copied' : 'Could not copy address', body: address, tone: ok ? undefined : 'error' });
  }
</script>

<svelte:window onkeydown={(e) => {
  if (portForward.open && e.key === 'Escape') {
    e.preventDefault();
    e.stopPropagation();
    portForward.open = false;
  }
}} />

{#if portForward.open}
  <div class="backdrop" role="presentation">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="forward-title">
      <header>
        <div><h2 id="forward-title">Port Forwarding</h2><span class="host">{hostLabel(host)}</span></div>
        <button type="button" class="forward-close" aria-label="Close port forwarding" onclick={() => (portForward.open = false)}>×</button>
      </header>
      <div class="content">
        <p class="intro">Access a service on the remote host from this computer. Local ports listen only on <code>127.0.0.1</code>.</p>
        {#if !connected}<p class="notice">Reconnect to this SSH host to start forwarding.</p>{/if}
        {#if portForward.error}<div class="error" role="alert">{portForward.error} <button type="button" onclick={() => void portForward.refresh()}>Retry</button></div>{/if}
        {#if portForward.loading}<p>Loading forwarding rules…</p>
        {:else if rules.length === 0 && !formOpen}<div class="empty">No forwarding rules yet. Add a remote service to get started.</div>{/if}
        <div class="rules">
          {#each rules as rule (rule.id)}
            <article class="rule">
              <div class="rule-heading"><span class="status {rule.status}">● {FORWARD_STATUS_LABELS[rule.status]}</span>{#if rule.autoStart}<span class="auto">Auto-start</span>{/if}</div>
              <div class="route"><div><code>127.0.0.1:{rule.localPort}</code><small>This computer</small></div><span aria-hidden="true">→</span><div><code>{rule.remoteHost}:{rule.remotePort}</code><small>Remote host</small></div></div>
              {#if rule.error}<p class="error">{rule.error}</p>{/if}
              <div class="actions">
                <button type="button" disabled={rule.status !== 'active'} onclick={() => void api.openURL(`http://127.0.0.1:${rule.localPort}`)}>Open in browser</button>
                <button type="button" onclick={() => void copyAddress(rule.localPort)}>Copy address</button>
                <span class="spacer"></span>
                {#if rule.status === 'active' || rule.status === 'starting'}
                  <button type="button" disabled={portForward.busy} onclick={() => void portForward.action('stop', rule.id)}>{rule.status === 'starting' ? 'Cancel' : 'Stop'}</button>
                {:else}
                  <button type="button" disabled={portForward.busy || !connected} onclick={() => void portForward.action('start', rule.id)}>{rule.status === 'failed' ? 'Retry' : 'Start'}</button>
                {/if}
                <button type="button" disabled={portForward.busy || rule.status === 'active' || rule.status === 'starting'} onclick={() => edit(rule)}>Edit</button>
                <button type="button" disabled={portForward.busy} onclick={() => void portForward.action('remove', rule.id)}>Remove</button>
              </div>
            </article>
          {/each}
        </div>
        {#if formOpen}
          <form onsubmit={(e) => { e.preventDefault(); void save(connected && !editing); }}>
            <h3>{editing ? 'Edit forwarding rule' : 'New forwarding rule'}</h3>
            <div class="fields">
              <label>Remote hostname<input bind:this={firstInput} bind:value={remoteHost} placeholder="localhost" required spellcheck="false" /></label>
              <label>Remote port<input type="number" min="1" max="65535" required bind:value={remotePort} oninput={(e) => { if (!localEdited) localPort = e.currentTarget.valueAsNumber; }} placeholder="3000" /></label>
              <label>Local port<input type="number" min="1" max="65535" required bind:value={localPort} oninput={() => (localEdited = true)} placeholder="3000" /></label>
            </div>
            <label class="checkbox"><input type="checkbox" bind:checked={autoStart} /> Start automatically when connected</label>
            {#if formError}<p class="error" role="alert">{formError}</p>{/if}
            <div class="actions form-actions"><button type="button" disabled={portForward.busy} onclick={() => (formOpen = false)}>Cancel</button><button type="submit" class="primary" disabled={portForward.busy}>{portForward.busy ? 'Saving…' : editing ? 'Save' : connected ? 'Start forwarding' : 'Save rule'}</button></div>
          </form>
        {:else}<button type="button" class="primary add" disabled={portForward.busy || !host || Boolean(wslDistro(host))} onclick={() => edit()}>+ Add forwarding rule</button>{/if}
        <p class="hint">Active means the tunnel is running—not that the remote service is healthy. Forwarding stops when you disconnect.</p>
      </div>
    </div>
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; z-index: 1010; background: #0008; display: grid; place-items: center; padding: 20px; }
  .dialog { width: min(760px, 95vw); max-height: 88vh; display: flex; flex-direction: column; background: var(--bg-primary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 10px; box-shadow: 0 16px 40px #0005; }
  header { display: flex; align-items: center; justify-content: space-between; padding: 16px 20px; border-bottom: 1px solid var(--border-color); }
  header > div { display: flex; align-items: center; gap: 12px; }
  h2 { font-size: 1rem; margin: 0; } h3 { font-size: 0.9rem; margin: 0 0 14px; }
  .host, .intro, .hint, small, .auto { color: var(--text-muted); }
  .host { font-size: 0.8rem; } .content { padding: 20px; overflow-y: auto; }
  .intro { margin: 0 0 18px; font-size: 0.85rem; line-height: 1.5; }
  .empty { padding: 24px 12px; text-align: center; color: var(--text-muted); font-size: 0.85rem; }
  .rules { display: grid; gap: 12px; }
  .rule, form { padding: 16px; border: 1px solid var(--border-color); border-radius: 7px; background: var(--bg-secondary); }
  .rule-heading { display: flex; justify-content: space-between; font-size: 0.75rem; margin-bottom: 12px; }
  .status { color: var(--text-muted); } .status.active { color: var(--success-color, #16a34a); } .status.failed, .error { color: var(--danger, #ef4444); } .status.starting { color: var(--accent-color); }
  .route { display: flex; align-items: center; gap: 20px; flex-wrap: wrap; } .route > div { min-width: 0; } code { font-family: var(--font-mono); overflow-wrap: anywhere; } .route code { font-size: 0.9rem; } small { display: block; margin-top: 5px; font-size: 0.72rem; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; align-items: center; } .spacer { flex: 1; }
  button { cursor: pointer; border: 1px solid var(--border-color); border-radius: 4px; padding: 5px 9px; font-size: 0.75rem; color: var(--text-primary); background: var(--bg-primary); } button:hover:not(:disabled) { background: var(--hover-bg); } button:disabled { opacity: 0.45; cursor: default; } .forward-close { font-size: 1.3rem; border: none; padding: 0 4px; }
  .primary { background: var(--accent-color); color: white; border-color: var(--accent-color); } .primary:hover:not(:disabled) { filter: brightness(1.1); background: var(--accent-color); } .add, form { margin-top: 18px; }
  .fields { display: grid; grid-template-columns: 2fr 1fr 1fr; gap: 12px; } label { display: flex; flex-direction: column; gap: 6px; font-size: 0.75rem; } input:not([type='checkbox']) { width: 100%; min-width: 0; box-sizing: border-box; padding: 7px 8px; background: var(--bg-primary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 4px; } .checkbox { flex-direction: row; align-items: center; margin-top: 14px; } .form-actions { justify-content: flex-end; }
  .error, .notice, .hint { font-size: 0.78rem; line-height: 1.5; } .hint { margin-bottom: 0; margin-top: 20px; } .notice { padding: 10px; background: var(--bg-secondary); border-radius: 4px; }
  @media (max-width: 560px) { .fields { grid-template-columns: 1fr; } header > div { flex-wrap: wrap; } }
</style>
