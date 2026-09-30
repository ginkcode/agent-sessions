<script lang="ts">
  import { connectionStore } from '../../stores/connection.svelte';
  import HostEnvDialog from '../common/HostEnvDialog.svelte';
  import { filterHosts } from '../../link';

  let menuOpen = $state(false);
  let manualHost = $state('');
  // The alias being typed also narrows the SSH host list.
  let visibleHosts = $derived(filterHosts(connectionStore.hosts, manualHost));
  let envDialogOpen = $state(false);
  let envDialogHost = $state('');
  let menuEl = $state<HTMLElement | null>(null);

  let statusClass = $derived.by(() => {
    switch (connectionStore.phase) {
      case 'connected':
        return 'status-connected';
      case 'connecting':
        return 'status-connecting';
      case 'reconnecting':
        return 'status-reconnecting';
      case 'disconnected':
        return 'status-disconnected';
      default:
        return 'status-local';
    }
  });

  let statusTitle = $derived.by(() => {
    switch (connectionStore.phase) {
      case 'connected':
        return `Connected to ${connectionStore.currentHost}`;
      case 'connecting':
        return `Connecting to ${connectionStore.currentHost}…`;
      case 'reconnecting':
        return `Reconnecting to ${connectionStore.currentHost}…`;
      case 'disconnected':
        return `Disconnected from ${connectionStore.currentHost}`;
      default:
        return 'Target: Local Machine';
    }
  });

  function toggleMenu() {
    menuOpen = !menuOpen;
    if (menuOpen) {
      manualHost = '';
      void connectionStore.refreshHosts();
    }
  }

  function closeMenu() {
    menuOpen = false;
  }

  function handleSelectLocal() {
    closeMenu();
    if (connectionStore.phase !== 'local') {
      void connectionStore.disconnect();
    }
  }

  function handleSelectHost(name: string) {
    closeMenu();
    if (connectionStore.host !== name || connectionStore.phase !== 'connected') {
      void connectionStore.connect(name);
    }
  }

  function handleManualConnect(e: Event) {
    e.preventDefault();
    const alias = manualHost.trim();
    if (!alias) return;
    closeMenu();
    manualHost = '';
    void connectionStore.connect(alias);
  }

  function handleOpenEnv(e: Event, host: string) {
    e.stopPropagation();
    envDialogHost = host;
    envDialogOpen = true;
    closeMenu();
  }

  function handleWindowClick(e: MouseEvent) {
    if (menuOpen && menuEl && !menuEl.contains(e.target as Node)) {
      closeMenu();
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && menuOpen) {
      closeMenu();
    }
  }
</script>

<svelte:window onclick={handleWindowClick} onkeydown={handleKeydown} />

<div class="host-selector" bind:this={menuEl}>
  <button
    type="button"
    class="host-chip"
    title={statusTitle}
    aria-label={statusTitle}
    aria-expanded={menuOpen}
    aria-haspopup="true"
    onclick={toggleMenu}
  >
    <span class="status-dot {statusClass}"></span>
    <span class="host-name">{connectionStore.currentHost}</span>
    <span class="caret-icon" aria-hidden="true">▾</span>
  </button>

  {#if menuOpen}
    <div class="host-menu" role="menu">
      <div class="menu-section-header">Target Machine</div>

      <button
        type="button"
        class="menu-item"
        class:selected={connectionStore.phase === 'local'}
        role="menuitem"
        onclick={handleSelectLocal}
      >
        <span class="item-icon">💻</span>
        <span class="item-label">Local</span>
        {#if connectionStore.phase === 'local'}
          <span class="item-check">✓</span>
        {/if}
      </button>

      <div class="menu-divider"></div>

      <div class="menu-section-header with-action">
        <span>SSH Hosts</span>
        <button
          type="button"
          class="refresh-hosts-btn"
          title="Refresh SSH config hosts"
          onclick={() => void connectionStore.refreshHosts()}
        >
          ↻
        </button>
      </div>

      {#if connectionStore.loadingHosts}
        <div class="menu-note">Loading hosts…</div>
      {:else if connectionStore.hosts.length === 0}
        <div class="menu-note">No SSH config hosts found</div>
      {:else if visibleHosts.length === 0}
        <div class="menu-note">No SSH config hosts match "{manualHost.trim()}"</div>
      {:else}
        <div class="hosts-list">
          {#each visibleHosts as host}
            <div
              class="menu-item host-row"
              class:selected={connectionStore.isRemote && connectionStore.host === host.name}
              role="menuitem"
              tabindex="0"
              onclick={() => handleSelectHost(host.name)}
              onkeydown={(e) => {
                if (e.key === 'Enter') handleSelectHost(host.name);
              }}
            >
              <span class="item-icon">🌐</span>
              <div class="host-details">
                <span class="item-label">{host.name}</span>
                {#if host.hostName || host.user}
                  <span class="item-sub">
                    {host.user ? `${host.user}@` : ''}{host.hostName || ''}
                  </span>
                {/if}
              </div>
              <div class="host-row-actions">
                <button
                  type="button"
                  class="env-btn"
                  title="Configure environment overrides"
                  onclick={(e) => handleOpenEnv(e, host.name)}
                >
                  ⚙
                </button>
                {#if connectionStore.isRemote && connectionStore.host === host.name}
                  <span class="item-check">✓</span>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}

      <div class="menu-divider"></div>

      <form class="manual-connect-form" onsubmit={handleManualConnect}>
        <input
          type="text"
          class="manual-input"
          placeholder="Connect to alias…"
          bind:value={manualHost}
        />
        <button
          type="submit"
          class="manual-btn"
          disabled={!manualHost.trim()}
        >
          Go
        </button>
      </form>

      {#if connectionStore.isRemote || connectionStore.phase !== 'local'}
        <div class="menu-divider"></div>
        <button
          type="button"
          class="menu-item disconnect-item"
          role="menuitem"
          onclick={handleSelectLocal}
        >
          <span class="item-icon">🔌</span>
          <span class="item-label">Disconnect</span>
        </button>
      {/if}
    </div>
  {/if}
</div>

<HostEnvDialog
  open={envDialogOpen}
  host={envDialogHost}
  onClose={() => (envDialogOpen = false)}
/>

<style>
  .host-selector {
    position: relative;
    display: inline-flex;
    align-items: center;
  }

  .host-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 24px;
    padding: 0 8px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border-color);
    border-radius: 12px;
    font-size: 0.72rem;
    font-weight: 500;
    color: var(--text-primary);
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease;
  }

  .host-chip:hover {
    background: var(--bg-hover);
    border-color: var(--border-hover, var(--border-color));
  }

  .host-name {
    max-width: 110px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .status-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .status-local {
    background: var(--text-muted);
  }

  .status-connected {
    background: #22c55e;
    box-shadow: 0 0 6px rgba(34, 197, 94, 0.4);
  }

  .status-connecting,
  .status-reconnecting {
    background: #f59e0b;
    animation: pulse 1.2s infinite ease-in-out;
  }

  .status-disconnected {
    background: #ef4444;
  }

  @keyframes pulse {
    0%, 100% {
      opacity: 1;
      transform: scale(1);
    }
    50% {
      opacity: 0.4;
      transform: scale(0.85);
    }
  }

  .caret-icon {
    font-size: 0.65rem;
    opacity: 0.6;
  }

  .host-menu {
    position: absolute;
    bottom: calc(100% + 6px);
    left: 0;
    width: 250px;
    background: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
    padding: 6px 0;
    z-index: 105;
  }

  .menu-section-header {
    padding: 4px 10px 2px;
    font-size: 0.65rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }

  .menu-section-header.with-action {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .refresh-hosts-btn {
    background: transparent;
    border: none;
    color: var(--text-muted);
    font-size: 0.75rem;
    cursor: pointer;
    padding: 0 4px;
  }

  .refresh-hosts-btn:hover {
    color: var(--text-primary);
  }

  .menu-item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 6px 10px;
    background: transparent;
    border: none;
    text-align: left;
    font-size: 0.78rem;
    color: var(--text-primary);
    cursor: pointer;
  }

  .menu-item:hover {
    background: var(--bg-hover);
  }

  .menu-item.selected {
    background: rgba(var(--accent-rgb, 59, 130, 246), 0.12);
    font-weight: 600;
  }

  .item-icon {
    font-size: 0.85rem;
    flex-shrink: 0;
  }

  .item-label {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .item-check {
    color: var(--accent-color);
    font-size: 0.8rem;
    font-weight: bold;
    flex-shrink: 0;
  }

  .hosts-list {
    max-height: 180px;
    overflow-y: auto;
  }

  .host-row {
    justify-content: space-between;
  }

  .host-details {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
  }

  .item-sub {
    font-size: 0.68rem;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .host-row-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
  }

  .env-btn {
    background: transparent;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    font-size: 0.8rem;
    padding: 2px 4px;
    border-radius: 3px;
  }

  .env-btn:hover {
    background: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .menu-divider {
    height: 1px;
    background: var(--border-color);
    margin: 4px 0;
  }

  .menu-note {
    padding: 6px 10px;
    font-size: 0.72rem;
    color: var(--text-muted);
    font-style: italic;
  }

  .manual-connect-form {
    display: flex;
    gap: 4px;
    padding: 4px 8px;
  }

  .manual-input {
    flex: 1;
    padding: 4px 8px;
    font-size: 0.74rem;
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    outline: none;
  }

  .manual-input:focus {
    border-color: var(--accent-color);
  }

  .manual-btn {
    padding: 4px 8px;
    font-size: 0.74rem;
    background: var(--accent-color);
    color: white;
    border: none;
    border-radius: 4px;
    cursor: pointer;
  }

  .manual-btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .disconnect-item {
    color: #ef4444;
  }
</style>
