# GIT-06 — Fetch, pull and push

- Status: ⬜ Not started
- Estimate: 3 h
- Dependencies: GIT-04
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Keep the branch in sync with its upstream from the Changes pane.

## Design

- `GitSync(op)`: `fetch`, `pull --ff-only` and `push` (with `-u origin
  <branch>` when there is no upstream yet). Never force. A pull that cannot
  fast-forward reports Git's message and suggests the terminal.
- Credentials: non-interactive (`GIT_TERMINAL_PROMPT=0`, SSH `BatchMode=yes`
  through `GIT_SSH_COMMAND` only when the user has not set one). Keys,
  agents and credential helpers work; prompts fail with a clear message.
- 120 s timeout, cancellable from the UI.
- The branch button gets a sync action showing ahead/behind; Push appears
  when ahead, Pull when behind.

## Done when

- Tests against a local bare repository cover fetch, fast-forward pull,
  refused non-fast-forward pull, push with and without upstream, and the
  non-interactive environment.
