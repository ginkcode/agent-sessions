# Code diagnostics frontend (CD-10 / CD-11)

## CD-11 integration

Completed local approval/check/status controls; overall progress **10/28**
(CD-01…CD-08 + CD-10…CD-11). CD-09 production remote identity remains blocked.
The dialog calls asynchronous `show(dir)` and `hide()` on visibility/route changes,
with no check execution on attach, approval, dismissal, save or context changes.
`CodeChecks.svelte` consumes shared selectors and store-owned mutation gating,
including ack/event-gap ownership and immediate fail-closed revocation.
See [CD-11 completion evidence](../tasks/CD-11.md#completion-evidence) for tests
and limitations. Manual keyboard/focus QA has not been performed.


## Dependency exception and release boundary

CD-10 proceeds locally against the committed CD-01 contract and CD-09 RPC
contract/transport recovery tests. **CD-09 remains blocked** on production SSH/WSL
authenticated host identity acquisition and launch binding. Nothing here activates
remote capabilities or treats picker aliases as stable identities. Old/identity-less
remotes expose unsupported diagnostics while file browsing/editing continues.

## Ownership and integration

`frontend/src/lib/stores/codeDiagnostics.svelte.ts` owns only attachment/results
state. It has no FilesStore, CodeMirror, source-text, draft, revision-counter, tab,
selection or history ownership. Existing scanner `Diagnostics` are unchanged.
`codeDiagnostics.ts` defines distinct transport types and pure exact-dedup/count/
saved-basis eligibility helpers. Wails methods pass the stable `expectedHostId`
on every call and normalize errors without changing existing APIs. The browser
mock has a synthetic approval/check/revoke lifecycle, not executable discovery.

Later CD-11 dialog wiring must call `attach(dir, connectionState)` on explicit
project lifecycle, and `hide()`/`detach()` on hide/replacement/disposal. Reopen or
reconnect always attaches anew. Subscribe-before-attach and connection-event
cleanup are internal; explicit `disconnect()` is also available to the route
owner. Connection epoch guards apply independently to replies and callbacks.
No component is wired yet; no check runs automatically on attach or approval.

Full replacement snapshots are authoritative. Run/cancel acknowledgements never
install findings or roll back newer provider runs; previous run IDs in retained
failed/canceled sets are intentionally allowed by the contract. Multipart slices
are concatenated by provider/origin/scope/run identity and published atomically.
Exact duplicates are ignored; metadata/part conflicts, gaps, five-second assembly
timeout, and count/escaped-byte overflow discard buffers and resnapshot. Recovery
is coalesced with at most two requests per attachment until explicit `refresh()`;
there is no unbounded automatic retry. Errors retain stale findings. Equal
resnapshots resolve uncertainty; lower snapshots cannot roll back newer events.
Only explicit newer revoked snapshots may transition an owned generation.

Projections expose providers/coverage/truncation, exact-deduplicated findings with
per-provider source/basis attribution, default error/warning counts and component-
bounded descendant totals. Saved inline eligibility requires current freshness,
a verified equal revision and a clean displayed file; actual range/text validation
remains the renderer's responsibility. File-level and uncertain findings remain
panel/count eligible. Live/document inline support is not advertised.

## Validation (macOS)

- `npm test`: all frontend tests pass, including compiled real rune-store races,
  actual CD-01 Go multipart fixtures, early/duplicate/reversed events, gaps,
  conflicting parts, timeout, recovery exhaustion, encoded size bounds, empty
  replacement, stale/cancel retention, run acknowledgement, revoke races,
  epoch/host/reconnect/old-callback isolation and browser mock lifecycle.
- Wails mapping tests cover every Stage 1 method, exact host/argument order,
  absent bindings and normalized string/structured errors.
- Existing draft/history tests still pass; no editor imports added. Production
  build retains the separate lazy `fileEditor` chunk.
- `npm run check`: zero errors; existing `ignoreDeprecations: "6.0"` warning remains.
- `npm run build`: pass; existing large chunk advisory remains.
- `go test ./internal/codediagnostics/... ./internal/engine/... ./internal/app/... ./internal/rpc/...`: pass.
- Same relevant Go suite with `-race`: pass.

No real-host activation, UI, native platform QA, LSP or document sync is claimed.
