# NAV-05 — Navigation in the editor

- Status: ⬜ Not started
- Estimate: 3 h
- Dependencies: NAV-04
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

The same navigation while editing, against the draft the server has.

## Design

- `fileEditor.ts`: a `hoverTooltip` extension and a mod-click handler
  (`posAtCoords`), plus F12/Shift+F12 keys, added in `freshState`. Callbacks
  come through `FileEditorOptions`.
- Requests use the live document basis (`documentId`, `version`) only when
  the draft is synced; while a change is still pending, the request waits for
  the next sync (up to 1 s) instead of asking at a stale position.
- Results reuse the viewer's opening, picker and history.

## Done when

- Tests cover the synced and pending-sync cases and the keymap entries.
- Manual check still needed: tooltip placement in CodeMirror.
