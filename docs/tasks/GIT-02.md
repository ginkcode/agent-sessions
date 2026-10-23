# GIT-02 — Stage, unstage, discard and commit

- Status: ⬜ Not started
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
