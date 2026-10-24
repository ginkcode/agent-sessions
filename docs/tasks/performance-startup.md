# Startup responsiveness — sequential task briefs

Status: **2/6 complete** 2026-10-24: ST-00/ST-01 complete; ST-02…ST-05 pending.
[Plan and status/compatibility contract](../plan/performance-startup.md) ·
[Roadmap](../plan/performance.md) · [Tracker](../../TASKS.md#startup-priority-package).

This priority package precedes performance Milestone 2; it pulls forward a narrow
startup slice, not all of Milestone 4. M1 remains **12/15 complete**, PF-07
partial/blocked, PF-08/PF-10 blocked; its historical evidence stays intact.
The reported roughly 10-second delay is not a measured baseline and its exact
cause is unknown. ST-01 synthetic measurement is recorded in
[../perf.md](../perf.md#st-01--full-engine-startup-baseline-2026-10-24);
the reported exact delay remains undiagnosed.

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
| [ST-02](#st-02--backend-discovery-readiness-contract) | Pending | Lifecycle status snapshot/event across adapters | 4 h | ST-01 |
| [ST-03](#st-03--truthful-frontend-startup-states) | Pending | Honest readiness and compatible empty/error states | 3 h | ST-02 |
| [ST-04](#st-04--remove-the-measured-startup-blocker) | Pending, measurement-blocked | One evidence-selected safe optimization | ≤4 h | ST-03; ST-01 evidence |
| [ST-05](#st-05--integrated-startup-gate) | Pending | Integration, before/after evidence and limits | 4 h | ST-04 |

Total planned effort: **≤20 h**. ST-00 is documentation only.
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
or native paint claim. ST-04 remains measurement-blocked; sequential ST-02 is next.

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

**Status:** pending. **Estimate:** 4 h. **Dependency:** ST-01.

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

**Status:** pending. **Estimate:** 3 h. **Dependency:** ST-02.

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

**Status:** pending, measurement-blocked. **Estimate:** ≤4 h.
**Dependency:** ST-03 and the ST-01 measurement/decision.

**Selected intervention:** none; measurement-blocked by ST-01 synthetic evidence
([baseline/decision](../perf.md#st-01--full-engine-startup-baseline-2026-10-24)).
Warm watcher setup (~4 ms) dominates synchronous setup on this tiny fixture but
publication (~2 ms) already precedes it and queries bypass engine mu. No measured
10-second delay, transport/frontend bottleneck, or meaningful multi-provider
notification delay supports an intervention. Before coding, run a generated watch
directory-fanout sweep and fast/slow provider cases, then approved generated-root
adapter/frontend hydration measurements; keep fixture creation excluded. Select
only the demonstrated blocker and its deterministic lifecycle/ordering tests.
Do not assume synchronous watchers explain the reported delay.

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

**Status:** pending. **Estimate:** 4 h. **Dependency:** ST-04.

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
