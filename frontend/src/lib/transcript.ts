import type { Message, Part } from './types';

export type TranscriptMode = 'chat' | 'activity' | 'all';

export const transcriptModes: { value: TranscriptMode; label: string; title: string }[] = [
  { value: 'chat', label: 'Chat', title: 'User messages and assistant answers; hides tools, thinking and subagent links' },
  { value: 'activity', label: 'Activity', title: 'Conversation, tools and thinking; hides meta messages' },
  { value: 'all', label: 'All', title: 'All activity, including meta and system messages' },
];

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
