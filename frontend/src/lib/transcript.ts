import type { FileRef, Message, Part } from './types';

export type TranscriptMode = 'chat' | 'activity' | 'all';

export const transcriptModes: { value: TranscriptMode; label: string; title: string }[] = [
  { value: 'chat', label: 'Chat', title: 'User messages and assistant answers; hides tools, thinking and subagent links' },
  { value: 'activity', label: 'Activity', title: 'Conversation, tools and thinking; hides meta messages' },
  { value: 'all', label: 'All', title: 'All activity, including meta and system messages' },
];

export function isTranscriptMode(value: unknown): value is TranscriptMode {
  return transcriptModes.some((m) => m.value === value);
}

/** Search targets bypass filtering so hidden tool/thinking hits remain reachable. */
export function visibleTranscriptParts(
  message: Message,
  mode: TranscriptMode,
  forceVisible = false,
): Part[] {
  // Loaders can send null parts, e.g. an assistant turn with only empty thinking.
  const parts = message.parts ?? [];
  if (forceVisible || mode === 'all') return parts;
  if (message.isMeta) return [];
  if (mode === 'activity') return parts;
  if (message.role === 'system') return [];
  return parts.filter((part) =>
    (part.kind === 'text' && !!part.text?.trim()) ||
    (part.kind === 'file' && !!part.file) ||
    (message.role === 'assistant' && part.kind === 'notice' && part.text === 'Assistant error')
  );
}

function fileMarkdown(file: FileRef): string {
  const name = file.name || file.path?.split(/[\\/]/).pop() || file.ref || 'Attachment';
  if (!file.path) return name;
  const target = /[\s()<>]/.test(file.path) ? `<${file.path}>` : file.path;
  return `[${name.replace(/[[\]]/g, '\\$&')}](${target})`;
}

/**
 * The message as Markdown, as Chat level shows it: text, attached files as
 * links and the assistant error notice. Empty when Chat level shows nothing.
 */
export function messageMarkdown(message: Message): string {
  const blocks: string[] = [];
  for (const part of visibleTranscriptParts(message, 'chat')) {
    if (part.kind === 'text' && part.text?.trim()) blocks.push(part.text.trim());
    else if (part.kind === 'file' && part.file) blocks.push(fileMarkdown(part.file));
    else if (part.kind === 'notice') blocks.push(`> ${part.text}`);
  }
  return blocks.join('\n\n');
}

/** The message's chat text alone: what Translate sends. */
export function messageText(message: Message): string {
  return visibleTranscriptParts(message, 'chat')
    .filter((part) => part.kind === 'text' && part.text?.trim())
    .map((part) => part.text!.trim())
    .join('\n\n');
}
