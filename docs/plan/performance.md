# Performance improvement roadmap

Status: in progress 2026-10-24; PF-01/PF-02 synthetic baselines complete (2/15 M1 tasks).

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
| 1 | Synthetic baselines; correct/coalesced transcript loads; validated Codex source fast paths; unchanged checkpoint reuse; frontend listing retention; directory ignore-check batching | In progress; PF-01/PF-02 complete (2/15) |
| 2 | Provider-owned incremental transcript state, immutable snapshots, byte-bounded cache, revision-aware FTS reuse, compatible transcript deltas | Roadmap only |
| 3 | Shared per-root project index, separate Git-status freshness, coalesced refresh and safe invalidation | Roadmap only |
| 4 | Per-source persistent checkpoints, targeted scans, progressive cold discovery, catalog deltas/paging, bounded fair FTS job batches | Roadmap only |
| 5 | Lazy expensive content, bounded rendering memoization, transcript/tree/source virtualization | Roadmap only |

These numbers name this performance effort, not the original M1…M8 milestones.

**Before implementing each milestone:** write its design and ≤4h task briefs in
`docs`, link them from `TASKS.md`, and settle dependencies and acceptance gates.
Only Milestone 1 is broken down now. Later milestones must use the measurements
and contracts established by earlier work; roadmap entries are not executable
specifications or permission to implement them wholesale.

## Milestone 1

Detailed plan, dependencies, estimates and acceptance criteria:
[performance-m1.md](../tasks/performance-m1.md). Progress: **2/15 complete**.

PF-01 evidence: [synthetic baseline](../perf.md#pf-01--synthetic-catalog-and-scanner-baseline-2026-10-24).
PF-02 evidence: [transcript baseline](../perf.md#pf-02--synthetic-transcript-baseline-2026-10-24).
Next: PF-03; no cache or checkpoint optimization implemented.

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
