# Internal terminal maximization (2026-10-24)

## TM-01 — State, toolbar and layout (complete)

Implemented transient store mode, accessible toolbar control, dock-control lock,
maximized flex sizing and covered-pane hiding/inertness. The same panel stays
mounted. Left grid is disabled only while maximized; dialogs/toasts remain in the
zero-height shell with visible overflow, outside the hidden pane selectors.
Active terminal focus/resize reacts to the toggle with canceled stale animation
frames. Existing terminal suite: 28/28 PASS; full regression gate is TM-02.

Add a top-right Maximize terminal / Restore terminal button. Expanded mode fills
the app workspace, not OS fullscreen. Preserve the mounted terminal views, live
sessions, scrollback, saved dock height and bottom/left position. Hide covered panes
and their resize handles without hiding dialogs/toasts or reconnect status. Disable
dock moves/resizing while maximized. Hide/final-tab removal clears transient mode;
reload does not restore it. Escape continues to terminal programs. Reuse observer
fitting and return focus to the active terminal after a toolbar toggle.

## TM-02 — Regression gate (complete)

Added four tests to `frontend/test/terminal.test.mjs`. In both dock positions,
the real store with the generated mock backend keeps tab IDs, active tab, backend
terminal object, sink output and persisted layout when maximizing. It ignores
height writes while maximized, restores height/position, and reloads unmaximized.
Hide/toggle clears maximization and reopening stays normal. Confirmed final agent
close and final shell exit clear it, a canceled close does not, and plain Escape
still reaches the terminal. The compiled `TerminalPanel` renders the Maximize and
Restore labels with `aria-pressed`, the same keyed view, the inline height only
when docked, and a dock button that is disabled only while maximized. A static
`App.svelte` check confirms one `TerminalPanel`, no left grid and no terminal
splitter while maximized, inert covered panes, hidden pane selectors and mounted
dialogs.

Results: terminal 32/32 and full frontend 712/712 PASS. `npm run check` reports 0
errors, plus the existing `ignoreDeprecations` warning. `npm run build` passes
with the existing >500 kB chunk warning; `git diff --check` is clean.

Not verified: native geometry, paint, focus return, xterm refit and PTY
rows/columns were not exercised in the running app. SSR and static checks do not
establish them. Manual checklist: bottom/left → maximize → type/scroll → resize
window → restore; check process/scrollback continuity, terminal size (`stty
size`), focus, Hide/reopen, dialogs over the maximized terminal, and Escape in
a terminal program.

Original brief:

Test the real store, generated backend and panel markup: toggle/restore, persisted
layout, hide/reopen, final close/exit, canceled close, unchanged tabs/sinks/input and
output, accessible button state, both dock positions and plain Escape. Run full
frontend tests, check/build and diff checks. Document native geometry, focus and
PTY sizing as unverified unless actually exercised in the app. No process starts
or termination for verification, installs, private data, remote deployment or push.
