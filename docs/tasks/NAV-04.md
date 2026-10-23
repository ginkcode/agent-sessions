# NAV-04 — Navigation in the viewer

- Status: ⬜ Not started
- Estimate: 4 h
- Dependencies: NAV-02
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Read code by clicking through it: Cmd/Ctrl+click goes to a definition, hover
shows the type and docs, and back returns to where you were.

## Design

- `SourceView`: while Cmd (macOS) or Ctrl is held, the word under the pointer
  is underlined and the pointer is a hand. Cmd/Ctrl+click asks for the
  definition. The UTF-16 position comes from `caretPositionFromPoint` and
  the text-node walk that `revealSourceRange` uses.
- One result opens the file at the range; several open a small picker list.
  None shows "No definition found" next to the pointer for two seconds.
- Hover: after 400 ms over a word, a tooltip renders the hover Markdown with
  the transcript renderer (escaped, code blocks highlighted), at most 60% of
  the viewer height, scrollable. It closes on pointer leave, scroll or Esc.
- Context menu on a word: Go to Definition (F12), Find References
  (Shift+F12), Copy.
- Navigation history per Go to File dialog: Back (Ctrl+- / Cmd+[) and
  Forward (Ctrl+Shift+- / Cmd+]), plus buttons in the toolbar.
- When navigation is unavailable, the menu items are disabled with the reason
  as a tooltip ("Approve code checks to use navigation").

## Done when

- Unit tests cover position mapping, the history stack and the result
  handling; server-render tests cover the tooltip and menu states.
- Manual check still needed: hover timing and underline paint.
