# GIT-03 — Branches

- Status: ⬜ Not started
- Estimate: 3 h
- Dependencies: GIT-02
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

See and change the current branch without leaving the app.

## Design

- `GitBranches`: local branches with upstream and last commit time, and
  remote branches without a local one, from `git for-each-ref`, sorted by
  recent use. At most 500, with `truncated`.
- `GitSwitch(name, create)`: `git switch name`, `git switch -c name`, or for a
  remote branch `git switch --track origin/name`. Git refuses when local
  changes would be overwritten; that message is shown as it is. Unsaved
  drafts in the app block the switch with a message, since the files under
  them may change.
- After a switch, open tabs reload (drafts are never touched) and the tree,
  status and diagnostics refresh.
- RPC methods under `GitWrite` (switch) and `GitChanges` (list).

## Done when

- Tests cover listing, ordering, create, track a remote branch, refusal with
  local changes, and the draft block.
