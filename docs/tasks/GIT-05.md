# GIT-05 — Staged and unstaged diffs

- Status: ⬜ Not started
- Estimate: 3 h
- Dependencies: GIT-04
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Opening a file from the Changes pane shows the change that row stands for.

## Design

- Today's viewer diff compares the working file with `HEAD`. Add a diff
  basis: `head` (today), `index` (unstaged: working file against the index)
  and `staged` (index against `HEAD`, shown on the staged content read with
  `git show :path`).
- `ReadFile` gains an optional `diffBasis`; staged reads return the index
  content, read-only.
- The viewer's diff toolbar shows which basis is in use and lets the user
  switch between them when a file has both staged and unstaged changes.

## Done when

- Tests cover each basis for modified, added and deleted files.
- Server-render tests cover the basis switch.
