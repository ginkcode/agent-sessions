# GIT-04 — Changes pane

- Status: ⬜ Not started
- Estimate: 4 h
- Dependencies: GIT-02, GIT-03
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

A Changes mode in the Go to File left pane for the everyday Git loop: review,
stage, commit, switch branch.

## Design

- Mode switch entry **Changes** with a count badge. Ctrl+Shift+G opens it.
- Top: branch button (name, ahead/behind arrows) opening a filterable branch
  menu with "Create branch…", and the in-progress merge or rebase note.
- Commit box: message field (grows to 8 lines), **Commit** (Cmd/Ctrl+Enter),
  a menu with "Commit staged", "Commit all", "Amend last commit". With nothing
  staged, Commit stages all after saying so in the button label ("Commit
  all").
- Sections: **Merge conflicts**, **Staged changes**, **Changes**, each
  collapsible with a count and section actions (stage all, unstage all,
  discard all). Rows: file icon, name, directory, status letter in the Git
  colours, and hover actions (open file, discard, stage or unstage).
- Clicking a row opens the file's diff (GIT-05). Keyboard: Up/Down, Space
  stages or unstages, Enter opens.
- Refreshes after every action, on window focus, when the tree refreshes, and
  after saves.
- Hidden with a note when the host lacks `gitChanges`; write actions disabled
  without `gitWrite`.

## Done when

- Server-render tests cover sections, counts, actions, the commit button
  labels and unsupported states; store tests cover refresh and action
  sequencing.
- Manual check still needed: the full loop in the app.
