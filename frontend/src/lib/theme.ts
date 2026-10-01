export type ThemeMode = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

// The order the theme button steps through.
export const THEME_MODES: readonly ThemeMode[] = ['system', 'light', 'dark'];

const LABELS: Record<ThemeMode, string> = { system: 'System', light: 'Light', dark: 'Dark' };

export function isThemeMode(value: unknown): value is ThemeMode {
  return typeof value === 'string' && (THEME_MODES as readonly string[]).includes(value);
}

export function nextThemeMode(mode: ThemeMode): ThemeMode {
  const i = THEME_MODES.indexOf(mode);
  return THEME_MODES[(i + 1) % THEME_MODES.length];
}

export function resolveTheme(mode: ThemeMode, systemDark: boolean): ResolvedTheme {
  if (mode === 'system') return systemDark ? 'dark' : 'light';
  return mode;
}

// Tooltip for the theme button: the current mode and what a click switches to.
export function themeButtonTitle(mode: ThemeMode, resolved: ResolvedTheme): string {
  const current = mode === 'system' ? `System (${LABELS[resolved].toLowerCase()})` : LABELS[mode];
  return `Theme: ${current}. Click for ${LABELS[nextThemeMode(mode)]}`;
}
