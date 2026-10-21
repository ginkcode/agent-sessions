<script lang="ts">
  import { modalFocus } from '../../modalFocus';
  import { importer } from '../../stores/importer.svelte';
  import { appState } from '../../stores/appState.svelte';
  import { link } from '../../stores/link.svelte';
  import { hostLabel } from '../../hosts';
  import { knownDirectories } from '../../tree';

  // The directory is typed, picked from the directories that already have
  // sessions, or, on this computer, browsed with the native folder dialog.
  // The native dialog can't browse a remote host, so Browse is local only.
  let path = $state('');
  let input: HTMLInputElement | undefined = $state();
  let browsing = $state(false);
  let browseError = $state<string | null>(null);

  let dirs = $derived(knownDirectories(appState.groups));
  let shown = $derived.by(() => {
    const q = path.trim().toLowerCase();
    return q ? dirs.filter((d) => d.toLowerCase().includes(q)) : dirs;
  });

  $effect(() => {
    if (!importer.chooser) return;
    path = importer.chooser === 'change' ? importer.cwd : '';
    browseError = null;
    input?.focus();
  });

  async function browse() {
    browsing = true;
    browseError = null;
    try {
      const dir = await importer.browseDirectory();
      if (dir) path = dir;
    } catch (err) {
      browseError = err instanceof Error ? err.message : String(err);
    } finally {
      browsing = false;
      input?.focus();
    }
  }

  function choose(dir = path) {
    const value = dir.trim();
    if (value) void importer.chooseDirectory(value);
  }

  function cancel() {
    void importer.chooseDirectory(null);
  }
</script>

{#if importer.chooser}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="dialog-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) cancel();
    }}
    onkeydown={(e) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        cancel();
      }
    }}
  >
    <div class="dialog" use:modalFocus role="dialog" aria-modal="true" aria-labelledby="import-dir-title">
      <header class="dialog-header">
        <h2 id="import-dir-title">Choose Directory to Import Into</h2>
        {#if link.dataHost}
          <span class="host-pill">{hostLabel(link.dataHost)}</span>
        {/if}
      </header>

      <div class="dialog-body">
        <p class="hint">
          {#if importer.chooser === 'start'}
            First choose where the session should live on {link.dataHost ? hostLabel(link.dataHost) : 'this computer'},
            then pick the bundle file. The directory must already exist.
          {:else}
            The session is imported into this directory on {link.dataHost ? hostLabel(link.dataHost) : 'this computer'}.
            It must already exist.
          {/if}
        </p>
        <div class="path-row">
          <input
            bind:this={input}
            bind:value={path}
            type="text"
            class="text-input"
            placeholder="/path/to/project"
            autocomplete="off"
            autocorrect="off"
            autocapitalize="off"
            spellcheck="false"
            onkeydown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                choose();
              }
            }}
          />
          {#if !link.dataHost}
            <button type="button" class="btn" disabled={browsing} onclick={browse}>Browse…</button>
          {/if}
        </div>
        {#if browseError}
          <p class="hint browse-error" role="alert">{browseError}</p>
        {/if}
        {#if shown.length > 0}
          <span class="group-label">Directories with sessions</span>
          <ul class="dir-list">
            {#each shown as dir (dir)}
              <li>
                <button
                  type="button"
                  class="dir-item"
                  class:selected={dir === path.trim()}
                  title={dir}
                  onclick={() => (path = dir)}
                  ondblclick={() => choose(dir)}
                >
                  📁 {dir}
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      <footer class="dialog-footer">
        <button type="button" class="btn" onclick={cancel}>Cancel</button>
        <button type="button" class="btn primary-btn" disabled={!path.trim()} onclick={() => choose()}>
          {importer.chooser === 'start' ? 'Choose Bundle…' : 'Use Directory'}
        </button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .dialog-backdrop {
    position: fixed;
    inset: 0;
    background-color: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1010;
    padding: 16px;
  }

  .dialog {
    background-color: var(--bg-primary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.35);
    width: 100%;
    max-width: 560px;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    color: var(--text-primary);
    overflow: hidden;
  }

  .dialog-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 14px 18px;
    border-bottom: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .dialog-header h2 {
    margin: 0;
    font-size: 1.05rem;
    font-weight: 600;
  }

  .host-pill {
    font-size: 0.72rem;
    padding: 2px 8px;
    border-radius: 10px;
    background: var(--bg-tertiary);
    color: var(--text-secondary);
  }

  .dialog-body {
    padding: 14px 18px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    overflow: hidden;
  }

  .hint {
    margin: 0;
    font-size: 0.8rem;
    color: var(--text-secondary);
  }

  .group-label {
    margin-top: 4px;
    font-size: 0.75rem;
    font-weight: 600;
    color: var(--text-secondary);
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .path-row {
    display: flex;
    gap: 8px;
  }

  .browse-error {
    color: #ef4444;
  }

  .text-input {
    flex: 1;
    min-width: 0;
    width: 100%;
    box-sizing: border-box;
    padding: 7px 10px;
    font-size: 0.82rem;
    font-family: var(--font-mono);
    background-color: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    color: var(--text-primary);
  }

  .text-input:focus {
    outline: none;
    border-color: var(--accent-color, #3b82f6);
  }

  .dir-list {
    list-style: none;
    margin: 0;
    padding: 4px;
    overflow-y: auto;
    border: 1px solid var(--border-color);
    border-radius: 6px;
    background-color: var(--bg-secondary);
  }

  .dir-item {
    display: block;
    width: 100%;
    text-align: left;
    padding: 5px 8px;
    border-radius: 4px;
    font-size: 0.8rem;
    font-family: var(--font-mono);
    color: var(--text-primary);
    cursor: pointer;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dir-item:hover {
    background-color: var(--hover-bg);
  }

  .dir-item.selected {
    background-color: var(--active-bg);
    color: var(--accent-color);
  }

  .dialog-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 12px 18px;
    border-top: 1px solid var(--border-color);
    background-color: var(--bg-secondary);
  }

  .btn {
    height: 34px;
    padding: 0 16px;
    font-size: 0.82rem;
    font-weight: 500;
    border-radius: 4px;
    border: 1px solid var(--border-color);
    background-color: var(--bg-primary);
    color: var(--text-primary);
    cursor: pointer;
  }

  .btn:hover:not(:disabled) {
    background-color: var(--hover-bg);
  }

  .btn:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .primary-btn {
    background-color: var(--accent-color, #3b82f6);
    border-color: var(--accent-color, #3b82f6);
    color: #fff;
  }

  .primary-btn:hover:not(:disabled) {
    background-color: #2563eb;
    border-color: #2563eb;
  }
</style>
