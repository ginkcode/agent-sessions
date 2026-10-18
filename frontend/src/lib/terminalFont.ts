// Names come from the local OS, not the connected SSH/WSL host. Quote the
// family as a CSS string so punctuation cannot introduce extra font families.
export const SYSTEM_MONO_FONTS = 'ui-monospace, SFMono-Regular, Menlo, Consolas, "DejaVu Sans Mono", monospace';

export function isTerminalFont(value: unknown): value is string {
  return typeof value === 'string' && value.length <= 200 && !/[\x00-\x1f\x7f]/.test(value);
}

export function terminalFontFamily(name: string, defaultFamily: string): string {
  if (!isTerminalFont(name) || !name) return defaultFamily;
  return `${JSON.stringify(name)}, ${SYSTEM_MONO_FONTS}`;
}
