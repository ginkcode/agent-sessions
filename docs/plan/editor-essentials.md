# Editor essentials: search, agents, navigation and Git

Status: planned 2026-10-24 on branch `feature/editor-essentials`, which starts
from `feature/code-diagnostics`. Task briefs are in
[docs/tasks](../tasks/README.md#editor-essentials) (PS, AG, NAV and GIT).

## Goal

Make the app usable as the main coding tool next to the agents, so that daily
work no longer needs VS Code or Zed. Four areas are in scope:

1. **Project search** (PS): find in files across the project, with replace.
2. **Agent sessions** (AG): start a new Claude Code, Codex or OpenCode session
   in a project, link terminal tabs to their sessions, and alert when an agent
   finishes its turn or waits for permission.
3. **Code navigation** (NAV): go to definition, find references and hover,
   using the language servers that code checks already run.
4. **Git changes** (GIT): a Changes view with stage, unstage, discard, commit,
   branch switch and sync.

Out of scope for now: a permanent editor area outside the Go to File dialog,
per-session change review, completion, rename refactoring, debugging, and
task runners.

## Decisions

These were chosen for the user experience first, then for technical
feasibility. Each task brief repeats the parts it needs.

- **Search runs in Go on the host** that holds the project (the desktop app
  for local projects, `agent-sessions-cli serve` for SSH and WSL). It uses
  the same file set as the Go to File listing: Git-tracked and untracked
  files without ignored ones, or the walk with the existing skip list outside
  Git. This gives the same results on every host with nothing to install,
  unlike depending on ripgrep. Go's RE2 regular expressions have no
  backtracking, so a regex cannot hang the host.
- **Search and replace cover unsaved drafts.** The frontend searches draft
  text in memory and shows those results in place of the file on disk.
  Replace applies to a draft as one undoable edit and leaves it unsaved;
  clean files are written through the save path.
- **Replace never overwrites a newer file.** Each file is replaced against the
  revision the search saw. A changed file is reported and skipped, the
  remaining files still apply, and the user can search again. Files the edit
  policy protects (`.git`, credentials, agent data) are never written.
- **New sessions follow the terminal setting.** They open in the built-in
  terminal panel by default and in the external terminal when that is the
  chosen mode, on local, WSL and SSH hosts.
- **Agent state combines two signals.** Session files tell what the agent last
  did (turn finished, tool call waiting for a result). The terminal tells
  whether the agent is drawing output, rang the bell or sent a desktop
  notification sequence (OSC 9 or 777). Neither is reliable alone: transcript
  writes lag behind the TUI, and output stops both when the agent finishes and
  when it waits for approval.
- **Alerts are visible in the app first.** Tab and session-list badges plus a
  count in the window title always work. System notifications are sent only
  while the window is not focused, through each platform's own mechanism,
  and can be turned off in Settings.
- **Navigation uses the same trust as code checks.** It works only for an
  approved project with a running language server, so it never starts a
  process the user has not approved. Positions are taken from the document
  version the server has, so a draft being typed in does not jump to stale
  places. Definitions outside the project (Go standard library, module cache,
  site-packages, `node_modules` outside the root) open read-only, and only for
  paths the language server returned in that answer.
- **Git commands run the user's own `git`**, non-interactively
  (`GIT_TERMINAL_PROMPT=0`, no editor). Hooks run as they do from the command
  line. Nothing forces: no force push, no reset of commits, and discarding
  changes always asks first. Errors show Git's own message.
- **Old remote servers degrade.** Every new remote operation has its own
  capability flag, and the UI hides or disables the action when the flag is
  missing, like `saveFile` and `blame` today.

## Architecture overview

All new backend operations follow the existing path:
`frontend/src/lib/api.ts` → `internal/app` binding → `engine.Backend`
(local `Engine` or `rpc.Client`) → `engine.Service` (known-directory check) →
`internal/files` or `internal/codediagnostics`. Remote calls go through
`internal/rpc` with a new method and a `Capabilities` flag. Long operations
take a run ID and an explicit cancel binding, as `RunCodeDiagnostics` and
`CancelCodeDiagnostics` do.

| Area | Backend home | Frontend home |
|---|---|---|
| Search and replace | `internal/files/search.go`, `replace.go` | Go to File left pane, Search mode |
| New sessions | `provider.NewSessionCommand`, `internal/app/panel.go` | Session list, session header, Go to File header |
| Agent state | provider scanners (`TurnState`), terminal output monitor | `stores/terminals.svelte.ts`, session rows, tabs |
| Navigation | `internal/codediagnostics/lsp_navigation.go` | `SourceView`, `fileEditor.ts`, References panel |
| Git | `internal/files/git_*.go` | Go to File left pane, Changes mode |

## Validation

Each task adds Go unit tests with temporary directories and fake servers, and
frontend server-render or store tests, as the existing features do. Native
GUI checks cannot run in this environment, so every brief lists what still
needs a manual look.
