# Performance Milestone 1 — Baselines and targeted reuse

- Status: in progress 2026-10-24; **6/15 tasks complete**.
- Parent plan: [performance.md](../plan/performance.md).
- All estimates include focused tests and are planning estimates, not promises.
- Every task is ≤4 developer hours. Split it here and in `TASKS.md` before coding
  if current-code investigation reveals a larger scope. Never drop acceptance
  criteria silently to fit an estimate.

## Scope

Improve repeated work without incremental transcript parsing, a shared backend
project index, wire/API deltas, schema migrations or virtualization. Those belong
to later milestones. Retain standalone provider behavior and old remote behavior.
PF-01 completed 2026-10-24; repeated synthetic baseline and verification are in
[../perf.md](../perf.md#pf-01--synthetic-catalog-and-scanner-baseline-2026-10-24).
PF-02 completed 2026-10-24; transcript baseline evidence is appended to
[../perf.md](../perf.md#pf-02--synthetic-transcript-baseline-2026-10-24).
PF-03 completed 2026-10-24; project listing baseline evidence is appended to
[../perf.md](../perf.md#pf-03--synthetic-project-listing-baseline-2026-10-24).
PF-04 completed 2026-10-24; atomic per-key transcript invalidation and deterministic
race evidence are in [../perf.md](../perf.md#pf-04--atomic-transcript-invalidation-2026-10-24).
PF-05 completed 2026-10-24; coalesced loads, owned cancellation and repeated
same-key evidence are in [../perf.md](../perf.md#pf-05--coalesced-transcript-loads-2026-10-24).
PF-06 completed 2026-10-24; optional source contract, bounded canonical fallback,
ownership and Codex feasibility are in [../perf.md](../perf.md#pf-06--validated-source-loading-contract-2026-10-24).
PF-07A is complete; PF-07 remains partial pending the measured activation gate (PF-07B matrix/evidence implemented). PF-08…PF-15 are unimplemented.

## Task index

PF-01 through PF-06 are **complete**; PF-07 is **partial** (A complete, B evidence implemented; activation gated); all other rows are **not started**. Dependencies are completion gates, not mere ordering
suggestions. All implementation tasks also depend on the shared guardrails in the
[parent plan](../plan/performance.md#shared-guardrails).

| Task | Deliverable | Est | Dependencies |
|---|---|---|---|
| [PF-01](#pf-01--catalog-and-scan-baselines) | Catalog and scan baselines | 4 h | None |
| [PF-02](#pf-02--transcript-baselines) | Transcript load/concurrency baselines | 4 h | None |
| [PF-03](#pf-03--project-listing-baselines) | Directory/Git/listing baselines | 4 h | None |
| [PF-04](#pf-04--atomic-transcript-cache-invalidation) | Atomic per-session cache invalidation | 4 h | PF-02 |
| [PF-05](#pf-05--coalesced-transcript-loads) | Coalesced loads and owned cancellation | 4 h | PF-04 |
| [PF-06](#pf-06--validated-source-loading-contract) | Optional validated source-loading contract | 3 h | PF-02 |
| [PF-07](#pf-07--codex-load-and-blob-fast-paths) | Codex Load/Blob fast paths (partial: activation gated) | 4 + 4 h | PF-06 |
| [PF-08](#pf-08--source-aware-engine-and-fts-callers) | Source-aware engine and FTS integration | 4 h | PF-05, PF-07 |
| [PF-09](#pf-09--claude-checkpoint-reuse) | Claude unchanged checkpoint reuse | 4 h | PF-01 |
| [PF-10](#pf-10--codex-checkpoint-reuse) | Codex unchanged checkpoint reuse | 4 h | PF-01, PF-07 |
| [PF-11](#pf-11--bounded-frontend-listing-snapshots) | Bounded listing snapshots | 3 h | PF-03 |
| [PF-12](#pf-12--listing-freshness-and-draft-regressions) | Listing freshness and draft safety | 4 h | PF-11 |
| [PF-13](#pf-13--bounded-directory-frontiers) | Bounded directory frontier collection | 3 h | PF-03 |
| [PF-14](#pf-14--batched-ignore-checks) | Batched ignore checks and parity tests | 4 h | PF-13 |
| [PF-15](#pf-15--integrated-evidence-and-milestone-gate) | Integration and before/after evidence | 4 h | PF-01…PF-14 |

Total: **61 h** (PF-07 split into 4 h + 4 h), including focused tests. Native/manual QA or unexpected scope
requires a follow-up package, not an invented passing result.

Baseline tasks may be independent. Cache/source work shares `service.go`;
checkpoint/source work shares Codex files. Coordinate edits even when dependencies
allow parallel work. Default execution is one task at a time, updating status and
evidence before proceeding.

## PF-01 — Catalog and scan baselines

**Status:** complete 2026-10-24. Synthetic fixtures, counters, metadata regressions
and three repeated baseline runs recorded in [../perf.md](../perf.md#pf-01--synthetic-catalog-and-scanner-baseline-2026-10-24).

**Goal:** identify startup/refresh cost before changing checkpoints.

**Likely files:** `internal/index/refresh_test.go`, index benchmark helpers,
Claude/Codex scan tests, `docs/perf.md`.

- Reuse `TestBenchmarkWarmFirstPaint` as a reference, adding repeatable Go
  benchmarks for 1k/10k metadata rows and generated provider sources.
- Separate database open, catalog restoration, cold discovery, unchanged scan,
  and one-source append. Exercise active/index/sidecar metadata changes.
- Count source reads and checkpoint transformations through narrow test seams;
  avoid production logging of session contents or paths.
- Use explicit temporary roots/cache directories; setup is outside timed work.

**Done when:** fixture correctness is asserted, benchmark commands are documented,
results include environment/fixture sizes/repeated timings and allocations, and
no private stores are discovered. Machine-dependent timings are not CI failures.

## PF-02 — Transcript baselines

**Status:** complete 2026-10-24. Generated 38/100 MB provider fixtures, full-message
small-equivalent checks, cold/warm/invalidated/concurrent measurements, load byte
accounting and reusable per-attempt barriers implemented. Three repeated runs,
allocation/payload/estimated and measured memory evidence, verification and limits
are in [../perf.md](../perf.md#pf-02--synthetic-transcript-baseline-2026-10-24).
No cache optimization is included.

**Goal:** establish full-parse, cache-hit, concurrent-miss and payload costs.

**Likely files:** `internal/engine/service_test.go`, provider load benchmark files,
`internal/provider/providertest/fake.go`, `internal/jsonl` benchmarks.

- Generate 38/100 MB transcripts from valid synthetic records, including split
  assistant messages, tool completion and incomplete final records.
- Benchmark cold load, warm page, invalidated reload and concurrent same-key versus
  independent-key requests; count provider calls and bytes read.
- Measure allocations, serialized page sizes and retained-memory estimates where
  meaningful. Label estimates separately from actual heap measurements.
- Add reusable barrier-driven fake loading for later invalidation/cancellation tests.

**Done when:** full output is checked against small equivalent fixtures, benchmarks
exclude generation, and repeated provider work is observable without wall-clock assertions.

## PF-03 — Project listing baselines

**Status:** complete 2026-10-24. Generated below/above-cap Git trees, nested
ignore/deleted/untracked/empty/symlink and root/context isolation regressions;
request-local Git counts, repeated component benchmarks and mocked store lifecycle
request counts. Full race suite and frontend tests/check/build pass; new-code lint
passes, existing files-package lint debt remains. Native GUI/Windows not measured.
[Evidence and limits](../perf.md#pf-03--synthetic-project-listing-baseline-2026-10-24).

**Goal:** separate Git subprocess, path validation, directory walk and UI reopen costs.

**Likely files:** `internal/files/{files,directories,search}_test.go`, benchmark
helpers, frontend `test/go-to-file.test.mjs` and `test/file-drafts.test.mjs`.

- Generate Git trees below/above 20k files, empty/ignored directories, deleted
  tracked files, untracked paths, nested ignore rules and supported symlinks.
- Reuse `BenchmarkSearch`; add listing/traversal measurements and a narrow
  Git-invocation counting seam. Assert caps/truncation and fixture isolation.
- Establish store-level close/reopen/list-request behavior using existing mocks.
  This is not a measured native GUI latency test.

**Done when:** commands and baseline results distinguish components; unavailable
Git/symlink/platform cases are explicitly skipped; no user repositories are read.

## PF-04 — Atomic transcript cache invalidation

**Status:** complete 2026-10-24. Existing four-entry LRU wrapped by a narrow
coordinator with atomic lookup/token capture/invalidation/publication, unique
per-key identities and clear epoch. Barrier race tests, bookkeeping/capacity,
error/cancellation and paging regressions pass. Full backend race suite,
build/vet, changed-line lint and frontend verification recorded in
[../perf.md](../perf.md#pf-04--atomic-transcript-invalidation-2026-10-24), including
pre-existing repository-wide lint failures. No coalescing or lifecycle change.

**Goal:** prevent stale publication after invalidation without globally rejecting
useful unrelated loads.

**Likely files:** `internal/engine/service.go`, new transcript-specific coordinator
and tests, existing `lru.go` (reuse rather than broad generic changes).

- Coordinate lookup, token capture, invalidation and publication under one lock;
  perform provider I/O outside it. Use unique per-key tokens plus a clear epoch.
- Preserve current behavior: already-started reads may receive their snapshot,
  but an invalidated load cannot repopulate the current cache.
- Retain current cache capacity in this milestone. Bound token bookkeeping and
  prevent stale identity reuse; do not introduce byte-budget policy yet.

**Done when:** barrier tests prove eviction/clear versus publication ordering,
unrelated eviction isolation, removal/reappearance, and bounded bookkeeping;
existing paging/cache tests and affected race tests pass.

## PF-05 — Coalesced transcript loads

**Status:** complete 2026-10-24. Narrow current-flight coordinator, independent
waiters, lifetime/orphan cancellation, four-provider-load bound and engine restart
coverage; deterministic race tests and repeated synthetic evidence in
[../perf.md](../perf.md#pf-05--coalesced-transcript-loads-2026-10-24).

**Goal:** concurrent requests for the same current session share one provider load.

**Likely files:** transcript coordinator, `service.go`, engine/service lifecycle
and tests. Reuse PF-02 barriers rather than delay-based assertions.

- Join by key and current token/epoch. Invalidated callers never join old work;
  old completion cannot delete/overwrite a newer flight.
- Derive shared load context from engine lifetime, not its first caller. Each
  waiter cancels independently; last-waiter cancellation cancels orphan work.
- Bound active provider loads and wait context-aware; shutdown cancels owned work.
  Reuse an existing limiter if appropriate, otherwise keep coordination narrow.

**Done when:** tests cover same/different keys, failures/retries, cancellation of
first/other/last waiter, old/new completion ordering, clear, shutdown and concurrency
bounds. Same-generation misses invoke the provider once. Race tests pass.

## PF-06 — Validated source-loading contract

**Status:** complete 2026-10-24. Independent optional interfaces, single canonical
fallback, ownership/freshness contract and synthetic invalid-hint/race tests;
[evidence and limits](../perf.md#pf-06--validated-source-loading-contract-2026-10-24).

**Goal:** callers with current metadata can avoid rediscovery without weakening
canonical source selection.

**Likely files:** `internal/provider/provider.go`, provider contract tests/fakes,
Codex resolution helpers and tests.

- Define optional source-aware Load/Blob interfaces; do not break base Provider.
- Document ref/source identity, canonical metadata, freshness validation and
  cancellation. A stale hint triggers canonical re-resolution or a defined stale
  error; it never authorizes an arbitrary path or duplicate.
- Specify engine catalog versus FTS queued-revision metadata ownership. Keep
  standalone calls without hints fully supported.

**Done when:** contract tests demonstrate invalid hints cannot bypass path checks,
fallback rules are explicit, and no remote/public frontend protocol changes are needed.

### PF-06 contract and PF-07 feasibility

`provider.SourceLoader` / `SourceBlobLoader` take `(ctx, ref, SessionMeta)`;
`LoadWithSource` / `BlobWithSource` dispatch independently and fall back to the
base method once on `errors.Is(err, ErrStaleSource)`. Missing optional support or
mismatched identity also uses the base method. Other errors propagate; cancellation
is checked before dispatch and before fallback. No retry loop or public/wire field
is added. Implementations can instead re-resolve canonically internally. The
metadata value is an unmodified canonical scan snapshot, never authorization.
Standalone Load/Blob remain the canonical fallback and support fresh providers.

Engine callers own a catalog snapshot plus the transcript coordinator's current
token/epoch. FTS must read session metadata consistently with the queued revision
(or skip a superseded job), independently of engine/catalog state. Its existing
transaction checks of both current and queued revisions remain mandatory before
document replacement; fallback must not overwrite DB metadata with `Load.Meta`.
Neither caller is integrated in PF-06. Blob keys retain standalone validation and
are not revision-pinned: fallback may reject a key whose source changed.

Codex inspection establishes these constraints for PF-07:

- `Scan` discovers conventional active/archive files, then reads the highest
  numeric valid `state_N.sqlite`. Valid index paths can add unconventional files.
  `indexRolloutPath` checks lexical and resolved containment, configured-root
  aliases, rollout naming, regular-file status and readability. It is necessary
  before using a hinted path, but proves neither session ID nor canonicality.
- Raw rollout aggregates choose newer **raw** UpdatedAt, active on a tie, then
  lexically smaller path. A valid index path overrides that choice only if the
  parsed file ID matches. An invalid index path discards that row; an absent path
  permits discovered selection plus overlays. Path/ID mismatch discards overlays.
- Nonempty index title/prompt/CWD/model/version/branch/times override raw values;
  counts/tokens remain parsed. Archive-column presence matters. If spawn edges
  exist they clear raw ancestry and require a visible canonical parent. Therefore
  even a targeted thread lookup cannot blindly trust its parent ID.
- `SessionMeta` has no file identity, source size/mtime, discovery generation or
  SQLite revision proof. UpdatedAt can come from records or index overlays.
  A hint matching the file cannot exclude a newer/added duplicate elsewhere,
  a rewritten file with preserved timestamps, a new higher-numbered index, WAL
  updates, lost index overlays or changed parent visibility. Checking only the
  hinted file or DB mtime would silently weaken standalone semantics.
- PF-07 may skip provider-wide **rollout parsing** only when it has evidence for
  all relevant selection/overlay dependencies. Safe conservatism is required:
  absent that evidence return ErrStaleSource or do one canonical resolution.
  Discovery/reconciliation may still be necessary to exclude competing sources;
  this contract does not promise zero directory enumeration or that every valid
  catalog hint is fast. Provider-owned bounded evidence may be developed in
  PF-07, but a trusted path, immutable snapshot, watcher proof or persisted token
  must not be invented from this metadata. Preserve existing best-effort read
  races; no atomic filesystem/index snapshot is claimed.

PF-07's “valid hints cause no provider-wide scan” gate applies to **validated**
hints with sufficient evidence, not merely matching caller fields. If proving a
useful no-Scan case while retaining duplicate/index parity exceeds its four-hour
budget, split PF-07 before coding, not by relaxing canonicality. PF-06 introduces
no Codex fast path, validation cache, index query rewrite or engine/FTS activation.

## PF-07 — Codex Load and Blob fast paths

**Status:** PF-07A complete 2026-10-24; parent **partial**: PF-07B matrix/evidence implemented, but larger warm workloads regress; activation gate remains. [Evidence and limitations](../perf.md#pf-07a--bounded-content-proof-and-accepting-codex-sources-2026-10-24). Split before implementation. Investigation shows safe proof/reconciliation plus the full mutation/evidence matrix exceeds 4 h. Parent acceptance below is unchanged.

### PF-07A — Bounded content proof and accepting source methods (4 h; PF-06)

Implement provider-owned immutable canonical snapshots, capped per provider root.
Reconcile discovered and index-added paths and hash complete bytes (streaming,
not size/mtime trust); reread index semantics including WAL through SQLite.
Establish proof lazily on an optional cold call only; ordinary Scan and standalone
Load/Blob retain their original I/O. Publish only when before/after proofs agree
around one canonical Scan; serve its metadata directly even if proof fails,
without a second fallback Scan. Account proof bytes/opens/passes/enumerations
explicitly through an opt-in SourceWork collector. On a proven
unchanged snapshot, both optional methods accept canonical metadata without Scan
or unrelated rollout parsing. Uncertain/oversized/changed evidence returns stale
for the PF-06 single standalone fallback. Reuse existing load/blob parsers.
Test accepting and ordinary zero-proof work counts, forged/stale hints, same-stat rewrite, duplicate
appearance, index overlay change, cancellation, snapshot isolation and bounds;
run focused/race and full verification. This deliberately trades full byte reads
for no unrelated parsing, not a zero-I/O cache or checkpoint optimization.

### PF-07B — Mutation matrix and repeated evidence (4 h; PF-07A)

**Evidence implemented 2026-10-24; parent not accepted.** [Mutation/recovery tests, repeated 38/100 MiB benchmarks, retained heap and activation gate](../perf.md#pf-07b--mutation-matrix-recovery-and-larger-evidence-2026-10-24). Warm full-source hashing regresses on byte-heavy workloads; PF-08 must remain gated rather than claiming a universal fast path.

Complete targeted Load/Blob parity tests for index path override, numeric index
replacement/removal/corruption, WAL updates, raw duplicate ordering under index
times, archive-column presence, authoritative parent removal/visibility,
disappearance/replacement/symlink changes, concurrent Scan/validation publication,
and lazy-output/valid and invalid final tails. Add repeated synthetic benchmarks
and retained-state/work-count evidence, documenting digest overhead and fallback
frequency. Preserve every parent acceptance criterion; only after these gates
may PF-07 be counted complete and PF-08 start.

**Goal:** known valid sessions load without scanning unrelated rollouts.

**Likely files:** `internal/provider/codex/{load,blob,scan_rollout}.go`, load/index tests.

- Reuse `loadRollout(meta)` and `indexRolloutPath` validation before opening.
- Preserve index-selected path overrides, title/archive/parent overlays and
  duplicate ordering: newer UpdatedAt, active on tie, then path ordering.
- Validate hint freshness/source identity and bounded fallback per PF-06. Never
  manufacture a trusted path from `ref.ID`.

**Done when:** valid hints cause no provider-wide scan; Load and Blob both use the
fast path; stale/missing/replaced/symlinked sources, index changes and duplicate
selection match canonical standalone behavior. Existing lazy-output/tail tests pass.

## PF-08 — Source-aware engine and FTS callers

**Goal:** activate the fast path across interactive and indexing calls.

**Likely files:** `internal/engine/service.go`, `internal/index/worker.go`, engine
cache/FTS tests; optional interface fakes from PF-06.

- Engine supplies resolved catalog metadata, while unknown providers retain Load/Blob.
- FTS loads metadata consistent with its queued revision and retains transactional
  current-revision checks before replacing indexed documents.
- Do not couple FTS to the UI cache or introduce index → engine imports. This task
  removes resolution work, not full transcript parsing or FTS queue idle time.

**Done when:** tests cover providers with/without optional support, disappeared
sources, metadata updates/deletion during indexing and invalidation during load.
Benchmarks show fewer scans for both page and Blob/indexing paths, with identical output.

## PF-09 — Claude checkpoint reuse

**Goal:** avoid repeatedly decoding unchanged metadata checkpoints safely.

**Likely files:** `internal/provider/claude/scan.go`, scan tests and benchmark helpers.

- Use validated in-memory decoded state or a compact derived summary, keyed by
  checkpoint identity/version and source dependencies; bound retained state.
- Preserve sidecar metadata changes, source replacement, removals, incompatible
  checkpoints and split-record usage/tool state.
- Do not simply return on unchanged size/mtime before validating needed metadata.
  Cache entries must not mutate previously returned ScanState objects.

**Done when:** unchanged scans perform less measured checkpoint work with identical
results; cold/new-provider/reset/version/sidecar/append/rewrite tests pass, including
existing checkpoint-roundtrip and resume-at-boundary tests.

## PF-10 — Codex checkpoint reuse

**Goal:** avoid unchanged-source decode/re-encode work without changing canonicalization.

**Likely files:** `internal/provider/codex/scan_rollout.go`, index/scan tests.

- Reuse validated checkpoint state/summaries with bounded memory and explicit
  invalidation. Preserve raw rollout metadata separately from canonical overlays.
- Keep index disappearance, authoritative parent changes, archive changes,
  duplicate selection and fallback correct even when rollout bytes do not change.
- Do not drop deduplication maps just to save memory or rewrite persisted checkpoint
  schema/version unnecessarily.

**Done when:** operation counts fall on unchanged scans; parity tests cover index
addition/removal/change, duplicates, source deletion/replacement, versions and appends.
Retained-state cost is recorded; previously returned checkpoint state stays immutable.

## PF-11 — Bounded frontend listing snapshots

**Goal:** reopen Go to File with immediate cached paths, then background refresh.

**Likely files:** `frontend/src/lib/stores/files.svelte.ts`, `test/go-to-file.test.mjs`.

- Add a bounded listing-only cache keyed by data-host identity and normalized root.
  Do not evict saved editor/draft state with listings.
- Reuse existing sequence/epoch/host guards; do not replace fresh content/probe calls.
- Use raw state only for immutable replacement. Preserve honest loading/error/
  truncation presentation and explicit refresh.

**Done when:** reopen exposes a snapshot before list resolution, refresh replaces it,
cache bounds and host/root isolation are tested, and close still preserves editors.

## PF-12 — Listing freshness and draft regressions

**Goal:** retained snapshots cannot hide app mutations or risk user work.

**Likely files:** files store, create/rename/delete/save/Git integration paths,
`test/{file-drafts,create-directory,rename-file,delete-file,go-to-file}.test.mjs`.

- Invalidate affected snapshots after successful mutations, including while hidden;
  reconnect revalidates. Clear/reset behavior respects existing draft guards.
- Reject late replies from old roots/hosts/requests. Unknown/failed probes never
  become proof that a file was deleted.
- Preserve dirty drafts, conflict/uncertain-save state, undo and cached tabs.

**Done when:** mutation/reopen/reconnect/late-reply tests cover stale snapshots,
refresh failure and host switches; existing draft safety suites pass. Native UI
verification remains separate and explicitly unperformed unless actually run.

## PF-13 — Bounded directory frontiers

**Goal:** prepare larger safe ignore-check batches without changing listing results.

**Likely files:** `internal/files/directories.go`, directory tests.

- Collect bounded candidate-directory frontiers instead of coupling checks to each
  128-entry ReadDir chunk. Keep existing time/entry/directory limits and cancellation.
- Preserve empty directories, metadata exclusions, symlink handling and explicit
  truncated results. No candidate is descended into before its ignore decision.

**Done when:** fixture outputs match the current implementation; wide/deep trees,
empty directories, caps/cancellation and symlinks retain existing behavior. This
structural step does not claim subprocess reductions until PF-14.

## PF-14 — Batched ignore checks

**Goal:** reduce Git process count using PF-13 frontiers.

**Likely files:** `internal/files/{directories,files}.go`, directory/listing tests.

- Batch candidates within safe input/memory budgets using the existing Git runner.
- Preserve check-before-descent and fail closed on ignore-command failure. Never
  enter ignored dependency trees optimistically or infer all directories from files.
- Keep deleted-tracked filtering and listing caps; this is not a project-index cache.

**Done when:** a wide-tree fixture launches fewer ignore subprocesses with equivalent
paths/truncation semantics. Tests cover nested ignore rules, ignored parents,
empty folders, failure, cancellation, unusual filenames and Windows path behavior.

## PF-15 — Integrated evidence and milestone gate

**Goal:** establish what improved and what remains, without overstating results.

**Likely files:** benchmark/test suites, `docs/perf.md`, this tracker and `TASKS.md`.

- Repeat PF-01…PF-03 workloads on the same host; record distributions, allocations,
  payload/work counts and retained memory where measured. Preserve historical data.
- Run `go test ./...`, `go vet ./...`, `go build ./...`, affected race tests, and
  frontend `npm test`, `npm run check`, `npm run build` with installed tooling.
- Exercise in-process RPC/local parity and existing Windows-oriented path/Git
  tests; no Windows package, private-session scan or GUI launch implied.
- Record failures, skipped/unavailable checks and remaining bottlenecks. Identify
  follow-up tasks if a blocker cannot fit this package; do not mark them complete.

**Done when:** deterministic gates pass, before/after evidence is reproducible,
remaining full-parse/cold-start limitations are explicit, and native GUI QA is
separately passed or marked unperformed. Summarize recommendations for the next
milestone, whose detailed plan/tasks must be written before implementation.
