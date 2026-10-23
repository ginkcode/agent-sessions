import type { AgentID, AgentStatus } from './types';
import type { ContextMenuItem } from './components/common/ContextMenu.svelte';
import { ALL_AGENTS } from './portable';

/** An agent a new session can start with. */
export interface NewSessionChoice {
  agent: AgentID;
  name: string;
  /** Why it cannot start: the host reported its CLI missing. */
  missing?: string;
}

/**
 * The agents to offer: the host's report, or every agent while there is
 * none, so a launch tells what is missing rather than the menu guessing.
 */
export function newSessionChoices(report: AgentStatus[] | null | undefined): NewSessionChoice[] {
  if (!report) return ALL_AGENTS.map((a) => ({ agent: a.id, name: a.label }));
  return report.map((s) =>
    s.installed
      ? { agent: s.agent, name: s.name }
      : { agent: s.agent, name: s.name, missing: s.hint || `${s.name} is not installed on this host` },
  );
}

/**
 * The agent a new session starts with by default: preferred when it can
 * start, else the first one that can, else preferred anyway, whose launch
 * then says what to install.
 */
export function defaultNewSessionAgent(choices: NewSessionChoice[], preferred: AgentID): AgentID {
  const ok = choices.filter((c) => !c.missing);
  return (ok.find((c) => c.agent === preferred) ?? ok[0])?.agent ?? preferred;
}

/** "New Codex Session" menu items, one per agent; the first starts a group. */
export function newSessionMenuItems(
  choices: NewSessionChoice[],
  start: (agent: AgentID) => void,
  opts: { separator?: boolean; disabled?: string } = {},
): ContextMenuItem[] {
  return choices.map((c, i) => ({
    label: `New ${c.name} Session`,
    disabled: Boolean(opts.disabled || c.missing),
    title: opts.disabled || c.missing || `Start a new ${c.name} session in this directory`,
    separator: Boolean(opts.separator && i === 0),
    onSelect: () => start(c.agent),
  }));
}
