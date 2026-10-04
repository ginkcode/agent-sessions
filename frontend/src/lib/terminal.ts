import type { TerminalSettings } from './types';

/**
 * The terminal choices Settings lists: Automatic, naming what it opens, then
 * each installed terminal. A chosen terminal that is gone stays listed as
 * not found, so the dropdown never shows a choice the user did not make.
 */
export function terminalOptions(s: TerminalSettings): { value: string; label: string }[] {
  const options = [{ value: '', label: `Automatic (${s.auto ? s.auto.name : 'none found'})` }];
  for (const t of s.options) options.push({ value: t.id, label: t.name });
  if (s.missing && s.selected) options.push({ value: s.selected, label: `${s.selectedName || s.selected} (not found)` });
  return options;
}
