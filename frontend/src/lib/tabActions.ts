export type TabCloseAction = 'close' | 'others' | 'right' | 'left';

export const TAB_CLOSE_ACTIONS: { action: TabCloseAction; label: string }[] = [
  { action: 'close', label: 'Close' },
  { action: 'others', label: 'Close Others' },
  { action: 'right', label: 'Close Right' },
  { action: 'left', label: 'Close Left' },
];

export function tabsToClose(ids: string[], target: string, action: TabCloseAction): string[] {
  const index = ids.indexOf(target);
  if (index < 0) return [];
  if (action === 'close') return [target];
  if (action === 'others') return ids.filter((id) => id !== target);
  return action === 'right' ? ids.slice(index + 1) : ids.slice(0, index);
}

/**
 * The file tab a key selects, counting from 1: Cmd+1–9 on macOS, Ctrl+1–9
 * elsewhere. The key's position is read, so layouts that type other
 * characters on the digit row work too. Null for any other key.
 */
export function tabShortcut(
  e: Pick<KeyboardEvent, 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): number | null {
  const chord = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  if (!chord || e.altKey || e.shiftKey) return null;
  const match = /^(?:Digit|Numpad)([1-9])$/.exec(e.code);
  return match ? Number(match[1]) : null;
}
