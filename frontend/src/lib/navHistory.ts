import type { GroupMode, SessionRef } from './types';
import { refsEqual } from './manage';

// A place the user visited: the session shown and the group the list was
// narrowed to.
export interface NavEntry {
  ref: SessionRef | null;
  groupKey: string | null;
  groupMode: GroupMode;
}

interface StoredEntry extends NavEntry {
  // Recorded by stepping through the list with the arrow keys; further steps
  // update it instead of adding one entry per session passed.
  step?: boolean;
}

export const NAV_HISTORY_LIMIT = 100;

function sameEntry(a: NavEntry, b: NavEntry): boolean {
  return refsEqual(a.ref, b.ref) && a.groupKey === b.groupKey && a.groupMode === b.groupMode;
}

// True when ref is one of refs or a subagent of one ("<id>/agent-<x>").
function isRemoved(ref: SessionRef | null, refs: SessionRef[]): boolean {
  if (!ref) return false;
  return refs.some((r) => r.agent === ref.agent && (r.id === ref.id || ref.id.startsWith(r.id + '/')));
}

// NavHistory is the back/forward list of one host. entries[index] is where
// the user is now, unless detached: the current place was removed (its
// session deleted) and the user is between entries[index] and
// entries[index + 1] until the next place is recorded.
export class NavHistory {
  private entries: StoredEntry[] = [];
  private index = -1;
  private detached = false;
  private readonly limit: number;

  constructor(limit = NAV_HISTORY_LIMIT) {
    this.limit = limit;
  }

  get canGoBack(): boolean {
    return this.detached ? this.index >= 0 : this.index > 0;
  }

  get canGoForward(): boolean {
    return this.index + 1 < this.entries.length;
  }

  // Records a new place, dropping the places ahead of the current one.
  push(entry: NavEntry): void {
    this.add({ ...entry });
  }

  // Records a place reached with the arrow keys: a run of steps is one entry.
  step(entry: NavEntry): void {
    const current = this.entries[this.index];
    if (!this.detached && current?.step) {
      this.entries[this.index] = { ...entry, step: true };
      this.normalize();
    } else {
      this.add({ ...entry, step: true });
    }
  }

  // Updates the current place after a change the user did not ask for as a
  // move, such as the first session selected on load or the neighbour
  // selected after a delete.
  replace(entry: NavEntry): void {
    if (this.index < 0 && !this.detached) {
      this.add({ ...entry });
      return;
    }
    if (this.detached) {
      this.entries.splice(this.index + 1, 0, { ...entry });
      this.index++;
      this.detached = false;
    } else {
      this.entries[this.index] = { ...entry, step: this.entries[this.index].step };
    }
    this.normalize();
  }

  back(): NavEntry | null {
    if (!this.canGoBack) return null;
    if (this.detached) {
      this.detached = false;
    } else {
      this.index--;
    }
    return this.current();
  }

  forward(): NavEntry | null {
    if (!this.canGoForward) return null;
    this.index++;
    this.detached = false;
    return this.current();
  }

  // Drops every place showing one of refs or one of their subagents.
  remove(refs: SessionRef[]): void {
    if (!refs.length || !this.entries.some((e) => isRemoved(e.ref, refs))) return;
    const kept: StoredEntry[] = [];
    let index = -1;
    let detached = this.detached;
    this.entries.forEach((entry, i) => {
      const removed = isRemoved(entry.ref, refs);
      if (!removed) kept.push(entry);
      if (i === this.index) {
        if (removed) detached = true;
        index = kept.length - 1;
      }
    });
    this.entries = kept;
    this.index = index;
    this.detached = detached;
    this.normalize();
  }

  clear(): void {
    this.entries = [];
    this.index = -1;
    this.detached = false;
  }

  private current(): NavEntry | null {
    const entry = this.entries[this.index];
    if (!entry) return null;
    return { ref: entry.ref, groupKey: entry.groupKey, groupMode: entry.groupMode };
  }

  private add(entry: StoredEntry): void {
    this.entries.splice(this.index + 1);
    this.detached = false;
    const current = this.entries[this.index];
    if (current && sameEntry(current, entry)) {
      current.step = entry.step;
      return;
    }
    this.entries.push(entry);
    this.index = this.entries.length - 1;
    if (this.entries.length > this.limit) {
      this.entries.splice(0, this.entries.length - this.limit);
      this.index = this.entries.length - 1;
    }
  }

  // Merges neighbouring entries that became equal, keeping index on the
  // same place.
  private normalize(): void {
    const merged: StoredEntry[] = [];
    let index = -1;
    this.entries.forEach((entry, i) => {
      const last = merged[merged.length - 1];
      if (!last || !sameEntry(last, entry)) merged.push(entry);
      if (i === this.index) index = merged.length - 1;
    });
    this.entries = merged;
    this.index = index;
  }
}

export type NavDirection = 'back' | 'forward';

type ShortcutEvent = Pick<KeyboardEvent, 'key' | 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey'>;

// The keys that go back or forward: the keyboard's Browser Back/Forward
// keys, Alt+Left/Right on Windows and Linux, and Cmd+[ / Cmd+] on macOS.
export function navigationShortcut(event: ShortcutEvent, mac: boolean): NavDirection | null {
  if (event.key === 'BrowserBack') return 'back';
  if (event.key === 'BrowserForward') return 'forward';
  if (event.shiftKey) return null;
  if (mac) {
    if (!event.metaKey || event.ctrlKey || event.altKey) return null;
    if (event.key === '[') return 'back';
    if (event.key === ']') return 'forward';
    return null;
  }
  if (!event.altKey || event.ctrlKey || event.metaKey) return null;
  if (event.key === 'ArrowLeft') return 'back';
  if (event.key === 'ArrowRight') return 'forward';
  return null;
}

// The mouse's side buttons as DOM buttons 3 and 4. WebKitGTK reports them
// as button 0 and WKWebView not as 3 and 4; the Linux and macOS apps send
// them as MOUSE_NAVIGATE_EVENT.
export function navigationButton(button: number): NavDirection | null {
  if (button === 3) return 'back';
  if (button === 4) return 'forward';
  return null;
}
