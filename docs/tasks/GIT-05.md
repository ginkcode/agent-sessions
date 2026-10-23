# GIT-05 — Staged and unstaged diffs

- Status: ✅ Done (2026-10-24)
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

## Outcome

- Backend: `files.ReadGitDiff(ctx, policy, root, rel, basis)` serves `head`
  (working file against `HEAD`), `index` (working file against the index) and
  `staged` (index blob against `HEAD`). It is a separate method rather than a
  `ReadFile` option, so plain reads are untouched. It goes through engine, RPC
  `readGitDiff` (behind a new `gitDiff` capability) and `App.ReadGitDiff`.
  Older hosts reject with `GIT_DIFF_UNSUPPORTED` without a request being sent.
- Staged reads are always read-only, with no revision. A missing working file
  returns `deleted` with the removal hunk. A path in neither the tree, `HEAD`
  nor the index is not-found, not a fake deletion. Unmerged files cannot be
  read staged. Binary and too-large checks cover both sides. Reads wait
  behind Git writes on the per-repo lock. A file deleted in the index but
  recreated on disk gets a whole-file `head` hunk, because Git will not diff
  it against `HEAD`.
- Viewer: opening a row in the Changes pane opens a review snapshot. Staged
  rows open with the `staged` basis and unstaged rows with `index`. The header
  shows a `Diff basis` switch and an **Open working file** button. Snapshots
  are always read-only (`Read-only: Git review snapshot`). They never become
  live language-server documents, drafts, blame or diagnostics targets.
  Opening the same file normally turns the tab back into the working file. A
  deleted snapshot shows its removed lines rather than "empty".
- Tests: Go tests for each basis on modified, added and deleted files,
  including unborn repos, conflicts, protected paths, binary and large files,
  and cancellation. API capability tests, a store test (snapshots are never
  live or editable) and a server-render test for the basis switch.
