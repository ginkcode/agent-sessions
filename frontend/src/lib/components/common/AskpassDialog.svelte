<script lang="ts">
  import { connectionStore } from '../../stores/connection.svelte';

  let answer = $state('');
  let showPassword = $state(false);
  let submitting = $state(false);
  let inputEl = $state<HTMLInputElement | null>(null);

  let isYesNo = $derived(
    connectionStore.pendingAskpass?.prompt.toLowerCase().includes('(yes/no') ?? false
  );

  $effect(() => {
    if (connectionStore.pendingAskpass) {
      answer = '';
      showPassword = false;
      submitting = false;
      setTimeout(() => {
        inputEl?.focus();
      }, 50);
    }
  });

  async function handleSubmit(e?: Event) {
    if (e) e.preventDefault();
    if (!connectionStore.pendingAskpass || submitting) return;
    submitting = true;
    try {
      await connectionStore.askpassReply(connectionStore.pendingAskpass.id, answer);
    } finally {
      submitting = false;
      answer = '';
    }
  }

  function handleCancel() {
    if (!connectionStore.pendingAskpass) return;
    connectionStore.cancelAskpass(connectionStore.pendingAskpass.id);
    answer = '';
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      handleCancel();
    }
  }
</script>

{#if connectionStore.pendingAskpass}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) handleCancel();
    }}
    onkeydown={handleKeydown}
  >
    <div
      class="dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="askpass-dialog-title"
    >
      <header class="dialog-header">
        <div class="header-title-group">
          <span class="auth-icon" aria-hidden="true">🔑</span>
          <h2 id="askpass-dialog-title">SSH Authentication</h2>
        </div>
        {#if connectionStore.host}
          <span class="host-badge">{connectionStore.host}</span>
        {/if}
      </header>

      <form onsubmit={handleSubmit}>
        <div class="dialog-body">
          <p class="prompt-text">{connectionStore.pendingAskpass.prompt}</p>

          <div class="input-wrapper">
            <input
              bind:this={inputEl}
              type={isYesNo || showPassword ? 'text' : 'password'}
              class="auth-input"
              bind:value={answer}
              placeholder={isYesNo ? 'yes or no' : 'Credentials…'}
              autocomplete="off"
              autocorrect="off"
              autocapitalize="off"
              spellcheck="false"
              disabled={submitting}
            />
            {#if !isYesNo}
              <button
                type="button"
                class="toggle-reveal-btn"
                title={showPassword ? 'Hide password' : 'Show password'}
                aria-label={showPassword ? 'Hide password' : 'Show password'}
                onclick={() => (showPassword = !showPassword)}
                tabindex="-1"
              >
                {showPassword ? '👁️' : '🔒'}
              </button>
            {/if}
          </div>
          <p class="security-note">
            Credentials are sent securely over a local Unix domain socket and never stored on disk.
          </p>
        </div>

        <footer class="dialog-footer">
          <button
            type="button"
            class="btn btn-secondary"
            onclick={handleCancel}
            disabled={submitting}
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn btn-primary"
            disabled={submitting}
          >
            {submitting ? 'Authenticating…' : 'Continue'}
          </button>
        </footer>
      </form>
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    z-index: 120;
    background: rgba(0, 0, 0, 0.6);
    display: grid;
    place-items: center;
    backdrop-filter: blur(2px);
  }

  .dialog {
    width: min(440px, 92vw);
    background: var(--bg-primary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 12px 36px rgba(0, 0, 0, 0.4);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 14px 18px;
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border-color);
  }

  .header-title-group {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .auth-icon {
    font-size: 1.1rem;
  }

  h2 {
    margin: 0;
    font-size: 1rem;
    font-weight: 600;
  }

  .host-badge {
    font-size: 0.75rem;
    font-family: var(--font-mono, monospace);
    background: var(--bg-tertiary);
    color: var(--text-secondary);
    padding: 2px 8px;
    border-radius: 4px;
    border: 1px solid var(--border-color);
  }

  .dialog-body {
    padding: 18px;
  }

  .prompt-text {
    margin: 0 0 12px;
    font-size: 0.85rem;
    line-height: 1.4;
    word-break: break-word;
    font-weight: 500;
  }

  .input-wrapper {
    position: relative;
    display: flex;
    align-items: center;
  }

  .auth-input {
    width: 100%;
    padding: 8px 36px 8px 10px;
    font-size: 0.85rem;
    font-family: var(--font-mono, monospace);
    background: var(--bg-secondary);
    color: var(--text-primary);
    border: 1px solid var(--border-color);
    border-radius: 6px;
    outline: none;
    box-sizing: border-box;
  }

  .auth-input:focus {
    border-color: var(--accent-color);
    box-shadow: 0 0 0 1px var(--accent-color);
  }

  .toggle-reveal-btn {
    position: absolute;
    right: 6px;
    background: transparent;
    border: none;
    cursor: pointer;
    padding: 4px;
    font-size: 0.85rem;
    opacity: 0.7;
  }

  .toggle-reveal-btn:hover {
    opacity: 1;
  }

  .security-note {
    margin: 10px 0 0;
    font-size: 0.72rem;
    color: var(--text-muted);
    line-height: 1.4;
  }

  .dialog-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 18px;
    background: var(--bg-secondary);
    border-top: 1px solid var(--border-color);
  }

  .btn {
    padding: 6px 14px;
    font-size: 0.82rem;
    font-weight: 500;
    border-radius: 5px;
    cursor: pointer;
    border: 1px solid var(--border-color);
  }

  .btn-secondary {
    background: var(--bg-tertiary);
    color: var(--text-primary);
  }

  .btn-secondary:hover:not(:disabled) {
    background: var(--bg-hover);
  }

  .btn-primary {
    background: var(--accent-color);
    color: white;
    border-color: var(--accent-color);
  }

  .btn-primary:hover:not(:disabled) {
    filter: brightness(1.1);
  }

  .btn:disabled {
    opacity: 0.6;
    cursor: not-allowed;
  }
</style>
