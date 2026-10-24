# Startup responsiveness — sequential task briefs

Status: original package **6/6 complete**; large-index follow-up **ST-06A/ST-06B complete** 2026-10-24. The original gate did not reproduce the user's continuing >10 s delay. Original completion:  ST-00/ST-01 complete; ST-02 complete (ST-02A engine, ST-02B adapters); ST-03/ST-04 complete; ST-05 integrated gate complete with native GUI/SSH/Windows execution explicitly unperformed. Package closed.
[Plan and status/compatibility contract](../plan/performance-startup.md) ·
[Roadmap](../plan/performance.md) · [Tracker](../../TASKS.md#startup-priority-package).

This priority package precedes performance Milestone 2; it pulls forward a narrow
startup slice, not all of Milestone 4. M1 remains **12/15 complete**, PF-07
partial/blocked, PF-08/PF-10 blocked; its historical evidence stays intact.
The reported roughly 10-second delay is not a measured baseline and its exact
cause is unknown. ST-01 synthetic measurement is recorded in
[../perf.md](../perf.md#st-01--full-engine-startup-baseline-2026-10-24);
the reported exact delay remains undiagnosed. Final integrated evidence:
[ST-05](../perf.md#st-05--integrated-startup-gate-2026-10-24).

## Execution rules and index

Execute strictly **ST-00 → ST-01 → ST-02 → ST-03 → ST-04 → ST-05**, reviewing each
completed task and updating status/evidence before moving on. Every estimate
includes focused tests, is ≤4 developer hours, and is not an elapsed-time promise.
Split a larger task here and in `TASKS.md` **before coding**, especially if ST-02
adapter/lifecycle work exceeds its budget. Do not drop acceptance criteria.
Likely files are integration points, not instructions to create duplicate systems.

| Task | Status | Deliverable | Estimate | Dependency |
|---|---|---|---|---|
| [ST-00](#st-00--document-startup-scope-and-tasks) | Complete | Scope, contracts, briefs and links | 1 h | None |
| [ST-01](#st-01--measure-the-real-startup-path) | Complete | Full-startup baseline; ST-04 measurement blocker | 4 h | ST-00 |
| [ST-02A](#st-02--backend-discovery-readiness-contract) | Complete | Engine lifecycle snapshot/event and outcomes | ≤4 h | ST-01 |
| [ST-02B](#st-02--backend-discovery-readiness-contract) | Complete | Compatible App/RPC adapters and parity | ≤4 h | ST-02A |
| [ST-03](#st-03--truthful-frontend-startup-states) | Complete | Honest readiness and compatible empty/error states | 3 h | ST-02B |
| [ST-04](#st-04--remove-the-measured-startup-blocker) | Complete | Measured fanout; early runner provider publication | ≤4 h | ST-03; ST-01 evidence |
| [ST-05](#st-05--integrated-startup-gate) | Complete | Integration, before/after evidence and limits | 4 h | ST-04 |

Total planned effort: **≤24 h after ST-02 split**. ST-00 is documentation only.
Reviewed task commits are separate, on `feature/improve-performance` with
required attribution; no push. Do not begin Milestone 2 implementation here.

All tasks inherit the plan's [safety/lifecycle invariants](../plan/performance-startup.md#safety-and-lifecycle-invariants):
generated fixtures and explicit provider/cache roots only; no private stores/logs
or private caches, no `scripts/qa-perf-bench.sh`, default-root app launch, installs,
app replacement, process termination or Windows packaging. Preserve commit →
catalog apply → event ordering, ClearCache/Forget/Retire exclusion, atomic reader
snapshots, writer promotion, schema seeding, corrupt-cache recovery and root
isolation. Preserve drafts/selection/navigation and host boundaries. Own and
bound cancellation/background work; no late publications or leaked watchers.

## ST-00 — Document startup scope and tasks

**Status:** complete 2026-10-24. **Estimate:** 1 h. **Dependency:** none.

**Scope:** create the startup plan and these sequential briefs; link both from
`TASKS.md`, both documentation indexes and the performance roadmap. Document
readiness/status compatibility, safety and completion gates without changing code.

**Done when:** ST-00 alone is complete, ST-01…ST-05 pending; startup is ahead of M2;
M1 12/15 history and PF-07/08/10 blockers remain; no exact-delay diagnosis or
native responsiveness claim is made. Documentation links and `git diff --check`
are checked. No implementation, private data access or benchmark run.

## ST-01 — Measure the real startup path

**Status:** complete 2026-10-24. **Estimate:** 4 h. **Dependency:** ST-00.

**Evidence:** [full startup baseline](../perf.md#st-01--full-engine-startup-baseline-2026-10-24):
45 generated full startups across cold/warm writer, warm reader, disabled and
fallback; opt-in backend/frontend content-free observations; deterministic
writer/reader watcher barriers, focused race and frontend checks. No optimization
or native paint claim. ST-04 remained measurement-blocked at that point (resolved in ST-04); sequential ST-02 was next.

**Likely files:** `internal/engine/{engine,cache}.go` and startup tests,
`internal/index`/`internal/watch` test seams, frontend API/store tests,
`docs/perf.md`, this brief. Recheck current code first.

- Add opt-in content-free timings for database open/integrity, catalog restore and
  publication, scan-state restoration, watcher registration, bootstrap completion,
  first successful groups/counts and sessions responses, and frontend usable data.
- Use a common origin within each process; never subtract unrelated frontend and
  backend clocks. Reuse context-local M1 observers; record no paths, prompts or
  session payloads. Distinguish backend queryability, frontend data state and
  native paint.
- Repeat full engine startup with generated fixtures and explicit roots for cold
  application-cache and warm-cache writer/reader, disabled cache and fallback.
  Exclude fixture construction. `TestBenchmarkWarmFirstPaint` covers only
  Open → LoadCatalog → Reset and cannot stand in for full startup.
- Append environment/revision, fixture sizes, distributions, ordering/work counts,
  reproduction commands and limits to `docs/perf.md`. Timings are advisory, not
  arbitrary CI thresholds or proof of the user's exact delay.
- Identify the dominant safe ST-04 intervention and record the decision/evidence;
  if inconclusive, document that and scope further measurement rather than guess.

**Done when:** full-path repeated synthetic baseline and deterministic observation
coverage exist, phase ordering is explicit (writer cache publication already
precedes bootstrap), and a defensible intervention or explicit measurement blocker
is recorded. Focused tests pass; no private roots discovered.

## ST-02 — Backend discovery readiness contract

**Status:** complete 2026-10-24, split before coding: **ST-02A complete**
(engine), **ST-02B complete** (adapters). Adapter negotiation/routing plus
lifecycle and outcome capture exceeded 4 h together. **Dependency:** ST-01.
ST-03 is now complete.

- **ST-02A (≤4 h, complete):** engine-owned snapshot/event, lifecycle and
  attempt invalidation, actual detection/outcome capture, barrier-driven tests.
- **ST-02B (≤4 h, complete):** additive optional Backend/App/Wails/RPC adapters,
  capability, unknown-method/unavailable fallback, client notification routing,
  mocks, generated Wails bindings/TS types and local-vs-RPC parity tests. No
  frontend presentation work. All original adapter acceptance remains required.

**Final contract (ST-02A, `internal/engine/discovery.go`).** Optional
`engine.DiscoveryBackend` with `DiscoveryStatus(context.Context) (DiscoveryStatus, error)`
(not added to `Backend`, so older backends/mocks still compile); `*Engine`
implements it. Event `discovery:status` (`engine.DiscoveryStatusEvent`) carries
the full snapshot on every change. JSON fields:

| Field | Meaning |
|---|---|
| `lifetime` | Increments on every `Start`; `0` only before the first Start (`state: initializing`) |
| `revision` | Increments on every change within a lifetime; order by (`lifetime`, `revision`) |
| `state` | `initializing` → `restoring` (cache path) → `reconciling` → terminal `settled` / `degraded` / `failed` |
| `catalogUsable` | Catalog published and queryable (cached, partial or legitimately empty); independent of `complete` |
| `complete` | Latest attempt reached a terminal state |
| `cacheMode` | `writer`, `reader`, `disabled`, `fallback` (cache unusable, in-memory scan), `none` (zero configured providers) |
| `configuredProviders` | Providers the engine was built with |
| `detectedProviders` | Providers whose `Detect` reported present in the latest attempt |
| `failedProviders` | Providers with a detect or scan error (or refused duplicate ID) in the latest attempt |
| `detectionComplete` | Every configured provider answered `Detect` in the latest attempt |

Outcome rules: `settled` = no provider failures and detection complete
(including successful empty and zero detected/configured providers);
`degraded` = isolated provider failures or incomplete detection with at least one
healthy provider; `failed` = cancellation error, or providers configured and none
healthy. Live-badge errors are not discovery failures. Cache failure followed by
successful in-memory scan is `settled` with `cacheMode: fallback`. “No Agents
Detected” is truthful only for `complete && detectionComplete &&
detectedProviders == 0 && failedProviders == 0`; detection never derives from
catalog rows, FTS progress or `catalog:changed`. Status values carry no paths
or error text; diagnostics remain the detail channel. Reader promotion starts a
new `reconciling` attempt with `cacheMode: writer` in the same lifetime; the
superseded reader attempt cannot publish. Close ends the lifetime: an incomplete
lifetime reads as `failed`/complete without an event, and no later callback can
publish into it or a later lifetime.

**ST-02B adapter contract (names fixed in ST-02A; implemented):** RPC method
`discoveryStatus` (no params, returns `DiscoveryStatus`), capability
`Capabilities.DiscoveryStatus` / JSON `discoveryStatus` (omitempty, absent on
old servers), client forwards notification `discovery:status` like
`index:progress`. Client returns `engine.ErrUnsupportedAction` without the
capability, and maps JSON-RPC `-32601`/`unsupported` to it. Wails App method
`DiscoveryStatus() (app.DiscoveryStatusReply, error)` with JSON
`{available: boolean, status?: DiscoveryStatus}`; `available:false` means
“startup status unavailable” (old host or backend without
`DiscoveryBackend`), never fabricated completion. Offline routes return their
existing offline error. Remote events stay connection-generation scoped.
Wails adds `host` (empty for Local) and `generation` (0 for Local) to both
`DiscoveryStatusReply` and the flattened `app.DiscoveryStatusEvent` payload.
RPC keeps the raw engine snapshot, as for index progress. `Connect`/`Disconnect`
change generation; automatic reconnect currently keeps it, so ST-03 must reset
held status on connection phase changes and refetch on reconnect (the restarted
remote engine begins lifetime 1). No frontend presentation is implemented here.

**ST-02A evidence (2026-10-24).** Detection/scan outcomes are captured by a
context `scan.DiscoveryObserver` invoked inside the existing Runner and
Refresher Bootstrap passes: no extra `Detect`/`Scan`, unchanged scheduling,
worker counts and watcher timing. Status lives in a separate `discoveryOwner`
(read RWMutex; publish mutex orders events; lock order `e.mu` → publish → read),
so reads never wait on `e.mu`. Writer/reader `LoadSnapshot`, commit → catalog
apply → event order, ClearCache/Forget/Retire exclusion, cache recovery and
promotion logic are unchanged; status calls are added after publication only.
Lifecycle change: disabled/fallback bootstrap now runs on the lifetime's
cancellable `runCtx` in `e.bg` with dependencies captured under `e.mu`
(previously an unowned goroutine on Start's ctx), so Close cancels and waits for
it and an old scan cannot use a restarted lifetime's catalog bus.
Tests (`internal/engine/discovery_test.go`, startup barrier test): 24-case
matrix (disabled/writer/reader/fallback × successful empty, zero detected, isolated
detect failure, isolated scan failure, all failed, zero configured); held-startup
status read (watcher barrier, writer and reader: `reconciling`, `catalogUsable`,
not complete) without waiting on `e.mu`; Close cancels a held bootstrap and drops
its late result, then restart settles lifetime 2 (disabled and writer); late
lifetime/attempt rejection; reader promotion with the reader's Detect held at a
barrier — writer attempt settles and the released reader result cannot overwrite
it. No sleeps; channel barriers with a 10 s failure timeout only.
Limitations: status events are emitted synchronously (`beginDiscovery`,
`restoring`, `reconciling` while Start holds `e.mu`); a blocking emitter would
delay Start. Manual `Scan`/watcher refreshes after the attempt do not update
status. No adapter, RPC, Wails, frontend or transport change in ST-02A.

**Recorded checks (final tree):** `go test -race -count=1 ./internal/engine
./internal/scan ./internal/index ./internal/watch` PASS (169.589s / 2.102s /
37.243s / 3.858s); focused discovery/startup barrier race tests, `-count=3`,
PASS (2.347s). `go test ./...` PASS (engine 16.053s); `go vet ./...`,
`go build ./...`, `GOOS=windows go vet ./...` all PASS (exit 0).
`git diff --check` PASS. Initial promotion test timed out because it held Scan
while Retire correctly waited; corrected to hold Detect outside the per-provider
scan exclusion, then repeated race/full gates passed. Frontend checks not run:
no frontend files changed. No private roots, benchmark script, app launch,
install, process termination, Windows packaging, commit or push.

**ST-02B evidence (2026-10-24).** Server advertises `discoveryStatus` only for a
backend implementing `DiscoveryBackend`, dispatches the method or returns
`-32601`; client caps gate requests, maps `-32601`/`unsupported` to
`ErrUnsupportedAction`, decodes and forwards `discovery:status`. App uses one
captured route; missing/unsupported means `available:false`, offline returns its
disconnect error. Local-only and generation emitters retain existing filtering
and add route identity only to discovery events. Generated ignored Wails bindings
used the documented temporary `cmd/agent-sessions/wails.json`/
`wails generate module -tags webkit2_41` flow (config removed). Frontend adds
types, optional-binding wrapper, mock unknown status and subscription helper only.
Tests: in-process RPC with generated fake providers proves forwarded event
ordering and final event/local/RPC snapshot parity over two lifetimes, plus no-capability, `-32601`,
`unsupported` and old-browsing paths; App local/remote/offline/unavailable and
captured-route replies; local/remote/stale-host/return-to-Local event isolation; frontend
wrapper/subscription tests. Event barrier only, no sleeps. Limitations: no ST-03
store/UI, native GUI or real remote/SSH test; automatic reconnect generation unchanged.

**Recorded checks (ST-02B final tree):** `go test -race -count=1
./internal/app ./internal/rpc ./internal/engine` PASS (2.890s / 9.133s /
178.689s); focused discovery race
tests `-count=3` PASS. `go test ./...`, `go vet ./...`, `go build ./...`,
`GOOS=windows go vet ./...` PASS. Frontend `npm test` (683/683), `npm run check`
(0 errors, existing `ignoreDeprecations` warning) and `npm run build` PASS;
`git diff --check` PASS. No changed-code linter is installed.
No private roots, benchmark script, app launch, install, process termination,
Windows packaging, commit or push.

**Likely files:** `internal/engine/{engine,cache,events}.go`, Backend/local/App and
RPC adapters and tests. Finalize concrete DTO/event/capability names here before
ST-03; split before coding if adapter/lifecycle scope exceeds 4 h.

- Engine owns a small lifecycle-scoped snapshot and change event: initializing/
  restoring, reconciling, settled, failed/degraded outcomes; usable catalog is
  separate from discovery completion. Cached/partial rows remain queryable.
- Emit terminal results for successful empty scans, zero detected providers,
  disabled/fallback cache and isolated provider failures. Cache failure followed
  by successful fallback is not fatal. Do not infer provider detection from rows
  or readiness from FTS progress/catalog-change events.
- Reads cannot wait behind lengthy startup work on the engine mutex. Guard status
  transitions/publications by lifetime; cancel/Close/restart rejects old callbacks.
- Expose snapshot/events through existing local Backend/App/RPC routes and
  capability/unknown-method conventions, preserving older-host browsing.

**Done when:** barrier-driven tests cover cached reconciliation, empty/no-provider
success, fallback, isolated failures, cancellation/Close/restart and reader
promotion; status reads remain available during held startup work. Local/RPC
contract tests agree, unsupported-method behavior is specified, ordering and
existing cache recovery/exclusion invariants are retained. Run affected race tests.

## ST-03 — Truthful frontend startup states

**Status:** complete 2026-10-24. **Estimate:** 3 h. **Dependency:** ST-02B (complete ST-02).

**ST-03 evidence (2026-10-24).** Dedicated `discovery.svelte.ts` subscribes to
connection/discovery events before snapshots, orders by lifetime/revision within
host/generation, rejects superseded replies, clears status on phase/route changes
and refetches after reconnect. Snapshot failure/unsupported status explicitly
becomes unavailable, not completed or indefinitely loading. Discovery replies
use their own route/epoch guard rather than the catalog reload epoch.

Both panes distinguish discovering, completed empty with/without detected agents,
failed/degraded outcomes and usable filtered no-matches. Cached rows stay visible
with an Updating indication; unavailable hosts keep legacy browsing behavior.
Terminal events **and snapshots** queue a background catalog reload behind existing
catalog events (successful empty scans need not emit catalog changes). A pending
terminal reload delays definitive empty text; host resets invalidate queued work.
Groups-before-sessions sequencing remains to preserve stale-group reconciliation,
selection and auto-selection; drafts/navigation and startup observation hooks are
unchanged. No backend changes were needed.

Fourteen deterministic Node tests cover held initial status, stale event/snapshot
ordering, pre-subscription event recovery, host/generation isolation, reconnect
lifetime reset, unavailable/error snapshots, rendered pending cache/empty/outcome/
filter states, terminal reload and selection retention. Promises only, no timers.
Final frontend gates: `npm test` **697/697 PASS** (0 failed/skipped/cancelled),
`npm run check` **PASS**, 508 files, 0 errors, 1 existing `ignoreDeprecations`
warning; `npm run build` **PASS** (1.10 s, existing chunk-size warning).
`git diff --check` PASS. Native GUI paint/layout and real remote/SSH use remain
unverified; this is truthful presentation, not a measured startup speedup or a
diagnosis of the reported ten-second delay. ST-04 was then still measurement-blocked.
No private data/cache access, installs, app launch, process termination, packaging,
commit or push. No Go changes/checks required for this frontend-only task.

**Likely files:** `frontend/src/lib/stores/appState.svelte.ts`,
`frontend/src/lib/components/sidebar/GroupTree.svelte`,
`frontend/src/lib/components/sessionlist/SessionList.svelte`, API/types/mock and
related tests. Follow the subscription pattern in `stores/search.svelte.ts`.

- Subscribe before fetching snapshot; newer events supersede old replies. Scope
  requests/events to host and connection generation; reconcile on reconnect.
- Empty pending discovery shows loading/discovering, not definitive absence.
  Cached/new rows show immediately with a lightweight updating indication.
- Distinguish successful empty discovery, failed/degraded outcomes and zero filter
  matches. Retain rows on refresh/error. “No Agents Detected” needs actual
  detection information; use a truthful session-empty message otherwise.
- Older unsupported hosts explicitly mean “startup status unavailable,” never
  fabricated completion or an indefinite spinner. Keep browsing functional.
- Remove groups-before-sessions dependency only if stale-group reconciliation
  stays correct. Preserve selection, drafts and navigation.

**Done when:** tests cover delayed initial status, newer event vs stale snapshot,
missed events, host switch/reconnect, pending nonempty cache, true empty, failures,
old-host fallback, zero filter matches and selection retention. Frontend tests,
check and build pass (or existing/unavailable checks are explicitly reported).
Native GUI paint/layout remains unverified unless separately exercised.

## ST-04 — Remove the measured startup blocker

**Status:** complete 2026-10-24 (measurement gate resolved; one bounded
notification intervention). **Estimate:** ≤4 h.
**Dependency:** ST-03 and the ST-01 measurement/decision.

**Pre-implementation evidence and selection (2026-10-24, HEAD `0c65dd4`).**
Generated full warm-writer startup, equal 256 rows / 4,096 records; nine trials at
0/1,000/10,000 leaf directories (+0/10/100 buckets) under the Claude projects
watch root. Fixture creation, seeding and Close excluded, explicit throwaway
provider/cache roots. Start median **6.115 / 45.189 / 415.195 ms**; watcher setup
**3.349 / 42.276 / 412.209 ms**; publication endpoint **1.768 / 1.800 / 2.023 ms**.
Each tier has zero opens/records/record bytes/encodes and 2,304 checkpoint decodes.
This demonstrates fanout scaling at the synchronous App.OnStartup boundary, not a
10-second diagnosis; queries bypass engine mu and cached rows are already usable.
Owned asynchronous watcher installation would require broader lifecycle and
installation reconciliation work, so it is deliberately not selected here.

**One selected intervention:** notify disabled/fallback startup after each
successful runner catalog application, retaining the final full refresh. Nine
generated two-provider trials per mode, one artificial 300 ms slow scan: first
nonempty query median **1.311 / 1.353 ms**, first catalog event **402.322 /
402.341 ms**, bootstrap **300.290 / 300.350 ms** (disabled/fallback). Eighteen scans
per mode, two final rows/trial, no real sources. Fast rows already exist but their
notification unnecessarily waits for the slowest scan plus the 100 ms bus window.
Use a serial per-run runner callback after catalog application/report updates;
no shared mutable runner callback, extra scans, cache, integrity change, watcher
scheduling or discovery-completion change. Detection remains sequential.

**Acceptance tests fixed before implementation:** channel-held slow provider;
fast catalog rows visible when notification arrives while discovery remains
reconciling; no failed-provider callback; absent/duplicate/cancelled results do not
publish successful application; live-error successful application still notifies;
final report/catalog parity and one scan/provider; successful empty/removal
applications notify; Close cancels/joins held bootstrap and restart uses the new
bus. Keep final terminal/full refresh for empty/failure recovery. Focused runner,
engine startup/discovery/cache/exclusion race coverage and vet; repeat generated
publication measurements. Existing 100 ms coalescing is preserved, so fast events
may still coalesce with other completed work. No adapter/frontend/native elapsed
claim; ST-05 remains pending.

**Implementation evidence (2026-10-24).** `scan.Runner.RunWithOnApplied` invokes
an optional per-pass callback on the collecting goroutine after a successful
`applyLive` and report state/diag update; `Run` is unchanged (nil callback).
`startRunnerDiscovery` passes a callback that calls `NotifyFullRefresh` on the bus
captured under `e.mu`; the final apply/clear/full refresh/terminal status remain.
Cache-backed writer/reader/promotion paths already notify per provider through
`Refresher.onChanged` and are unchanged. Paired final reruns (construction
excluded, nine trials, disabled/fallback): first catalog event median
**401.241 / 401.255 → 100.162 / 100.192 ms**, first nonempty query
1.178 / 1.223 → 1.175 / 1.203 ms (unchanged: rows were already queryable), bootstrap
~300.2 ms unchanged, 18 scans/mode, 2 final rows/trial. The remaining ~100 ms is
the preserved bus debounce. Tests: `internal/scan/publication_test.go`
(nine-outcome callback matrix; channel-held slow provider with release/cancel)
and `TestStartupRunnerPublishesFastProvider` (disabled/fallback × complete/Close-
restart: real bus event and fast rows while held and reconciling; terminal
settled; final two rows; one scan/provider/lifetime; restarted bus emits).
Focused scan/engine race PASS (2.186 s / 4.101 s), full opt-in startup race PASS
(33.251 s), fresh index/watch race PASS (41.753 s / 3.893 s), full scan/engine
package tests PASS (1.060 s / 16.117 s), app regression PASS (1.848 s), vet and
diff check PASS. Frontend untouched; native/transport/hydration timing and full
repository gates remain ST-05 work, not claimed here.
Fanout scaling remains a documented, unselected follow-up. See
[perf.md](../perf.md#st-04--startup-scaling-and-runner-publication-2026-10-24).

Choose **one**, not every candidate:

- If watcher registration/scan-state setup blocks usability, move only eligible
  independent work into owned background initialization. Test race-safe
  publication, installation-race reconciliation, Close/restart cleanup, reader
  snapshot consistency and writer promotion.
- If disabled/fallback cache notification is dominant, reuse runner per-provider
  completion to notify after successful catalog application, without waiting for
  the slowest provider. Test fast/slow providers, failures and final parity.
- If database restore/integrity, transport or frontend sequencing dominates, scope
  that safe fix first. Do not bypass integrity, split atomic `LoadSnapshot` or
  silently undertake a progressive parser/catalog rewrite.

**Done when:** the selected change removes measured blocking work with deterministic
barrier/work-count evidence, preserves existing semantics and eventual final
catalog parity, and has affected race/regression coverage. Split oversized work;
if evidence does not support a safe intervention, leave this task pending/blocked
with the reason rather than claiming an optimization.

## ST-05 — Integrated startup gate

**Status:** complete 2026-10-24 (automated gates; native GUI, real SSH/remote
and Windows execution explicitly unperformed). **Estimate:** 4 h.
**Dependency:** ST-04.

**Evidence (2026-10-24, `7b401fa` + ST-05 test-only changes).**
[Full results](../perf.md#st-05--integrated-startup-gate-2026-10-24). The ST-01
full-startup workload and ST-04 fanout/publication sweeps were repeated with
generated explicit roots (nine trials per mode; isolated final run). Full
startup is unchanged within noise (warm writer Start median 7.330 → 6.774 ms,
publication ~2 ms, first sessions 8.520 → 7.643 ms; identical work counts).
Watcher fanout is **not** improved (Start median 6.461 / 46.471 / 427.056 ms at
0 / 1k / 10k extra directories). The ST-04 disabled/fallback first catalog event
stays improved (401.241 / 401.255 → 100.241 / 100.290 ms with a 300 ms slow
fake), while first nonempty query (~1.3 ms) and discovery completion (~300 ms)
are unchanged. No warm writer/reader event improvement is claimed. Backend
query endpoints, frontend applied state and native paint are separate; only the
first is timed.

Acceptance review mapped every item to existing barrier tests (see the coverage
table in perf.md). One concrete gap was closed by extending
`TestStartupPublicationQueryableDuringWatcherSetup` (writer and reader): after
the held watcher installation is released, discovery reaches `settled` and the
provider-backed final catalog (2 rows, count 2) supersedes the 1-row cached
hint. No discovery correctness bug found; no production code changed.
GroupTree/SessionList keep legacy empty text for **unavailable** status
(intentional old-host compatibility, not detection-backed; native rendering
unverified). Changed-code lint since `0d734ad` found 5 errcheck and 3 gofumpt
issues in ST-01…ST-04 test files; fixed test-only, now 0 issues.

**Recorded checks (final tree):** `go test ./...` PASS (36 ok, 3 no tests);
`go vet ./...`, `go build ./...`, `GOOS=windows go vet ./...` PASS; race engine /
scan / index / watch / app / rpc PASS (171.826 / 2.146 / 37.046 / 3.913 / 2.960 /
9.153 s); opt-in startup race PASS (28.510 s); focused barrier tests `-count=3`
PASS; `golangci-lint --new-from-rev=0d734ad` 0 issues (whole-repo preexisting
findings untouched); frontend `npm test` 697/697, `npm run check` 0 errors (1
existing warning), `npm run build` PASS (existing chunk warning); no frontend
lint configured; `git diff --check` PASS.

**Remaining limits:** the reported roughly 10-second delay is **not diagnosed**;
watcher fanout scaling inside synchronous Start remains a documented follow-up
(owned asynchronous watcher installation); the early event is disabled/fallback
only and debounce-bounded. Native GUI paint/responsiveness, Wails transport,
real SSH/remote, Windows/WSL execution and packaging were not performed. M1
stays 12/15 (PF-07 partial/blocked, PF-08/PF-10 blocked); Milestone 2 design and
task breakdown is the next separate step, with no implementation here.

**Likely files:** startup/backend/RPC/frontend test suites, `docs/perf.md`, startup
plan/briefs, indexes and `TASKS.md` status.

- Repeat ST-01 workloads and append before/after distributions, environment,
  revision, fixtures, work counts, remaining bottlenecks and reproduction commands.
  Preserve M1 and baseline history. Synthetic results do not diagnose the exact
  reported delay or prove native paint.
- Barrier tests prove warm cached rows queryable while the measured unrelated
  slow phase is held, eventual final-catalog parity, successful empty/failed
  terminal status without hanging, fallback, cancellation/Close/restart, reader
  promotion and missed/pre-subscription event recovery. No sleep-based races.
- Exercise local and in-process RPC parity, old-host fallback and the full frontend
  status/host/filter/selection matrix from ST-03.
- Run affected Go race tests, `go test ./...`, `go vet ./...`, `go build ./...`,
  Windows cross-vet where supported, frontend `npm test`, `npm run check`,
  `npm run build`, and changed-code lint using installed tooling. Report existing
  failures/unavailable checks honestly; do not install tools to fill gaps.

**Done when:** deterministic integration gates pass and repeated evidence states
what improved and what remains. Native/manual GUI responsiveness is either
separately exercised on approved generated roots or explicitly unperformed, not
inferred from API timing. Update statuses without completing blocked PF tasks;
Milestone 2 design/task breakdown remains a separate next step, not implementation
in this package.


## ST-06A — Reproduce large-index startup cost

**Status:** complete 2026-10-24; [evidence](../perf.md#st-06a--large-index-startup-baseline-2026-10-24). **Estimate:** ≤4 h. **Dependency:** ST-05.

The user still observes >10 s of empty discovery locally, with a session containing
2,952 messages, while a remote host loads in <3 s. Earlier synthetic fixtures were
too small to test database-size scaling. Do not infer exact device latency from them.

- Add an opt-in generated approximately 300 MB FTS index including a 3,000-message
  transcript. No private database or logs may be read. Measure `Open`, Ping,
  `quick_check(1)`, migrations and `LoadCatalog` separately across repeated opens.
- Use real metadata commits and FTS replacement logic; record actual final size,
  page-cache/WAL conditions and fixture-generation exclusions honestly. No CI timing
  thresholds or machine-dependent duration subtraction.
- Record evidence and select the next safe intervention. Commit independently.

**Done when:** repeatable large-index evidence explains a demonstrable pre-publication
cost; no claim that it fully reproduces the user's device or native UI timing.

## ST-06B — Publish cached hints before full integrity verification

**Status:** complete 2026-10-24; [evidence](../perf.md#st-06b--early-writer-catalog-hints-2026-10-24). **Estimate:** ≤4 h. **Dependency:** ST-06A.

Wails v2.14.0 darwin `frontend.go:248–253` already invokes `OnStartup` on a
separate goroutine before `mainWindow.Run`; no asynchronous desktop startup rewrite
is needed. Catalog queries and discovery snapshots do not take the engine mutex.
Keep synchronous Engine.Start and its existing Close/ClearCache serialization.

Candidate design: while owning the writer lock, read only the current-schema cache
metadata through the existing read-only API and publish a consistent cached snapshot
before the existing full writer open/integrity/migration path. Keep verification
mandatory and the discovery state nonterminal. After verification/recovery, replace
hints with the authoritative catalog and notify again, including an empty rebuild.
A failed early read must not block normal startup or trigger destructive recovery.
Do not split the established reader snapshot or alter promotion ordering.

**Acceptance:** deterministic barrier inside verification proves cached groups/counts/
sessions can be queried and catalog event delivered beforehand; failed/corrupt early
read falls through safely; successful integrity recovery removes stale hints; writer
failure followed by fallback cannot leave ghost hints; cancellation/Close/restart and
reader promotion invariants preserved. Repeated generated large-index timing must
show publication no longer waits for the full integrity scan. Native responsiveness
remains unverified. Split before coding if design exceeds this scope.


## ST-07 — Remote startup responsiveness (2026-10-24)

Approved follow-up: remote initialize currently hides early cached hints behind
synchronous Engine.Start. Local Start stays synchronous; integrity/recovery stays
mandatory. Generated fixtures only, no private stores or real host connections.

| Task | Status | Scope | Estimate |
|---|---|---|---|
| ST-07A | Complete | Document readiness/admission/lifecycle contracts | 1 h |
| ST-07B | Complete | Atomic discovery snapshot/change subscription | ≤3 h |
| ST-07C | Complete | Prepared RPC startup, early initialize and method gate | ≤4 h |
| ST-07D | Complete | CLI wiring and generated real-engine integration | ≤3 h |
| ST-07E | Complete | Opt-in content-free remote phase timing | ≤3 h |
| ST-07F | Complete | Integrated regression gate and repeated evidence | ≤3 h |

Execute sequentially; commit per completed task, no push. Split oversized work
before coding. Each scope includes focused tests.

### ST-07B acceptance
Add a snapshot-plus-change-channel watcher under discovery's existing mutex.
Begin/update/stop wake subscribers with no missed event or stale-lifetime readiness.
Use the writerOpen barrier to prove CatalogUsable precedes verification; cover
restart and canceled/failed/no-cache cases. Preserve local Start/Close semantics.

### ST-07C acceptance
Keep SetStarter compatible; add prepared backend/capabilities/roots/start composition.
Do not wrap Backend or lose optional interfaces. Apply client env before preparation;
compute diagnostics host identity/capabilities before Start can take engine.mu.
Register an owned startup task before launching with Serve-lifetime cancellation.
Initialize returns at new-lifetime CatalogUsable or successful Start completion;
without discovery observation wait for completion. Errors before readiness fail
initialize; errors afterward terminate connection. Duplicate initialize stays refused.

Only ping, listGroups, listSessions, agentCounts, getSessionMeta, discoveryStatus
may run before full Start completes. Every other method waits cancellation-aware
outside backend locks (deny by default). Test cancelRequest and unknown methods.
Shutdown cancels Serve, closes transport, joins handlers/startup, then expires
diagnostics; CLI closes engine last. No scheduled Start after Close.

### ST-07D acceptance
Production serve switches to preparation; publish engine ownership before startup.
Preserve env-root ordering, two clients/shared cache, idle timeout and host identity.
Real engine with generated cached rows and a held startup phase: initialize and
catalog requests succeed while full Start is held; other requests wait. Disconnect
must cancel/join startup and release cache ownership. Existing synchronous starters,
reader snapshots/promotion and local restart remain unchanged. Test pre-connected
events recovered by discovery snapshot and host/generation isolation.

### ST-07E acceptance
Context-local, opt-in, content-free phases: probe, bundle selection/hash, deploy,
preface, initialize, server preparation/identity/catalog-ready/full Start, first
successful groups/counts/sessions. No default logging, payloads, errors, host/path
identifiers or cross-process timestamp subtraction. Generated fake SSH/pipe tests
only; distinguish empty responses from actual usable rows. Record distributions,
not CI latency thresholds. Reuse StartupObservation rather than global hooks.

### ST-07F acceptance
Repeat synthetic before/after measurements in docs/perf.md; deterministic barriers
prove catalog availability independent of verification, not only a faster handshake.
Affected race tests; full Go test/vet/build, Windows cross-vet, changed-code installed
lint and diff check. Run frontend checks if frontend changes; report native GUI,
real SSH/WSL and Windows execution unperformed. Retest instructions must rebuild
remote bundles as well as desktop; checksum matching already handles same-version
rebuilds. Build bundles only into a fresh temporary directory if needed. No installs,
private data, process termination, app replacement, packaging or push.

ST-07B evidence: snapshot/change subscription wakes on begin/update/stop under the
discovery mutex, stale lifetime updates do not wake or publish. Held writer-open
cases verify observer snapshot equals lock-free status before verification. Focused
engine discovery/hint race tests `-count=3` PASS (4.613 s), changed-code lint 0 issues.

ST-07C evidence: optional prepared startup keeps backend identity, runs capability
initialization before tracked Start, waits for new-lifetime usability, and gates all
noncatalog calls cancellation-aware. Serve joins startup before diagnostics expiry.
Existing synchronous composition remains unchanged. RPC race suite PASS (9.150 s),
focused prepared tests repeated three times PASS, changed-code lint 0 issues.

ST-07D evidence: production CLI now prepares engine ownership before tracked startup.
Real-engine generated-cache RPC test reads catalog/status with watcher setup held.
RPC / CLI / app race suites PASS (9.217 / 2.121 / 2.891 s); changed-code lint 0 issues.
No real remote deployment or native UI exercised.

ST-07E evidence: content-free context phases cover client probe/bundle selection/
deploy/preface/initialize, server prepare/identity/catalog wait/full startup and first
successful remote catalog calls. Fake SSH observation tests PASS; RPC/remote race
suites PASS (9.329 / 20.082 s), changed-code lint 0 issues. Nine-trial generated
150 ms workload: first successful empty catalog response median 150.933 ms before /
486 µs after; this is not useful nonempty data or real SSH latency.

ST-07F complete: [integrated evidence and retest instructions](../perf.md#st-07--remote-early-catalog-handshake-2026-10-24).
Full Go test/vet/build/Windows cross-vet PASS; focused race gate repeated three times
PASS. No real host/GUI/Windows or 300 MiB end-to-end RPC measurement; frontend unchanged
and not rerun. Existing PF blockers remain unchanged; nothing pushed.


## ST-08 — Startup connection snapshot rollback (2026-10-24)

ST-08A complete (≤2 h): fix ConnectionStore initial-snapshot admission. The backend
captures Local before diagnostics enrichment, which can wait behind local startup;
a later snapshot may overwrite newer remote events and seed the link as Local.
Reuse discovery's event counter plus an init/destroy lifecycle token. Live events
win; destroyed initializations cannot seed or refresh. Keep normal snapshot seeding
and genuine disconnect behavior. Do not serialize hydration or weaken API guards.

ST-08B complete (≤3 h): deferred-promise regressions for stale Local after connecting/
connected, destroy/re-init, normal snapshot/error/disconnect, and late catalog replies
using real API epoch guards. Assert remote phase/host/title/link/data identity and no
extra reset. Confirmed scope is host-identity rollback, not an independently proven
Local-row overwrite. Document a separate fix if tests reveal one. Full frontend tests,
check/build and diff checks; native GUI unverified. Generated fixtures only, no Go
changes expected, no app launch/private data/packaging/push. Commit each task.

ST-08A: event-counter/lifecycle admission implemented; five deferred regression
cases cover connecting/connected stale Local snapshot, destroy/re-init, ordinary
seed/disconnect and failure. Focused connection/discovery 39/39 PASS. Initial fixture
failed two assertions because Proxy receivers lost bound mutation state; fixture
methods bound correctly and suite rerun.

ST-08B: real compiled AppState plus real `guardEpoch` holds Local groups/counts/
sessions across an epoch change and completed remote reset. Releasing Local replies
leaves remote target, groups, totals, rows, selected identity/header, loading and error
state unchanged. `connectionState` remains explicitly unguarded: its stale snapshot
still reaches the caller, so ST-08A's event/lifecycle admission is necessary. No
separate Local-row overwrite was reproduced by these deterministic interleavings.

Verification (2026-10-24): focused connection/discovery 40/40 PASS; full frontend
703/703 PASS; `npm run check` completes with zero errors and the existing
`tsconfig.json` invalid `ignoreDeprecations` warning; production build PASS with the
existing >500 kB chunk warning; `git diff --check` PASS. An initial direct Node test
invocation omitted the project's resolver and failed module resolution before tests;
rerunning with `--loader ./test/resolver.mjs` passed. No Go changes or native GUI
verification; real-device startup/paint behavior and the user's exact visible-row
symptom still require a rebuilt app test. No private data, remote deployment or
packaging used.
