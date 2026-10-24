<script lang="ts">
  import { gitChanges } from '../../../stores/gitChanges.svelte';
  import { connectionStore } from '../../../stores/connection.svelte';
  import { branchLabel, type ChangeSection } from '../../../gitChanges';
  import type { GitChange } from '../../../types';
  import FileIcon from '../FileIcon.svelte';

  let { onOpen }: { onOpen: (path: string, staged: boolean) => void } = $props();
  const sections: { key: ChangeSection; label: string }[] = [
    { key: 'conflicts', label: 'Merge conflicts' }, { key: 'staged', label: 'Staged changes' }, { key: 'unstaged', label: 'Changes' },
  ];
  let groups = $derived(gitChanges.groups);
  let blocker = $derived(gitChanges.writeBlocker);
  let status = $derived(gitChanges.status);
  let commitAll = $derived(groups.staged.length === 0);
  let amend = $state(false);
  let branchesOpen = $state(false);
  let branchQuery = $state('');
  let branchName = $state('');
  let filteredBranches = $derived(gitChanges.branches.filter(b => b.name.toLowerCase().includes(branchQuery.toLowerCase())));
  function rowKey(event: KeyboardEvent, entry: GitChange, section: ChangeSection) {
    if (event.key === 'Enter') { event.preventDefault(); onOpen(entry.path, section === 'staged'); }
    else if (event.key === ' ' && !blocker) {
      event.preventDefault();
      void (section === 'staged' ? gitChanges.unstage([entry.path]) : gitChanges.stage([entry.path]));
    } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const root = (event.currentTarget as HTMLElement).closest('.changes-pane');
      const rows = [...root?.querySelectorAll<HTMLButtonElement>('.change-open') ?? []];
      const index = rows.indexOf(event.currentTarget as HTMLButtonElement);
      rows[(index + (event.key === 'ArrowDown' ? 1 : -1) + rows.length) % rows.length]?.focus();
    }
  }
</script>

<div class="changes-pane">
  {#if !connectionStore.capabilities.gitChanges}
    <p class="note">This host cannot report Git changes. Update agent-sessions on the host, then reconnect.</p>
  {:else}
    <div class="repo-head">
      <button type="button" class="branch" aria-expanded={branchesOpen} title="Switch or create branch" onclick={() => { branchesOpen = !branchesOpen; if (branchesOpen) void gitChanges.loadBranches(); }}>{branchLabel(status)} ▾</button>
      {#if status?.ahead || status?.behind}<span title="Ahead / behind upstream">↑{status.ahead} ↓{status.behind}</span>{/if}
      <button type="button" title="Refresh changes" aria-label="Refresh changes" disabled={gitChanges.loading || gitChanges.busy} onclick={() => void gitChanges.refresh()}>↻</button>
    </div>
    {#if status?.ok}
      <div class="sync-actions">
        {#if gitChanges.syncOperation}
          <span role="status">{gitChanges.syncOperation === 'fetch' ? 'Fetching' : gitChanges.syncOperation === 'pull' ? 'Pulling' : 'Pushing'}…</span>
          <button type="button" onclick={() => gitChanges.cancelSync()}>Cancel</button>
        {:else}
          <button type="button" disabled={Boolean(gitChanges.syncBlocker)} title={gitChanges.syncBlocker ?? 'Fetch remote updates without changing working files'} onclick={() => void gitChanges.sync('fetch')}>Fetch</button>
          {#if status.behind > 0}
            <button type="button" disabled={Boolean(gitChanges.syncBlocker) || status.detached || Boolean(status.operation) || groups.conflicts.length > 0} title={gitChanges.syncBlocker ?? (status.operation ? `Finish the ${status.operation} first` : 'Pull with fast-forward only; never merge or rebase')} onclick={() => void gitChanges.sync('pull')}>Pull ↓{status.behind}</button>
          {/if}
          {#if status.ahead > 0 || !status.upstream}
            <button type="button" disabled={Boolean(gitChanges.syncBlocker) || status.detached || !status.hasCommits || Boolean(status.operation) || groups.conflicts.length > 0} title={gitChanges.syncBlocker ?? (status.operation ? `Finish the ${status.operation} first` : 'Push commits; never force')} onclick={() => void gitChanges.sync('push')}>{status.upstream ? `Push ↑${status.ahead}` : 'Publish branch…'}</button>
          {/if}
        {/if}
      </div>
      {#if gitChanges.syncMessage}<p class="note" role="status">{gitChanges.syncMessage}</p>{/if}
      {#if !connectionStore.capabilities.gitSync}<p class="note">This host does not support Git sync. Update the server and reconnect.</p>{/if}
    {/if}
    {#if branchesOpen}
      <div class="branches">
        <input aria-label="Filter branches" placeholder="Filter branches…" bind:value={branchQuery} />
        {#if gitChanges.branchesLoading}<p class="note">Reading branches…</p>{/if}
        {#each filteredBranches as branch (branch.name + branch.remote)}
          <button type="button" disabled={Boolean(blocker) || branch.current} title={blocker ?? branch.upstream ?? branch.name} onclick={async () => { if (await gitChanges.switchBranch(branch.name, false, branch.remote)) branchesOpen = false; }}>{branch.current ? '✓ ' : ''}{branch.name}{branch.remote ? ' (remote)' : ''}</button>
        {/each}
        {#if gitChanges.branchesTruncated}<p class="note">Only the first 500 branches are shown.</p>{/if}
        <input aria-label="New branch name" placeholder="New branch name" bind:value={branchName} />
        <button type="button" disabled={Boolean(blocker) || !branchName.trim()} onclick={async () => { if (await gitChanges.switchBranch(branchName.trim(), true)) { branchesOpen = false; branchName = ''; } }}>Create branch…</button>
      </div>
    {/if}
    {#if status?.operation}<p class="note">{status.operation} in progress. Resolve conflicts before committing.</p>{/if}
    {#if status && !status.ok}<p class="note">{status.reason}</p>{/if}
    {#if gitChanges.error}<p class="error" role="alert">{gitChanges.error}</p>{/if}
    {#if status?.ok}
      <div class="commit-box">
        <textarea aria-label="Commit message" placeholder="Commit message" rows="3" bind:value={gitChanges.message} disabled={gitChanges.busy}
          onkeydown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.altKey) { e.preventDefault(); void gitChanges.commit(amend); } }}></textarea>
        <div class="commit-actions">
          <button type="button" class="commit" title={blocker ?? 'Commit saved changes (Cmd/Ctrl+Enter)'} disabled={Boolean(blocker) || !gitChanges.message.trim() || (!groups.staged.length && !groups.unstaged.length) || groups.conflicts.length > 0}
            onclick={() => void gitChanges.commit(amend)}>{amend ? 'Amend last commit' : commitAll ? 'Commit all' : 'Commit staged'}</button>
          {#if groups.staged.length && groups.unstaged.length}<button type="button" disabled={Boolean(blocker) || !gitChanges.message.trim() || groups.conflicts.length > 0} onclick={() => void gitChanges.commit(amend, true)}>Commit all</button>{/if}
          <label title="Allowed only when the last commit is not on the local upstream reference"><input type="checkbox" bind:checked={amend} disabled={Boolean(blocker) || !status.upstream || status.ahead < 1} /> Amend</label>
        </div>
        {#if blocker}<p class="note">{blocker}</p>{/if}
      </div>
      <div class="change-groups">
        {#each sections as section}
          {@const entries = groups[section.key]}
          {#if entries.length}
            <div class="section-head">
              <button type="button" class="section-toggle" aria-expanded={!gitChanges.collapsed.has(section.key)} onclick={() => gitChanges.toggle(section.key)}>{gitChanges.collapsed.has(section.key) ? '▸' : '▾'} {section.label} <span>{entries.length}</span></button>
              {#if section.key === 'staged'}
                <button type="button" aria-label="Unstage all" title={blocker ?? 'Unstage all'} disabled={Boolean(blocker)} onclick={() => void gitChanges.unstage(entries.map(e => e.path))}>−</button>
              {:else}
                <button type="button" aria-label="Stage all {section.label}" title={blocker ?? 'Stage all'} disabled={Boolean(blocker)} onclick={() => void gitChanges.stage(entries.map(e => e.path))}>+</button>
                {#if section.key === 'unstaged'}<button type="button" aria-label="Discard all changes" title={blocker ?? 'Discard all changes'} disabled={Boolean(blocker)} onclick={() => void gitChanges.discard(entries.map(e => e.path))}>↶</button>{/if}
              {/if}
            </div>
            {#if !gitChanges.collapsed.has(section.key)}
              {#each entries as entry (entry.path)}
                <div class="change-row">
                  <button type="button" class="change-open" title={entry.originalPath ? `${entry.originalPath} → ${entry.path}` : entry.path} onclick={() => onOpen(entry.path, section.key === 'staged')} onkeydown={(e) => rowKey(e, entry, section.key)}>
                    <FileIcon path={entry.path} /><span class="path">{entry.path}</span><span class="status git-{entry.conflict ? 'C' : (section.key === 'staged' ? entry.index : entry.worktree) === '?' ? 'U' : section.key === 'staged' ? entry.index : entry.worktree}">{entry.conflict ? '!' : section.key === 'staged' ? entry.index : entry.worktree}</span>
                  </button>
                  <button type="button" aria-label="{section.key === 'staged' ? 'Unstage' : 'Stage'} {entry.path}" title={blocker ?? (section.key === 'staged' ? 'Unstage' : 'Stage')} disabled={Boolean(blocker)} onclick={() => void (section.key === 'staged' ? gitChanges.unstage([entry.path]) : gitChanges.stage([entry.path]))}>{section.key === 'staged' ? '−' : '+'}</button>
                  {#if section.key === 'unstaged'}<button type="button" aria-label="Discard {entry.path}" title={blocker ?? 'Discard changes'} disabled={Boolean(blocker)} onclick={() => void gitChanges.discard([entry.path])}>↶</button>{/if}
                </div>
              {/each}
            {/if}
          {/if}
        {/each}
        {#if !status.entries.length}<p class="note">Working tree clean.</p>{/if}
        {#if status.truncated}<p class="note">Only the first 5,000 changes are shown.</p>{/if}
      </div>
    {:else if gitChanges.loading}<p class="note" role="status">Reading changes…</p>{/if}
  {/if}
</div>

<style>
  .changes-pane { flex: 1; min-height: 0; overflow: auto; font-size: 12px; }
  .repo-head, .section-head, .change-row, .commit-actions { display: flex; align-items: center; gap: 5px; }
  .repo-head { padding: 10px; border-bottom: 1px solid var(--border-color); }
  .sync-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 5px; padding: 6px 10px; border-bottom: 1px solid var(--border-color); }
  .branch { flex: 1; overflow: hidden; text-overflow: ellipsis; text-align: left; }
  .branches { padding: 8px; display: flex; flex-direction: column; gap: 4px; max-height: 280px; overflow: auto; }
  .branches input { padding: 5px; color: var(--text-primary); background: var(--bg-secondary); border: 1px solid var(--border-color); }
  .branches button { text-align: left; }
  button { padding: 3px 6px; border-radius: 3px; cursor: pointer; color: var(--text-secondary); }
  button:hover { background: var(--bg-hover); color: var(--text-primary); }
  button:disabled { opacity: .45; cursor: default; }
  .commit-box { padding: 10px; }
  textarea { width: 100%; resize: vertical; min-height: 56px; max-height: 150px; padding: 6px; background: var(--bg-secondary); color: var(--text-primary); border: 1px solid var(--border-color); border-radius: 4px; font: inherit; box-sizing: border-box; }
  .commit-actions { margin-top: 5px; flex-wrap: wrap; }
  .commit { flex: 1; background: var(--accent-color); color: var(--bg-primary); }
  label { display: flex; align-items: center; gap: 3px; color: var(--text-secondary); }
  .section-head { padding: 4px 8px; background: var(--bg-secondary); }
  .section-toggle { flex: 1; text-align: left; }
  .change-row { padding: 1px 8px; }
  .change-open { flex: 1; min-width: 0; display: flex; align-items: center; gap: 5px; text-align: left; }
  .path { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status { font-family: var(--font-mono); }
  .note, .error { padding: 6px 10px; margin: 0; overflow-wrap: anywhere; color: var(--text-secondary); }
  .error { color: var(--error-color); }
</style>
