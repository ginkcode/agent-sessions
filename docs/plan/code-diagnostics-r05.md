# R05 — internal Engine/session owner route

Date: 2026-10-22. **R05 implemented and offline-tested; not an enabled live
feature.** Parent progress remains **19/28**. Stage 1/2 HOLDs remain unchanged.

## Route and boundaries

- `LiveDocuments` owns one `SessionManager` for the connection lifetime, with
  per-project `DiscoveryDocuments` resolvers built from exactly the registrations
  supplied to Manager. Concrete provider/policy keys, whole-proposal current
  approval/root/fingerprint preflight, and R03 transport URI isolation are reused,
  not replaced. There is no per-provider grant or duplicate per-package owner.
- Engine's internal `newDiagnosticsOwnerWithLive` accepts an explicit reviewed
  discovery function (which may call `DiscoverLive`) and fake/approved launcher.
  It composes live registrations with existing saved checkers during asynchronous
  discovery, installs the document owner, and maps `openedDocuments` metadata,
  bounded adapter notices and terminal failure status into Manager snapshots/events.
- Open validates approval, contained path, identity and displayed text before
  asking the owner to attach/reuse a session. The actual displayed text is sent;
  disk is not reread. A new lifecycle after explicit session recovery sends its
  own current text. Existing ChangeDocument generation-reopen behavior remains.
  There is no automatic crash retry, checker trigger, preference or automation.
- Failures retain existing live findings as **stale**, with a bounded path-free
  failure notice. Reopening successfully resets failure metadata. Rejected/ambiguous
  pushes never become successful empty results. Adapter raw errors/bodies are not
  logged or persisted.
- Last document close / attachment detach starts session idle grace, not closing
  one document while peers remain. Within-grace URI reuse stays conservative;
  expiry creates a new transport on subsequent open. Revoke invalidates the
  Manager epoch first, then joins session teardown outside Engine/Manager locks.
  Engine reset/shutdown closes the session owner and joins its processes.

**Production remains checker-only:** `newDiagnosticsOwner` passes no discovery
or launcher. No default real-server launcher or inferred reviewed environment was
introduced. Live RPC/client capability advertisement remains false. Internal
constructor wiring and fake route evidence must not be described as production
activation or real-server support verification. Explicit reviewed host metadata,
launcher integration and release authorization remain prerequisites to activation.

## Evidence

- `live_owner_test.go`: real Manager + LiveDocuments + SessionManager with framed
  fake server; no launch before approval, draft differs from disk, crash retains
  stale findings/status, explicit recovery sends new displayed text, old generation
  rejected, exactly one reused owner, reused-URI empty rejected, fake-clock idle
  expiry starts isolated generation, new publication accepted, revoke invalidates.
- `code_diagnostics_live_owner_test.go`: actual asynchronous Engine discovery,
  composite approval, document operation and snapshot failure route; failing fake
  launch occurs only after approval, `openedDocuments` failure survives snapshot,
  revoke/reset launch nothing. Successful framed sync is tested through the real
  owner/Manager/session, **not yet the complete Engine/App/RPC/frontend route**.
- Existing R02/R04 suites retain concurrent Go/TS/Svelte ownership and composite
  grant regression coverage. R14 owns broader full-route coverage; R15 owns repeated
  teardown instrumentation/native leak evidence. No native/remote claims here.

Validation:

- `go test ./...` — passed.
- `go test -race ./internal/codediagnostics ./internal/engine` — passed.
- `go test ./internal/codediagnostics -run TestLiveOwnerDisplayedRoute -count=10` — passed.
- `go tool golangci-lint run ./internal/codediagnostics/...` — 0 issues.
- `go tool golangci-lint run --new-from-rev=HEAD ./internal/engine/...` — 0 issues
  against the pre-R05 HEAD. Unscoped engine lint exposes existing errcheck,
  unused and unparam issues in unrelated files; those were not changed.
- `git diff --check` — passed.

No installed language tools, downloads, version probes or body logs. No R06
retention/normalization/budget work, frontend work or parent completion claimed.
