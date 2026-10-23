import type { AgentAttention } from './attention';
import type { AgentID } from './types';
import { ALL_AGENTS } from './portable';

/** What an alert is about: the agent waits for the user, or finished its turn. */
export type AlertKind = 'needs' | 'done';

export function isAlertKind(state: AgentAttention): state is AlertKind {
  return state === 'needs' || state === 'done';
}

const URGENCY: Record<AgentAttention, number> = { needs: 3, done: 2, working: 1, idle: 0, exited: 0 };

/**
 * The state a summary of several tabs shows: needs before done before
 * working; null when none is any of these.
 */
export function mostUrgent(states: Iterable<AgentAttention>): AgentAttention | null {
  let best: AgentAttention | null = null;
  for (const state of states) {
    if (URGENCY[state] > (best ? URGENCY[best] : 0)) best = state;
  }
  return best;
}

/** How a state reads in a tooltip or to a screen reader; empty when it shows nothing. */
export function attentionLabel(state: AgentAttention | null | undefined): string {
  switch (state) {
    case 'needs':
      return 'Needs you';
    case 'done':
      return 'Finished';
    case 'working':
      return 'Working';
    default:
      return '';
  }
}

/** What a summary of the agent tabs says: "An agent needs you". */
export function attentionSummary(state: AgentAttention | null | undefined): string {
  switch (state) {
    case 'needs':
      return 'An agent needs you';
    case 'done':
      return 'An agent finished';
    case 'working':
      return 'An agent is working';
    default:
      return '';
  }
}

/**
 * The text of a desktop notification: "Claude Code needs you" or "Codex
 * finished", with the tab's session title and what the agent said, if it
 * sent a notification of its own.
 */
export function alertText(
  kind: AlertKind,
  agent: AgentID | string | undefined,
  label: string,
  said = '',
): { title: string; body: string } {
  const name = ALL_AGENTS.find((a) => a.id === agent)?.label ?? 'An agent';
  const title = kind === 'needs' ? `${name} needs you` : `${name} finished`;
  const lines = [label.trim(), said.trim()].filter((line, i, all) => line && all.indexOf(line) === i);
  return { title, body: lines.join('\n') };
}

/**
 * Keeps a tab to one alert of each kind per prompt: a tab that needs the
 * user, starts working and needs them again before they type, alerts once.
 * round counts the user's inputs to the tab.
 */
export class AlertLog {
  private sent = new Map<string, { round: number; kinds: Set<AlertKind> }>();

  /** Whether to alert about tab reaching kind in round, recording it when so. */
  take(tab: string, kind: AlertKind, round: number): boolean {
    let entry = this.sent.get(tab);
    if (!entry || entry.round !== round) {
      entry = { round, kinds: new Set() };
      this.sent.set(tab, entry);
    }
    if (entry.kinds.has(kind)) return false;
    entry.kinds.add(kind);
    return true;
  }

  forget(tab: string): void {
    this.sent.delete(tab);
  }
}
