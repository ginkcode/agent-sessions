<script lang="ts">
  import type { SessionRef } from '../../../types';
  import { appState } from '../../../stores/appState.svelte';

  interface Props {
    child: SessionRef;
  }

  let { child }: Props = $props();

  function handleClick() {
    appState.selectSession(child);
  }
</script>

<div class="subagent-link-wrapper">
  <button
    type="button"
    class="subagent-link-btn"
    title="Open child subagent session"
    onclick={handleClick}
  >
    <span class="subagent-icon">🔗</span>
    <span class="subagent-label">Subagent:</span>
    <span class="subagent-id">{child.id}</span>
    <span class="subagent-arrow">→ Open</span>
  </button>
</div>

<style>
  .subagent-link-wrapper {
    margin-top: 6px;
  }

  .subagent-link-btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 10px;
    border-radius: 6px;
    background-color: rgba(168, 85, 247, 0.12);
    border: 1px solid rgba(168, 85, 247, 0.3);
    color: #a855f7;
    font-size: 0.75rem;
    font-weight: 500;
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .subagent-link-btn:hover {
    background-color: rgba(168, 85, 247, 0.2);
    border-color: #a855f7;
  }

  .subagent-id {
    font-family: var(--font-mono);
    font-weight: 600;
  }

  .subagent-arrow {
    margin-left: 2px;
    font-weight: 600;
  }
</style>
