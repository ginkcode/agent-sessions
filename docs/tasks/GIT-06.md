# GIT-06 — Fetch, pull and push

- Status: ✅ Done (2026-10-24)
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

## Outcome

- Backend: `files.GitSync(ctx, root, {op})` runs `fetch`, `pull --ff-only
  --no-rebase` and `push`. It returns a summary and the refreshed
  upstream/ahead/behind state. Push uses explicit refspecs with no `+`, so
  even a forcing configured refspec cannot force. A branch with no upstream
  is published with `-u`, to the only remote or `origin`. Other cases are
  refused: ambiguous or missing remotes, a `pushRemote`/`pushDefault` that
  points elsewhere, unborn branches, a detached HEAD, and merges, rebases or
  conflicts in progress (pull and push only).
- Non-interactive: `GIT_TERMINAL_PROMPT=0` is always set. `ssh -o
  BatchMode=yes` is added only when none of `GIT_SSH_COMMAND`, `GIT_SSH` or
  `core.sshCommand` is set. On Unix the commands run without a controlling
  terminal. Agents and credential helpers keep working; prompts fail with
  `authRequired`. A GUI askpass helper set up by the user may still appear.
- 120 s timeout. Cancellation goes from an `AbortSignal` to `CancelGitSync`
  (locally) or `$/cancelRequest` (remote), and the process group is killed.
  The per-repo Git lock is held throughout. Errors carry stable codes plus
  Git's output, capped at 4 KB, and the same codes reach the client over RPC.
  The `gitSync` capability is advertised only together with `gitWrite`.
- Changes pane: Fetch is always shown. **Pull ↓N** appears when behind,
  **Push ↑N** when ahead, and **Publish branch…** when there is no upstream.
  Publishing asks first; a push to an existing upstream does not. While a
  sync runs, the pane shows its progress and a **Cancel** button. A
  cancellation is a notice, not an error. Pull refuses unsaved drafts. Pull
  and push are disabled while a merge, rebase or conflict is in progress.
  After a pull, even a failed or cancelled one, tabs, the file list and
  diagnostics reload. A late reply after a project or host switch is ignored.
- Tests: local bare repositories only. They cover fetch, fast-forward pull,
  refused non-fast-forward pull, push with and without upstream, no-force,
  remote selection, refusals during a merge or rebase, and the
  non-interactive environment: a fake `ssh` checks BatchMode and that there
  is no tty, and that each user SSH override is respected. Also covered:
  cancellation and timeout, RPC code round-trips, store cancellation, draft
  and publish rules, and server-render tests of the sync controls.
