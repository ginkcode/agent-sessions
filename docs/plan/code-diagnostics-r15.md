# R15 — owned cleanup instrumentation and repeated lifecycle tests (2026-10-22)

**Complete (offline).** Parent CD-26 stays **partial**; progress stays **19/28**; production
`liveCodeDiagnostics` stays false; Stage 1/2 HOLDs unchanged. R16 not started.

## Instrumentation (counters owned by the object under test; no global matching)

- `SessionManager.OwnedResources()` → `Runs` (session run loops), `ProcessWaiters`
  (goroutines blocked in `SessionProcess.Wait`), `TimerWaiters` (goroutines blocked on an
  injected `After` timer: idle grace and deadlines). Increment before `go`, decrement by
  `defer` in the goroutine. The two process-waiter spawn sites were folded into one
  `waitProcess` helper (same behavior).
- `LSPTransport.LiveGoroutines()` → reader/writer/stderr goroutines still running (3 live).
- `Manager.OwnedWorkers()` → running checker-run and document-release workers.
- `Engine.OwnedCodeDiagnosticsResources()` aggregates the owner's session and manager
  counters for the full route. Test-visible accessors only; no behavior or flag change.
- Process cleanup is asserted on the fake processes the tests create (terminated + reaped).

## Tests

- `codediagnostics/live_cleanup_test.go`: counters observe live resources (so zero means
  something); 5 repeated cycles each of crash, revoke, disconnect, idle expiry (fake
  clock), crash→retry→disconnect and canceled-start→revoke, ending in `Close`; repeated
  shutdown with hide/resume grace waiter; hung-shutdown kill via fake clock; LiveDocuments
  with open documents under revoke/detach/close/crash/disconnect (4 cycles each, fresh
  framed fixture, document workers 0); transport close/EOF/cancel-pending ×10 (pending
  waiters 0, goroutines 0). Waits use bounded condition polling, not sleeps-as-assertions.
- `rpc/code_diagnostics_live_cleanup_test.go`: full Client→Server→Engine route, two
  providers/processes, 3 cycles each of revoke, detach, one-process crash, Engine close and
  RPC client disconnect; owned counters 0 and every fake process terminated.

## Result

No leak found; no production fix needed. Validation: `go test ./...` pass; `go test -race`
codediagnostics/engine/rpc/app pass; `-race -count=10 -run 'Cleanup|RepeatedRevoke'` pass
(plus count=30 for session cleanup); `golangci-lint --new-from-rev=HEAD` on
codediagnostics/engine/rpc 0 issues; frontend `npm test` 451 pass, `check` 0 errors (existing
warning), `build` pass (existing chunk warning); `git diff --check` clean. No command blocked.

## Limits

Counters cover LSP session/transport/manager workers and document release; the checker
Supervisor workers/watchers, Engine event pump and RPC handler goroutines are only covered
by their existing Close/Wait joins (not counted). No native watcher/OS process-tree or
platform leak claim; fake processes only; no real server. No manual UI smoke (R16).
