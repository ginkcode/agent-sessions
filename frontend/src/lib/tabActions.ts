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
