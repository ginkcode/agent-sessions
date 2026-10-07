<script lang="ts">
  import { manage, type SettingsSection } from '../../stores/manage.svelte';
  import { launcher } from '../../stores/launcher.svelte';
  import { translateSettings } from '../../stores/translate.svelte';
  import { formatBytes } from '../../format';
  import { terminalOptions } from '../../terminal';
  import { preferences } from '../../stores/preferences.svelte';
  import { isTranscriptMode, transcriptModes } from '../../transcript';
  import { isTimeZoneMode, TIME_ZONE_OPTIONS } from '../../date';
  import Dropdown from './Dropdown.svelte';

  let transcriptMode = $derived(transcriptModes.find((m) => m.value === preferences.transcriptMode));

  let sections = $derived(
    (
      [
        { id: 'general', label: 'General' },
        { id: 'translation', label: 'Translation' },
        { id: 'terminal', label: 'Terminal', hidden: !launcher.info.chooseTerminal },
        { id: 'management', label: 'Session management' },
        { id: 'handoff', label: 'Handoff files' },
      ] as { id: SettingsSection; label: string; hidden?: boolean }[]
    ).filter((s) => !s.hidden),
  );
  let section = $derived(sections.some((s) => s.id === manage.settingsSection) ? manage.settingsSection : 'general');

  // The Translation form edits drafts, refilled whenever saved settings arrive.
  let baseURL = $state('');
  let model = $state('');
  let language = $state('');
  let apiKey = $state('');
  let replacingKey = $state(false);
  let showKey = $state(false);
  let loadedFrom: unknown = null;

  $effect(() => {
    const s = translateSettings.settings;
    if (!s || s === loadedFrom) return;
    loadedFrom = s;
    baseURL = s.baseURL;
    model = s.model;
    language = s.language;
    apiKey = '';
    replacingKey = false;
    showKey = false;
  });

  let keyInputShown = $derived(!translateSettings.settings?.apiKeySet || replacingKey);

  async function saveTranslation(clearApiKey = false) {
    await translateSettings.save({ baseURL, model, language, apiKey: clearApiKey ? '' : apiKey, clearApiKey });
  }

  function close() {
    translateSettings.clearTest();
    manage.closeSettings();
  }

  function handleWindowKeydown(e: KeyboardEvent) {
    if (!manage.settingsDialogOpen || e.key !== 'Escape' || e.defaultPrevented) return;
    e.preventDefault();
    close();
  }
</script>

<svelte:window onkeydown={handleWindowKeydown} />

{#if manage.settingsDialogOpen}
  <div class="backdrop" role="presentation">
    <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title">
      <header>
        <h2 id="settings-title">Settings</h2>
        <button type="button" aria-label="Close settings" onclick={close}>✕</button>
      </header>
      <div class="layout">
        <nav class="nav" aria-label="Settings sections">
          <ul role="tablist" aria-orientation="vertical">
            {#each sections as s (s.id)}
              <li role="presentation">
                <button
                  type="button"
                  role="tab"
                  id="settings-tab-{s.id}"
                  aria-selected={section === s.id}
                  aria-controls="settings-panel"
                  class:active={section === s.id}
                  onclick={() => (manage.settingsSection = s.id)}
                >{s.label}</button>
              </li>
            {/each}
          </ul>
        </nav>
        <div class="body" id="settings-panel" role="tabpanel" aria-labelledby="settings-tab-{section}">
          {#if section === 'general'}
            <section class="section transcript">
              <h3>Transcript</h3>
              <div class="transcript-row">
                <Dropdown
                  options={transcriptModes}
                  value={preferences.transcriptMode}
                  onChange={(mode) => { if (isTranscriptMode(mode)) preferences.setTranscriptMode(mode); }}
                  ariaLabel="Default transcript display"
                />
              </div>
              {#if transcriptMode}
                <p class="hint">{transcriptMode.title}. The header buttons change it for the open session only.</p>
              {/if}
            </section>

            <section class="section time-zone">
              <h3>Time zone</h3>
              <div class="time-zone-row">
                <Dropdown
                  options={TIME_ZONE_OPTIONS}
                  value={preferences.timeZone}
                  onChange={(zone) => { if (isTimeZoneMode(zone)) preferences.setTimeZone(zone); }}
                  ariaLabel="Time zone for timestamps"
                />
              </div>
              <p class="hint">Used for message, session and list timestamps on every host. Local time follows this computer's time zone.</p>
            </section>
          {:else if section === 'translation'}
            <section class="section translation">
              <h3>Translation</h3>
              <p class="hint">
                Right-click a message and choose Translate. Any OpenAI-compatible chat completions API works.
                Message text is sent to this provider from this computer, whichever host is selected.
              </p>
              {#if translateSettings.error}
                <p class="error" role="alert">{translateSettings.error}</p>
              {/if}
              <form class="fields" onsubmit={(e) => { e.preventDefault(); void saveTranslation(); }}>
                <label class="field">
                  <span>Base URL</span>
                  <input class="text-input" type="url" bind:value={baseURL} placeholder="https://api.openai.com/v1" autocomplete="off" spellcheck="false" />
                </label>
                <div class="field">
                  <span id="translate-key-label">API key</span>
                  {#if keyInputShown}
                    <div class="input-wrapper">
                      <input
                        class="text-input secret"
                        type={showKey ? 'text' : 'password'}
                        bind:value={apiKey}
                        aria-labelledby="translate-key-label"
                        placeholder={replacingKey ? 'New API key' : 'sk-…'}
                        autocomplete="off"
                        autocapitalize="off"
                        spellcheck="false"
                      />
                      <button
                        type="button"
                        class="toggle-reveal-btn"
                        title={showKey ? 'Hide key' : 'Show key'}
                        aria-label={showKey ? 'Hide key' : 'Show key'}
                        onclick={() => (showKey = !showKey)}
                        tabindex="-1"
                      >{showKey ? '👁️' : '🔒'}</button>
                    </div>
                    {#if replacingKey}
                      <button type="button" class="link-btn" onclick={() => { replacingKey = false; apiKey = ''; }}>Keep the saved key</button>
                    {/if}
                  {:else}
                    <div class="key-row">
                      <span class="key-saved">Saved</span>
                      <button type="button" onclick={() => (replacingKey = true)}>Replace</button>
                      <button type="button" disabled={translateSettings.saving} onclick={() => void saveTranslation(true)}>Clear</button>
                    </div>
                  {/if}
                  <span class="hint">Stored in this computer's settings file, readable only by you. It is never shown again.</span>
                </div>
                <label class="field">
                  <span>Model</span>
                  <input class="text-input" type="text" bind:value={model} placeholder="gpt-4o-mini" autocomplete="off" spellcheck="false" />
                </label>
                <label class="field">
                  <span>Language</span>
                  <input class="text-input" type="text" bind:value={language} placeholder="Vietnamese" autocomplete="off" />
                </label>
                <div class="actions">
                  <button
                    type="button"
                    disabled={!translateSettings.configured || translateSettings.testing || translateSettings.saving}
                    title={translateSettings.configured ? 'Translate "Hello" with the saved settings' : 'Save a base URL, API key and model first'}
                    onclick={() => void translateSettings.test()}
                  >{translateSettings.testing ? 'Testing…' : 'Test'}</button>
                  <button type="submit" class="primary" disabled={translateSettings.saving}>{translateSettings.saving ? 'Saving…' : 'Save'}</button>
                </div>
                {#if translateSettings.testResult}
                  <p class="hint test-ok" role="status">"Hello" → {translateSettings.testResult}</p>
                {:else if translateSettings.testError}
                  <p class="error" role="alert">{translateSettings.testError}</p>
                {/if}
              </form>
            </section>
          {:else if section === 'terminal'}
            <section class="section terminal">
              <h3>Terminal (this computer)</h3>
              <p class="hint">Resume and "Continue in" → Open in terminal start the agent in a new window of this app.</p>
              {#if launcher.terminalError}
                <p class="error" role="alert">{launcher.terminalError}</p>
              {/if}
              {#if launcher.terminal}
                <div class="terminal-row">
                  <Dropdown
                    options={terminalOptions(launcher.terminal)}
                    value={launcher.terminal.selected}
                    onChange={(id) => launcher.choose(id)}
                    ariaLabel="Terminal app"
                  />
                </div>
                {#if launcher.terminal.missing}
                  <p class="error" role="alert">{launcher.terminal.selectedName} is no longer installed. Choose another terminal, or use Copy command.</p>
                {:else if launcher.terminal.options.length === 0}
                  <p class="hint">No supported terminal app was found. Install one, then reopen Settings; Copy command still works.</p>
                {/if}
                {#if launcher.terminal.hint}
                  <p class="hint">{launcher.terminal.hint}</p>
                {/if}
              {:else if !launcher.terminalError}
                <p class="hint">Looking for terminal apps…</p>
              {/if}
            </section>
          {:else if section === 'management'}
            <section class="section management">
              <h3>Session management</h3>
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
                  <p>Deletion may remove transcripts, related files and cached history. Sessions that are live, or whose agent cannot be verified as stopped, are blocked. Other sessions are moved to the system Trash or Recycle Bin, or permanently deleted, depending on the provider. Always review the preview before confirming.</p>
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
              <p class="hint">Claude sessions move to the system Trash or Recycle Bin when supported. Codex and OpenCode sessions have no Trash or Recycle Bin copy; deleting them is permanent.</p>
            </section>
          {:else if section === 'handoff'}
            <section class="section cache">
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
          {/if}
        </div>
      </div>
      <footer><button type="button" onclick={close}>Close</button></footer>
    </div>
  </div>
{/if}

<style>
  .backdrop { position: fixed; inset: 0; z-index: 110; background: rgba(0,0,0,.5); display: grid; place-items: center; }
  .dialog { width: min(760px, 94vw); height: min(560px, 85vh); display: flex; flex-direction: column; background: var(--bg-primary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 8px; box-shadow: 0 8px 32px rgba(0,0,0,.3); overflow: hidden; }
  header, footer { display: flex; justify-content: space-between; align-items: center; padding: 12px 16px; background: var(--bg-secondary); flex: none; }
  header { border-bottom: 1px solid var(--border-color); }
  footer { border-top: 1px solid var(--border-color); justify-content: flex-end; }
  h2 { margin: 0; font-size: 1rem; }
  button { padding: 5px 10px; border-radius: 4px; border: 1px solid var(--border-color); background: var(--bg-tertiary); color: var(--text-primary); cursor: pointer; }
  button:disabled { opacity: .5; cursor: not-allowed; }
  button.primary { background: var(--accent-color); border-color: var(--accent-color); color: #fff; }
  .layout { flex: 1; min-height: 0; display: flex; }
  .nav { flex: none; width: 180px; padding: 10px 8px; border-right: 1px solid var(--border-color); background: var(--bg-secondary); overflow-y: auto; }
  .nav ul { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .nav button { width: 100%; text-align: left; border: none; background: transparent; color: var(--text-secondary); font-size: .8rem; padding: 6px 10px; }
  .nav button:hover { background: var(--bg-tertiary); color: var(--text-primary); }
  .nav button.active { background: var(--bg-tertiary); color: var(--accent-color); font-weight: 600; }
  .body { flex: 1; min-width: 0; overflow-y: auto; padding: 16px 20px; font-size: .8rem; }
  .section > p:not(.hint):not(.error) { margin: 0 0 12px; line-height: 1.5; color: var(--text-secondary); }
  .setting { display: flex; align-items: center; gap: 8px; margin: 12px 0; font-weight: 600; }
  .setting.disabled { opacity: .5; }
  .setting input { accent-color: var(--accent-color); }
  .body .hint { font-size: .75rem; color: var(--text-muted); }
  .body .error { color: #ef4444; }
  .warning { padding: 12px; background: rgba(245,158,11,.1); border: 1px solid #f59e0b; border-radius: 4px; }
  .warning p { margin: 6px 0; line-height: 1.5; }
  .actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 10px; }
  .section + .section { margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--border-color); }
  h3 { margin: 0 0 6px; font-size: .85rem; }
  .section .hint, .section .error { margin: 0 0 10px; line-height: 1.5; }
  .terminal-row, .transcript-row, .time-zone-row { margin-bottom: 10px; }
  .cache-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .cache-usage { color: var(--text-secondary); }
  .fields { display: flex; flex-direction: column; gap: 12px; }
  .field { display: flex; flex-direction: column; gap: 4px; }
  .field > span:first-child { font-weight: 600; }
  .field .hint { margin: 0; }
  .text-input { width: 100%; padding: 6px 10px; font-size: .8rem; background: var(--bg-secondary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 6px; outline: none; box-sizing: border-box; }
  .text-input:focus { border-color: var(--accent-color); box-shadow: 0 0 0 1px var(--accent-color); }
  .text-input.secret { padding-right: 36px; font-family: var(--font-mono, monospace); }
  .input-wrapper { position: relative; display: flex; align-items: center; }
  .toggle-reveal-btn { position: absolute; right: 6px; background: transparent; border: none; padding: 4px; font-size: .8rem; opacity: .7; }
  .toggle-reveal-btn:hover { opacity: 1; }
  .key-row { display: flex; align-items: center; gap: 8px; }
  .key-saved { flex: 1; color: var(--text-secondary); }
  .link-btn { align-self: flex-start; padding: 0; border: none; background: none; color: var(--accent-color); font-size: .75rem; }
  .test-ok { color: var(--text-secondary); }
</style>
