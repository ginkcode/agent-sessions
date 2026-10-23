import type { SessionTurn } from './types';

/**
 * Where an agent tab stands: drawing output (working), waiting for the user
 * (needs), finished its turn (done), ended (exited), or none of these known
 * (idle).
 */
export type AgentAttention = 'idle' | 'working' | 'needs' | 'done' | 'exited';

/** Output this recent means the agent is working: busy agents animate. */
export const QUIET_MS = 1500;

/**
 * Output this soon after input or a resize is the terminal echoing keys or
 * redrawing, not the agent working.
 */
export const PROVOKED_MS = 250;

/** What a tab's terminal and session have shown, as Unix milliseconds (0: never). */
export interface AttentionFacts {
  exited: boolean;
  /** The agent's last output of its own. */
  workedAt: number;
  /** The user's last input; a tab started with a prompt counts its start. */
  inputAt: number;
  /**
   * The agent rang the bell or sent a notification while the user was not
   * looking at the tab, and the user has neither looked at it nor typed in
   * it since.
   */
  signaled: boolean;
  turn?: SessionTurn;
  /** When the catalog last reported the session; 0 when only known from linking. */
  turnAt: number;
}

/**
 * The tab's state, by the first rule that holds:
 * - its process ended: exited;
 * - recent output of its own: working;
 * - a bell or notification since the user last looked or typed: needs;
 * - the session, reported after the user's last input and after the agent
 *   then worked, stops at a tool call without a result (an approval
 *   prompt): needs; finished its turn: done; is still in its turn: working;
 * - otherwise idle: nothing since the user's input says where it stands.
 * Input clears needs and done.
 */
export function agentAttention(f: AttentionFacts, now: number): AgentAttention {
  if (f.exited) return 'exited';
  if (f.workedAt > 0 && now - f.workedAt < QUIET_MS) return 'working';
  if (f.signaled) return 'needs';
  const fresh = f.inputAt > 0 && f.turnAt > f.inputAt && f.workedAt > f.inputAt;
  if (fresh && f.turn === 'tool') return 'needs';
  if (fresh && f.turn === 'done') return 'done';
  if (fresh && f.turn === 'working') return 'working';
  return 'idle';
}

/** A bell, or a desktop notification an agent sent through the terminal. */
export interface TerminalSignal {
  kind: 'bell' | 'notify';
  /** The notification's text; empty for a bell. */
  text: string;
}

const ESC = 0x1b;
const BEL = 0x07;
const CAN = 0x18;
const SUB = 0x1a;
const OSC_MAX = 4096;

type ScanState = 'ground' | 'esc' | 'osc' | 'string';

/**
 * Finds bells and OSC 9 / OSC 777 notifications in a terminal's output,
 * which may split a sequence across chunks. A BEL that ends an OSC
 * sequence terminates it and is not a bell; one inside a DCS, SOS, PM or
 * APC string is part of it. OSC 9 payloads that start with a number and a
 * semicolon (or are only a number) are ConEmu commands such as progress
 * (OSC 9;4), not notifications.
 */
export class TerminalSignalScanner {
  private state: ScanState = 'ground';
  private osc: number[] = [];
  private decoder = new TextDecoder();

  /**
   * Scans a chunk: its signals, and whether it holds anything else, which
   * is the agent drawing.
   */
  scan(data: Uint8Array): { signals: TerminalSignal[]; drew: boolean } {
    const signals: TerminalSignal[] = [];
    let drew = false;
    for (const byte of data) {
      switch (this.state) {
        case 'ground':
          if (byte === BEL) signals.push({ kind: 'bell', text: '' });
          else if (byte === ESC) this.state = 'esc';
          else drew = true;
          break;
        case 'esc':
          drew = this.escape(byte) || drew;
          break;
        case 'osc':
          if (byte === BEL) {
            drew = this.endOsc(signals) || drew;
            this.state = 'ground';
          } else if (byte === ESC) {
            // ESC ends the string: ESC \ is the usual terminator.
            drew = this.endOsc(signals) || drew;
            this.state = 'esc';
          } else if (byte === CAN || byte === SUB) {
            this.osc = [];
            this.state = 'ground';
          } else if (this.osc.length < OSC_MAX) {
            this.osc.push(byte);
          }
          break;
        case 'string':
          if (byte === ESC) this.state = 'esc';
          else if (byte === CAN || byte === SUB) this.state = 'ground';
          break;
      }
    }
    return { signals, drew };
  }

  /** The byte after an ESC; whether it draws. */
  private escape(byte: number): boolean {
    switch (byte) {
      case 0x5d: // ]
        this.state = 'osc';
        this.osc = [];
        return false;
      case 0x50: // P
      case 0x58: // X
      case 0x5e: // ^
      case 0x5f: // _
        this.state = 'string';
        return false;
      case 0x5c: // \, a string terminator
        this.state = 'ground';
        return false;
      case ESC:
        return false;
      default:
        this.state = 'ground';
        return true;
    }
  }

  /** Ends an OSC sequence: a notification is a signal, anything else draws. */
  private endOsc(signals: TerminalSignal[]): boolean {
    const payload = this.decoder.decode(new Uint8Array(this.osc));
    this.osc = [];
    const text = notificationText(payload);
    if (text === null) return true;
    signals.push({ kind: 'notify', text });
    return false;
  }
}

/** The text of an OSC 9 or OSC 777 notify payload; null for other sequences. */
export function notificationText(payload: string): string | null {
  if (payload.startsWith('9;')) {
    const text = payload.slice(2);
    return /^\d+(;|$)/.test(text) ? null : text.trim();
  }
  if (payload.startsWith('777;notify;')) {
    const [title = '', ...body] = payload.slice('777;notify;'.length).split(';');
    return [title.trim(), body.join(';').trim()].filter(Boolean).join(': ');
  }
  return null;
}

/**
 * Whether terminal input holds something the user did, rather than only the
 * terminal's own replies: focus reports, cursor position, device attribute,
 * mode and colour reports.
 */
export function isUserInput(data: string): boolean {
  const replies =
    /\x1b\[[IO]|\x1b\[\d+;\d+R|\x1b\[[?>=]?[\d;]*c|\x1b\[\??[\d;]*n|\x1b\[\??[\d;]*\$y|\x1b\[\d+(?:;\d+)*t|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[^\x1b]*\x1b\\/g;
  return data.replace(replies, '').length > 0;
}

/** What the tracker keeps of a tab. */
interface Tracked extends AttentionFacts {
  scanner: TerminalSignalScanner;
  /** The last input or resize, which provokes output. */
  provokedAt: number;
  /** The text of the last notification, for an alert. */
  signalText: string;
  /** How many times the user has typed into the tab. */
  inputs: number;
  state: AgentAttention;
  timer: ReturnType<typeof setTimeout> | undefined;
}

export interface AttentionClock {
  now(): number;
  setTimeout(fn: () => void, ms: number): ReturnType<typeof setTimeout>;
  clearTimeout(timer: ReturnType<typeof setTimeout>): void;
}

const systemClock: AttentionClock = {
  now: () => Date.now(),
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (timer) => clearTimeout(timer),
};

/**
 * Follows the agent tabs' output, input and sessions, and reports each
 * change of their state.
 */
export class AttentionTracker {
  private tabs = new Map<string, Tracked>();
  private readonly onChange: (id: string, state: AgentAttention) => void;
  private readonly clock: AttentionClock;

  constructor(onChange: (id: string, state: AgentAttention) => void, clock: AttentionClock = systemClock) {
    this.onChange = onChange;
    this.clock = clock;
  }

  /** Starts following a tab; one started with a prompt counts it as input. */
  add(id: string, prompted: boolean): void {
    const now = this.clock.now();
    this.tabs.set(id, {
      exited: false,
      workedAt: 0,
      inputAt: prompted ? now : 0,
      signaled: false,
      turnAt: 0,
      scanner: new TerminalSignalScanner(),
      provokedAt: now,
      signalText: '',
      inputs: 0,
      state: 'idle',
      timer: undefined,
    });
  }

  remove(id: string): void {
    const t = this.tabs.get(id);
    if (t?.timer !== undefined) this.clock.clearTimeout(t.timer);
    this.tabs.delete(id);
  }

  has(id: string): boolean {
    return this.tabs.has(id);
  }

  state(id: string): AgentAttention | undefined {
    return this.tabs.get(id)?.state;
  }

  /** The text of the tab's last notification, empty after a bell. */
  signalText(id: string): string {
    return this.tabs.get(id)?.signalText ?? '';
  }

  /**
   * How many times the user has typed into the tab: each prompt or answer
   * starts what its alerts are about.
   */
  inputs(id: string): number {
    return this.tabs.get(id)?.inputs ?? 0;
  }

  /** Output from the tab; signals count only while the user is not looking at it. */
  output(id: string, data: Uint8Array, viewed: boolean): void {
    const t = this.tabs.get(id);
    if (!t) return;
    const now = this.clock.now();
    const { signals, drew } = t.scanner.scan(data);
    if (drew && now - t.provokedAt >= PROVOKED_MS) t.workedAt = now;
    const last = signals.at(-1);
    if (last && !viewed) {
      t.signaled = true;
      t.signalText = last.text;
    }
    this.update(id, t);
  }

  /** Input sent to the tab; the terminal's own replies are not the user's. */
  input(id: string, data: string): void {
    const t = this.tabs.get(id);
    if (!t || !isUserInput(data)) return;
    t.inputAt = t.provokedAt = this.clock.now();
    t.inputs++;
    t.signaled = false;
    this.update(id, t);
  }

  /** The tab was resized: its redraw is not work. */
  resized(id: string): void {
    const t = this.tabs.get(id);
    if (t) t.provokedAt = this.clock.now();
  }

  /** The user started or stopped looking at the tab. */
  seen(id: string): void {
    const t = this.tabs.get(id);
    if (!t) return;
    t.signaled = false;
    this.update(id, t);
  }

  exited(id: string): void {
    const t = this.tabs.get(id);
    if (!t) return;
    t.exited = true;
    this.update(id, t);
  }

  /**
   * The session's turn: reported is true when the catalog reported the
   * session just now, false when it is only known from linking the tab.
   */
  turn(id: string, turn: SessionTurn | undefined, reported: boolean): void {
    const t = this.tabs.get(id);
    if (!t) return;
    t.turn = turn;
    if (reported) t.turnAt = this.clock.now();
    this.update(id, t);
  }

  private update(id: string, t: Tracked): void {
    const now = this.clock.now();
    const state = agentAttention(t, now);
    // Working ends when the output goes quiet: look again then.
    if (state === 'working' && t.timer === undefined && t.workedAt > 0 && now - t.workedAt < QUIET_MS) {
      t.timer = this.clock.setTimeout(() => {
        t.timer = undefined;
        if (this.tabs.get(id) === t) this.update(id, t);
      }, Math.max(0, t.workedAt + QUIET_MS - now));
    }
    if (state === t.state) return;
    t.state = state;
    this.onChange(id, state);
  }
}
