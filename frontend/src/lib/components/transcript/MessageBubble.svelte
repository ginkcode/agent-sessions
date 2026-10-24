<script lang="ts">
  import type { Message, SessionRef, SearchHitKind } from '../../types';
  import { formatAbsoluteTime } from '../../date';
  import { preferences } from '../../stores/preferences.svelte';
  import { manage } from '../../stores/manage.svelte';
  import { toast } from '../../stores/toast.svelte';
  import { translateSettings } from '../../stores/translate.svelte';
  import { translations, translationKey } from '../../stores/translations.svelte';
  import { copyToClipboard } from '../../portable';
  import { copyFailed } from '../../guidance';
  import { appState } from '../../stores/appState.svelte';
  import { messageMarkdown, messageText, visibleTranscriptParts, withTranslation, type TranscriptMode } from '../../transcript';
  import { printMessage } from '../../print';
  import ContextMenu, { type ContextMenuItem } from '../common/ContextMenu.svelte';
  import TextPart from './parts/TextPart.svelte';
  import ReasoningPart from './parts/ReasoningPart.svelte';
  import ToolPart from './parts/ToolPart.svelte';
  import PatchPart from './parts/PatchPart.svelte';
  import CompactionPart from './parts/CompactionPart.svelte';
  import NoticePart from './parts/NoticePart.svelte';
  import ImagePart from './parts/ImagePart.svelte';
  import { isImageFile } from '../../images';
  import { captureSelection, selectionMenuItems, type SelectedText } from '../../selectionTranslation';
  import SelectionTranslation from './SelectionTranslation.svelte';

  interface Props {
    message: Message;
    sessionRef: SessionRef;
    /** Position in the transcript; keys the translation of a message without an id. */
    index?: number;
    mode?: TranscriptMode;
    forceVisible?: boolean;
    searchKind?: SearchHitKind | null;
  }

  let {
    message,
    sessionRef,
    index = 0,
    mode = 'activity',
    forceVisible = false,
    searchKind = null,
  }: Props = $props();

  let formattedTime = $derived(message.time ? formatAbsoluteTime(message.time, preferences.timeZone) : '');
  let isSystem = $derived(message.role === 'system');
  let isUser = $derived(message.role === 'user');
  let isAssistant = $derived(message.role === 'assistant');
  let shownParts = $derived(visibleTranscriptParts(message, mode, forceVisible));

  // Copy and Translate work on what Chat level shows of user and assistant
  // messages; other messages keep the default context menu.
  let markdown = $derived(isUser || isAssistant ? messageMarkdown(message) : '');
  let text = $derived(markdown ? messageText(message) : '');
  let key = $derived(translationKey(sessionRef, message, index, translateSettings.language));
  let translation = $derived(translations.get(key));
  let translated = $derived(translation?.status === 'done' && !translation.showOriginal);

  // A translation replaces the text parts with one, where the first was.
  let visibleParts = $derived(translated && translation ? withTranslation(shownParts, translation.text) : shownParts);

  let body: HTMLDivElement | undefined = $state();
  let menu = $state<{ x: number; y: number; selection?: SelectedText } | null>(null);
  let snippet = $state<SelectedText | null>(null);

  $effect(() => {
    void message;
    void sessionRef;
    void translateSettings.language;
    snippet = null;
    menu = null;
  });

  function openMenu(e: MouseEvent) {
    snippet = null;
    menu = null;
    const selection = window.getSelection();
    if (selection && !selection.isCollapsed) {
      if (!body?.contains(e.target as Node)) return;
      const captured = captureSelection(body, selection);
      if (!captured) return;
      e.preventDefault();
      menu = { x: e.clientX, y: e.clientY, selection: captured };
      return;
    }
    if (!markdown) return;
    e.preventDefault();
    menu = { x: e.clientX, y: e.clientY };
  }

  async function copySelection(text: string) {
    try {
      if (!(await copyToClipboard(text))) throw new Error('The clipboard is not available.');
      toast.show({ title: 'Selection copied', body: 'Copied the selected text.' });
    } catch (err) {
      toast.show(copyFailed('the selection', err));
    }
  }

  async function copyMessage() {
    try {
      if (!(await copyToClipboard(markdown))) {
        toast.show(copyFailed('the message', 'The clipboard is not available.'));
        return;
      }
      toast.show({ title: 'Message copied', body: 'Copied as Markdown, as Chat level shows it.' });
    } catch (err) {
      toast.show(copyFailed('the message', err));
    }
  }

  // Opens what Chat level shows, in the language shown, for editing and printing.
  function print() {
    const parts = visibleTranscriptParts(message, 'chat');
    printMessage({
      sessionRef,
      title: appState.selectedSessionMeta?.title ?? '',
      parts: translated && translation ? withTranslation(parts, translation.text) : parts,
    });
  }

  function translate() {
    void translations.translate(key, text);
  }

  let menuItems = $derived.by((): ContextMenuItem[] => {
    const selected = menu?.selection;
    if (selected) {
      return selectionMenuItems(translateSettings.configured, translateSettings.language,
        () => void copySelection(selected.text),
        () => (snippet = selected),
        () => manage.openSettings('translation'));
    }
    const items: ContextMenuItem[] = [
      { label: 'Copy', onSelect: () => void copyMessage() },
    ];
    if (!translateSettings.configured) {
      items.push(
        { label: 'Translate', onSelect: () => {}, disabled: true, title: 'Set up translation in Settings' },
        { label: 'Set up translation…', onSelect: () => manage.openSettings('translation') },
      );
    } else if (!text) {
      items.push({ label: 'Translate', onSelect: () => {}, disabled: true, title: 'This message has no text to translate' });
    } else if (translation?.status === 'done') {
      items.push(
        { label: translation.showOriginal ? 'Show translation' : 'Show original', onSelect: () => translations.toggleOriginal(key) },
        { label: 'Retranslate', onSelect: translate },
      );
    } else if (translation?.status === 'loading') {
      items.push({ label: 'Translating…', onSelect: () => {}, disabled: true });
    } else {
      items.push({ label: `Translate to ${translateSettings.language}`, onSelect: translate });
    }
    items.push({ label: 'Print / Save as PDF…', onSelect: print });
    return items;
  });
</script>

{#if visibleParts.length > 0}
  <!-- The message actions menu opens on right-click or the context menu key. -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="message-bubble-wrapper"
    class:role-user={isUser}
    class:role-assistant={isAssistant}
    class:role-system={isSystem}
    class:is-meta={message.isMeta}
    oncontextmenu={openMenu}
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
          {#if translation?.status === 'loading'}
            <span class="translation-tag" role="status">Translating…</span>
          {:else if translation?.status === 'done'}
            <span class="translation-tag">
              {translated ? translateSettings.language : 'Original'} ·
              <button type="button" class="translation-toggle" onclick={() => translations.toggleOriginal(key)}>
                {translated ? 'Show original' : 'Show translation'}
              </button>
            </span>
          {:else if translation?.status === 'error'}
            <span class="translation-tag failed" title={translation.error}>
              Translation failed ·
              <button type="button" class="translation-toggle" onclick={translate}>Retry</button>
            </span>
          {/if}
        </div>

        {#if formattedTime}
          <div class="meta-right">
            <span class="time-label" title={message.time}>{formattedTime}</span>
          </div>
        {/if}
      </div>

      <div class="message-parts-body" bind:this={body}>
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
          {:else if part.kind === 'file' && part.file && isImageFile(part.file)}
            <ImagePart file={part.file} {sessionRef} />
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
  {#if menu}
    <ContextMenu x={menu.x} y={menu.y} items={menuItems} ariaLabel={menu.selection ? 'Selection actions' : 'Message actions'} onClose={() => (menu = null)} />
  {/if}
  {#if snippet}
    <SelectionTranslation selection={snippet} language={translateSettings.language} onClose={() => (snippet = null)} />
  {/if}
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
    flex-wrap: wrap;
    gap: 4px 24px;
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

  .translation-tag {
    font-size: 10.4px;
    color: var(--text-muted);
  }

  .translation-tag.failed {
    color: var(--danger, #ef4444);
  }

  .translation-toggle {
    padding: 0;
    border: none;
    background: none;
    font: inherit;
    color: var(--accent-color);
    cursor: pointer;
  }

  .translation-toggle:hover {
    text-decoration: underline;
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
