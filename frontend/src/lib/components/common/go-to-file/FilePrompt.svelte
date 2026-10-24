<script lang="ts">
  import { tick } from 'svelte';
  import { filePrompt } from '../../../stores/filePrompt.svelte';
  let panel: HTMLDivElement | undefined = $state();
  $effect(() => {
    if (!filePrompt.request) return;
    const previous = document.activeElement;
    const cancel = () => panel?.querySelector<HTMLButtonElement>('[data-cancel]')?.focus();
    void tick().then(cancel);
    // Keep focus in the prompt, even if something behind it (such as an
    // editor that just finished loading) takes focus.
    const contain = (e: FocusEvent) => {
      if (panel && e.target instanceof Node && !panel.contains(e.target)) cancel();
    };
    document.addEventListener('focusin', contain);
    return () => {
      document.removeEventListener('focusin', contain);
      if (previous instanceof HTMLElement) previous.focus();
    };
  });
  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform);
  function key(e: KeyboardEvent) {
    e.stopPropagation();
    if (e.key === 'Escape') { e.preventDefault(); filePrompt.answer(null); }
    // Shortcuts the dialog and editor own (find, save, close) must not reach
    // the browser either while the prompt decides.
    const mod = isMac ? e.metaKey : e.ctrlKey;
    if (mod && ['f', 's', 'w'].includes(e.key.toLowerCase())) e.preventDefault();
    if (e.key === 'Tab') {
      const buttons = [...(panel?.querySelectorAll<HTMLButtonElement>('button') ?? [])];
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      e.preventDefault();
      buttons[(index + (e.shiftKey ? -1 : 1) + buttons.length) % buttons.length]?.focus();
    }
  }
</script>

{#if filePrompt.request}
  <div class="file-prompt-backdrop" role="presentation">
    <div class="file-prompt" role="alertdialog" aria-modal="true" aria-labelledby="file-prompt-title" aria-describedby="file-prompt-message" tabindex="-1" bind:this={panel} onkeydown={key}>
      <h2 id="file-prompt-title">{filePrompt.request.title}</h2>
      <p id="file-prompt-message">{filePrompt.request.message}</p>
      <div class="actions">
        <button type="button" data-cancel onclick={() => filePrompt.answer(null)}>Cancel</button>
        {#each filePrompt.request.choices as choice}
          <button type="button" class:danger={choice.danger} onclick={() => filePrompt.answer(choice.value)}>{choice.label}</button>
        {/each}
      </div>
    </div>
  </div>
{/if}

<style>
  .file-prompt-backdrop { position: fixed; inset: 0; z-index: 1200; display: grid; place-items: center; background: #0008; padding: 24px; }
  .file-prompt { width: min(520px, 100%); background: var(--bg-primary); color: var(--text-primary); padding: 24px; border: 1px solid var(--border-color); border-radius: 8px; box-shadow: 0 16px 48px #0006; }
  h2 { margin: 0 0 12px; font-size: 1rem; }
  p { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 45vh; overflow: auto; font-size: .82rem; line-height: 1.5; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 20px; }
  button { padding: 6px 14px; border: 1px solid var(--border-color); border-radius: 4px; cursor: pointer; background: var(--bg-secondary); font-size: .8rem; }
  button:hover { background: var(--hover-bg); }
  button.danger { color: #ef4444; }
</style>
