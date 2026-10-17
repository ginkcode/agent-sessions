import type { AgentID, SessionRef } from './types';

export interface BudgetPreset {
  id: string;
  label: string;
  tokens: number;
  description: string;
}

export const BUDGET_PRESETS: BudgetPreset[] = [
  {
    id: 'compact',
    label: 'Compact',
    tokens: 25000,
    description: '≈25k tokens — minimal context for fast handoff',
  },
  {
    id: 'detailed',
    label: 'Detailed',
    tokens: 80000,
    description: '≈80k tokens — balanced working state and recent turns (default)',
  },
  {
    id: 'full',
    label: 'Full',
    tokens: 150000,
    description: '≈150k tokens — generous context with extended history',
  },
  {
    id: 'unlimited',
    label: 'Unlimited',
    tokens: -1,
    description: 'Unlimited — complete session history preserved',
  },
];

export interface AgentOption {
  id: AgentID;
  label: string;
}

export const ALL_AGENTS: AgentOption[] = [
  { id: 'claude-code', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
  { id: 'opencode', label: 'OpenCode' },
];

export function targetAgentsFor(sourceAgent?: string): AgentOption[] {
  return ALL_AGENTS.filter((a) => a.id !== sourceAgent);
}

/**
 * Puts text on the clipboard and reports whether it worked. The desktop app
 * writes it natively: its web view may refuse the page's own clipboard
 * writes (NotAllowedError, e.g. when it decides the page is not focused).
 * The browser build, and a failed native write, use the web clipboard.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    const runtime = (globalThis as any).runtime;
    if (runtime?.ClipboardSetText && (await runtime.ClipboardSetText(text))) return true;
  } catch (err) {
    console.error('Native clipboard write failed:', err);
  }
  if (typeof navigator === 'undefined' || !navigator.clipboard?.writeText) {
    return false;
  }
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch (err) {
    console.error('Clipboard write failed:', err);
    return false;
  }
}

/**
 * The clipboard's text, or '' when it cannot be read. The desktop app reads
 * it natively: its web view may refuse the page's own clipboard reads.
 */
export async function readClipboard(): Promise<string> {
  try {
    const runtime = (globalThis as any).runtime;
    if (runtime?.ClipboardGetText) return (await runtime.ClipboardGetText()) || '';
    if (typeof navigator !== 'undefined' && navigator.clipboard?.readText) {
      return await navigator.clipboard.readText();
    }
  } catch (err) {
    console.error('Clipboard read failed:', err);
  }
  return '';
}
