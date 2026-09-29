<script lang="ts">
  import { manage } from '../../stores/manage.svelte';
  import { formatBytes } from '../../format';

  function close() {
    manage.closeSettings();
  }
</script>

{#if manage.settingsDialogOpen}
  <div class="backdrop" role="presentation">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title">
      <header>
        <h2 id="settings-title">Session management settings</h2>
        <button type="button" aria-label="Close settings" onclick={close}>✕</button>
      </header>
      <div class="body">
        <p>Session deletion is disabled by default. Enable it only if you intend to remove local session data.</p>
        {#if manage.settingsError}
          <p class="error" role="alert">{manage.settingsError}</p>
        {/if}
        <label class="setting">
          <input
            type="checkbox"
            checked={manage.settings.enabled}
            disabled={manage.loadingSettings}
            onchange={() => manage.toggleManageEnabled()}
          />
          Enable session management
        </label>
        {#if manage.firstEnableWarningVisible}
          <div class="warning" role="alert">
            <strong>Before enabling</strong>
            <p>Deletion may remove transcripts, related files and cached history. Live sessions are blocked, but other sessions can be moved to Trash or permanently deleted depending on the provider. Always review the preview before confirming.</p>
            <div class="actions">
              <button type="button" onclick={() => (manage.firstEnableWarningVisible = false)}>Cancel</button>
              <button type="button" disabled={manage.loadingSettings} onclick={() => manage.setManageEnabled(true).then(() => (manage.firstEnableWarningVisible = false))}>I understand — enable</button>
            </div>
          </div>
        {/if}
        <label class="setting" class:disabled={!manage.settings.enabled}>
          <input
            type="checkbox"
            checked={manage.settings.allowPermanentDelete}
            disabled={!manage.settings.enabled || manage.loadingSettings}
            onchange={(e) => manage.setAllowPermanentDelete((e.currentTarget as HTMLInputElement).checked)}
          />
          Allow permanent deletion (cannot be undone)
        </label>
        <p class="hint">Codex and OpenCode sessions have no Trash copy. Deleting them is permanent.</p>

        <section class="cache">
          <h3>Handoff files</h3>
          <p class="hint">
            "Continue in" writes the handoff prompt and full history to files that the launch command points at. Files older than 30 days are removed automatically.
          </p>
          {#if manage.handoffCacheError}
            <p class="error" role="alert">{manage.handoffCacheError}</p>
          {/if}
          <div class="cache-row">
            <span class="cache-usage" title={manage.handoffCache?.dir}>
              {#if !manage.handoffCache}
                Checking…
              {:else if manage.handoffCache.files === 0}
                No cached files
              {:else}
                {manage.handoffCache.files} {manage.handoffCache.files === 1 ? 'file' : 'files'}, {formatBytes(manage.handoffCache.bytes)}
              {/if}
            </span>
            <button
              type="button"
              disabled={manage.handoffCacheBusy || !manage.handoffCache || manage.handoffCache.files === 0}
              onclick={() => manage.clearHandoffCache()}
            >{manage.handoffCacheBusy ? 'Deleting…' : 'Delete cache'}</button>
          </div>
        </section>
      </div>
      <footer><button type="button" onclick={close}>Close</button></footer>
    </div>
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; z-index: 110; background: rgba(0,0,0,.5); display: grid; place-items: center; }
  .dialog { width: min(480px, 92vw); max-height: 85vh; overflow-y: auto; background: var(--bg-primary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 8px; box-shadow: 0 8px 32px rgba(0,0,0,.3); }
  header, footer { display: flex; justify-content: space-between; align-items: center; padding: 12px 16px; background: var(--bg-secondary); }
  header { border-bottom: 1px solid var(--border-color); }
  footer { border-top: 1px solid var(--border-color); justify-content: flex-end; }
  h2 { margin: 0; font-size: 1rem; }
  button { padding: 5px 10px; border-radius: 4px; border: 1px solid var(--border-color); background: var(--bg-tertiary); color: var(--text-primary); cursor: pointer; }
  button:disabled { opacity: .5; cursor: not-allowed; }
  .body { padding: 16px; font-size: .8rem; }
  .body > p { margin: 0 0 12px; line-height: 1.5; color: var(--text-secondary); }
  .setting { display: flex; align-items: center; gap: 8px; margin: 12px 0; font-weight: 600; }
  .setting.disabled { opacity: .5; }
  .setting input { accent-color: var(--accent-color); }
  .body .hint { font-size: .75rem; color: var(--text-muted); }
  .body .error { color: #ef4444; }
  .warning { padding: 12px; background: rgba(245,158,11,.1); border: 1px solid #f59e0b; border-radius: 4px; }
  .warning p { margin: 6px 0; line-height: 1.5; }
  .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 10px; }
  .cache { margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--border-color); }
  h3 { margin: 0 0 6px; font-size: .85rem; }
  .cache .hint { margin: 0 0 10px; line-height: 1.5; }
  .cache-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .cache-usage { color: var(--text-secondary); }
</style>
