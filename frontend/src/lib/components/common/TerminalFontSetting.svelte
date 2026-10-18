<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../api';
  import { preferences } from '../../stores/preferences.svelte';
  import { terminalFontFamily } from '../../terminalFont';

  let fonts = $state<string[]>([]);
  let loading = $state(true);
  let error = $state('');
  let selectedMissing = $derived(Boolean(preferences.terminalFont) && !loading && !error && !fonts.includes(preferences.terminalFont));

  async function refresh() {
    loading = true;
    error = '';
    try { fonts = await api.terminalFonts(); }
    catch (err) { error = err instanceof Error ? err.message : String(err); }
    finally { loading = false; }
  }

  onMount(() => { void refresh(); });
</script>

<div class="font-setting">
  <label for="terminal-font">Built-in terminal font</label>
  <div class="font-row">
    <select id="terminal-font" value={preferences.terminalFont} onchange={(e) => preferences.setTerminalFont(e.currentTarget.value)} disabled={loading}>
      <option value="">App default (JetBrains Mono)</option>
      {#if selectedMissing || (preferences.terminalFont && !fonts.includes(preferences.terminalFont))}
        <option value={preferences.terminalFont}>{preferences.terminalFont} (unavailable)</option>
      {/if}
      {#each fonts as font}<option value={font}>{font}</option>{/each}
    </select>
    <button type="button" onclick={() => void refresh()} disabled={loading} aria-label="Refresh installed fonts">{loading ? 'Loading…' : 'Refresh'}</button>
  </div>
  <p class="hint">Choose a monospace font installed on this computer, including Nerd Fonts. No additional fonts are bundled or downloaded. Applies immediately to all built-in terminals.</p>
  {#if selectedMissing}<p class="error">The selected font is no longer installed. A system monospace fallback will be used.</p>{/if}
  {#if error}<p class="error" role="alert">Could not list installed fonts: {error}</p>{/if}
  <div class="sample" style:font-family={terminalFontFamily(preferences.terminalFont, 'var(--font-mono)')} aria-label="Terminal font preview">user@host ~/project ❯ git status<br />0123456789 ─│┌┐└┘   </div>
</div>

<style>
  .font-setting { margin-top: 20px; }
  label { display: block; font-weight: 600; font-size: 0.85rem; margin-bottom: 8px; }
  .font-row { display: flex; gap: 8px; }
  select { flex: 1; min-width: 0; }
  select, button { padding: 7px 9px; border: 1px solid var(--border-color); border-radius: 4px; color: var(--text-primary); background: var(--bg-primary); font-size: 0.8rem; }
  button { cursor: pointer; } button:disabled, select:disabled { opacity: 0.6; }
  .hint, .error { font-size: 0.78rem; line-height: 1.5; margin: 8px 0; }
  .hint { color: var(--text-muted); } .error { color: var(--danger, #ef4444); }
  .sample { padding: 12px; border: 1px solid var(--border-color); border-radius: 4px; background: var(--bg-secondary); font-size: 13px; line-height: 1.6; overflow-x: auto; white-space: nowrap; }
</style>
