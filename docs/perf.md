# Agent Sessions MVP Performance & QA Report

**Date:** 2026-09-28  
**Milestone:** M1 MVP QA Pass (M1-28)  
**Host Environment:** Ubuntu 26.04 LTS x86_64, Linux kernel 7.0.0-34-generic, Go 1.26.8, WebKitGTK 4.1  
**Scanned Data:** Live local environments for Claude Code (`~/.claude`), OpenAI Codex CLI (`~/.codex`), and OpenCode (`~/.local/share/opencode`).

---

## 1. SPEC §8 Performance Targets & Results

| Metric | SPEC §8 Target | Measured Value | Status | Notes |
|---|---|---|---|---|
| **Cold first scan** | < 5,000 ms (≈300 MB history) | **485 ms** | **PASS** (10.3× faster) | Scans all 3 providers concurrently; 68 total sessions found |
| **Warm scan / re-scan** | < 1,000 ms | **120 ms** | **PASS** (8.3× faster) | Catalog state reuse; unchanged files skipped |
| **Loading 38 MB session** | < 1,000 ms to first screen | **629 ms** (full parse) | **PASS** (1.6× faster) | 37.2 MB JSONL (`695fb531...`, 2,321 msgs, 6,058 records) parsed in 629 ms; paged first 50 msgs displayed immediately |
| **Session list scroll FPS** | 60 FPS (1,000+ items) | **60 FPS** | **PASS** | Fixed-height `VirtualList` caps active DOM items to ~25 |
| **Transcript scroll FPS** | 60 FPS (large session) | **60 FPS** | **PASS** | Progressive pagination (50 msgs/page) + DOM load-on-scroll |
| **Desktop binary size** | < 30 MB ELF | **13.4 MB** | **PASS** | Complete self-contained desktop binary with embedded frontend assets |
| **Memory usage (idle)** | < 150 MB RSS | **~48 MB** | **PASS** | Go backend + WebKitGTK idle runtime |
| **Memory usage (38 MB view)** | < 250 MB RSS | **~112 MB** | **PASS** | Controlled by 4-entry transcript LRU cache |

---

## 2. Ingestion & Diagnostics Breakdown

Scanned across detected local providers:

```
AGENT        PRESENT  ROOTS                              GENERATIONS           SESSIONS  PARSE ERRORS
claude-code  true     ~/.claude                          projects/*.jsonl            31             0
codex        true     ~/.codex                           state:state_5.sqlite         1             0
opencode     true     ~/.local/share/opencode            v2, v1                      36             0
TOTAL                                                                                68             0
```

- **Parse Errors:** `0` errors across all providers.
- **Diagnostic Notices:** Claude Code logged 89 unknown non-conversational event types (`atis-latch`, `cost-state`, `queue-operation`), counted without crashing the scan or polluting transcripts.
- **Total Messages Indexed:** 22,710 across all 68 sessions.

---

## 3. Large Session Transcript Profiling

Tested against the largest local session:
- **File:** `~/.claude/projects/-home-haith-Workspaces-ginkcode-new-vimoon/695fb531-6661-4157-8042-50faff27f74b.jsonl`
- **File size:** 37.2 MB (39,010,488 bytes)
- **Turn records:** 6,058 raw JSON records
- **Consolidated messages:** 2,321 (split assistant content blocks merged, tool calls paired)
- **Token usage:** 56,929,190 tokens
- **Full Parse & Consolidate Duration:** 629 ms
- **First Page Retrieval (50 messages):** 631 ms on cold load, < 2 ms on LRU hit
- **Paging / Slicing Duration:** < 1 ms per page

Tested against large OpenCode SQLite session:
- **Session:** `ses_f6614946bffexSe3jwGrQ2j625`
- **Messages:** 789 messages (1,686 database parts)
- **Load Duration:** 185 ms

Tested against Codex session:
- **Session:** `01a0e61e-e703-75a2-bc1d-6349e32f6dd4`
- **Messages:** 2 messages (rollout JSONL)
- **Load Duration:** 6 ms

---

## 4. UI Rendering & Virtualization Verification

1. **VirtualList DOM Nodes:**
   - With 68 local sessions (and tested up to 2,000 synthetic sessions), the rendered row count in DOM is bounded between 20 and 28 elements.
   - `ResizeObserver` on container keeps scroll metrics accurate during splitter drag operations.

2. **Progressive Transcript Loading:**
   - 50 messages per page.
   - Load-on-scroll triggers when user scrolls within 250px of the bottom.
   - Stale-selection guards prevent out-of-order race conditions when switching sessions rapidly.

3. **Zero-Dependency Markdown & Highlighting:**
   - Regex GFM renderer with strict HTML entity escaping for all untrusted text.
   - Syntax highlighter outputs CSS classes (`hljs-*`), allowing instant light/dark mode switching with zero re-parsing.
   - Container-level click delegation for copy buttons handles arbitrarily long code listings with no per-block event listeners.

4. **WebView Security Hardening:**
   - CSP: `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self' ipc: wails:; frame-src 'none'; object-src 'none'; base-uri 'none';`
   - External links intercepted and dispatched via native OS browser opener (`xdg-open` / `wruntime.BrowserOpenURL`), with fail-closed scheme validation.

---

## 5. Verification Commands

Run the automated benchmarking script:
```bash
./scripts/qa-perf-bench.sh
```

Run test suite:
```bash
go test -race -count=1 ./...
cd frontend && npm test && npm run check
```

Build release desktop binary:
```bash
make gui-build
./build/bin/agent-sessions
```


---

## PF-01 — Synthetic catalog and scanner baseline (2026-10-24)

This is a new current-code baseline, **not** a rerun or validation of the historical
MVP/private-store results above. Only generated `testing.TB.TempDir()` provider
roots and explicit SQLite cache directories were used. No real session stores,
private caches, QA performance script, GUI or native package were used.

### Environment and method

- Revision: `035ef9f` plus uncommitted PF-01 code/docs; branch `feature/editor-essentials`.
- Apple M5, 32 GiB RAM; Darwin 27.0.0, arm64; Go 1.26.8, CGO enabled,
  default GOMAXPROCS 10. Installed Node 22.23.2 used for frontend checks.
- Three independent benchmark repetitions, `-benchtime=200ms -benchmem`.
  Fixture construction, database seeding and append writes are outside timers.
  Database close is excluded from open/open-restore measurements.
- Catalog: 1,000 / 10,000 unique deterministic metadata rows (counts, tokens,
  timestamps, model, prompt, CWD/repository and source fields). Roundtrip tests
  compare every restored field and catalog ordering. Restore includes
  `LoadCatalog → NewCatalog → Reset → All`; open includes existing DB integrity
  checks, not initial schema creation. Open-restore combines both paths.
- Each provider: 64 generated files, 32 user records per file. Claude totals
  250,624 bytes / 2,048 records. Codex adds one session_meta per file: 346,688
  bytes / 2,112 records. No index/sidecars/live sessions in the timed fixture;
  metadata-change correctness/work accounting is tested separately.
- Cold means no prior ScanState, **not** flushed OS disk caches. Unchanged uses
  the initial state. Append adds one newline-terminated user record to one file,
  then repeatedly scans from the *same pre-append snapshot*, so every iteration
  measures exactly one pending append without unbounded fixture growth.
- `ScanWork` is opt-in/context-local, atomic across workers, and contains no paths
  or session contents. Counts are logical transcript open attempts, consumed
  records/record bytes (excluding newline), and checkpoint JSON transformations.
  They do not measure kernel reads, physical I/O, discovery/index/sidecar bytes,
  retained heap, or wall time with accounting disabled. No schema/API change.

Reproduce safely (do not use the historical QA script above):

```sh
go test ./internal/index ./internal/provider ./internal/provider/claude ./internal/provider/codex
go test ./internal/index ./internal/provider -run '^$' -bench '^BenchmarkSynthetic' -benchmem -benchtime=200ms -count=3
```

### Measured summary

Times below are median [min–max] across three repetitions. B/op and allocations
are ranges, **allocated work**, not retained memory or RSS. Absolute timings are
advisory and are never CI assertions.

| Workload | ms/op median [min–max] | B/op range | allocs/op range |
|---|---:|---:|---:|
| Catalog 1k open | 1.547 [1.528–1.563] | 20,222–20,314 | 188 |
| Catalog 1k restore | 4.284 [4.283–4.468] | 4,707,500–4,707,724 | 21,034 |
| Catalog 1k open-restore | 6.007 [5.966–6.008] | 4,729,615–4,729,681 | 21,226–21,227 |
| Catalog 10k open | 12.527 [12.327–14.831] | 20,265–20,397 | 188–189 |
| Catalog 10k restore | 59.952 [53.453–67.491] | 53,223,618–53,223,670 | 210,083 |
| Catalog 10k open-restore | 68.646 [68.028–71.628] | 53,245,445–53,245,706 | 210,273–210,276 |
| Claude cold | 3.724 [3.719–3.734] | 67,738,318–67,744,977 | 11,722–11,745 |
| Claude unchanged | 0.269 [0.266–0.273] | 188,053–188,070 | 1,337 |
| Claude one-source append | 0.373 [0.366–0.374] | 1,242,470–1,242,571 | 1,364 |
| Codex cold | 6.044 [6.016–6.047] | 68,764,572–68,767,334 | 17,852–17,869 |
| Codex unchanged | 3.103 [3.101–3.117] | 1,356,366–1,356,785 | 10,745–10,746 |
| Codex one-source append | 3.335 [3.225–3.356] | 2,423,647–2,424,677 | 10,765–10,769 |

| Provider / mode | Transcript opens | Records | Record bytes | Checkpoint decodes | Checkpoint encodes |
|---|---:|---:|---:|---:|---:|
| Claude cold | 64 | 2,048 | 248,576 | 0 | 64 |
| Claude unchanged | 0 | 0 | 0 | 64 | 0 |
| Claude append | 1 | 1 | 122 | 64 | 1 |
| Codex cold | 64 | 2,112 | 344,576 | 64 | 128 |
| Codex unchanged | 0 | 0 | 0 | 192 | 64 |
| Codex append | 1 | 1 | 165 | 192 | 65 |

### Correctness, verification and limits

- New synthetic scan tests assert initial counts/prompts, zero unchanged changes,
  exactly one appended user count, no second append effect, source cardinality,
  diagnostics, and exact open/record/checkpoint work counts.
- Existing `TestScanSubagentAndMetadataChange` now asserts sidecar-only update work
  (1 open, 0 records, 2 decodes, 1 encode); metadata/count/token/removal parity
  remains checked. Existing Codex changed-title/index-disappearance test asserts
  index-only work (0 opens/records, 3 decodes, 1 encode). Existing edge, duplicate,
  archive, index-fallback, checkpoint roundtrip and resume-boundary tests pass.
- New active-session regression uses only its own process PID and temporary
  live-session file: initial overlay, status change and removal are observed via
  the existing Live channel without transcript reparsing. It intentionally does
  not change unchanged-Scan's existing emission semantics.
- Passed: focused packages; affected `go test -race`; `go test ./...`;
  `go vet ./...`; `go build ./...`; frontend tests (665 passed), check and build.
  Cached pinned gofumpt 0.12.0 used. Cached golangci-lint 2.14.0 full-repo run
  reports existing unrelated findings (including errcheck, unused helpers,
  refresh SA2001 and search suggestions); new task files have no remaining
  findings. No unrelated lint cleanup attempted.
- No native GUI/Windows execution, disk-cache flushing, retained-memory profiling,
  large/private history claims or performance optimization is implied. Synthetic
  sources are intentionally manageable; this baseline does not model transcript
  loading, thousands of source files, filesystem watcher setup or FTS work.
- Evidence points to unchanged Codex checkpoint canonicalization (three decode
  passes and one encode per source), Claude's one decode per unchanged source,
  and catalog restoration allocation scaling. Cold scans allocate roughly 1 MiB
  per opened source even on these small files; source-open allocation deserves
  later profiling, not an unmeasured fix inside PF-01.
- Recommended next task: **PF-02** transcript/load/concurrency baselines; retain
  this PF-01 evidence for PF-09/PF-10 before/after comparisons.

### Raw repeated benchmark output

```text
goos: darwin
goarch: arm64
pkg: github.com/ginkcode/agent-sessions/internal/index
cpu: Apple M5
BenchmarkSyntheticCatalog/1000/open-10    	     152	   1527958 ns/op	   20314 B/op	     188 allocs/op
BenchmarkSyntheticCatalog/1000/open-10    	     156	   1563197 ns/op	   20222 B/op	     188 allocs/op
BenchmarkSyntheticCatalog/1000/open-10    	     153	   1546931 ns/op	   20226 B/op	     188 allocs/op
BenchmarkSyntheticCatalog/1000/restore-10 	      48	   4468280 ns/op	 4707724 B/op	   21034 allocs/op
BenchmarkSyntheticCatalog/1000/restore-10 	      50	   4283617 ns/op	 4707500 B/op	   21034 allocs/op
BenchmarkSyntheticCatalog/1000/restore-10 	      51	   4282637 ns/op	 4707503 B/op	   21034 allocs/op
BenchmarkSyntheticCatalog/1000/open-restore-10         	      37	   5966387 ns/op	 4729654 B/op	   21227 allocs/op
BenchmarkSyntheticCatalog/1000/open-restore-10         	      37	   6008429 ns/op	 4729681 B/op	   21226 allocs/op
BenchmarkSyntheticCatalog/1000/open-restore-10         	      38	   6007123 ns/op	 4729615 B/op	   21227 allocs/op
BenchmarkSyntheticCatalog/10000/open-10                	      18	  12327315 ns/op	   20330 B/op	     188 allocs/op
BenchmarkSyntheticCatalog/10000/open-10                	      19	  12526763 ns/op	   20265 B/op	     188 allocs/op
BenchmarkSyntheticCatalog/10000/open-10                	      18	  14830718 ns/op	   20397 B/op	     189 allocs/op
BenchmarkSyntheticCatalog/10000/restore-10             	       4	  67491010 ns/op	53223670 B/op	  210083 allocs/op
BenchmarkSyntheticCatalog/10000/restore-10             	       4	  59951604 ns/op	53223642 B/op	  210083 allocs/op
BenchmarkSyntheticCatalog/10000/restore-10             	       4	  53452938 ns/op	53223618 B/op	  210083 allocs/op
BenchmarkSyntheticCatalog/10000/open-restore-10        	       3	  71628194 ns/op	53245706 B/op	  210276 allocs/op
BenchmarkSyntheticCatalog/10000/open-restore-10        	       3	  68027972 ns/op	53245498 B/op	  210275 allocs/op
BenchmarkSyntheticCatalog/10000/open-restore-10        	       3	  68645694 ns/op	53245445 B/op	  210273 allocs/op
PASS
ok  	github.com/ginkcode/agent-sessions/internal/index	27.438s
goos: darwin
goarch: arm64
pkg: github.com/ginkcode/agent-sessions/internal/provider
cpu: Apple M5
BenchmarkSyntheticScan/claude/cold-10    	      63	   3734303 ns/op	         0 cp-decodes/op	        64.00 cp-encodes/op	    250624 fixture-B	    248576 record-B/op	      2048 records/op	        64.00 source-opens/op	67744401 B/op	   11742 allocs/op
BenchmarkSyntheticScan/claude/cold-10    	      62	   3719340 ns/op	         0 cp-decodes/op	        64.00 cp-encodes/op	    250624 fixture-B	    248576 record-B/op	      2048 records/op	        64.00 source-opens/op	67744977 B/op	   11745 allocs/op
BenchmarkSyntheticScan/claude/cold-10    	      58	   3724175 ns/op	         0 cp-decodes/op	        64.00 cp-encodes/op	    250624 fixture-B	    248576 record-B/op	      2048 records/op	        64.00 source-opens/op	67738318 B/op	   11722 allocs/op
BenchmarkSyntheticScan/claude/unchanged-10         	     873	    265918 ns/op	        64.00 cp-decodes/op	         0 cp-encodes/op	    250624 fixture-B	         0 record-B/op	         0 records/op	         0 source-opens/op	  188067 B/op	    1337 allocs/op
BenchmarkSyntheticScan/claude/unchanged-10         	     908	    269040 ns/op	        64.00 cp-decodes/op	         0 cp-encodes/op	    250624 fixture-B	         0 record-B/op	         0 records/op	         0 source-opens/op	  188053 B/op	    1337 allocs/op
BenchmarkSyntheticScan/claude/unchanged-10         	     894	    273129 ns/op	        64.00 cp-decodes/op	         0 cp-encodes/op	    250624 fixture-B	         0 record-B/op	         0 records/op	         0 source-opens/op	  188070 B/op	    1337 allocs/op
BenchmarkSyntheticScan/claude/one-source-append-10 	     678	    373321 ns/op	        64.00 cp-decodes/op	         1.000 cp-encodes/op	    250624 fixture-B	       122.0 record-B/op	         1.000 records/op	         1.000 source-opens/op	 1242571 B/op	    1364 allocs/op
BenchmarkSyntheticScan/claude/one-source-append-10 	     668	    366249 ns/op	        64.00 cp-decodes/op	         1.000 cp-encodes/op	    250624 fixture-B	       122.0 record-B/op	         1.000 records/op	         1.000 source-opens/op	 1242500 B/op	    1364 allocs/op
BenchmarkSyntheticScan/claude/one-source-append-10 	     655	    373611 ns/op	        64.00 cp-decodes/op	         1.000 cp-encodes/op	    250624 fixture-B	       122.0 record-B/op	         1.000 records/op	         1.000 source-opens/op	 1242470 B/op	    1364 allocs/op
BenchmarkSyntheticScan/codex/cold-10               	      37	   6016338 ns/op	        64.00 cp-decodes/op	       128.0 cp-encodes/op	    346688 fixture-B	    344576 record-B/op	      2112 records/op	        64.00 source-opens/op	68764572 B/op	   17852 allocs/op
BenchmarkSyntheticScan/codex/cold-10               	      37	   6043628 ns/op	        64.00 cp-decodes/op	       128.0 cp-encodes/op	    346688 fixture-B	    344576 record-B/op	      2112 records/op	        64.00 source-opens/op	68767334 B/op	   17869 allocs/op
BenchmarkSyntheticScan/codex/cold-10               	      37	   6046916 ns/op	        64.00 cp-decodes/op	       128.0 cp-encodes/op	    346688 fixture-B	    344576 record-B/op	      2112 records/op	        64.00 source-opens/op	68766172 B/op	   17863 allocs/op
BenchmarkSyntheticScan/codex/unchanged-10          	      81	   3116727 ns/op	       192.0 cp-decodes/op	        64.00 cp-encodes/op	    346688 fixture-B	         0 record-B/op	         0 records/op	         0 source-opens/op	 1356719 B/op	   10746 allocs/op
BenchmarkSyntheticScan/codex/unchanged-10          	      81	   3100622 ns/op	       192.0 cp-decodes/op	        64.00 cp-encodes/op	    346688 fixture-B	         0 record-B/op	         0 records/op	         0 source-opens/op	 1356366 B/op	   10745 allocs/op
BenchmarkSyntheticScan/codex/unchanged-10          	      79	   3103301 ns/op	       192.0 cp-decodes/op	        64.00 cp-encodes/op	    346688 fixture-B	         0 record-B/op	         0 records/op	         0 source-opens/op	 1356785 B/op	   10746 allocs/op
BenchmarkSyntheticScan/codex/one-source-append-10  	      80	   3225017 ns/op	       192.0 cp-decodes/op	        65.00 cp-encodes/op	    346688 fixture-B	       165.0 record-B/op	         1.000 records/op	         1.000 source-opens/op	 2424677 B/op	   10769 allocs/op
BenchmarkSyntheticScan/codex/one-source-append-10  	      68	   3355997 ns/op	       192.0 cp-decodes/op	        65.00 cp-encodes/op	    346688 fixture-B	       165.0 record-B/op	         1.000 records/op	         1.000 source-opens/op	 2423647 B/op	   10765 allocs/op
BenchmarkSyntheticScan/codex/one-source-append-10  	      78	   3334757 ns/op	       192.0 cp-decodes/op	        65.00 cp-encodes/op	    346688 fixture-B	       165.0 record-B/op	         1.000 records/op	         1.000 source-opens/op	 2423656 B/op	   10766 allocs/op
PASS
ok  	github.com/ginkcode/agent-sessions/internal/provider	5.313s
```


## PF-02 — Synthetic transcript baseline (2026-10-24)

PF-02 complete; PF-01 evidence above is unchanged. No cache optimization.

### Method and correctness

- Revision `035ef9f` plus uncommitted PF-01/PF-02 work, Apple M5 / 32 GiB,
  Darwin 27.0.0 arm64, Go 1.26.8, default GOMAXPROCS 10.
- Generated temporary roots only; no private session stores, QA performance script,
  app launches, installs, or process termination. Each source has 256 turns / 512
  messages, split assistant records, 256 completed tool results, and a valid JSON
  final record without a newline adding an unanswered tool to the last assistant.
  Existing provider tests also cover malformed/truncated JSON tails.
- “38/100 MB” means decimal target prompt bytes plus valid envelope overhead, not
  MiB. All generated prompt bytes are checked, then complete message output is
  compared with the small equivalent after normalizing only prompt length.
  Identity/count/token/model/creation metadata is compared too; path, mtime and
  prompt-derived metadata intentionally differ. Cold/warm/invalidated pages match.
- Three repetitions, one operation each (`-benchtime=1x -count=3`). Final runs
  executed without other verification jobs; aborted exploratory runs are excluded.
  Generation, service/barrier setup, heap sampling and JSON marshaling are outside
  timers. Cold means provider parse/cache miss, **not** flushed filesystem caches.
  Warm is GetMessages only, not serialization/network/frontend rendering.
- Same-key and independent-key operations are batches of four concurrent misses.
  The reusable `providertest.Barrier` blocks all four before forwarding real Load
  calls, eliminating scheduler-dependent cache hits. Independent keys route to
  four separate generated roots through a test-only adapter. Batch timing includes
  goroutine/barrier scheduling; no wall-clock assertions. Deterministic tests
  release loads in reverse order and verify output and subsequent cache hits.
- `LoadWork` uses context-local atomics for public Load calls, successful transcript
  opens, and bytes delivered by file Read calls (including tail/newlines). This is
  **not physical disk I/O**; discovery/index/sidecar bytes are excluded. Existing
  `ScanWork` separately counts canonical-resolution record bytes (no newlines or
  tail), exposing Codex's extra full scan. No logging, content/path retention or
  production behavior/cache policy changes.

Reproduce safely:

```sh
go test ./internal/engine -run 'TestTranscript' -count=1
go test ./internal/provider/providertest -run TestBarrier -count=10
go test ./internal/engine -run '^$' -bench '^BenchmarkTranscript$' -benchmem -benchtime=1x -count=3
go test -race ./internal/engine ./internal/provider/claude ./internal/provider/codex ./internal/provider/providertest -run 'Test(TranscriptBaselineEquivalence|Barrier|GetMessages|Load|.*Evict.*|.*Cache.*)' -count=1
go test -race ./internal/engine ./internal/provider/providertest -run 'Test(TranscriptConcurrentMissBaseline|Barrier)' -count=10
```

### Repeated timing and allocation summary

Median [min–max] of three single-operation samples; advisory, not CI thresholds.
B/op is cumulative allocation, not retained heap. Concurrent rows are per **batch**.
Warm nanosecond samples are especially noisy at 1x; the robust result is zero
provider calls/bytes and about 8 KiB of shallow page-copy allocation.

| Provider / target / mode | ms/op median [min–max] | B/op range | allocs/op range |
|---|---:|---:|---:|
| claude/38MB/provider-cold | 384.548666 [376.542875–384.669833] | 202,456,848–202,457,344 | 36,745–36,758 |
| claude/38MB/cold-page | 403.148375 [394.399917–403.654333] | 202,465,480–202,465,672 | 36,751–36,758 |
| claude/38MB/warm-page | 0.001959 [0.001708–0.002208] | 8,240–8,240 | 2–2 |
| claude/38MB/invalidated | 389.215041 [388.237875–395.956292] | 202,465,512–202,468,616 | 36,758–36,769 |
| claude/38MB/same-key-4 | 414.349875 [409.229541–417.494918] | 809,866,776–809,872,448 | 147,131–147,133 |
| claude/38MB/independent-key-4 | 422.546000 [414.389834–445.371250] | 809,868,056–809,870,232 | 147,114–147,162 |
| claude/100MB/provider-cold | 1044.725666 [1030.151334–1064.910041] | 506,781,888–506,783,088 | 36,747–36,754 |
| claude/100MB/cold-page | 1019.021541 [1005.775167–1019.639584] | 506,791,416–506,791,784 | 36,752–36,762 |
| claude/100MB/warm-page | 0.003542 [0.002792–0.005708] | 8,240–8,352 | 2–3 |
| claude/100MB/invalidated | 1040.826709 [1039.741458–1041.261791] | 506,791,448–506,792,856 | 36,757–36,767 |
| claude/100MB/same-key-4 | 1098.334917 [1091.758875–1102.974666] | 2,027,166,328–2,027,167,416 | 147,142–147,147 |
| claude/100MB/independent-key-4 | 1124.937625 [1120.778041–1131.795041] | 2,027,165,872–2,027,166,560 | 147,132–147,141 |
| codex/38MB/provider-cold | 641.262708 [639.532000–647.186375] | 244,256,768–244,269,936 | 50,124–50,133 |
| codex/38MB/cold-page | 595.952625 [592.217999–611.419292] | 244,261,672–244,281,336 | 50,123–50,144 |
| codex/38MB/warm-page | 0.002042 [0.001792–0.002333] | 8,240–8,240 | 2–2 |
| codex/38MB/invalidated | 614.305375 [609.924833–616.346833] | 244,278,072–244,281,912 | 50,136–50,144 |
| codex/38MB/same-key-4 | 689.029917 [688.688334–701.021292] | 977,095,120–977,109,600 | 200,545–200,562 |
| codex/38MB/independent-key-4 | 686.296583 [680.195292–708.728583] | 977,101,328–977,107,872 | 200,546–200,555 |
| codex/100MB/provider-cold | 1666.129208 [1665.711375–1668.671542] | 609,411,856–609,413,296 | 50,131–50,134 |
| codex/100MB/cold-page | 1663.465499 [1650.557167–1715.253459] | 609,420,376–609,420,856 | 50,136–50,137 |
| codex/100MB/warm-page | 0.003375 [0.002917–0.004000] | 8,240–8,240 | 2–2 |
| codex/100MB/invalidated | 1680.405708 [1661.063500–1684.713667] | 609,420,216–609,420,552 | 50,136–50,139 |
| codex/100MB/same-key-4 | 1867.674584 [1847.084918–1884.795458] | 2,437,668,032–2,437,669,824 | 200,547–200,554 |
| codex/100MB/independent-key-4 | 1897.435167 [1897.342249–1903.175999] | 2,437,670,224–2,437,677,744 | 200,543–200,562 |

### Work, payload and memory

Each cold/provider/invalidated operation asserts one Load/open and exactly one
source-file byte count; warm asserts zero; both four-request batches assert four.
Codex resolution record bytes are additional to transcript bytes, not a substitute.

| Provider / target | Source bytes / Load | Resolution record bytes / Load | Latest 50-message JSON bytes | Full transcript JSON bytes | Retained **estimate** bytes | Measured live HeapAlloc delta bytes |
|---|---:|---:|---:|---:|---:|---:|
| claude/38MB | 38,155,553 | 0 | 3,722,952 | 38,122,450 | 38,220,486 | 40,424,976 |
| claude/100MB | 100,155,681 | 0 | 9,777,652 | 100,122,579 | 100,220,614 | 101,691,920 |
| codex/38MB | 38,198,233 | 38,196,812 | 3,723,052 | 38,123,397 | 38,221,400 | 40,419,304 |
| codex/100MB | 100,198,361 | 100,196,940 | 9,777,752 | 100,123,526 | 100,221,528 | 101,698,776 |

- Retained estimate: struct/slice capacity plus message text, IDs and tool-content
  lengths using `unsafe.Sizeof`. Excludes allocator rounding, metadata strings,
  sharing, runtime/GC overhead and parse temporaries; not a measured heap size.
- Measured live heap: process-wide `runtime.MemStats.HeapAlloc` delta after forced
  GCs before/after one public Load, with the result kept alive. Sampled once per
  provider/size parent fixture, repeated as context on child rows (not three
  independent heap samples). This is noisy, not peak heap/RSS or cache capacity.
- Latest page retains shallow references into a full cached transcript. Four
  independent 100 MB entries can retain roughly four times the per-transcript
  estimate (~401 MB); this is an extrapolation, not a measured cache heap.
- Same-key misses currently do four loads and ~4× allocations, just like independent
  keys. At 100 MB that is ~2.03 GB Claude / ~2.44 GB Codex cumulative allocated work
  per four-request batch. Codex also scans ~400.8 MB of resolution records per batch.
  Warm pages avoid this work but still serialize ~9.78 MB for this text-heavy fixture.
  No claim about typical private histories or native GUI latency is implied.

### Verification and limits

Passed: focused engine/provider load/paging/barrier tests; large-equivalent tests;
focused affected race tests (including the large fixtures); barrier/concurrent
regressions repeated ten times under race; `go test ./...`; `go build ./...`;
`go vet ./...`; frontend tests (665 passed), check (0 errors, existing
ignoreDeprecations warning), and build. `git diff --check` passes.
No native GUI/Windows execution or peak memory profile. Existing dirty PF-01 files
and baselines were preserved. PF-03…PF-15 remain unimplemented; PF-04/PF-05 can reuse
the per-attempt barrier, cancellation context, ordered replies, failure replies,
ignore-cancellation mode and real-provider forwarding. Future coalescing changes
must update the explicitly named four-load baseline and its barrier expectations.

### Raw repeated benchmark output

```text
goos: darwin
goarch: arm64
pkg: github.com/ginkcode/agent-sessions/internal/engine
cpu: Apple M5
BenchmarkTranscript/claude/38MB/provider-cold-10         	       1	 376542875 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202456848 B/op	   36745 allocs/op
BenchmarkTranscript/claude/38MB/provider-cold-10         	       1	 384548666 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202457344 B/op	   36758 allocs/op
BenchmarkTranscript/claude/38MB/provider-cold-10         	       1	 384669833 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202457040 B/op	   36750 allocs/op
BenchmarkTranscript/claude/38MB/cold-page-10             	       1	 394399917 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202465592 B/op	   36754 allocs/op
BenchmarkTranscript/claude/38MB/cold-page-10             	       1	 403654333 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202465672 B/op	   36758 allocs/op
BenchmarkTranscript/claude/38MB/cold-page-10             	       1	 403148375 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202465480 B/op	   36751 allocs/op
BenchmarkTranscript/claude/38MB/warm-page-10             	       1	      1708 ns/op	  40424976 heap-live-B/transcript	         0 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	  38122450 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/claude/38MB/warm-page-10             	       1	      2208 ns/op	  40424976 heap-live-B/transcript	         0 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	  38122450 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/claude/38MB/warm-page-10             	       1	      1959 ns/op	  40424976 heap-live-B/transcript	         0 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	  38122450 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/claude/38MB/invalidated-10           	       1	 388237875 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202468616 B/op	   36769 allocs/op
BenchmarkTranscript/claude/38MB/invalidated-10           	       1	 389215041 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202465512 B/op	   36758 allocs/op
BenchmarkTranscript/claude/38MB/invalidated-10           	       1	 395956292 ns/op	  40424976 heap-live-B/transcript	         1.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	  38155553 source-B/op	         1.000 source-opens/op	  38122450 transcript-JSON-B	202465512 B/op	   36758 allocs/op
BenchmarkTranscript/claude/38MB/same-key-4-10            	       1	 414349875 ns/op	  40424976 heap-live-B/transcript	         4.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	 152622212 source-B/op	         4.000 source-opens/op	  38122450 transcript-JSON-B	809872448 B/op	  147132 allocs/op
BenchmarkTranscript/claude/38MB/same-key-4-10            	       1	 409229541 ns/op	  40424976 heap-live-B/transcript	         4.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	 152622212 source-B/op	         4.000 source-opens/op	  38122450 transcript-JSON-B	809867368 B/op	  147131 allocs/op
BenchmarkTranscript/claude/38MB/same-key-4-10            	       1	 417494918 ns/op	  40424976 heap-live-B/transcript	         4.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	 152622212 source-B/op	         4.000 source-opens/op	  38122450 transcript-JSON-B	809866776 B/op	  147133 allocs/op
BenchmarkTranscript/claude/38MB/independent-key-4-10     	       1	 422546000 ns/op	  40424976 heap-live-B/transcript	         4.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	 152622212 source-B/op	         4.000 source-opens/op	  38122450 transcript-JSON-B	809870232 B/op	  147146 allocs/op
BenchmarkTranscript/claude/38MB/independent-key-4-10     	       1	 445371250 ns/op	  40424976 heap-live-B/transcript	         4.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	 152622212 source-B/op	         4.000 source-opens/op	  38122450 transcript-JSON-B	809868056 B/op	  147114 allocs/op
BenchmarkTranscript/claude/38MB/independent-key-4-10     	       1	 414389834 ns/op	  40424976 heap-live-B/transcript	         4.000 loads/op	   3722952 page-JSON-B	         0 resolve-record-B/op	  38220486 retained-est-B/transcript	 152622212 source-B/op	         4.000 source-opens/op	  38122450 transcript-JSON-B	809869704 B/op	  147162 allocs/op
BenchmarkTranscript/claude/100MB/provider-cold-10        	       1	1044725666 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506781888 B/op	   36754 allocs/op
BenchmarkTranscript/claude/100MB/provider-cold-10        	       1	1064910041 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506783088 B/op	   36752 allocs/op
BenchmarkTranscript/claude/100MB/provider-cold-10        	       1	1030151334 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506782896 B/op	   36747 allocs/op
BenchmarkTranscript/claude/100MB/cold-page-10            	       1	1005775167 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506791624 B/op	   36757 allocs/op
BenchmarkTranscript/claude/100MB/cold-page-10            	       1	1019021541 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506791784 B/op	   36762 allocs/op
BenchmarkTranscript/claude/100MB/cold-page-10            	       1	1019639584 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506791416 B/op	   36752 allocs/op
BenchmarkTranscript/claude/100MB/warm-page-10            	       1	      5708 ns/op	 101691920 heap-live-B/transcript	         0 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	 100122579 transcript-JSON-B	    8352 B/op	       3 allocs/op
BenchmarkTranscript/claude/100MB/warm-page-10            	       1	      2792 ns/op	 101691920 heap-live-B/transcript	         0 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	 100122579 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/claude/100MB/warm-page-10            	       1	      3542 ns/op	 101691920 heap-live-B/transcript	         0 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	 100122579 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/claude/100MB/invalidated-10          	       1	1041261791 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506792856 B/op	   36759 allocs/op
BenchmarkTranscript/claude/100MB/invalidated-10          	       1	1039741458 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506791832 B/op	   36767 allocs/op
BenchmarkTranscript/claude/100MB/invalidated-10          	       1	1040826709 ns/op	 101691920 heap-live-B/transcript	         1.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 100155681 source-B/op	         1.000 source-opens/op	 100122579 transcript-JSON-B	506791448 B/op	   36757 allocs/op
BenchmarkTranscript/claude/100MB/same-key-4-10           	       1	1091758875 ns/op	 101691920 heap-live-B/transcript	         4.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 400622724 source-B/op	         4.000 source-opens/op	 100122579 transcript-JSON-B	2027167416 B/op	  147142 allocs/op
BenchmarkTranscript/claude/100MB/same-key-4-10           	       1	1098334917 ns/op	 101691920 heap-live-B/transcript	         4.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 400622724 source-B/op	         4.000 source-opens/op	 100122579 transcript-JSON-B	2027166328 B/op	  147143 allocs/op
BenchmarkTranscript/claude/100MB/same-key-4-10           	       1	1102974666 ns/op	 101691920 heap-live-B/transcript	         4.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 400622724 source-B/op	         4.000 source-opens/op	 100122579 transcript-JSON-B	2027167400 B/op	  147147 allocs/op
BenchmarkTranscript/claude/100MB/independent-key-4-10    	       1	1120778041 ns/op	 101691920 heap-live-B/transcript	         4.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 400622724 source-B/op	         4.000 source-opens/op	 100122579 transcript-JSON-B	2027165872 B/op	  147141 allocs/op
BenchmarkTranscript/claude/100MB/independent-key-4-10    	       1	1131795041 ns/op	 101691920 heap-live-B/transcript	         4.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 400622724 source-B/op	         4.000 source-opens/op	 100122579 transcript-JSON-B	2027166544 B/op	  147134 allocs/op
BenchmarkTranscript/claude/100MB/independent-key-4-10    	       1	1124937625 ns/op	 101691920 heap-live-B/transcript	         4.000 loads/op	   9777652 page-JSON-B	         0 resolve-record-B/op	 100220614 retained-est-B/transcript	 400622724 source-B/op	         4.000 source-opens/op	 100122579 transcript-JSON-B	2027166560 B/op	  147132 allocs/op
BenchmarkTranscript/codex/38MB/provider-cold-10          	       1	 641262708 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244256864 B/op	   50128 allocs/op
BenchmarkTranscript/codex/38MB/provider-cold-10          	       1	 639532000 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244269936 B/op	   50133 allocs/op
BenchmarkTranscript/codex/38MB/provider-cold-10          	       1	 647186375 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244256768 B/op	   50124 allocs/op
BenchmarkTranscript/codex/38MB/cold-page-10              	       1	 592217999 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244264440 B/op	   50128 allocs/op
BenchmarkTranscript/codex/38MB/cold-page-10              	       1	 595952625 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244281336 B/op	   50144 allocs/op
BenchmarkTranscript/codex/38MB/cold-page-10              	       1	 611419292 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244261672 B/op	   50123 allocs/op
BenchmarkTranscript/codex/38MB/warm-page-10              	       1	      2042 ns/op	  40419304 heap-live-B/transcript	         0 loads/op	   3723052 page-JSON-B	         0 resolve-record-B/op	  38221400 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	  38123397 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/codex/38MB/warm-page-10              	       1	      2333 ns/op	  40419304 heap-live-B/transcript	         0 loads/op	   3723052 page-JSON-B	         0 resolve-record-B/op	  38221400 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	  38123397 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/codex/38MB/warm-page-10              	       1	      1792 ns/op	  40419304 heap-live-B/transcript	         0 loads/op	   3723052 page-JSON-B	         0 resolve-record-B/op	  38221400 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	  38123397 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/codex/38MB/invalidated-10            	       1	 609924833 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244281912 B/op	   50144 allocs/op
BenchmarkTranscript/codex/38MB/invalidated-10            	       1	 614305375 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244278072 B/op	   50136 allocs/op
BenchmarkTranscript/codex/38MB/invalidated-10            	       1	 616346833 ns/op	  40419304 heap-live-B/transcript	         1.000 loads/op	   3723052 page-JSON-B	  38196812 resolve-record-B/op	  38221400 retained-est-B/transcript	  38198233 source-B/op	         1.000 source-opens/op	  38123397 transcript-JSON-B	244281544 B/op	   50144 allocs/op
BenchmarkTranscript/codex/38MB/same-key-4-10             	       1	 701021292 ns/op	  40419304 heap-live-B/transcript	         4.000 loads/op	   3723052 page-JSON-B	 152787248 resolve-record-B/op	  38221400 retained-est-B/transcript	 152792932 source-B/op	         4.000 source-opens/op	  38123397 transcript-JSON-B	977103248 B/op	  200545 allocs/op
BenchmarkTranscript/codex/38MB/same-key-4-10             	       1	 689029917 ns/op	  40419304 heap-live-B/transcript	         4.000 loads/op	   3723052 page-JSON-B	 152787248 resolve-record-B/op	  38221400 retained-est-B/transcript	 152792932 source-B/op	         4.000 source-opens/op	  38123397 transcript-JSON-B	977095120 B/op	  200551 allocs/op
BenchmarkTranscript/codex/38MB/same-key-4-10             	       1	 688688334 ns/op	  40419304 heap-live-B/transcript	         4.000 loads/op	   3723052 page-JSON-B	 152787248 resolve-record-B/op	  38221400 retained-est-B/transcript	 152792932 source-B/op	         4.000 source-opens/op	  38123397 transcript-JSON-B	977109600 B/op	  200562 allocs/op
BenchmarkTranscript/codex/38MB/independent-key-4-10      	       1	 680195292 ns/op	  40419304 heap-live-B/transcript	         4.000 loads/op	   3723052 page-JSON-B	 152787248 resolve-record-B/op	  38221400 retained-est-B/transcript	 152792932 source-B/op	         4.000 source-opens/op	  38123397 transcript-JSON-B	977101328 B/op	  200546 allocs/op
BenchmarkTranscript/codex/38MB/independent-key-4-10      	       1	 686296583 ns/op	  40419304 heap-live-B/transcript	         4.000 loads/op	   3723052 page-JSON-B	 152787248 resolve-record-B/op	  38221400 retained-est-B/transcript	 152792932 source-B/op	         4.000 source-opens/op	  38123397 transcript-JSON-B	977107872 B/op	  200555 allocs/op
BenchmarkTranscript/codex/38MB/independent-key-4-10      	       1	 708728583 ns/op	  40419304 heap-live-B/transcript	         4.000 loads/op	   3723052 page-JSON-B	 152787248 resolve-record-B/op	  38221400 retained-est-B/transcript	 152792932 source-B/op	         4.000 source-opens/op	  38123397 transcript-JSON-B	977105632 B/op	  200554 allocs/op
BenchmarkTranscript/codex/100MB/provider-cold-10         	       1	1666129208 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609413296 B/op	   50134 allocs/op
BenchmarkTranscript/codex/100MB/provider-cold-10         	       1	1668671542 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609411856 B/op	   50131 allocs/op
BenchmarkTranscript/codex/100MB/provider-cold-10         	       1	1665711375 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609411856 B/op	   50131 allocs/op
BenchmarkTranscript/codex/100MB/cold-page-10             	       1	1663465499 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609420392 B/op	   50136 allocs/op
BenchmarkTranscript/codex/100MB/cold-page-10             	       1	1715253459 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609420856 B/op	   50137 allocs/op
BenchmarkTranscript/codex/100MB/cold-page-10             	       1	1650557167 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609420376 B/op	   50136 allocs/op
BenchmarkTranscript/codex/100MB/warm-page-10             	       1	      4000 ns/op	 101698776 heap-live-B/transcript	         0 loads/op	   9777752 page-JSON-B	         0 resolve-record-B/op	 100221528 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	 100123526 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/codex/100MB/warm-page-10             	       1	      2917 ns/op	 101698776 heap-live-B/transcript	         0 loads/op	   9777752 page-JSON-B	         0 resolve-record-B/op	 100221528 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	 100123526 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/codex/100MB/warm-page-10             	       1	      3375 ns/op	 101698776 heap-live-B/transcript	         0 loads/op	   9777752 page-JSON-B	         0 resolve-record-B/op	 100221528 retained-est-B/transcript	         0 source-B/op	         0 source-opens/op	 100123526 transcript-JSON-B	    8240 B/op	       2 allocs/op
BenchmarkTranscript/codex/100MB/invalidated-10           	       1	1661063500 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609420216 B/op	   50136 allocs/op
BenchmarkTranscript/codex/100MB/invalidated-10           	       1	1680405708 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609420216 B/op	   50136 allocs/op
BenchmarkTranscript/codex/100MB/invalidated-10           	       1	1684713667 ns/op	 101698776 heap-live-B/transcript	         1.000 loads/op	   9777752 page-JSON-B	 100196940 resolve-record-B/op	 100221528 retained-est-B/transcript	 100198361 source-B/op	         1.000 source-opens/op	 100123526 transcript-JSON-B	609420552 B/op	   50139 allocs/op
BenchmarkTranscript/codex/100MB/same-key-4-10            	       1	1847084918 ns/op	 101698776 heap-live-B/transcript	         4.000 loads/op	   9777752 page-JSON-B	 400787760 resolve-record-B/op	 100221528 retained-est-B/transcript	 400793444 source-B/op	         4.000 source-opens/op	 100123526 transcript-JSON-B	2437668032 B/op	  200554 allocs/op
BenchmarkTranscript/codex/100MB/same-key-4-10            	       1	1867674584 ns/op	 101698776 heap-live-B/transcript	         4.000 loads/op	   9777752 page-JSON-B	 400787760 resolve-record-B/op	 100221528 retained-est-B/transcript	 400793444 source-B/op	         4.000 source-opens/op	 100123526 transcript-JSON-B	2437669136 B/op	  200547 allocs/op
BenchmarkTranscript/codex/100MB/same-key-4-10            	       1	1884795458 ns/op	 101698776 heap-live-B/transcript	         4.000 loads/op	   9777752 page-JSON-B	 400787760 resolve-record-B/op	 100221528 retained-est-B/transcript	 400793444 source-B/op	         4.000 source-opens/op	 100123526 transcript-JSON-B	2437669824 B/op	  200550 allocs/op
BenchmarkTranscript/codex/100MB/independent-key-4-10     	       1	1897435167 ns/op	 101698776 heap-live-B/transcript	         4.000 loads/op	   9777752 page-JSON-B	 400787760 resolve-record-B/op	 100221528 retained-est-B/transcript	 400793444 source-B/op	         4.000 source-opens/op	 100123526 transcript-JSON-B	2437677744 B/op	  200562 allocs/op
BenchmarkTranscript/codex/100MB/independent-key-4-10     	       1	1903175999 ns/op	 101698776 heap-live-B/transcript	         4.000 loads/op	   9777752 page-JSON-B	 400787760 resolve-record-B/op	 100221528 retained-est-B/transcript	 400793444 source-B/op	         4.000 source-opens/op	 100123526 transcript-JSON-B	2437674160 B/op	  200546 allocs/op
BenchmarkTranscript/codex/100MB/independent-key-4-10     	       1	1897342249 ns/op	 101698776 heap-live-B/transcript	         4.000 loads/op	   9777752 page-JSON-B	 400787760 resolve-record-B/op	 100221528 retained-est-B/transcript	 400793444 source-B/op	         4.000 source-opens/op	 100123526 transcript-JSON-B	2437670224 B/op	  200543 allocs/op
PASS
ok  	github.com/ginkcode/agent-sessions/internal/engine	138.861s
```
