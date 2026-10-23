# GIT-02 — Stage, unstage, discard and commit

- Status: ✅ Done (2026-10-24)
- Estimate: 4 h
- Dependencies: GIT-01
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

The Git write operations behind the Changes view, run safely and
non-interactively.

## Design

- One runner for writes: `git -C root --literal-pathspecs` with
  `GIT_TERMINAL_PROMPT=0`, `GIT_EDITOR=true`, `GIT_SEQUENCE_EDITOR=true`, no
  pager, a 60 s timeout (commits can run hooks), Git's stderr returned as the
  error message (trimmed to 4 KB). One write at a time per repository.
- `GitStage(paths)`: `git add -A -- paths`. `GitUnstage(paths)`:
  `git restore --staged -- paths` (or `git rm --cached` before the first
  commit). Empty `paths` means everything.
- `GitDiscard(paths)`: tracked files `git restore --worktree -- paths`;
  untracked files are deleted. The frontend always asks first, naming the
  files. Files with an unsaved draft in the app are refused with a message.
- `GitCommit(message, amend, stageAll)`: the message goes in on stdin
  (`commit -F -`); empty messages are refused. Amend is only offered when
  the last commit is not on the upstream yet. Signing and hooks follow the
  user's Git configuration.
- Paths are validated as relative and inside the project, like `Resolve`.
- RPC methods with capability `GitWrite` (sent only by servers that can
  save), app bindings with the host check used by `SaveFile`, frontend API.

## Done when

- Temporary-repository tests cover each operation, the first-commit cases,
  amend, a failing pre-commit hook (its output is the error), discarding an
  untracked file, path validation and the timeout.

## Outcome

- Added one noninteractive, literal-pathspec runner with a 60-second deadline,
  bounded 4 KB error output, bounded command capture and repository-common-dir
  serialization (including linked worktrees). Hooks and signing configuration
  remain enabled; on macOS/Linux timeout kills the Git process group and hooks.
- Stage/unstage handle explicit files and project-scoped all-files actions;
  unborn unstage removes only index entries, preserving modified worktree files.
  Commit messages use stdin. Amend refuses missing upstreams, shallow history
  and a HEAD already reachable from the local upstream tracking ref.
- Discard restores tracked worktree files from the index and deletes untracked
  files through the existing rooted Delete policy. Whole-request preflight
  refuses protected metadata/credential/store paths, links, special targets and
  caller-supplied unsaved drafts before any discard. Paths cannot escape a
  subdirectory project or enable Git pathspec magic.
- Engine authorization, optional RPC GitWrite (only with SaveFile), selected-host
  app bindings and frontend API/types/mock are wired. Draft discovery and named
  confirmation belong to the subsequent Changes UI; its request can supply
  `draftPaths` now. No Git action was run against the development repo outside
  the authorized task commit.
- Validation passed: `go test ./...`, `go build ./...`, `go vet ./...`, and
  `go test -race ./internal/files ./internal/rpc ./internal/app ./internal/engine`;
  frontend `npm run check` (0 errors), `npm test` (643 passed), and `npm run build`.
  Existing frontend ignoreDeprecations/large-chunk warnings remain.
- Caveats: operations are not transactional; a hook failure after stageAll leaves
  its index changes staged, and a later filesystem failure can leave a partial
  discard. Upstream safety reflects locally available refs, not a network fetch.
  Windows uses CommandContext termination of Git itself, not a process-group
  kill of descendants; shell-hook timeout tests run on macOS/Linux.
