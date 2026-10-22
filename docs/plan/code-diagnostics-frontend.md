# Code diagnostics frontend (CD-10 / CD-11 / CD-12 / CD-13 / CD-14 / CD-15)

## CD-15 editor lint

Overall progress is **14/28** (CD-01…CD-08 + CD-10…CD-15); CD-09 remains blocked.
The editor-only lazy runtime uses `@codemirror/lint` state effects and CD-14's
verified saved-range projection. Dirty/conflicted/uncertain drafts and invalid,
stale or document-basis findings never receive saved markers. Every document
transaction (including undo) clears markers immediately; current revision-bound
props must reapply. Cache restoration sanitizes lint state before display without
losing history, selection or scroll. No diagnostic changes edit text or save files.

412 frontend tests pass; check/build pass with existing warnings. Native tooltip,
gutter, keyboard/screen-reader and theme QA remain CD-17. See
[CD-15 evidence](../tasks/CD-15.md#completion-evidence). No next task started.

## CD-14 source indicators

Overall progress is **13/28** (CD-01…CD-08 + CD-10…CD-14); CD-09 remains blocked.
Verified clean saved revisions alone receive UTF-16 source overlays. A pure projection
rejects invalid coordinates, preserves overlapping/exclusive/empty/EOF locations,
and keeps file-level, stale and uncertain findings as attributed plain text messages.
CSS Highlights walk verified source text nodes without altering escaped syntax HTML,
find offsets or selection. Accessible gutter labels coexist with Git/line selection;
keyboard activation opens messages. Unsupported WebViews retain gutters/messages.
Diff, blame, rendered previews and images never receive source overlays. Directory,
active-path and attachment gates prevent cross-file markers; empty replacement clears.
No CodeMirror static imports. See [CD-14 evidence](../tasks/CD-14.md#completion-evidence).
Validation: **409/409 frontend tests**, check 0 errors (existing configuration warning),
build passed with existing chunk advisory and lazy editor chunk. Native paint/theme,
wrap/layout, focus and screen-reader QA remains CD-17; no next task started.

## CD-13 tree counts

Overall progress is **12/28** (CD-01…CD-08 + CD-10…CD-13); CD-09 remains blocked.
Cached shared exact groups now feed file and component-ancestor count maps, summary,
Problems and inline selectors. Every file contributes once per ancestor, independent
of expansion, filtering, listing truncation or tabs. Provider originals survive
shared grouping and single-provider clearing. Tree/search badges expose severity,
stale/uncertain and partial/truncated context independently of Git and ignored tags;
no discovery changes or diagnostic-only nodes are introduced. See
[completion evidence](../tasks/CD-13.md#completion-evidence). Frontend validation:
**403/403 tests**, check 0 errors (existing configuration warning), build passed
(existing chunk-size advisory). Native layout/theme/screen-reader QA remains CD-17.

## CD-12 Problems navigation

Overall progress is **11/28** (CD-01…CD-08 + CD-10…CD-12); CD-09 remains blocked.
The shared collapsible panel groups exact findings, filters severities locally,
retains source attribution and supports native button activation plus scoped arrow
and wrapping next/previous navigation. It is a sibling of the mounted viewer.
`problemNavigation.ts` keeps UTF-16 exclusive ranges separate from line-only blame,
validates revision/freshness/draft eligibility and maps source text-node offsets.
Viewer navigation uses kept tabs and captures request/tab/path/host/root/connection/
attachment generation identity through load/render and lazy editor mount. Dirty or
unverified saved ranges stay panel-only; no saves, reloads or destructive guards are
bypassed. Markdown/SVG/diff locations switch to source. CodeMirror selection is a
selection-only transaction preserving document/undo history.

See [CD-12 completion evidence](../tasks/CD-12.md#completion-evidence). Frontend
validation: **399/399 tests**, check 0 errors (existing config warning), build passed.
Node async/store/controller/SSR and keyboard-handler fixtures are not native QA;
focus/paint/scroll and native activation verification remain CD-16/CD-17 work.

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
