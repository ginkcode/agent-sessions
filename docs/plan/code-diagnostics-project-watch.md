# Approved project watcher + `go-syntax` saved-file provider (2026-10-23)

Scope: one ~4h unit on `feature/code-diagnostics`. Real-time **saved-file** Go
syntax diagnostics for every contained `.go` file, opened or not, driven by an
approved, bounded, local fsnotify project watcher. This **revises the CD-25 policy**
("Chosen policy: **no watchers**", [CD-25](../tasks/CD-25.md#evidence-and-remaining-work-2026-10-22))
because the user explicitly asked for diagnostics that follow ANY project file
changing on disk. The revision covers only the trusted app watcher described
here. CD-19's rejection of every LSP dynamic registration (`client/registerCapability`
for `workspace/didChangeWatchedFiles`) is unchanged, and no server-supplied glob or
path is accepted.

## Pipeline (disk only; never a document)

```
fsnotify (contained dirs only) ─► debounce 200ms / max-wait 2s, bounded pending set
  ─► GoSyntax.Update(paths) | GoSyntax.Full() on topology change / overflow
       (os.OpenRoot reads, go/parser AllErrors|SkipObjectResolution, no exec)
  ─► Manager.PublishInProcess(ctx, "go-syntax", result, externalChanges="watching")
       (rejects stale context, revoked/unapproved, non-in-process provider)
  ─► codeDiagnostics:updated event (existing) ─► tree counts / red names
  ─► frontend: coalesced read-only reconcileFiles() for displayed clean tabs
  └► OnChanges: workspace/didChangeWatchedFiles to an already-running gopls only
```

No hidden `didOpen` is sent for unopened files, nothing is saved, and no file
body or log is persisted. Findings carry `Basis{saved, "sha256:<hex>"}`, the same
revision format `internal/files` uses. A clean tab whose saved revision matches
can therefore draw them inline. Dirty or mismatched tabs keep their draft and only
show the findings in counts and lists.

## Backend

- `internal/codediagnostics/go_syntax.go`: the `GoSyntax` walker/parser.
  - Findings: severity error, source `go syntax`, UTF-16 one-rune ranges that never split a rune or CRLF, at most 10 per file. A file with a BOM gets freshness `uncertain`.
  - Go-ignored names (`.`/`_` prefix, `testdata`) are skipped without a notice.
  - `node_modules`, `vendor`, `dist`, `build` and `target` are skipped and mark the result **partial**.
  - Symlinked directories are never walked. Symlinked files are read only through `os.Root`, so a link that escapes the root fails.
  - Limits: 50,000 entries, 5,000 files, 1 MiB per file, 64 MiB total, depth 32. Reaching any limit marks the result partial. A partial result is never shown as all-clean.
  - `(size, mtime)` caching means unchanged files are not re-parsed.
- `manager.go` / `manager_inprocess.go`: adds `Registration.InProcess`.
  - An in-process provider adds no tool to the approval proposal, so its hash is unchanged. It becomes `unsupported` when the attachment has no executable proposal to approve.
  - It is gated by the exact composite project approval plus `Availability=="available"`.
  - `Run` ignores in-process IDs.
  - `Revoke` resets them to `unwatched`.
  - `InProcessStatus` publishes stale + `unwatched` + a failure notice when the watcher ends abnormally.
- `project_watch.go`: `ProjectWatcher` runs one goroutine, counted in `ownedWork`.
  - Defaults: 4,096 watched dirs, 16,384 walk entries, 1,024 pending paths.
  - Directory topology events, fsnotify overflow errors and a full pending queue all trigger one full rescan. A new directory watch triggers one extra re-walk to catch files created before the watch was added.
  - Failure to add a watch, or reaching a limit, marks the result partial with a notice.
  - If fsnotify cannot start, it scans once and reports `unwatched` + partial.
  - The initial scan runs on the watcher goroutine, never in discovery or rendering.
  - `Stop` cancels the scan and the `OnChanges` context, then joins the goroutine.
- `lsp_documents.go` / `live_owner.go`: `WatchedFiles` sends at most 256 contained events (types 1/2/3) to the **current** gopls session.
  - Create and change paths must resolve inside the root. Delete paths are validated portable joins.
  - It never launches a session and registers nothing.
  - gopls coverage stays `openedDocuments`/`unwatched`, because gopls itself still only reports on opened documents.
- Engine `code_diagnostics_watch.go`:
  - `syncWatcher` runs after discovery (for a persisted grant) and after Approve.
  - `stopWatcher` runs on Revoke and Detach. `close()` stops every watcher before `manager.Close()`.
  - Host or root changes surface as a stale context, which ends the watcher through `InProcessStatus`.
  - `discoverCheckers` registers `go-syntax` only when contained `.go` sources exist.
  - Only the local diagnostics owner runs watchers. Remote stays fail-closed (CD-09).

## Frontend

- `codeDiagnostics.svelte.ts`: when a provider reporting `externalChanges: "watching"` publishes a new runId, `receive` calls the coalesced `reconcileWatched()`.
  - Only one pass runs at a time, plus at most one trailing pass.
  - It calls the existing read-only `files.reconcileFiles()`, which keeps drafts, versions, uncertain saves and the conflict context. Sequence and generation guards drop late replies.
- `CodeChecks.svelte`: adds copy for the `watching` coverage state ("updates when saved files change on disk").

## Tests (fake events/timers/processes; disposable temp dirs only)

- `go_syntax_test.go`: UTF-16/CRLF/emoji ranges, create/fix/delete updates, limits marked partial, escaping symlinks, in-process approval gating, and the no-proposal case reported as unsupported.
- `project_watch_test.go`:
  - Fake fsnotify and fake clock: debounce, coalescing, topology rescan and overflow.
  - Stop/revoke cleanup, plus degraded `unwatched`/partial status.
  - A **real fsnotify** tempdir run: error → fix → create → new subdirectory → delete → cleanup (20× race runs passed).
- `lsp_documents_test.go`: a fake gopls server receives `workspace/didChangeWatchedFiles` with only contained, owned and valid-kind URIs. No session is launched before Attach.
- `internal/engine/code_diagnostics_watch_test.go`: no watcher before approval; it then reaches watching and picks up a fix. Revoke, reapprove, detach and `ResetCodeDiagnostics` cleanup are covered. The test skips if the host offers no checker proposal.
- `frontend/test/code-diagnostics.test.mjs`: a new runId from a watching provider triggers one coalesced read-only reconciliation, while a repeated runId or an unwatched provider does not. Keeping dirty drafts during reconciliation is covered by the existing `live-documents.test.mjs` `reconcileDisplayed` tests, which this change reuses.

## Validation (2026-10-23)

| Check | Result |
|---|---|
| `go test ./...` | pass |
| `go test -race -count=1 ./internal/codediagnostics ./internal/engine` | pass |
| `go vet ./...` | clean |
| `gofmt -l` on touched files | clean (4 unrelated pre-existing files are listed and were left untouched) |
| `npm test` (frontend) | 467/467 pass |
| `npm run check` | 0 errors; 1 pre-existing warning (`ignoreDeprecations`) |
| `npm run build` | pass (existing chunk-size warning) |
| golangci-lint / staticcheck | not run: not installed, and nothing may be installed |

## Real-time behavior and limits (honest)

- **Go, saved files:** real time. Any contained `.go` file that is created, changed, renamed or deleted on disk updates syntax findings, tree counts and red names in about 0.2–2s, whether it is open or not.
- **Go, type and vet errors in unopened files:** not real time. `go vet` stays a manual or approved run, and gopls only covers opened documents. gopls is told about changed files only while its session is running.
- **TypeScript/Svelte:** no unopened-file diagnostics. A watched change only makes clean displayed tabs reconcile. Their checkers still run on demand only, and their coverage stays `unwatched`.
- **Prerequisites:** the watcher runs only on the local host and only after the composite project approval. That approval exists only when the host offers at least one executable checker proposal (go-vet or svelte-check). Otherwise go-syntax reports `unsupported`.
- **Not done:** no demo/mock update, no manual UI or native QA, and no remote watching. Any skipped tree or limit hit shows partial coverage.
- Progress stays **19/28**. CD-25 stays 🟡.
