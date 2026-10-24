import type { LaunchInfo, TerminalOpenMode, TerminalSettings } from './types';
import { wslDistro } from './hosts';
import { isFullscreenShortcut } from './fullscreen';

/**
 * Where Resume and "Continue in" open agents: the built-in panel unless the
 * user chose an external terminal or the backend has no panel.
 */
export function openMode(info: LaunchInfo): TerminalOpenMode {
  return info.panel && info.open !== 'external' ? 'panel' : 'external';
}

/** Whether an external terminal opens for data from host (undefined is Local). */
export function canOpenExternal(info: LaunchInfo, host: string | undefined): boolean {
  if (!host) return info.terminal;
  return Boolean(info.wsl && wslDistro(host));
}

/** Whether the built-in panel opens for data from host: SSH hosts too. */
export function canOpenPanel(info: LaunchInfo, host: string | undefined): boolean {
  if (!info.panel) return false;
  if (host?.startsWith('wsl:')) return Boolean(info.wsl && wslDistro(host));
  return true;
}

/** Whether Open in terminal works for data from host, where agents open. */
export function canOpenTerminal(info: LaunchInfo, host: string | undefined): boolean {
  return openMode(info) === 'panel' ? canOpenPanel(info, host) : canOpenExternal(info, host);
}

/** Whether the place agents do not open by default works for host. */
export function canOpenOther(info: LaunchInfo, host: string | undefined): boolean {
  return openMode(info) === 'panel' ? canOpenExternal(info, host) : canOpenPanel(info, host);
}

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

/** Whether a key toggles the built-in terminal panel: Ctrl+` everywhere. */
export function isPanelToggle(e: Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey'>): boolean {
  return e.ctrlKey && !e.metaKey && !e.altKey && (e.code === 'Backquote' || e.key === '`');
}

/** Whether a key opens a new shell tab: Cmd+T on macOS, Ctrl+Shift+T elsewhere. */
export function isNewShellShortcut(
  e: Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): boolean {
  const chord = mac ? e.metaKey && !e.ctrlKey && !e.shiftKey : e.ctrlKey && e.shiftKey && !e.metaKey;
  if (!chord || e.altKey) return false;
  // The letter typed, or where the layout types no Latin letters (Cyrillic,
  // Greek) the key in T's place.
  return /^[a-z]$/i.test(e.key) ? e.key.toLowerCase() === 't' : e.code === 'KeyT';
}

/**
 * Whether a key opens a new app window: Cmd+N on macOS, Ctrl+Shift+N
 * elsewhere (Ctrl+N alone is left to the terminal, where it is readline's
 * next-history).
 */
export function isNewWindowShortcut(
  e: Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): boolean {
  const chord = mac ? e.metaKey && !e.ctrlKey && !e.shiftKey : e.ctrlKey && e.shiftKey && !e.metaKey;
  if (!chord || e.altKey) return false;
  return /^[a-z]$/i.test(e.key) ? e.key.toLowerCase() === 'n' : e.code === 'KeyN';
}

/**
 * Which way a key moves between the app's windows: Alt+` for the next,
 * Alt+Shift+` for the previous, on Windows and Linux. The key's position is
 * read, so layouts that type another character there work too. On macOS,
 * Cmd+` is taken natively before the page sees it, so this is always null.
 */
export function windowCycleShortcut(
  e: Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): 'next' | 'previous' | null {
  if (mac || !e.altKey || e.ctrlKey || e.metaKey) return null;
  if (e.code !== 'Backquote' && e.key !== '`' && e.key !== '~') return null;
  return e.shiftKey ? 'previous' : 'next';
}

/**
 * Whether a key starts a new agent session: Cmd+Shift+A on macOS,
 * Ctrl+Shift+A elsewhere (A for agent; Ctrl+Shift+N is New window there).
 */
export function isNewSessionShortcut(
  e: Pick<KeyboardEvent, 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): boolean {
  const chord = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey;
  if (!chord || !e.shiftKey || e.altKey) return false;
  return /^[a-z]$/i.test(e.key) ? e.key.toLowerCase() === 'a' : e.code === 'KeyA';
}

/** The New session shortcut as the platform writes it. */
export function newSessionShortcutLabel(mac: boolean): string {
  return mac ? '⌘⇧A' : 'Ctrl+Shift+A';
}

/** The New window shortcut as the platform writes it. */
export function newWindowShortcutLabel(mac: boolean): string {
  return mac ? '⌘N' : 'Ctrl+Shift+N';
}

/** The New shell shortcut as the platform writes it. */
export function newShellShortcutLabel(mac: boolean): string {
  return mac ? '⌘T' : 'Ctrl+Shift+T';
}

/**
 * The panel tab a key selects, counting from 1: Cmd+1–9 on macOS, Alt+1–9
 * elsewhere. The key's position is read, so layouts that type other
 * characters on the digit row work too. Null for any other key.
 */
export function terminalTabShortcut(
  e: Pick<KeyboardEvent, 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): number | null {
  const chord = mac ? e.metaKey && !e.altKey : e.altKey && !e.metaKey;
  if (!chord || e.ctrlKey || e.shiftKey) return null;
  const match = /^(?:Digit|Numpad)([1-9])$/.exec(e.code);
  return match ? Number(match[1]) : null;
}

/**
 * What a key pressed in a panel terminal does besides typing: copy or paste
 * (Cmd+C/V on macOS, Ctrl+Shift+C/V elsewhere), or 'app' for the app's own
 * shortcuts, which the terminal leaves alone.
 */
export function terminalKeyAction(
  e: Pick<KeyboardEvent, 'type' | 'key' | 'code' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>,
  mac: boolean,
): 'copy' | 'paste' | 'app' | null {
  if (e.type !== 'keydown') return null;
  if (
    isPanelToggle(e) ||
    isNewShellShortcut(e, mac) ||
    isNewWindowShortcut(e, mac) ||
    isNewSessionShortcut(e, mac) ||
    windowCycleShortcut(e, mac) ||
    isFullscreenShortcut(e, mac)
  ) {
    return 'app';
  }
  if (e.key === ',' && (mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey) && !e.altKey) return 'app';
  const key = e.key.toLowerCase();
  const chord = mac ? e.metaKey && !e.ctrlKey && !e.shiftKey : e.ctrlKey && e.shiftKey && !e.metaKey;
  if (!chord || e.altKey) return null;
  if (key === 'c') return 'copy';
  if (key === 'v') return 'paste';
  return null;
}

/** An xterm.js theme: colors and the ANSI palette. */
export interface TerminalTheme {
  background: string;
  foreground: string;
  cursor: string;
  cursorAccent: string;
  selectionBackground: string;
  black: string;
  red: string;
  green: string;
  yellow: string;
  blue: string;
  magenta: string;
  cyan: string;
  white: string;
  brightBlack: string;
  brightRed: string;
  brightGreen: string;
  brightYellow: string;
  brightBlue: string;
  brightMagenta: string;
  brightCyan: string;
  brightWhite: string;
}

const PALETTES = {
  dark: {
    selectionBackground: 'rgba(59, 130, 246, 0.35)',
    black: '#1c1c1e', red: '#f87171', green: '#34d399', yellow: '#fbbf24',
    blue: '#60a5fa', magenta: '#c084fc', cyan: '#22d3ee', white: '#d4d4d8',
    brightBlack: '#71717a', brightRed: '#fca5a5', brightGreen: '#6ee7b7', brightYellow: '#fde68a',
    brightBlue: '#93c5fd', brightMagenta: '#d8b4fe', brightCyan: '#67e8f9', brightWhite: '#fafafa',
  },
  light: {
    selectionBackground: 'rgba(29, 78, 216, 0.22)',
    black: '#0b1220', red: '#b91c1c', green: '#047857', yellow: '#a16207',
    blue: '#1d4ed8', magenta: '#7e22ce', cyan: '#0e7490', white: '#9ca3af',
    brightBlack: '#5f6b7c', brightRed: '#dc2626', brightGreen: '#059669', brightYellow: '#ca8a04',
    brightBlue: '#2563eb', brightMagenta: '#9333ea', brightCyan: '#0891b2', brightWhite: '#d1d5db',
  },
};

/**
 * The panel's terminal theme for the app's light or dark theme; cssVar reads
 * the app's colors, so the terminal matches the panes around it.
 */
export function terminalTheme(resolved: 'light' | 'dark', cssVar: (name: string) => string): TerminalTheme {
  const background = cssVar('--bg-primary') || (resolved === 'dark' ? '#2c2c2b' : '#ffffff');
  const foreground = cssVar('--text-primary') || (resolved === 'dark' ? '#f2f2f2' : '#0b1220');
  return {
    ...PALETTES[resolved],
    background,
    foreground,
    cursor: cssVar('--accent-color') || foreground,
    cursorAccent: background,
  };
}
