# Performance improvement roadmap

Status: in progress 2026-10-24; PF-01…PF-06, PF-09 and PF-11…PF-15 complete: synthetic baselines, atomic transcript invalidation, coalesced loads, validated source-loading contract, checkpoint reuse, bounded frontend listing snapshots and their mutation freshness, bounded directory ignore frontiers, batched ignore checks and the milestone gate (12/15 M1 tasks; PF-07 partial, PF-08/PF-10 blocked).

## Context and goal

Large session histories, long transcripts, and large project directories can feel
slow. The app already has a persistent SQLite metadata/FTS index and an in-memory
transcript LRU. Improve reuse and incremental data flow rather than replace the
Go providers → index/catalog → service → Wails/RPC → Svelte architecture.

These priorities come from code inspection, not current profiling. Historical
results in [../perf.md](../perf.md) are not a baseline for the current code.
Measure synthetic workloads first and preserve correctness while removing work.

## Delivery sequence

| Performance milestone | Scope | Status |
|---|---|---|
| 1 | Synthetic baselines; correct/coalesced transcript loads; validated Codex source fast paths; unchanged checkpoint reuse; frontend listing retention; directory ignore-check batching | Gate closed; PF-01…PF-06, PF-09 and PF-11…PF-15 complete (12/15), PF-07 partial, PF-08/PF-10 blocked |
| Startup priority | Narrow startup responsiveness package pulled forward from M4, before M2; [plan](performance-startup.md), [ST-00…ST-05 briefs](../tasks/performance-startup.md) | ST-00/ST-01/ST-02 complete (ST-02A engine, ST-02B adapters); ST-03/ST-04 complete (measured fanout; early disabled/fallback publication); ST-05 pending; exact reported-delay cause unknown |
| 2 | Provider-owned incremental transcript state, immutable snapshots, byte-bounded cache, revision-aware FTS reuse, compatible transcript deltas | Roadmap only |
| 3 | Shared per-root project index, separate Git-status freshness, coalesced refresh and safe invalidation | Roadmap only |
| 4 | Per-source persistent checkpoints, targeted scans, progressive cold discovery, catalog deltas/paging, bounded fair FTS job batches | Roadmap only |
| 5 | Lazy expensive content, bounded rendering memoization, transcript/tree/source virtualization | Roadmap only |

These numbers name this performance effort, not the original M1…M8 milestones.

**Before implementing each milestone:** write its design and ≤4h task briefs in
`docs`, link them from `TASKS.md`, and settle dependencies and acceptance gates.
Milestone 1 and the sequential startup priority package are broken down now.
Complete startup before Milestone 2; this does not pull forward the whole M4
catalog overhaul or complete PF-07/08/10. Later milestones must use the measurements
and contracts established by earlier work; roadmap entries are not executable
specifications or permission to implement them wholesale.

## Milestone 1

Detailed plan, dependencies, estimates and acceptance criteria:
[performance-m1.md](../tasks/performance-m1.md). Progress: **12/15 complete**.

PF-01 evidence: [synthetic baseline](../perf.md#pf-01--synthetic-catalog-and-scanner-baseline-2026-10-24).
PF-02 evidence: [transcript baseline](../perf.md#pf-02--synthetic-transcript-baseline-2026-10-24).
PF-03 evidence: [project listing baseline](../perf.md#pf-03--synthetic-project-listing-baseline-2026-10-24).
PF-04 evidence: [atomic transcript invalidation](../perf.md#pf-04--atomic-transcript-invalidation-2026-10-24).
PF-05 evidence: [coalesced transcript loads](../perf.md#pf-05--coalesced-transcript-loads-2026-10-24).
PF-06 evidence: [validated source-loading contract](../perf.md#pf-06--validated-source-loading-contract-2026-10-24).
PF-07 partial: PF-07A accepting content-proof Load/Blob methods complete;
PF-07B matrix/recovery/evidence implemented, but larger byte-heavy workloads regress.
PF-07C safe opt-out complete: no defensible cheap automatic policy established;
production optional calls decline with zero proof and one canonical fallback.
[Decision and evidence](../perf.md#pf-07c--cost-policy-decision-and-production-bypass-2026-10-24).
PF-07 remains partial/blocked; PF-08/PF-10 blocked; PF-09 and PF-11…PF-15 complete; Milestone 1 gate closed at 12/15 ([evidence](../perf.md#pf-15--integrated-evidence-and-milestone-gate-2026-10-24)). Next: [startup priority package](performance-startup.md), then Milestone 2 design and task breakdown.
[Evidence and activation gate](../perf.md#pf-07b--mutation-matrix-recovery-and-larger-evidence-2026-10-24). M1 is 12/15. Caller activation
and Codex checkpoint optimization remain unimplemented. [Contract feasibility constraints](../tasks/performance-m1.md#pf-06-contract-and-pf-07-feasibility)
require conservative canonical fallback when metadata cannot prove selection freshness.
[PF-07A evidence/limits](../perf.md#pf-07a--bounded-content-proof-and-accepting-codex-sources-2026-10-24).

PF-09 complete: bounded Claude checkpoint summaries; [work counts, retained cost and verification](../perf.md#pf-09--claude-checkpoint-summary-reuse-2026-10-24).

PF-11 complete: bounded listing-only snapshots and immediate reopen; [work counts, verification and freshness limits](../perf.md#pf-11--bounded-frontend-listing-snapshots-2026-10-24).

PF-12 complete: app mutations (including hidden saves and Git writes) invalidate overlapping snapshots and supersede older visible list requests; [policy, regressions and limits](../perf.md#pf-12--listing-freshness-and-draft-safety-2026-10-24).
PF-13 complete: ignore decisions batch a bounded frontier spanning directories, still before descent; [equivalence, counts and limits](../perf.md#pf-13--bounded-directory-frontiers-2026-10-24).
PF-14 complete: 4096-path/256 KiB batches and `./`-anchored paths, so pathspec-like names cannot abort or redirect a decision; [matrix and counts](../perf.md#pf-14--batched-ignore-checks-2026-10-24).

Recommended order: baseline tasks → transcript cache correctness/coalescing →
Codex source loading → checkpoint reuse → listing retention → directory batching
→ integrated evidence. Independent branches of work are identified in the briefs.
Ship small independently testable changes, not a single performance rewrite.

## Later milestone boundaries

### 2 — Incremental transcripts

Reuse `internal/jsonl` safe offsets, but scanner metadata checkpoints are not
transcript parser snapshots. Preserve split assistant records, tool pairing,
pending usage, deduplication, models, sidecar dependencies, and best-effort final
unterminated records. Later records can change earlier messages: delta responses
need a base revision, reset fallback and earliest changed message index.

Publish immutable nested objects; a shallow message-slice copy is insufficient.
Validate append continuity and rebuild on truncation, replacement, rewrite or
uncertainty. Bound parser-state and transcript memory. Share loading with FTS
through an injected revision-aware interface, not an index → engine dependency
or an unbounded cache polluted by background jobs. Keep existing load paths for
providers without incremental support. Capability-gate wire changes.

### 3 — Shared project index

Cache file paths/directories separately from Git status. Authorize every request,
including hits; isolate roots, options and subdirectory scopes. Invalidate for
file mutations, untracked changes, ignore configuration, Git operations and
watcher overflow; reconcile missed events. HEAD/index mtimes alone are inadequate.
Cached listings never replace file-presence probes, revision checks or reads.

### 4 — Startup and catalog scaling

Preserve `CommitScan` → `Catalog.Apply` → event ordering and shared-cache
writer/reader promotion, schema seeding, `LoadSnapshot`, ClearCache/Forget/Retire
exclusion. Targeted watcher paths are hints; ambiguity/overflow falls back to full
reconciliation. Progressive scans publish committed upserts, but removals happen
only after successful full enumeration. Batch FTS work fairly without losing
debounce, maximum delay, retry backoff or revision checks. Measure watcher setup
and `quick_check`; do not remove integrity checks without a tested recovery design.

### 5 — Rendering

Measure lazy rendering/memoization before virtualization. Existing fixed-height
session-list virtualization is a pattern, not a variable-height transcript drop-in.
Preserve scroll anchors, search jumps, selection, load-earlier, keyboard navigation,
source diagnostics, source coordinates, copy behavior and presentation mode.

## Shared guardrails

- Use repository fixtures/generated temporary roots and explicit cache locations.
  Do not read real session stores or run `scripts/qa-perf-bench.sh`, which does so.
- Do not reuse another application's private cache or write to its session store.
- Preserve authorization, path/symlink checks and canonical provider selection.
- Preserve drafts, conflicts, uncertain saves, undo history and host isolation.
- Bound memory, active work and invalidation bookkeeping; do not only increase LRU size.
- Do not install tools, replace the installed app, terminate processes, push, or
  produce Windows test packages as part of this work without a separate request.
- Automated tests do not establish native GUI responsiveness. Record manual checks
  as unperformed until actually exercised; do not present estimates as measurements.

## Verification policy

Each task adds focused regression tests and deterministic work-count assertions
where practical. Use barriers for concurrency tests, not timing-dependent sleeps.
Run affected Go race tests, backend tests/build/vet and frontend tests/check/build.
Use installed tooling only and report unavailable checks rather than installing it.

Compare repeated cold/warm synthetic measurements on the same host, excluding
fixture construction. Record distributions, allocations, retained memory and
payload/work counts where supported. Absolute CI timing thresholds are advisory.
A cache hit must be equivalent to the correct uncached path; stale/error/cancel
paths deserve the same coverage as successful reuse.
