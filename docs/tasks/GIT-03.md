# GIT-03 — Branches

- Status: ✅ Done (2026-10-24)
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

## Outcome

- Added bounded branch listing using `for-each-ref`, ordered by newest commit
  timestamp then name, with upstream/current metadata and a 500-entry cap.
  Remote symbolic aliases and branches already represented by local branches
  (same short name or tracking upstream) are omitted.
- Added explicit local create/switch and remote tracking switch, using the
  existing serialized, noninteractive write runner. Full ref validation refuses
  options/revision expressions; local switches disable Git's remote guessing,
  and tracking switches use an exact `refs/remotes/...` ref and explicit local
  name. Git's refusal to overwrite local changes remains visible to callers.
- `GitSwitchRequest` carries `name`, `create`, optional `remote` and `draftPaths`;
  any unsaved draft blocks switching/creation. App bindings preserve the original
  host routing snapshot. RPC list uses GitChanges and switch uses writable-only
  GitWrite; frontend API/types/mock support the same interface.
- The subsequent Changes UI owns discovering live drafts and, after successful
  switch, reloading clean tabs plus tree/status/diagnostics; drafts must not be
  reloaded. The API comment documents that integration contract.
- Temporary-repository tests cover listing/filtering/order, create/local switch,
  remote tracking without inference, local-change refusal, draft refusal, invalid
  names, unborn listing and repo lock/deadline behavior. Parser cap, authorization,
  transport capability/roundtrip, app routing and frontend API tests pass.
- Validation passed: `go test ./...`, `go build ./...`, `go vet ./...`, affected
  package race tests, frontend check (0 errors), full tests (646 passed) and
  production build. Existing ignoreDeprecations and large-chunk warnings remain.
  An initial frontend fixture failure from concurrent Changes UI imports was
  corrected by the coordinating agent; the final full suite passed.
- Ordering uses last commit time (available from refs), not reflog checkout time;
  no network fetch occurs. Branch switching affects the repository/worktree even
  when the session project is a subdirectory, as Git itself does.
