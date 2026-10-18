import { api, subscribeTerminalData, subscribeTerminalExit } from '../api';
import type {
  AgentID,
  BundleHandoffRequest,
  HandoffRequest,
  SessionRef,
  TerminalSize,
  TerminalTab,
} from '../types';
import { isStaleReply } from '../link';
import { ALL_AGENTS } from '../portable';
import { copyingDrop, dropFailed, openFailed } from '../guidance';
import { wslDistro } from '../hosts';
import { toast } from './toast.svelte';

const STORAGE_KEY = 'agent-sessions:terminal-panel';

export const PANEL_HEIGHT = { min: 120, default: 280, maxShare: 0.7, shellMin: 300 };

/**
 * Where the panel sits: below all the panes, or on the left below the
 * sidebar and the session list, leaving the transcript full height.
 */
export type PanelPosition = 'bottom' | 'left';

export function isPanelPosition(value: unknown): value is PanelPosition {
  return value === 'bottom' || value === 'left';
}

/**
 * A tab whose process ends closes, except a failure this soon after it
 * started: the tab stays so its error can be read.
 */
export const KEEP_FAILED_MS = 3000;

/**
 * The panel height kept within its limits for a window this tall: at least
 * the minimum, at most 70% of the window, and leaving the sessions room.
 */
export function clampPanelHeight(height: number, windowHeight: number): number {
  const max = Math.min(Math.floor(windowHeight * PANEL_HEIGHT.maxShare), windowHeight - PANEL_HEIGHT.shellMin);
  return Math.round(Math.max(PANEL_HEIGHT.min, Math.min(height, max)));
}

/** Decodes a term:data payload to the bytes the terminal writes. */
export function decodeTerminalData(data: string): Uint8Array {
  const binary = atob(data);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

export interface PanelTab extends TerminalTab {
  /** Latest shell-reported directory; dir remains the launch directory. */
  cwd?: string;
  status: 'running' | 'exited';
  exitCode: number | null;
}

/** Where a tab's output goes once its view is mounted. */
export interface TerminalSink {
  write(data: Uint8Array): void;
  exit(code: number): void;
  /** Pastes text as if from the clipboard. */
  paste(text: string): void;
}

function agentName(agent: AgentID | string): string {
  return ALL_AGENTS.find((a) => a.id === agent)?.label ?? 'Agent';
}

/**
 * The built-in terminal panel: its tabs, whether it is open, where it sits
 * and its size.
 * Output arrives as events for every tab and is routed to each tab's view;
 * it is buffered until the view mounts, even when it arrives before the
 * call that started the tab returned.
 */
export class TerminalsStore {
  tabs = $state<PanelTab[]>([]);
  active = $state<string | null>(null);
  open = $state(false);
  height = $state(PANEL_HEIGHT.default);
  position = $state<PanelPosition>('bottom');
  /** A running agent tab whose close waits for confirmation. */
  confirming = $state<string | null>(null);
  private closeQueue: string[] = [];

  /** Bulk closes pause at each running agent rather than skipping confirmation. */
  closeMany(ids: string[]): void {
    this.cancelClose();
    this.closeQueue = [...ids];
    this.continueClosing();
  }

  private continueClosing(): void {
    while (this.closeQueue.length) {
      const id = this.closeQueue.shift()!;
      if (!this.close(id)) return;
    }
  }
  /** The size the views last reported, for a new tab's first size. */
  size: TerminalSize = { cols: 80, rows: 24 };

  private sinks = new Map<string, TerminalSink>();
  private buffered = new Map<string, Uint8Array[]>();
  private exits = new Map<string, number>();
  private starting = 0;
  private startedAt = new Map<string, number>();
  /** Input waiting for a tab's write in flight; null when none waits. */
  private writing = new Map<string, string | null>();
  private unsubscribe: (() => void) | null = null;

  /** Subscribes to terminal output and restores the panel's layout. */
  init(): void {
    if (!this.unsubscribe) {
      const offData = subscribeTerminalData((e) => this.receive(e.id, decodeTerminalData(e.data)));
      const offExit = subscribeTerminalExit((e) => this.exited(e.id, e.code));
      this.unsubscribe = () => {
        offData();
        offExit();
      };
    }
    if (typeof localStorage === 'undefined') return;
    try {
      const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}');
      if (typeof saved.open === 'boolean') this.open = saved.open;
      if (typeof saved.height === 'number' && Number.isFinite(saved.height)) {
        this.height = Math.max(PANEL_HEIGHT.min, Math.round(saved.height));
      }
      if (isPanelPosition(saved.position)) this.position = saved.position;
    } catch {
      // Keep the defaults.
    }
  }

  destroy(): void {
    this.unsubscribe?.();
    this.unsubscribe = null;
  }

  get activeTab(): PanelTab | undefined {
    return this.tabs.find((t) => t.id === this.active);
  }

  /**
   * Resumes a session in a new tab, on the host the session belongs to.
   * dir is the session's directory, shown where the backend cannot tell.
   */
  runResume(ref: SessionRef, dir = ''): Promise<PanelTab | null> {
    return this.start((size) => api.panelResume(ref, size), agentName(ref.agent), dir);
  }

  /** Writes the handoff files and starts the target agent in a new tab. */
  runHandoff(req: HandoffRequest, dir = ''): Promise<PanelTab | null> {
    return this.start((size) => api.panelHandoff(req, size), agentName(req.target), req.cwd || dir);
  }

  runBundleHandoff(req: BundleHandoffRequest, dir = ''): Promise<PanelTab | null> {
    return this.start((size) => api.panelBundleHandoff(req, size), agentName(req.target), req.cwd || dir);
  }

  /** Opens a plain shell on the selected host; failures are shown as a toast. */
  async newShell(cwd = ''): Promise<PanelTab | null> {
    try {
      return await this.start((size) => api.panelShell(cwd, size), 'Shell', cwd);
    } catch (err) {
      toast.show(openFailed(err));
      return null;
    }
  }

  /**
   * Starts a tab and shows the panel with it. Errors reach the caller; null
   * means the host changed while it started.
   */
  private async start(
    launch: (size: TerminalSize) => Promise<TerminalTab>,
    label: string,
    dir: string,
  ): Promise<PanelTab | null> {
    this.starting++;
    let started: TerminalTab;
    try {
      started = await launch({ ...this.size });
    } catch (err) {
      if (isStaleReply(err)) {
        this.starting--;
        this.closeOrphans();
        return null;
      }
      this.starting--;
      throw err;
    }
    this.starting--;
    const tab: PanelTab = { ...started, label, dir: started.dir || dir, status: 'running', exitCode: null };
    const code = this.exits.get(tab.id);
    if (code !== undefined) {
      this.exits.delete(tab.id);
      tab.status = 'exited';
      tab.exitCode = code;
      if (code === 0) {
        // It finished before it could be shown.
        this.buffered.delete(tab.id);
        return tab;
      }
    } else {
      this.startedAt.set(tab.id, Date.now());
    }
    this.tabs = [...this.tabs, tab];
    this.active = tab.id;
    this.setOpen(true);
    return tab;
  }

  /**
   * Ends terminals whose start reply was dropped as stale: their output
   * has no tab to go to.
   */
  private closeOrphans(): void {
    if (this.starting > 0) return;
    const known = new Set(this.tabs.map((t) => t.id));
    for (const id of new Set([...this.buffered.keys(), ...this.exits.keys()])) {
      if (known.has(id)) continue;
      this.buffered.delete(id);
      if (!this.exits.delete(id)) void api.terminalClose(id).catch(() => {});
    }
  }

  /** Connects a tab's view; output buffered so far is written at once. */
  attach(id: string, sink: TerminalSink): () => void {
    this.sinks.set(id, sink);
    const pending = this.buffered.get(id);
    this.buffered.delete(id);
    for (const data of pending ?? []) sink.write(data);
    const tab = this.tabs.find((t) => t.id === id);
    if (tab?.status === 'exited' && tab.exitCode !== null) sink.exit(tab.exitCode);
    return () => {
      if (this.sinks.get(id) === sink) this.sinks.delete(id);
    };
  }

  /**
   * Sends input to a tab. The backend may run calls out of order, so a
   * tab's input goes one write at a time, and what is typed meanwhile is
   * sent together next.
   */
  write(id: string, data: string): void {
    if (this.writing.has(id)) {
      this.writing.set(id, (this.writing.get(id) ?? '') + data);
      return;
    }
    void this.flush(id, data);
  }

  private async flush(id: string, data: string): Promise<void> {
    let next: string | null | undefined = data;
    while (next) {
      this.writing.set(id, null);
      await api.terminalWrite(id, next).catch(() => {});
      next = this.writing.get(id);
    }
    this.writing.delete(id);
  }

  /**
   * Types the paths of files dropped on the panel into the active tab, each
   * pasted on its own as terminal apps do, so agents attach images. An SSH
   * host gets copies first; failures are shown as a toast.
   */
  async dropFiles(paths: string[]): Promise<void> {
    const tab = this.activeTab;
    if (!tab || tab.status !== 'running' || paths.length === 0) return;
    let progress: number | undefined;
    if (tab.host && !wslDistro(tab.host)) {
      toast.show(copyingDrop(tab.host, paths.length));
      progress = toast.current?.id;
    }
    let typed: string[];
    try {
      typed = await api.terminalDropFiles(tab.id, paths);
    } catch (err) {
      toast.show(dropFailed(err));
      return;
    }
    if (progress !== undefined && toast.current?.id === progress) toast.dismiss();
    if (this.tabs.find((t) => t.id === tab.id)?.status !== 'running') return;
    const sink = this.sinks.get(tab.id);
    for (const text of typed) sink?.paste(`${text} `);
  }

  private receive(id: string, data: Uint8Array): void {
    const sink = this.sinks.get(id);
    if (sink) {
      sink.write(data);
      return;
    }
    const pending = this.buffered.get(id);
    if (pending) pending.push(data);
    else this.buffered.set(id, [data]);
  }

  private exited(id: string, code: number): void {
    const tab = this.tabs.find((t) => t.id === id);
    if (!tab) {
      this.exits.set(id, code);
      if (this.starting === 0) this.closeOrphans();
      return;
    }
    const startedAt = this.startedAt.get(id) ?? 0;
    this.startedAt.delete(id);
    const resumeClosing = this.confirming === id;
    if (resumeClosing) this.confirming = null;
    if (code === 0 || Date.now() - startedAt >= KEEP_FAILED_MS) {
      this.remove(tab);
      if (resumeClosing) this.continueClosing();
      return;
    }
    this.tabs = this.tabs.map((t) => (t.id === id ? { ...t, status: 'exited', exitCode: code } : t));
    this.sinks.get(id)?.exit(code);
    if (resumeClosing) {
      this.close(id);
      this.continueClosing();
    }
  }

  /**
   * Closes a tab and ends its process. A running agent asks first: this
   * returns false and sets confirming until confirmClose or cancelClose.
   */
  close(id: string, confirmed = false): boolean {
    const tab = this.tabs.find((t) => t.id === id);
    if (!tab) return true;
    if (tab.status === 'running' && tab.kind === 'agent' && !confirmed) {
      this.confirming = id;
      this.active = id;
      return false;
    }
    if (this.confirming === id) this.confirming = null;
    this.remove(tab);
    // Its exit event finds no tab and is dropped.
    if (tab.status === 'running') void api.terminalClose(id).catch(() => {});
    return true;
  }

  /** Drops a tab, selects its neighbour, and hides the panel when it was the last. */
  private remove(tab: PanelTab): void {
    const index = this.tabs.findIndex((t) => t.id === tab.id);
    this.tabs = this.tabs.filter((t) => t.id !== tab.id);
    this.sinks.delete(tab.id);
    this.buffered.delete(tab.id);
    this.startedAt.delete(tab.id);
    this.writing.delete(tab.id);
    if (this.active === tab.id) {
      this.active = this.tabs[Math.min(index, this.tabs.length - 1)]?.id ?? null;
    }
    if (this.tabs.length === 0) this.setOpen(false);
  }

  confirmClose(): void {
    if (this.confirming) this.close(this.confirming, true);
    this.continueClosing();
  }

  cancelClose(): void {
    this.confirming = null;
    this.closeQueue = [];
  }

  select(id: string): void {
    if (this.tabs.some((t) => t.id === id)) this.active = id;
  }

  /**
   * Selects the tab at a position counting from 1, when the panel shows and
   * has that many tabs; returns whether it did.
   */
  selectNth(n: number): boolean {
    const tab = this.tabs[n - 1];
    if (!this.open || !tab) return false;
    this.active = tab.id;
    return true;
  }

  /** Shows or hides the panel; showing an empty panel opens a shell. */
  async toggle(cwd = ''): Promise<void> {
    if (this.open) {
      this.setOpen(false);
      return;
    }
    this.setOpen(true);
    if (this.tabs.length === 0) await this.newShell(cwd);
  }

  setOpen(open: boolean): void {
    this.open = open;
    this.save();
  }

  setHeight(height: number, windowHeight: number): void {
    this.height = clampPanelHeight(height, windowHeight);
    this.save();
  }

  setPosition(position: PanelPosition): void {
    this.position = position;
    this.save();
  }

  /** Records a view's size so the next tab starts at it. */
  setCwd(id: string, cwd: string): void {
    const tab = this.tabs.find((t) => t.id === id);
    if (tab) tab.cwd = cwd;
  }

  setSize(cols: number, rows: number): void {
    if (cols > 0 && rows > 0) this.size = { cols, rows };
  }

  private save(): void {
    if (typeof localStorage === 'undefined') return;
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ open: this.open, height: this.height, position: this.position }));
    } catch {
      // Storage disabled.
    }
  }
}

export const terminals = new TerminalsStore();
