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

export async function copyToClipboard(text: string): Promise<boolean> {
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
