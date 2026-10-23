# Code diagnostics — live sync recovery (evidence)

Status: fixed 2026-10-23. Scope: live document bridge (frontend
`liveDocuments.ts`, `CodeDiagnosticsStore.liveSupports`) and additive provider
metadata (`ProviderState.documentExtensions`). No UI components changed.

## Symptom

While browsing Go to File: `main.go` → `mousenav_other.go` → `mousenav_linux.c`,
then reopening and using Refresh/save:

- `main.go` and `mousenav_other.go` showed *Live document synchronization failed
  (invalidArgument)*.
- `mousenav_linux.c` showed *Live document synchronization failed (unsupported)*.

## Cause 1: invalidArgument (orphaned backend documents)

`LiveDocumentBridge.ensure()` treated *any* moment when `liveAttachment` was
undefined as a lost attachment and called `invalidate()`. The store briefly
reports no attachment on the **same** attach token in two cases:

- Refresh → `resync()` sets status `resyncing`;
- a failed store action sets status `error`.

`invalidate()` cleared the bridge's local `docs` map but did not close anything,
because the attachment was not usable. The backend kept those documents open.
When the same token came back, `reconcileAll` opened the same paths again under
new document ids. `documents.go` rejects that with
`invalidArgument "Path already has an open document"`. Recovery
(`MAX_RECOVERIES=2`) could not succeed either, because the orphaned backend
document was never closed.

### Fix

- When the same token is temporarily not live, the bridge **pauses**. It keeps
  the lifecycles, sends nothing, cancels debounce timers, and reports empty
  `basis`/`statuses`. When the token is live again it **resumes**: it reconciles,
  then publishes any typing whose debounce was canceled, unless a save is still
  in flight.
- A **different** token still invalidates and replaces, as before.
- `interrupted()` (connection change) retires the old token, so it is never
  adopted again. Only a new attach token reopens documents.
- While paused, close-type events (tab closed, `cleared`, `removed`,
  `uncertain`, disk `absent`) still close on the owned attachment, so nothing
  leaks. A save that finishes while paused clears `saving` but sends no
  `didSave` for a window the backend cannot verify.
- URI-reuse isolation (`PublicationIsolated`) and approval checks are unchanged.
  The fix does not hide or rewrite any error.

## Cause 2: unsupported for `.c`

The frontend opened every displayed file. No provider owns `.c` files, so the
backend correctly answered `unsupported: No unique language server owns this
file`, and the UI showed that as a failed sync.

### Fix

The backend now advertises which file extensions each `openedDocuments`
provider accepts, as additive metadata
(`documentExtensions`, `omitempty`; see contract §3):

| provider | extensions |
|---|---|
| gopls | `.go` |
| typescript | `.ts .mts .cts .tsx .js .mjs .cjs .jsx` |
| svelte | `.svelte` |

`CodeDiagnosticsStore.liveSupports(path)` returns true only when an available
`openedDocuments` provider declares the file's extension and the path is under
its coverage roots. Files that fail this check are simply not live, not failed.
Older backends that send no metadata keep the previous behavior (open
everything). If the backend rejects a path a provider declared as supported,
that is still reported as a failure. A genuine `invalidArgument` stays visible.

## Real-gopls reproduction

This ran on a disposable temp copy of the repo, using the local gopls v0.24.0
(darwin/arm64), with no downloads. Real `LiveDocuments` + gopls handled the
same browse sequence:

- `main.go` open/change/save/close and reopen: all succeeded.
- `mousenav_other.go`: succeeded. gopls answers with *No packages found*,
  because the build tags exclude this file on the host platform.
- `mousenav_linux.c`: `unsupported: No unique language server owns this file`.
  This is correct backend behavior, and the frontend now gates it out.
- Provider notice: *Some Go diagnostics could not be verified; results are
  partial.*

Real gopls never produced `invalidArgument` on its own. That error came only
from the frontend opening the same path twice, which matches Cause 1.

## Finding for the UI (not changed here)

*No packages found* for build-tag-excluded files (for example
`mousenav_other.go` on darwin) is a legitimate gopls result, not a sync failure.
The UI agent owns how it is presented.

## Tests

- `frontend/test/live-sync-recovery.test.mjs` (11 tests): uses the real files
  and diagnostics stores with a fake backend that enforces the Go manager's
  document rules. It covers browse → reopen, Refresh/resync, typing and tab
  changes during resync, save after refresh, store-error recovery, genuine
  invalidArgument, declared-supported unsupported, older backend without
  metadata, close during resync, connection interruption + reattach, and save in
  flight across resync. 5 of these fail without the fix.
- `internal/codediagnostics/live_sync_recovery_test.go`:
  `TestLiveSyncBrowseSequence` (real `LiveDocuments` + fake LSP, including the
  backend error for a duplicate open and `unsupported` for `.c`), and
  `TestProviderDocumentExtensionsMatchResolvers` (the advertised extensions
  match the backend owner resolution).
- `go test ./...`, `go test -race` for codediagnostics/engine/app/rpc, and
  `npm test` (490/490) all pass. `TestStage1CheckerManagerPipeReplacement`
  (rpc) failed once during a full run. It passed when rerun: once alone, 30×
  normally, 50× with `-race`, and in the full suite. It looks like a
  timing-dependent flake unrelated to this change, because it uses a checker
  provider whose `documentExtensions` is omitted.

The *count 19/28* display is intentionally left unchanged.
