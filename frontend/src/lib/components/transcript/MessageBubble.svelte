<script lang="ts">
  import type { Message, SessionRef, SearchHitKind } from '../../types';
  import { formatAbsoluteTime } from '../../date';
  import { visibleTranscriptParts, type TranscriptMode } from '../../transcript';
  import TextPart from './parts/TextPart.svelte';
  import ReasoningPart from './parts/ReasoningPart.svelte';
  import ToolPart from './parts/ToolPart.svelte';
  import PatchPart from './parts/PatchPart.svelte';
  import CompactionPart from './parts/CompactionPart.svelte';
  import NoticePart from './parts/NoticePart.svelte';

  interface Props {
    message: Message;
    sessionRef: SessionRef;
    mode?: TranscriptMode;
    forceVisible?: boolean;
    searchKind?: SearchHitKind | null;
  }

  let {
    message,
    sessionRef,
    mode = 'activity',
    forceVisible = false,
    searchKind = null,
  }: Props = $props();

  let formattedTime = $derived(message.time ? formatAbsoluteTime(message.time) : '');
  let isSystem = $derived(message.role === 'system');
  let isUser = $derived(message.role === 'user');
  let isAssistant = $derived(message.role === 'assistant');
  let visibleParts = $derived(visibleTranscriptParts(message, mode, forceVisible));
</script>

{#if visibleParts.length > 0}
  <div
    class="message-bubble-wrapper"
    class:role-user={isUser}
    class:role-assistant={isAssistant}
    class:role-system={isSystem}
    class:is-meta={message.isMeta}
  >
    <div class="message-card">
      <div class="message-meta-header">
        <div class="meta-left">
          {#if isUser}
            <span class="role-icon">👤</span>
            <span class="role-label">User</span>
          {:else if isAssistant}
            <span class="role-icon">🤖</span>
            <span class="role-label">Assistant</span>
            {#if message.model}
              <span class="model-tag">{message.model}</span>
            {/if}
          {:else}
            <span class="role-icon">⚙️</span>
            <span class="role-label">System</span>
          {/if}
          {#if message.isSidechain}
            <span class="sidechain-tag">sidechain</span>
          {/if}
        </div>

        {#if formattedTime}
          <div class="meta-right">
            <span class="time-label" title={message.time}>{formattedTime}</span>
          </div>
        {/if}
      </div>

      <div class="message-parts-body">
        {#each visibleParts as part, idx (idx)}
          {#if part.kind === 'text' && part.text}
            <TextPart text={part.text} />
          {:else if part.kind === 'reasoning' && part.text}
            <ReasoningPart text={part.text} defaultExpanded={searchKind === 'reasoning'} />
          {:else if part.kind === 'tool' && part.tool}
            <ToolPart tool={part.tool} {sessionRef} defaultExpanded={searchKind === 'tool'} />
          {:else if part.kind === 'patch'}
            <PatchPart files={part.files} text={part.text} />
          {:else if part.kind === 'compaction'}
            <CompactionPart text={part.text} />
          {:else if part.kind === 'notice' && part.text}
            <NoticePart text={part.text} />
          {:else if part.kind === 'file' && part.file}
            <div class="file-attachment-pill">
              <span>📎</span>
              <span>{part.file.name || part.file.ref || 'Attachment'}</span>
            </div>
          {/if}
        {/each}
      </div>
    </div>
  </div>
{/if}

<style>
  .message-bubble-wrapper {
    display: flex;
    flex-direction: column;
    margin-bottom: 16px;
    width: 100%;
  }

  .message-bubble-wrapper.role-user {
    align-items: flex-end;
  }

  .message-bubble-wrapper.role-user .message-card {
    max-width: 85%;
    background-color: var(--bg-secondary);
    border-left: 3px solid var(--accent-color);
  }

  .message-bubble-wrapper.role-assistant .message-card {
    width: 100%;
    background-color: var(--bg-secondary);
    box-shadow: var(--shadow-card);
  }

  .message-bubble-wrapper.role-system .message-card {
    width: 100%;
    background-color: var(--bg-secondary);
    border: 1px dashed var(--border-color);
  }

  .message-card {
    border-radius: var(--radius-md);
    overflow: hidden;
  }

  .message-meta-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 6px 12px;
    background-color: var(--bg-secondary);
    font-size: 11.52px;
  }

  .meta-left {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .role-icon {
    font-size: 12.8px;
  }

  .role-label {
    font-weight: 600;
    color: var(--text-primary);
  }

  .model-tag {
    font-family: var(--font-mono);
    color: var(--text-muted);
    font-size: 10.88px;
    padding: 1px 4px;
    border-radius: 3px;
    background: var(--bg-tertiary);
  }

  .sidechain-tag {
    font-size: 10.4px;
    color: #a855f7;
    font-style: italic;
  }

  .meta-right {
    color: var(--text-muted);
    font-variant-numeric: tabular-nums;
  }

  .message-parts-body {
    padding: 12px;
  }

  .file-attachment-pill {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px;
    border-radius: 4px;
    background: var(--bg-tertiary);
    font-size: 12px;
    font-family: var(--font-mono);
    margin: 4px 0;
  }
</style>
