<script lang="ts">
  import { tick } from 'svelte';
  import type { SessionRef, Message } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import { manage } from '../../stores/manage.svelte';
  import { api } from '../../api';
  import SessionHeader from './SessionHeader.svelte';
  import MessageBubble from './MessageBubble.svelte';
  import LoadingSpinner from '../common/LoadingSpinner.svelte';
  import EmptyState from '../common/EmptyState.svelte';
  import ErrorState from '../common/ErrorState.svelte';
  import DeleteConfirmDialog from '../common/DeleteConfirmDialog.svelte';

  const PAGE_SIZE = 50;
  // Larger pages when jumping to the end, to cut round-trips on long sessions.
  const JUMP_PAGE_SIZE = 500;
  const SHOW_BOTTOM_BTN_PX = 400;

  let messages = $state<Message[]>([]);
  let isLoading = $state(false);
  let isLoadingMore = $state(false);
  let hasMore = $state(false);
  let loadError = $state<string | null>(null);
  let showMeta = $state(false);
  let resumeCopied = $state(false);
  let containerEl: HTMLElement | null = $state(null);
  let showScrollBottomBtn = $state(false);
  let jumpingToBottom = $state(false);
  let pendingLoad: Promise<void> | null = null;

  let currentRefKey = $derived(
    appState.selectedSessionRef
      ? `${appState.selectedSessionRef.agent}:${appState.selectedSessionRef.id}`
      : null
  );

  // Watch for session selection changes
  $effect(() => {
    const ref = appState.selectedSessionRef;
    if (ref) {
      loadInitialMessages(ref);
    } else {
      messages = [];
      hasMore = false;
      loadError = null;
    }
  });

  async function loadInitialMessages(ref: SessionRef) {
    isLoading = true;
    loadError = null;
    messages = [];
    hasMore = false;

    try {
      const page = await api.getMessages(ref, 0, PAGE_SIZE);
      // Ensure we are still looking at the same session
      if (
        appState.selectedSessionRef?.agent === ref.agent &&
        appState.selectedSessionRef?.id === ref.id
      ) {
        messages = page.messages || [];
        hasMore = page.hasMore;
      }
    } catch (err: any) {
      if (
        appState.selectedSessionRef?.agent === ref.agent &&
        appState.selectedSessionRef?.id === ref.id
      ) {
        loadError = err?.message || 'Failed to load session transcript';
      }
    } finally {
      isLoading = false;
    }
  }

  // Returns the in-flight load when one is running so callers can await it.
  function loadMoreMessages(limit = PAGE_SIZE): Promise<void> {
    if (pendingLoad) return pendingLoad;
    const ref = appState.selectedSessionRef;
    if (!ref || !hasMore) return Promise.resolve();

    isLoadingMore = true;
    pendingLoad = (async () => {
      try {
        const page = await api.getMessages(ref, messages.length, limit);
        if (
          appState.selectedSessionRef?.agent === ref.agent &&
          appState.selectedSessionRef?.id === ref.id
        ) {
          messages = [...messages, ...(page.messages || [])];
          hasMore = page.hasMore;
        }
      } catch (err: any) {
        console.error('Failed to load more messages:', err);
      } finally {
        isLoadingMore = false;
        pendingLoad = null;
      }
    })();
    return pendingLoad;
  }

  function distanceFromBottom(el: HTMLElement): number {
    return el.scrollHeight - el.scrollTop - el.clientHeight;
  }

  function handleScroll(e: Event) {
    const scrollBottom = distanceFromBottom(e.currentTarget as HTMLElement);
    showScrollBottomBtn = scrollBottom > SHOW_BOTTOM_BTN_PX;

    if (scrollBottom < 250 && hasMore && !isLoadingMore && !isLoading) {
      loadMoreMessages();
    }
  }

  // Content can grow without a scroll event (lazy pages, meta toggle), so
  // re-evaluate the button once the DOM reflects the change.
  $effect(() => {
    void messages.length;
    void showMeta;
    void tick().then(() => {
      if (containerEl) {
        showScrollBottomBtn = distanceFromBottom(containerEl) > SHOW_BOTTOM_BTN_PX;
      }
    });
  });

  // Load the remaining pages first so "Bottom" reaches the real end of the
  // transcript rather than the end of what happens to be loaded.
  async function scrollToBottom() {
    if (jumpingToBottom) return;
    jumpingToBottom = true;
    const key = currentRefKey;
    try {
      while (hasMore && currentRefKey === key) {
        const before = messages.length;
        await loadMoreMessages(JUMP_PAGE_SIZE);
        if (messages.length === before) break;
      }
      await tick();
      if (containerEl && currentRefKey === key) {
        containerEl.scrollTo({ top: containerEl.scrollHeight, behavior: 'smooth' });
      }
    } finally {
      jumpingToBottom = false;
    }
  }

  async function handleCopyResume() {
    if (!appState.selectedSessionRef) return;
    try {
      const cmd = await api.copyResumeCommand(appState.selectedSessionRef);
      if (typeof navigator !== 'undefined' && navigator.clipboard) {
        await navigator.clipboard.writeText(cmd);
        resumeCopied = true;
        setTimeout(() => {
          resumeCopied = false;
        }, 2000);
      }
    } catch (err) {
      console.error('Failed to copy resume command:', err);
    }
  }

  async function handleRevealSource() {
    if (!appState.selectedSessionRef) return;
    try {
      await api.revealSource(appState.selectedSessionRef);
    } catch (err) {
      console.error('Failed to reveal source file:', err);
    }
  }

  async function handleDelete() {
    if (!appState.selectedSessionRef || !manage.settings.enabled) return;
    await manage.requestDelete([appState.selectedSessionRef]);
  }
</script>

<div class="transcript-pane">
  {#if !appState.selectedSessionRef}
    <EmptyState
      icon="💬"
      title="No Session Selected"
      description="Select a session from the list to view its full conversation and tool activity."
    />
  {:else if isLoading}
    <div class="center-state">
      <LoadingSpinner size={28} label="Loading transcript…" />
    </div>
  {:else if loadError}
    <div class="center-state">
      <ErrorState
        title="Could not load transcript"
        message={loadError}
        onRetry={() => {
          if (appState.selectedSessionRef) {
            loadInitialMessages(appState.selectedSessionRef);
          }
        }}
      />
    </div>
  {:else if appState.selectedSessionRef}
    {#if appState.selectedSessionMeta}
      <SessionHeader
        meta={appState.selectedSessionMeta}
        {showMeta}
        onToggleMeta={() => (showMeta = !showMeta)}
        onResume={handleCopyResume}
        onReveal={handleRevealSource}
        onDelete={handleDelete}
        {resumeCopied}
      />
    {/if}

    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      bind:this={containerEl}
      class="messages-scroll-area"
      role="region"
      aria-label="Messages list"
      onscroll={handleScroll}
    >
      <div class="messages-inner-list">
        {#if messages.length === 0}
          <div class="empty-messages-notice">
            <span class="empty-icon">📭</span>
            <span>No messages found in this session transcript.</span>
          </div>
        {:else}
          {#each messages as msg, idx (msg.id || `${msg.role}-${msg.time}-${idx}`)}
            <MessageBubble
              message={msg}
              sessionRef={appState.selectedSessionRef}
              {showMeta}
            />
          {/each}
        {/if}

        {#if isLoadingMore}
          <div class="loading-more-bar">
            <LoadingSpinner size={16} label="Loading more messages…" />
          </div>
        {/if}

        {#if !hasMore && messages.length > 0}
          <div class="end-of-transcript-marker">
            <span>── End of transcript ──</span>
          </div>
        {/if}
      </div>
    </div>

    <!-- Outside the scroll area so it stays pinned to the pane corner. -->
    {#if showScrollBottomBtn || jumpingToBottom}
      <button
        type="button"
        class="scroll-bottom-pill"
        title="Scroll to bottom"
        disabled={jumpingToBottom}
        onclick={scrollToBottom}
      >
        {jumpingToBottom ? 'Loading…' : '↓ Bottom'}
      </button>
    {/if}
  {/if}

  <DeleteConfirmDialog
    open={manage.confirmDialogOpen}
    onClose={() => manage.dismissDialog()}
  />
</div>

<style>
  .transcript-pane {
    display: flex;
    flex-direction: column;
    width: 100%;
    height: 100%;
    background-color: var(--bg-primary);
    overflow: hidden;
    position: relative;
  }

  .center-state {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }

  .messages-scroll-area {
    flex: 1;
    overflow-y: auto;
    position: relative;
    padding: 16px;
    -webkit-overflow-scrolling: touch;
  }

  .messages-inner-list {
    max-width: 900px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
  }

  .empty-messages-notice {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 60px 20px;
    color: var(--text-muted);
    font-size: 0.85rem;
  }

  .loading-more-bar {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
  }

  .end-of-transcript-marker {
    display: flex;
    justify-content: center;
    padding: 24px 0 12px 0;
    font-size: 0.72rem;
    color: var(--text-muted);
    user-select: none;
  }

  .scroll-bottom-pill {
    position: absolute;
    bottom: 20px;
    right: 24px;
    padding: 6px 12px;
    border-radius: 20px;
    background-color: var(--accent-color);
    color: white;
    font-size: 0.75rem;
    font-weight: 600;
    box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2);
    border: none;
    cursor: pointer;
    transition: transform 0.15s ease, opacity 0.15s ease;
    z-index: 10;
  }

  .scroll-bottom-pill:disabled {
    cursor: progress;
  }

  .scroll-bottom-pill:hover:not(:disabled) {
    transform: translateY(-2px);
    opacity: 0.95;
  }
</style>
