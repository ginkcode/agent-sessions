# R14 — full-route fake-attachment tests (2026-10-22)

**Complete (offline).** Parent CD-26 stays partial; progress stays **19/28**; production
`liveCodeDiagnostics` remains false; release HOLDs unchanged. R11–R13 were skipped as
environment blockers (no approved real gopls / TS LSP / Svelte LS tools). R15 not started.

## Route exercised

`rpc.Client` → pipes/JSON-RPC framing → `rpc.Server` → real `engine.Engine` →
`diagnosticsOwner` event pump → `LiveDocuments`/`SessionManager` → Content-Length LSP
transport → real gopls/TS bridges → `Manager` (real `CodeDiagnosticsStore` equivalent on
the Go side: grant store, trust, manager) → events back through `Server.Notify` →
`Client` emitter. Only the language-server processes and timers are fake.

Test-only seam: `engine.WithInjectedLiveCodeDiagnostics` (`LiveCodeDiagnosticsInjection`:
host, discovery, session options, pump tick). Production never passes it; the default
Engine still has no launcher, `LiveCodeDiagnosticsReady()` is false and capability omitted.
The rpc test wrapper adds only the verified remote-host identity the production Engine
withholds.

## Tests

- `internal/rpc/code_diagnostics_live_route_test.go`: old capability omission (no launch);
  trust denial then approve then revoke (launch count); two providers (gopls + typescript)
  with versioned (current, range, escaped message), unversioned (uncertain, no range),
  late/old/foreign/bad-version pushes, empty clear not crossing ownership; R06 oversize
  message truncated with provider notice/partial; R10 `InvalidateCodeDiagnosticsPath`
  drops findings, sends `didClose`, and a later push cannot resurrect it; crash → failed,
  stale findings, other provider unaffected; sequence gap (40 commits without a pump tick)
  resnapshots to the authoritative latest state. Events are checked through the real pump
  and notify queue.
- `internal/app/code_diagnostics_live_route_test.go`: App → rpc.Client → rpc.Server →
  Engine: wrong expected host rejected before dispatch, approvalRequired before launch,
  approved open reaches the injected launcher, failure visible via App snapshot, offline
  fails closed with no launch, old server (no launcher) returns `unsupported`.
- `frontend/test/live-route-integration.test.mjs`: real `CodeDiagnosticsStore`,
  `FilesStore` and `LiveDocumentBridge`. **This leg is a recorded-sequence replay, not a
  live Go↔Node pipe.** `frontend/test/fixtures/live-route.json` is produced by
  `R14_RECORD=<path> go test ./internal/rpc -run TestRouteRecordFrontendFixture` from the
  Go route above (each call result and every emitted event, including the 2-part truncation
  update); the api object asserts the exact requests the bridge/store send and replays the
  recorded responses. Covers trust denial, two providers, late/unversioned/empty, truncation
  notice, reconcile, failure, old capability (no document sent).

## Results

`go test ./...`, `go test -race` (codediagnostics, engine, rpc, app), scoped
`golangci-lint --new-from-rev=HEAD` (0 issues), frontend `npm test` 451 pass, `npm run
check` 0 errors (existing warning), `npm run build` pass (existing chunk warning).
No gap bug was found; no production behavior changed.

## Limits

Fake processes only; no real-server behavior (R11–R13 blocked). Frontend leg is a replay,
so a protocol change requires re-recording the fixture. Process/timer/goroutine leak
checks (R15) and native UI smoke (R16) not done.
