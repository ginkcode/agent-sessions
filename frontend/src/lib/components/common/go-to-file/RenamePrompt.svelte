<script lang="ts">
  import { tick } from 'svelte';
  import { files } from '../../../stores/files.svelte';
  import { link } from '../../../stores/link.svelte';
  import { fileActions } from '../../../stores/fileActions.svelte';
  import { epoch } from '../../../link';
  let { path, onClose }: { path: string; onClose: () => void } = $props();
  const dir = files.dir;
  const host = link.dataHost;
  const started = epoch();
  let name = $state((() => path.split('/').pop() ?? '')());
  let error = $state('');
  let busy = $state(false);
  let panel: HTMLDivElement | undefined = $state();
  let input: HTMLInputElement | undefined = $state();
  const invalid = $derived(!name || name === '.' || name === '..' || /[\/\\\0]/.test(name));
  $effect(() => {
    const previous = document.activeElement;
    void tick().then(() => { input?.focus(); input?.select(); });
    const contain = (e: FocusEvent) => {
      if (panel && e.target instanceof Node && !panel.contains(e.target)) input?.focus();
    };
    document.addEventListener('focusin', contain);
    return () => {
      document.removeEventListener('focusin', contain);
      if (previous instanceof HTMLElement) previous.focus();
    };
  });
  async function submit() {
    if (busy || invalid) return;
    if (dir !== files.dir || host !== link.dataHost || started !== epoch()) {
      error = 'The project or connection changed. Cancel and try again.';
      return;
    }
    busy = true;
    error = '';
    try { await fileActions.rename(path, name); onClose(); }
    catch (err) { error = err instanceof Error ? err.message : String(err); }
    finally { busy = false; }
  }
  function key(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === 'Escape') { e.preventDefault(); if (!busy) onClose(); }
    if ((e.metaKey || e.ctrlKey) && ['s', 'w', 'f'].includes(e.key.toLowerCase())) e.preventDefault();
    if (e.key === 'Tab') {
      const controls = [...(panel?.querySelectorAll<HTMLElement>('input:not(:disabled), button:not(:disabled)') ?? [])];
      const index = controls.indexOf(document.activeElement as HTMLElement);
      e.preventDefault();
      controls[(index + (e.shiftKey ? -1 : 1) + controls.length) % controls.length]?.focus();
    }
  }
</script>

<div class="rename-backdrop" role="presentation">
  <div class="rename-prompt" role="dialog" tabindex="-1" aria-modal="true" aria-labelledby="rename-title" bind:this={panel} onkeydown={key}>
    <form onsubmit={(e) => { e.preventDefault(); void submit(); }}>
    <h2 id="rename-title">Rename</h2>
    <p>{path}</p>
    <label for="rename-name">New name</label>
    <input id="rename-name" bind:this={input} bind:value={name} readonly={busy} autocomplete="off" spellcheck="false" aria-invalid={invalid} aria-describedby={error ? 'rename-error' : undefined} />
    {#if error}<p id="rename-error" class="error" role="alert">{error}</p>{/if}
    <div class="actions">
      <button type="button" disabled={busy} onclick={onClose}>Cancel</button>
      <button type="submit" disabled={busy || invalid || name === path.split('/').pop()}>{busy ? 'Renaming…' : 'Rename'}</button>
    </div>
    </form>
  </div>
</div>

<style>
  .rename-backdrop { position: fixed; inset: 0; z-index: 1200; display: grid; place-items: center; background: #0008; padding: 24px; }
  .rename-prompt { width: min(520px, 100%); background: var(--bg-primary); color: var(--text-primary); padding: 24px; border: 1px solid var(--border-color); border-radius: 8px; box-shadow: 0 16px 48px #0006; }
  h2 { margin: 0 0 12px; font-size: 1rem; }
  p { margin: 0 0 12px; overflow-wrap: anywhere; font-size: .82rem; line-height: 1.5; }
  label { display: block; margin-bottom: 6px; font-size: .82rem; }
  input { width: 100%; box-sizing: border-box; padding: 8px 10px; border: 1px solid var(--border-color); border-radius: 4px; background: var(--bg-secondary); color: var(--text-primary); font-family: var(--font-mono); font-size: .8rem; }
  input:focus { outline: 2px solid var(--accent-color); outline-offset: 1px; }
  .error { margin: 8px 0 0; color: #ef4444; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
  button { padding: 6px 14px; border: 1px solid var(--border-color); border-radius: 4px; cursor: pointer; background: var(--bg-secondary); font-size: .8rem; }
  button:hover:not(:disabled) { background: var(--hover-bg); }
  button:disabled { opacity: .5; cursor: default; }
</style>
