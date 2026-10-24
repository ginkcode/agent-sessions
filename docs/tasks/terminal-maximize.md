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

## TM-02 — Regression gate (pending)

Test the real store, generated backend and panel markup: toggle/restore, persisted
layout, hide/reopen, final close/exit, canceled close, unchanged tabs/sinks/input and
output, accessible button state, both dock positions and plain Escape. Run full
frontend tests, check/build and diff checks. Document native geometry, focus and
PTY sizing as unverified unless actually exercised in the app. No process starts
or termination for verification, installs, private data, remote deployment or push.
