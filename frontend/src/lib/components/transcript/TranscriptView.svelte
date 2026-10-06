<script lang="ts">
  import { tick, untrack } from 'svelte';
  import type { SessionRef, Message, MessageJump } from '../../types';
  import { appState } from '../../stores/appState.svelte';
  import { search } from '../../stores/search.svelte';
  import { highlightTextNodes, jumpOffset } from '../../search';
  import { manage } from '../../stores/manage.svelte';
  import { api } from '../../api';
  import { copyToClipboard } from '../../portable';
  import { copiedGuidance, copyFailed, openedGuidance, openFailed } from '../../guidance';
  import { toast } from '../../stores/toast.svelte';
  import { link } from '../../stores/link.svelte';
  import { launcher } from '../../stores/launcher.svelte';
  import { preferences } from '../../stores/preferences.svelte';
  import { isStaleReply } from '../../link';
  import type { TranscriptMode } from '../../transcript';
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
  let mode = $state<TranscriptMode>(preferences.transcriptMode);
  let resumeCopied = $state(false);
  let resumeOpening = $state(false);
  let containerEl: HTMLElement | null = $state(null);
  let showScrollBottomBtn = $state(false);
  let jumpingToBottom = $state(false);
  let pendingLoad: Promise<void> | null = null;
  let pageOffset = $state(0);
  let jumpLoading = $state(false);
  let jumpNotice = $state<string | null>(null);
  // The jump this transcript page was loaded for; cleared by normal loads so
  // a meta target is only force-shown while it is the navigation target.
  let activeJump = $state<MessageJump | null>(null);
  let isLoadingEarlier = $state(false);
  let handledJumpID = 0;
  let loadGeneration = 0;
  let clearHighlight: (() => void) | null = null;
  let highlightTimer: ReturnType<typeof setTimeout> | null = null;

  let currentRefKey = $derived(
    appState.selectedSessionRef
      ? `${appState.selectedSessionRef.agent}:${appState.selectedSessionRef.id}`
      : null
  );

  // A search jump owns its initial page load. Normal selection still starts at
  // zero; guarding by jump id prevents the selection effect racing that load.
  let lastRef: SessionRef | null = null;
  $effect(() => {
    const ref = appState.selectedSessionRef;
    const jump = search.jump;
    const refChanged = ref !== lastRef;
    // The display mode is per session; jumps within it keep the chosen mode.
    if (ref && !refsMatch(lastRef, ref)) mode = untrack(() => preferences.transcriptMode);
    lastRef = ref;
    if (ref) {
      if (jump && jump.id > handledJumpID && refsMatch(ref, jump.ref)) {
        handledJumpID = jump.id;
        void loadSearchJump(jump);
      } else if (refChanged) {
        void loadInitialMessages(ref);
      }
    } else {
      messages = [];
      pageOffset = 0;
      hasMore = false;
      loadError = null;
    }
  });

  // Changing the default in Settings also applies to the open session.
  $effect(() => {
    mode = preferences.transcriptMode;
  });

  // A refresh or a change event for the open session reloads what is
  // loaded, in place, so new messages appear without losing the scroll.
  let lastVersion = appState.transcriptVersion;
  $effect(() => {
    const version = appState.transcriptVersion;
    if (version === lastVersion) return;
    lastVersion = version;
    const ref = untrack(() => appState.selectedSessionRef);
    if (ref) void reloadMessages(ref);
  });

  function refsMatch(a: SessionRef | null | undefined, b: SessionRef): boolean {
    return a?.agent === b.agent && a.id === b.id;
  }

  async function loadInitialMessages(ref: SessionRef) {
    const generation = ++loadGeneration;
    isLoading = true;
    jumpLoading = false;
    jumpNotice = null;
    activeJump = null;
    clearJumpHighlight();
    loadError = null;
    messages = [];
    pageOffset = 0;
    hasMore = false;

    try {
      const page = await api.getMessages(ref, 0, PAGE_SIZE);
      // Ensure we are still looking at the same session and request.
      if (generation === loadGeneration && refsMatch(appState.selectedSessionRef, ref)) {
        messages = page.messages || [];
        pageOffset = page.offset;
        hasMore = page.hasMore;
      }
    } catch (err: any) {
      if (generation === loadGeneration && refsMatch(appState.selectedSessionRef, ref)) {
        loadError = err?.message || 'Failed to load session transcript';
      }
    } finally {
      if (generation === loadGeneration) isLoading = false;
    }
  }

  async function reloadMessages(ref: SessionRef): Promise<void> {
    // A first load in flight reads the current content anyway.
    if (isLoading) return;
    if (pendingLoad) await pendingLoad;
    if (isLoading || !refsMatch(appState.selectedSessionRef, ref)) return;
    if (loadError) {
      await loadInitialMessages(ref);
      return;
    }
    // Supersedes in-flight page loads, which would splice into the old list.
    const generation = ++loadGeneration;
    try {
      const page = await api.getMessages(ref, pageOffset, Math.max(messages.length, PAGE_SIZE));
      if (generation !== loadGeneration || !refsMatch(appState.selectedSessionRef, ref)) return;
      messages = page.messages || [];
      hasMore = page.hasMore;
    } catch (err) {
      if (isStaleReply(err)) return;
      console.error('Failed to reload transcript:', err);
    }
  }

  async function loadSearchJump(jump: MessageJump): Promise<void> {
    const generation = ++loadGeneration;
    const offset = jump.messageIndex < 0 ? 0 : jumpOffset(jump.messageIndex);
    isLoading = true;
    jumpLoading = true;
    jumpNotice = null;
    activeJump = jump;
    clearJumpHighlight();
    loadError = null;
    messages = [];
    pageOffset = offset;
    hasMore = false;

    try {
      // Only the page around the target is requested; header metadata is
      // awaited too so its arrival doesn't shift the scroll after we land.
      const [page] = await Promise.all([
        api.getMessages(jump.ref, offset, PAGE_SIZE),
        jump.ready?.catch(() => undefined),
      ]);
      if (generation !== loadGeneration || !refsMatch(appState.selectedSessionRef, jump.ref)) return;
      messages = page.messages || [];
      pageOffset = page.offset;
      hasMore = page.hasMore;
      isLoading = false;
      await tick();
      if (generation !== loadGeneration || !refsMatch(appState.selectedSessionRef, jump.ref)) return;

      if (jump.messageIndex < 0) {
        containerEl?.scrollTo({ top: 0, behavior: 'smooth' });
        return;
      }

      const target = containerEl?.querySelector<HTMLElement>(
        `[data-message-index="${jump.messageIndex}"]`
      );
      if (!target) {
        jumpNotice = 'This search result moved or was removed after it was indexed.';
        return;
      }
      target.scrollIntoView({ block: 'center', behavior: 'smooth' });
      target.classList.add('search-jump-target');
      const disposeTextMarks = highlightTextNodes(target, jump.query);
      clearHighlight = () => {
        disposeTextMarks();
        target.classList.remove('search-jump-target');
      };
      highlightTimer = setTimeout(clearJumpHighlight, 5000);
    } catch (err: any) {
      if (generation === loadGeneration && refsMatch(appState.selectedSessionRef, jump.ref)) {
        loadError = err?.message || 'Failed to load the search result';
      }
    } finally {
      if (generation === loadGeneration) {
        isLoading = false;
        jumpLoading = false;
      }
    }
  }

  function clearJumpHighlight(): void {
    if (highlightTimer) clearTimeout(highlightTimer);
    highlightTimer = null;
    clearHighlight?.();
    clearHighlight = null;
  }

  function reopenSearch(): void {
    jumpNotice = null;
    search.show();
    void search.runNow();
  }

  // A jump starts mid-transcript; earlier pages load on demand, keeping the
  // visible content anchored while rows are prepended above it.
  async function loadEarlierMessages(): Promise<void> {
    const ref = appState.selectedSessionRef;
    if (!ref || pageOffset <= 0 || isLoadingEarlier) return;
    const generation = loadGeneration;
    const start = Math.max(0, pageOffset - PAGE_SIZE);
    isLoadingEarlier = true;
    try {
      const page = await api.getMessages(ref, start, pageOffset - start);
      if (generation !== loadGeneration || !refsMatch(appState.selectedSessionRef, ref)) return;
      const before = containerEl ? containerEl.scrollHeight - containerEl.scrollTop : 0;
      messages = [...(page.messages || []), ...messages];
      pageOffset = start;
      await tick();
      if (containerEl) containerEl.scrollTop = containerEl.scrollHeight - before;
    } catch (err) {
      console.error('Failed to load earlier messages:', err);
    } finally {
      isLoadingEarlier = false;
    }
  }

  // Returns the in-flight load when one is running so callers can await it.
  function loadMoreMessages(limit = PAGE_SIZE): Promise<void> {
    if (pendingLoad) return pendingLoad;
    const ref = appState.selectedSessionRef;
    if (!ref || !hasMore) return Promise.resolve();
    const generation = loadGeneration;

    isLoadingMore = true;
    pendingLoad = (async () => {
      try {
        const page = await api.getMessages(ref, pageOffset + messages.length, limit);
        if (generation === loadGeneration && refsMatch(appState.selectedSessionRef, ref)) {
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

  // Content can grow or shrink without a scroll event (lazy pages, display
  // mode), so re-evaluate the button and keep loading while it fits on screen.
  // Only new messages re-trigger it, so a failing page load is not retried in
  // a loop; scrolling retries it as before.
  $effect(() => {
    void messages.length;
    void mode;
    void hasMore;
    void containerEl;
    void tick().then(() => {
      if (!containerEl) return;
      const remaining = distanceFromBottom(containerEl);
      showScrollBottomBtn = remaining > SHOW_BOTTOM_BTN_PX;
      if (remaining < 250 && hasMore && !isLoadingMore && !isLoading) {
        void loadMoreMessages();
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
      if (!(await copyToClipboard(cmd))) {
        toast.show(copyFailed('resume command', 'The clipboard is not available.'));
        return;
      }
      resumeCopied = true;
      setTimeout(() => {
        resumeCopied = false;
      }, 2000);
      toast.show(copiedGuidance('resume', { host: link.dataHost, shell: launcher.copyShell }));
    } catch (err) {
      if (isStaleReply(err)) return;
      toast.show(copyFailed('resume command', err));
    }
  }

  async function handleOpenResume() {
    if (!appState.selectedSessionRef || resumeOpening) return;
    resumeOpening = true;
    try {
      await api.openResumeInTerminal(appState.selectedSessionRef);
      toast.show(openedGuidance('resume'));
    } catch (err) {
      if (isStaleReply(err)) return;
      toast.show(openFailed(err));
    } finally {
      resumeOpening = false;
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
      <LoadingSpinner size={28} label={jumpLoading ? 'Opening search result…' : 'Loading transcript…'} />
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
        {mode}
        onModeChange={(next) => (mode = next)}
        onResume={handleCopyResume}
        onOpenResume={handleOpenResume}
        {resumeOpening}
        onReveal={handleRevealSource}
        onDelete={handleDelete}
        {resumeCopied}
      />
    {/if}

    {#if jumpNotice}
      <div class="jump-notice" role="status">
        <span>{jumpNotice}</span>
        <button type="button" onclick={reopenSearch}>Search again</button>
        <button type="button" class="dismiss-notice" aria-label="Dismiss notice" onclick={() => (jumpNotice = null)}>×</button>
      </div>
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
          {#if pageOffset > 0}
            <div class="load-earlier-bar">
              <button type="button" disabled={isLoadingEarlier} onclick={loadEarlierMessages}>
                {isLoadingEarlier ? 'Loading earlier messages…' : `Load earlier messages (${pageOffset} before)`}
              </button>
            </div>
          {/if}
          {#each messages as msg, idx (msg.id || `${msg.role}-${msg.time}-${idx}`)}
            {@const globalIndex = pageOffset + idx}
            <div class="message-index-anchor" data-message-index={globalIndex}>
              <MessageBubble
                message={msg}
                sessionRef={appState.selectedSessionRef}
                {mode}
                forceVisible={activeJump?.messageIndex === globalIndex}
                searchKind={activeJump?.messageIndex === globalIndex ? activeJump.kind : null}
              />
            </div>
          {/each}
        {/if}

        {#if isLoadingMore}
          <div class="loading-more-bar">
            <LoadingSpinner size={16} label="Loading more messages…" />
          </div>
        {:else if hasMore && messages.length > 0}
          <!-- Fallback when a failed page leaves too little content to scroll. -->
          <div class="load-earlier-bar load-more-bar">
            <button type="button" onclick={() => loadMoreMessages()}>Load more messages</button>
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

  .message-index-anchor {
    border-radius: 9px;
    transition: background-color 0.25s ease, box-shadow 0.25s ease;
  }

  :global(.message-index-anchor.search-jump-target) {
    background: rgba(245, 158, 11, 0.09);
    box-shadow: 0 0 0 2px rgba(245, 158, 11, 0.45);
  }

  :global(mark.search-jump-match) {
    padding: 0 1px;
    border-radius: 2px;
    background: rgba(245, 158, 11, 0.55);
    color: inherit;
  }

  .load-earlier-bar {
    display: flex;
    justify-content: center;
    padding: 0 0 14px;
  }

  .load-earlier-bar button {
    padding: 5px 12px;
    border: 1px solid var(--border-color);
    border-radius: 14px;
    background: var(--bg-secondary);
    color: var(--text-secondary);
    font-size: 0.75rem;
  }

  .load-earlier-bar button:hover:not(:disabled) { color: var(--text-primary); background: var(--hover-bg); }
  .load-earlier-bar button:disabled { cursor: progress; opacity: 0.7; }

  .jump-notice {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 12px;
    border-bottom: 1px solid rgba(245, 158, 11, 0.4);
    background: rgba(245, 158, 11, 0.1);
    color: var(--text-secondary);
    font-size: 0.75rem;
  }

  .jump-notice span { flex: 1; }
  .jump-notice button {
    padding: 3px 7px;
    border-radius: 4px;
    color: var(--accent-color);
    font-weight: 600;
  }
  .jump-notice button:hover { background: var(--hover-bg); }
  .jump-notice .dismiss-notice { color: var(--text-muted); font-size: 1rem; }

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
