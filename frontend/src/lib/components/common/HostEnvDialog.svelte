<script lang="ts">
  import {
    connectionStore,
    getSavedHostEnv,
    saveHostEnv,
  } from '../../stores/connection.svelte';
  import { api } from '../../api';

  interface Props {
    open: boolean;
    host: string;
    onClose: () => void;
  }

  let { open = false, host = '', onClose }: Props = $props();

  let envMap = $state<Record<string, string>>({});
  let customKey = $state('');
  let customVal = $state('');

  const commonKeys = [
    { key: 'CLAUDE_CONFIG_DIR', desc: 'Claude Code configuration and session directory' },
    { key: 'CODEX_HOME', desc: 'Codex sessions directory' },
    { key: 'XDG_CONFIG_HOME', desc: 'Config base directory (~/.config)' },
    { key: 'XDG_DATA_HOME', desc: 'Data base directory (~/.local/share)' },
  ];

  $effect(() => {
    if (open && host) {
      envMap = { ...getSavedHostEnv(host) };
      customKey = '';
      customVal = '';
    }
  });

  function handleAddCustom() {
    const k = customKey.trim().toUpperCase();
    if (!k) return;
    envMap[k] = customVal.trim();
    customKey = '';
    customVal = '';
  }

  function handleRemoveKey(key: string) {
    const next = { ...envMap };
    delete next[key];
    envMap = next;
  }

  async function handleSave() {
    if (!host) return;
    saveHostEnv(host, envMap);
    if (connectionStore.isRemote && connectionStore.host === host) {
      await api.setHostEnv(envMap);
    }
    onClose();
  }
</script>

{#if open}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) onClose();
    }}
    onkeydown={(e) => {
      if (e.key === 'Escape') onClose();
    }}
  >
    <div
      class="dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="host-env-title"
    >
      <header class="dialog-header">
        <h2 id="host-env-title">Environment Overrides: {host}</h2>
        <button
          type="button"
          class="dialog-close"
          title="Close"
          aria-label="Close dialog"
          onclick={onClose}
        >
          ✕
        </button>
      </header>

      <div class="dialog-body">
        <p class="body-intro">
          Set environment variable overrides passed to <code>agent-sessions</code> on <strong>{host}</strong>.
          These are applied during session startup.
        </p>

        <div class="env-list">
          {#each commonKeys as item}
            <div class="env-row">
              <div class="env-info">
                <span class="env-key">{item.key}</span>
                <span class="env-desc">{item.desc}</span>
              </div>
              <input
                type="text"
                class="env-input"
                placeholder="Default"
                bind:value={envMap[item.key]}
              />
            </div>
          {/each}

          {#each Object.entries(envMap) as [key, val]}
            {#if !commonKeys.some((c) => c.key === key)}
              <div class="env-row custom">
                <div class="env-info">
                  <span class="env-key">{key}</span>
                </div>
                <div class="custom-input-group">
                  <input
                    type="text"
                    class="env-input"
                    bind:value={envMap[key]}
                  />
                  <button
                    type="button"
                    class="remove-key-btn"
                    title="Remove override"
                    onclick={() => handleRemoveKey(key)}
                  >
                    ✕
                  </button>
                </div>
              </div>
            {/if}
          {/each}
        </div>

        <div class="add-custom-section">
          <h4>Add Override</h4>
          <div class="custom-form">
            <input
              type="text"
              class="env-input"
              placeholder="VARIABLE_NAME"
              bind:value={customKey}
            />
            <input
              type="text"
              class="env-input"
              placeholder="Value"
              bind:value={customVal}
            />
            <button
              type="button"
              class="btn btn-secondary"
              disabled={!customKey.trim()}
              onclick={handleAddCustom}
            >
              Add
            </button>
          </div>
        </div>
      </div>

      <footer class="dialog-footer">
        <button type="button" class="btn btn-secondary" onclick={onClose}>
          Cancel
        </button>
        <button type="button" class="btn btn-primary" onclick={handleSave}>
          Save Overrides
        </button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    z-index: 120;
    background: rgba(0, 0, 0, 0.5);
    display: grid;
    place-items: center;
  }

  .dialog {
    width: min(540px, 92vw);
    max-height: 85vh;
    display: flex;
    flex-direction: column;
    background: var(--bg-primary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 8px 32px rgba(0, 0, 0, 0.3);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 12px 16px;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
  }

  h2 {
    margin: 0;
    font-size: 0.95rem;
    font-weight: 600;
  }

  .dialog-close {
    background: transparent;
    border: none;
    color: var(--text-secondary);
    font-size: 1rem;
    cursor: pointer;
  }

  .dialog-body {
    padding: 16px;
    overflow-y: auto;
  }

  .body-intro {
    margin: 0 0 16px;
    font-size: 0.8rem;
    color: var(--text-secondary);
    line-height: 1.4;
  }

  .env-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .env-row {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding-bottom: 10px;
    border-bottom: 1px solid var(--border-color);
  }

  .env-info {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }

  .env-key {
    font-size: 0.78rem;
    font-family: var(--font-mono, monospace);
    font-weight: 600;
  }

  .env-desc {
    font-size: 0.7rem;
    color: var(--text-muted);
  }

  .env-input {
    padding: 6px 10px;
    font-size: 0.8rem;
    font-family: var(--font-mono, monospace);
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    outline: none;
  }

  .env-input:focus {
    border-color: var(--accent-color);
  }

  .custom-input-group {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .custom-input-group .env-input {
    flex: 1;
  }

  .remove-key-btn {
    background: transparent;
    border: 1px solid var(--border-color);
    color: var(--text-secondary);
    border-radius: 4px;
    padding: 4px 8px;
    cursor: pointer;
  }

  .remove-key-btn:hover {
    color: #ef4444;
    border-color: #ef4444;
  }

  .add-custom-section {
    margin-top: 16px;
    padding-top: 12px;
  }

  h4 {
    margin: 0 0 8px;
    font-size: 0.8rem;
    color: var(--text-secondary);
  }

  .custom-form {
    display: flex;
    gap: 8px;
  }

  .custom-form input {
    flex: 1;
  }

  .dialog-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 16px;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border-color);
  }

  .btn {
    padding: 6px 14px;
    font-size: 0.82rem;
    border-radius: 4px;
    cursor: pointer;
    border: 1px solid var(--border-color);
  }

  .btn-secondary {
    background: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .btn-primary {
    background: var(--accent-color);
    color: white;
    border-color: var(--accent-color);
  }
</style>
