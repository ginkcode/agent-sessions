# Startup responsiveness — priority package before Milestone 2

Status: ST-00 documentation and ST-01 measurement complete 2026-10-24; ST-02A engine complete; ST-02B adapters and ST-03…ST-05 pending.
[Sequential task briefs](../tasks/performance-startup.md) ·
[Performance roadmap](performance.md) · [Task tracker](../../TASKS.md#startup-priority-package).

## Scope and evidence boundary

The user reports roughly 10 seconds at launch showing “No Agents Detected” and
“No matching sessions.” This is an observation, not a measured startup baseline;
the exact cause is unknown. Measure the real startup path before selecting a fix.
Pull this narrow package forward from performance Milestone 4, ahead of
Milestone 2 transcript work, without pulling forward the entire catalog overhaul.

Performance M1 remains closed at **12/15 complete**: PF-07 partial/blocked
(A/B implemented, C safe opt-out), PF-08/PF-10 blocked. Its evidence, production
fallback decisions and native GUI QA limitations remain unchanged. Startup is a
separate six-task package, not completion of those blocked tasks or all of M4.

ST-00 changes documentation only. No runtime implementation, measurement, app
launch or packaging is claimed by completing it. The reviewed ST-00 documentation
was committed separately as `0d734ad`. Execute ST-01 through
ST-05 sequentially, with review of each task before proceeding. Split any task
that will exceed four developer hours before coding; estimates include tests.
Milestone 2 still requires its own design and task breakdown after this package.

## Current path, not a diagnosis

- `internal/app/app.go` invokes `Engine.Start` synchronously;
  `internal/engine/engine.go` holds the engine mutex during startup.
- Writer startup in `internal/engine/cache.go` opens/restores the database and
  **publishes the cached catalog before bootstrap**, then restores scan state via
  `index.NewRefresher`, starts the indexer, installs watchers synchronously and
  starts asynchronous bootstrap. Do not describe bootstrap as preceding cache
  publication or assume publication already means frontend usability.
- Reader startup loads catalog and scan state together with atomic `LoadSnapshot`
  before publication. Keep that consistency, including promotion to writer.
- Recursive watcher registration in `internal/watch/watch.go` is synchronous.
  Whether it causes the reported delay is not known.
- Disabled/fallback-cache `Engine.Scan` notifies after the runner completes;
  `internal/scan/scan.go` already applies successful providers as they finish.
- `frontend/src/lib/stores/appState.svelte.ts` subscribes before hydration, then
  awaits groups/counts before sessions. Request flags are not discovery readiness.
  Empty replies can currently lead to definitive empty text before discovery ends.
- Catalog changes in `internal/engine/events.go` and FTS indexing progress are not
  explicit startup completion; successful empty discovery also needs a terminal
  outcome. Reuse existing cache/catalog, routing and request guards, not a new cache
  or speculative provider rewrite.

## ST-01 result and next gate

[Full repeated generated-startup evidence](../perf.md#st-01--full-engine-startup-baseline-2026-10-24)
records opt-in observation, nine trials per mode, work counts and focused tests.
ST-01 is complete without a performance intervention. ST-04 is measurement-blocked:
~4 ms watcher setup dominates this small warm fixture, but cached publication is
already ~2 ms and catalog reads bypass engine mu. No evidence explains the
reported 10-second delay. Generated fanout, multi-provider and adapter/frontend
measurement is required before selecting a safe bounded change; ST-02/03 remain
sequential work (ST-02A engine complete; ST-02B adapters next). Native paint remains unmeasured.

## Readiness/status compatibility policy

ST-02 must settle concrete DTO, event and capability names with tests before
ST-03 consumes them. The following behavior is required, not a claim that an API
already exists across adapters. ST-02A now implements the engine snapshot/event
contract; [the ST-02 brief](../tasks/performance-startup.md#st-02--backend-discovery-readiness-contract)
fixes DTO/event/capability names and documents outcome rules and test evidence.
ST-02B retains optional Backend/App/RPC negotiation, unavailable fallback and
parity acceptance; ST-03 must wait for it. The split was recorded before coding
because combined lifecycle/outcome/adapter work exceeded four hours.

- Own a small status snapshot and change event in the engine, scoped to one
  lifecycle. Represent initializing/restoring, reconciling, settled and
  failed/degraded outcomes. Track usable-catalog availability **separately** from
  discovery completion: cached or partial rows may be queryable while reconciling.
  Empty but successfully reconciled is settled; empty is not evidence of failure.
- Record enough outcome/detection information to distinguish successful empty
  discovery, zero detected providers and isolated provider failures. Catalog row
  count is not provider detection; FTS completion is not discovery completion.
- Every completed startup attempt has an honest terminal result, including zero
  providers, successful empty scans, disabled cache, corrupt/unavailable cache
  fallback and scan failures. Cache failure with successful fallback is not fatal.
  Retain usable rows on degraded/error outcomes; cached rows during reconciliation
  are hints, not proof that sources are still present.
- Status reads must not queue behind lengthy startup work under the engine mutex.
  Cancellation/Close/restart must invalidate the previous lifetime; late callbacks
  cannot publish into a new lifetime. Bound owned background work and cleanup.
- Expose status through existing engine/local Backend/App and RPC adapters, using
  existing capability advertisement and unknown-method fallback conventions.
  Keep old remote hosts usable. Unsupported status means explicitly **startup
  status unavailable**, not invented settled discovery or an endless spinner.
- Frontend subscribes before fetching the snapshot (use `stores/search.svelte.ts`
  as a pattern). Later events beat older snapshot replies. Guard requests/events
  by host and connection generation; reconcile on reconnect and ignore stale
  lifetimes. Recover from missed/pre-subscription events with a fresh snapshot.

## Presentation requirements

| Situation | Required behavior |
|---|---|
| Empty, initializing/restoring or reconciling | Loading/discovering, not definitive absence |
| Cached or newly discovered rows while reconciling | Show rows immediately with lightweight updating indication |
| Successful completed empty discovery | Truthful session-empty message |
| Failed/degraded discovery | Explicit outcome, retain usable rows; do not present a successful empty result |
| Filter returns zero on usable data | “No matching sessions” distinct from discovery state |
| Older host without status | Explicit unavailable semantics; ordinary browsing still works |

Do not show “No Agents Detected” unless actual detection information supports it;
otherwise use a truthful session-empty message. Preserve selection, drafts,
navigation, stale-group reconciliation and host boundaries. Remove the
unnecessary groups-before-sessions dependency only where those guards remain
correct. API response availability, frontend usable-data state and actual native
paint are different measurements.

## Measurement and intervention gate

ST-01 adds opt-in, content-free phase timing with a common origin **within each
process**; never subtract unrelated frontend/backend clocks. Include database
open/integrity, restore/publication, scan-state restoration, watcher registration,
bootstrap completion, first successful groups/counts and sessions responses and
frontend usable-data state. Reuse context-local M1 observation patterns. Do not
record paths, prompts or session payloads.

Use generated fixtures and explicit provider/cache roots. Repeat cold
application-cache and warm-cache writer/reader startup plus disabled/fallback
cache paths. Include full engine startup: `TestBenchmarkWarmFirstPaint` measures
only Open → LoadCatalog → Reset, not full startup or native paint. Append
reproducible distributions, environment/revision, fixture sizes, ordering/work
counts and limitations to [../perf.md](../perf.md); timings are advisory, not
arbitrary CI thresholds or proof of the user's exact delay.

Before ST-04 coding, record **one** dominant safe intervention and its acceptance
tests in its brief, using ST-01 evidence:

- Watcher/scan-state blocker: defer only eligible independent work into
  engine-owned initialization, with race-safe publication, Close/restart cleanup
  and reconciliation covering watcher installation races. Preserve atomic reader
  snapshots and writer promotion.
- Disabled/fallback notification blocker: reuse successful per-provider catalog
  application notifications instead of waiting for the slowest provider.
- Restore/integrity/transport/frontend blocker: scope the corresponding safe fix
  first. Do not bypass integrity checks, split atomic snapshots or promise a broad
  progressive parser rewrite. Split a larger follow-up rather than expanding scope.

## Safety and lifecycle invariants

- Generated fixtures only. Never access real agent logs, private session stores or
  private caches; never run `scripts/qa-perf-bench.sh`. Do not launch against
  default/private roots. Any separately requested native test needs approved
  generated roots.
- Preserve database commit → catalog apply → event order, ClearCache/Forget/Retire
  exclusion, schema seeding, corrupt-cache recovery, root isolation and shared
  reader/writer promotion. Do not bypass integrity checks.
- Preserve authorization, canonical provider behavior, path/symlink checks,
  selection/navigation, drafts and host boundaries. Do not duplicate cache layers.
- Own cancellation and bound background work; no leaked watcher installations or
  late writes/publications after Close or restart.
- No installs, app replacement, process termination, Windows packaging or pushes.
  No Milestone 2 implementation. ST-00 is documentation-only; reviewed task
  commits are handled separately on `feature/improve-performance` with required
  attribution, without pushing.

## Completion gate

ST-05 must record before/after distributions, work counts, remaining bottlenecks
and explicit manual/native QA limits. Barrier tests (not sleeps) cover delayed
watcher/state initialization, fast/slow providers, true empty, isolated failures,
fallback, cancellation/Close/restart, reader promotion and missed events. Warm
cached rows must be queryable while the measured unrelated slow phase is held at
a barrier, with eventual final-catalog parity.

Frontend tests cover delayed status, stale snapshot versus event, host switch and
reconnect, nonempty pending cache, true empty, failed/degraded discovery, old-host
unsupported status, zero filtered matches and selection retention. Verify local
and in-process RPC parity and old-host fallback. Run affected Go race tests,
`go test ./...`, `go vet ./...`, `go build ./...`, Windows cross-vet where supported,
and frontend `npm test`, `npm run check`, `npm run build`; use installed tooling
for changed-code lint and report unavailable/existing failures faithfully. These
are implementation gates, not checks claimed by ST-00. Automated evidence does
not establish native GUI responsiveness; native paint remains unverified unless
separately exercised.
