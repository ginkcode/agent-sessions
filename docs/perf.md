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

## PF-03 — Synthetic project listing baseline (2026-10-24)

PF-03 complete; PF-01/PF-02 evidence above is unchanged. No listing optimization.

### Method and correctness

- Revision `035ef9f` plus uncommitted PF-03 work (PF-01/PF-02 committed during the
  task as `bb9eb49`/`e5bbc44`; files under test unchanged by them), Apple M5 / 32 GiB,
  Darwin 27.0.0 arm64, Go 1.26.8, Git 2.54.0 (Apple Git-157), GOMAXPROCS 10.
- `internal/files/listing_bench_test.go` generates a Git repository in `TempDir` per
  case, with Git global/system config disabled, `HOME`/`XDG_CONFIG_HOME` redirected
  and inherited `GIT_*` variables cleared. Tracked state is index-only (no commit).
  Each tree has 16 source directories, root and nested `.gitignore` (directory,
  glob and negation rules), an ignored directory tree, a deleted tracked file, an
  untracked file and empty directories. No user repository or session store is read.
- `TestSyntheticListingBaseline` covers 128 and 20,001 generated files: listed
  count/cap/truncation, deleted/untracked status, ignored and negated paths,
  sorted output, empty-directory inclusion, ignored-directory exclusion, a nested
  subroot listing, and that an uncounted context records no Git work.
  `TestSyntheticListingSymlinkIsolation` checks in/out-of-root directory and file
  symlinks stay leaves, `Resolve` refuses outside targets and search does not read
  them (skips when symlinks are unavailable). `TestSyntheticGitDirectoryCap` checks
  the Git directory walker's 20,000 cap. Git-unavailable cases skip.
- Git-count seam: an unexported context key whose callback runs before each `git`
  command in `runGit` and `ignoredDirectories`. Absent by default; request-local, so
  concurrent listings cannot pollute counts. Both sizes run 6 commands per `List`:
  `ls-files` ×2, `rev-parse` ×1, `status` ×1, `check-ignore` ×2 (one per directory
  batch, here root and `nested`).
- Baseline observation, not changed: `Search` uses `gitFiles` directly and does not
  apply `List`'s 20,000-file cap, so it is untruncated and finds paths beyond the
  listing boundary.
- Frontend: `file-drafts.test.mjs` "PF-03 listing baseline" uses the existing store
  mocks and deferred replies (no sleeps): reopening an already-open populated root
  makes 0 requests; close drops the listing but keeps tabs/drafts/expansion;
  reopen and refresh each make 1 request while refresh keeps the old snapshot; a
  closed root's late reply cannot populate another root; the same path on another
  host is refetched. This is request behavior, not native GUI latency.

### Results

`go test ./internal/files -run '^$' -bench 'BenchmarkProjectListing|BenchmarkSearch$' -benchtime=5x -count=5 -benchmem`,
then the `Status`/`Ignored` components separately with the same flags. Generation
is outside timers; filesystem caches are warm after setup (not a cold-disk run).
Time is min / median / max of five runs; bytes and allocations are medians.
`List` is the composite; components are measured independently and overlap it.

| Benchmark | ms/op | Git/op | B/op | allocs/op |
|---|---:|---:|---:|---:|
| 1000/List | 43.9 / 45.3 / 55.2 | 6 | 1,457,128 | 8,232 |
| 1000/GitFiles | 8.6 / 8.6 / 8.8 | 1 | 682,544 | 3,163 |
| 1000/Status | 14.0 / 14.2 / 25.8 | 2 | 305,681 | 250 |
| 1000/Ignored | 6.7 / 7.0 / 7.1 | 1 | 22,497 | 113 |
| 1000/Directories | 14.2 / 14.4 / 15.2 | 2 | 446,152 | 4,702 |
| 1000/Walk | 0.93 / 0.93 / 0.95 | 0 | 301,600 | 3,377 |
| 1000/Resolve | 0.017 / 0.018 / 0.019 | 0 | 7,808 | 85 |
| 20001/List | 151.3 / 152.3 / 153.2 | 6 | 24,903,505 | 142,959 |
| 20001/GitFiles | 50.5 / 51.2 / 53.4 | 1 | 13,369,676 | 60,304 |
| 20001/Status | 38.2 / 39.2 / 65.6 | 2 | 4,440,948 | 378 |
| 20001/Ignored | 13.3 / 13.5 / 13.7 | 1 | 22,632 | 113 |
| 20001/Directories | 47.3 / 47.7 / 49.1 | 2 | 7,068,936 | 82,161 |
| 20001/Walk | 13.4 / 13.8 / 14.6 | 0 | 6,400,214 | 60,440 |
| 20001/Resolve | 0.016 / 0.018 / 0.022 | 0 | 7,808 | 85 |
| Search (existing, 5,000 files) | 108.5 / 109.4 / 111.9 | — | 103,651,028 | 122,980 |

- At 20,001 files, Git file listing (≈51 ms, including one `Lstat` per path),
  directory discovery with `check-ignore` (≈48 ms) and status (≈39 ms) dominate
  `List`; ignored listing adds ≈13 ms. The non-Git walk is ≈14 ms and path
  validation is ≈18 µs. The first `Status` run of each size is a warm-up outlier.
- These guide PF-11/PF-13/PF-14; absolute timings are advisory and host-specific.

### Verification and limits

Passed: `go test ./internal/files -run TestSynthetic -v`; `go test -race ./internal/files`;
`make test` (full race suite); `go vet ./...`; `go build ./...`; gofumpt on changed
files; `golangci-lint run --new-from-rev=HEAD ./internal/files/...` (0 issues).
Package-wide `golangci-lint` on `internal/files` reports 39 existing issues in
untouched lines (errcheck, gofumpt, staticcheck, errorlint), not introduced here.
Frontend: `npm test` (666 passed), `npm run check` (0 errors, existing
ignoreDeprecations warning), `npm run build` (existing chunk-size warning).
No native GUI, Windows, or Linux runs; no cold-cache or peak-memory profile; no
symlink-unavailable or Git-unavailable host was exercised (those paths skip).

## PF-04 — Atomic transcript invalidation (2026-10-24)

### Design and deterministic evidence

`internal/engine/transcript_cache.go` wraps the existing LRU (capacity remains
four transcripts). One coordinator mutex covers cache lookup plus token capture,
per-key/batch eviction, clear and conditional publication. Provider I/O runs
outside that mutex. Unique, non-zero-sized per-key token objects identify current
outstanding loads; clear also advances an epoch. Invalidated requests may return
their snapshot, but cannot publish it or remove a newer key's token.

Bookkeeping contains only keys with current outstanding loads, not historical
keys or tombstones. Completion/error removes the last pending token; eviction
removes its key immediately; clear drops all tokens. Detached tokens live only
with their outstanding callers. Thus bookkeeping is bounded by outstanding work,
not by catalog size or the number of past invalidations. Active provider-work
limits and coalescing remain PF-05, not implemented or claimed here.

Barrier regressions in `internal/engine/transcript_cache_test.go` cover single
and batch eviction/clear between lookup and publication, both old/new completion
orders, removal/reappearance of the same key, snapshot delivery to old callers,
publication-before-eviction, and isolation from unrelated/empty invalidations.
Direct coordinator tests exercise 1,000 completed unique keys, 1,000 repeated
same-key invalidation/clear cycles, multiple pending same-key attempts, failure
cleanup, capacity/recency and useful publication after unrelated capacity churn.
Cancellation/retry and unknown-provider cleanup are covered. Existing concurrent
miss tests still require independent provider calls (no accidental coalescing).
Existing paging/LRU and generated 38/100 MB transcript equivalence tests pass.
No test uses sleeps as its ordering mechanism.

Lifecycle review: `NewService` initializes the coordinator for both engine and
direct service tests; all production reads use `loadTranscript`. Refresh/change,
removal, promotion and full-clear paths retain their existing invalidation entry
points. No engine-start requirement, new goroutine, owned context or shutdown
hook is introduced; provider cancellation remains owned by each caller.

### Verification and limits

Environment: Go 1.26.8, Darwin arm64. Passed focused transcript/provider-helper
race tests including generated large fixtures; paging and focused invalidation,
capacity, cancellation and concurrent-miss race tests repeated 20 times;
`gofumpt -d` on changed Go files; changed-line engine `golangci-lint` (0 issues).
Frontend tests: 666 passed; Svelte check and production build passed (existing
ignoreDeprecations/chunk-size warnings). `make test` (full backend race suite),
`go build ./...` and `go vet ./...` passed. Repository-wide `make lint` reports
121 existing issues in untouched lines (including errcheck, errorlint, gofumpt
and staticcheck); repository-wide changed-line lint also reports 0 issues. No unrelated lint cleanup
is included.

This task establishes correctness, not a measured speedup. No new benchmark
comparison or native GUI responsiveness claim; no private session/cache stores,
installs, app launches, process termination, pushes or Windows packages.


## PF-05 — Coalesced transcript loads (2026-10-24)

### Design and deterministic evidence

The transcript-specific coordinator still uses the existing four-entry LRU.
Current flight pointer identity replaces PF-04's explicit token/epoch: eviction
or clear detaches the pointer, and only the matching current flight can publish
or delete itself. Detached readers can finish with their original snapshot;
new readers never join obsolete work. No historical key tombstones are retained.

Shared contexts belong to the service/engine lifetime, never the first waiter.
Standalone `NewService` uses a background lifetime; its last departing waiter
cancels orphan work. Engine Start binds the owner even with caching disabled;
Close cancels current and detached loads, including before Start. Restart installs
a fresh lifetime; repeated Start is a no-op. Unknown-provider errors remain
synchronous. Caller cancellation is independent, and shutdown releases waiters
even when a provider ignores cancellation. Such a provider cannot be forcibly
terminated: it retains its slot and owned-flight record until it returns.

A four-slot context-aware semaphore bounds actual provider I/O, including
superseded/orphaned work. Queued flights are cancellable and do not hold the cache
lock. Bookkeeping is proportional to outstanding requests/unfinished loads, not
all keys ever seen; there is no admission/queue-size policy in this task.

PF-02 barriers cover four shared successful waiters, distinct simultaneous keys,
shared failure/retry, first/other/last cancellation, owner cancellation, both
old/new completion orders after single/batch/clear invalidation, orphan versus
retry identity, uncooperative shutdown completion, engine Close/restart and the
concurrency bound with a cancelled queued request. State observations use locked
predicates (with a timeout only as a failure guard), not sleeps. PF-02 concurrent
tests/benchmarks now expect one same-key provider attempt, four independent-key
attempts, so they do not wait for four announcements from one shared flight.
Synthetic work collectors are attached to the owning lifetime, not a waiter.

### Repeated same-key evidence

Darwin arm64, Apple M5, Go 1.26.8, default GOMAXPROCS=10; generated temporary
38/100 MB PF-02 fixtures, four page requests per cold same-key batch, construction
excluded. Three one-iteration runs, same host as PF-02. Command:

```sh
go test ./internal/engine -run '^$' -bench '^BenchmarkTranscript/(claude|codex)/(38MB|100MB)/same-key-4$' -benchtime=1x -count=3
```

Historical before results are preserved in PF-02 above. Times are median
[min–max] milliseconds; allocation ranges are measured per four-request batch.

| Fixture | PF-02 before ms | PF-05 after ms | After B/op | After allocs/op |
|---|---:|---:|---:|---:|
| Claude 38 MB | 414.350 [409.230–417.495] | 392.843 [392.030–402.451] | 202,494,536–202,496,512 | 36,798–36,801 |
| Claude 100 MB | 1098.335 [1091.759–1102.975] | 1026.776 [1015.688–1043.684] | 506,818,920–506,822,400 | 36,801–36,809 |
| Codex 38 MB | 689.030 [688.688–701.021] | 590.582 [589.012–606.494] | 244,288,392–244,306,984 | 50,152–50,170 |
| Codex 100 MB | 1867.675 [1847.085–1884.795] | 1640.225 [1625.508–1653.801] | 609,430,536–609,447,688 | 50,152–50,167 |

All runs assert **4 → 1 loads and source opens per batch**, with logical source
bytes respectively 152,622,212 → 38,155,553; 400,622,724 → 100,155,681;
152,792,932 → 38,198,233; and 400,793,444 → 100,198,361. Allocations/bytes
are approximately one quarter of PF-02 (before B/op ranges in its table).
Codex resolution-record bytes also fall fourfold but still scan the source once:
38,196,812 / 100,196,940 after. Page JSON remains 3,722,952 / 9,777,652 bytes
for Claude, 3,723,052 / 9,777,752 for Codex. Measured single-transcript live
heap remains roughly 40.4 / 101.7 MB; retained-size estimates remain 38.2 / 100.2
MB, not a new cache-memory saving measurement.

Wall-clock improvement is modest relative to work/allocation reduction because
previous loads ran in parallel; these are synthetic one-shot batches, not a
native UI latency distribution, physical disk measurements or serial repeated
cache hits. Concurrent backend verification ran during these samples, adding
noise; no statistical speedup guarantee is claimed. Full parsing, Codex canonical
resolution, large payloads and the count-based cache budget remain unchanged.
A separate one-iteration Claude 38 MB run verifies cold-page, warm-page,
invalidated and independent-key-4 work-count expectations; it is not comparative
evidence for those modes.

### Verification and limits

Passed full `go test -race ./...`, `go test ./...`, `go build ./...`, `go vet ./...`;
focused coordinator race cases repeated 50 times; generated transcript equivalence
and paging regressions; changed-line engine `golangci-lint` (0 issues), and diff
whitespace checks. Frontend `npm test`, `npm run check`, `npm run build` passed.
An initial broad three-repeat race command with all large fixtures exceeded its
180-second aggregate timeout while parsing (not a barrier deadlock); the full
race suite passed with its normal timeout, and the focused repeated race run
excludes those expensive equivalence fixtures. Repository-wide lint debt recorded
in PF-04 is not changed or cleaned up here.

No native GUI/Windows/RPC latency claim. No private stores, QA perf script,
installs, app launches, process termination, pushes or Windows packages. PF-06
through PF-15 remain unimplemented; changes are left uncommitted for review.

## PF-06 — Validated source-loading contract (2026-10-24)

Added independent optional `SourceLoader` / `SourceBlobLoader` interfaces and
`LoadWithSource` / `BlobWithSource` dispatch helpers without changing Provider,
RPC, generated bindings or frontend protocols. Identity mismatch and unsupported
providers use standalone methods; wrapped `ErrStaleSource` triggers exactly one
canonical fallback. Other errors propagate, and cancellation prevents fallback.
The source value is an untrusted canonical metadata hint, not a capability.

[Contract and Codex feasibility](tasks/performance-m1.md#pf-06-contract-and-pf-07-feasibility)
records path/ref validation, canonical overlays, freshness, cancellation, Blob key
semantics, engine catalog ownership and FTS queued-revision ownership. Deep review
of discovery, index loading, canonical scan selection and Load/Blob found that
metadata alone cannot exclude unseen duplicates or prove index/WAL/parent
freshness. PF-07 must obtain sufficient provider-owned evidence or conservatively
re-resolve; file stat/UpdatedAt alone is not a valid shortcut. No performance gain,
zero-discovery guarantee, validation cache or fast-path implementation is claimed.

### Evidence and verification

- New dispatch tests count optional/base calls for success, mismatched ref, wrapped
  stale errors, ordinary failures, cancellation before dispatch and during stale
  rejection. Both Load and Blob are covered, including canonical fallback output.
- Generated temporary Codex fixtures test unsupported providers and a deliberately
  stale-only optional adapter. Outside-root/traversal/missing/duplicate hints and
  forged title overlays cannot alter canonical transcript/Blob output; invalid
  Blob keys and a source replaced by an outside-root symlink are rejected.
  This tests fallback safety, not a future accepting fast-path validator. PF-07
  must add accepting-path duplicate/index/freshness validation tests.
- Passed focused `go test ./internal/provider ./internal/provider/codex -run
  TestSource -count=1`; repeated source contract race tests with `-count=25`;
  `go test -race ./internal/provider/... -run
  'TestSource|TestLoad|TestBlob|TestIndex|TestScan' -count=1`.
- Passed `go test ./...`, `go build ./...`, `go vet ./...`; frontend `npm test`,
  `npm run check`, `npm run build`; cached pinned `go tool gofumpt`;
  `go tool golangci-lint run --new-from-rev=HEAD ./internal/provider/...` (0 issues)
  and `git diff --check`. Existing repository-wide lint debt remains as recorded
  in PF-04; no unrelated lint cleanup. Tool invocation used `GOPROXY=off`.

Only PF-06 is implemented. PF-07…PF-15 remain unimplemented; no engine/FTS caller
activation. No private stores, QA perf script, installs, process termination,
application launches, Windows packages, commits or pushes. Native GUI/manual QA
and Windows runtime verification are unperformed. Changes left for review.

## PF-07A — Bounded content proof and accepting Codex sources (2026-10-24)

Split PF-07 before coding into two ≤4 h packages; PF-07A complete, parent
**partial**, M1 still **6/15**. PF-07B retains the full mutation matrix and repeated
benchmark/retained-memory evidence; PF-08 remains blocked.

Only an optional cold source-aware call captures before/after reconciliation
evidence around one canonical Scan and publishes one private immutable snapshot
per Provider root under a mutex. Ordinary Scan and standalone Load/Blob are
unchanged and perform zero proof hashing, even after optional use. A cold call
serves its canonical resolution directly if proof cannot be established, avoiding
a second fallback Scan. Both
optional source methods accept matching canonical metadata only after rediscovery,
index reread (SQLite, not DB/WAL mtime), validated index-path classification and
streaming SHA-256 of every selectable source agree with the snapshot. Identity,
mode, size and mtime also agree, but are not sufficient alone. This preserves raw
duplicate and index/parent selection by reusing the existing canonical Scan, not
reimplementing its overlays. Changed/uncertain/unproven hints return stale for
PF-06's single standalone fallback. Incremental checkpoints never establish proof.

Limits: at most 4096 sources/index rows/edges, 512 MiB total rollout content per
proof and 8 MiB serialized index string/metadata payload retained; no rollout bytes
or parser checkpoints retained. Existing discovery/index readers still allocate
transient results before cap checks; this is not a bound on all Scan allocations.
Proof reads use 64 KiB chunks and context checks. Reconciliation still enumerates,
opens and reads unrelated sources; it eliminates unrelated JSON parsing, not disk
I/O. Optional cold resolution incurs two extra content passes; ordinary Scan incurs
none. Actual retained-memory measurement and larger repeated workloads remain
PF-07B. Normal
best-effort filesystem/SQLite read races remain; no atomic cross-source snapshot
or adversarial concurrent rewrite guarantee is claimed. Old snapshots may be
held by active callers, but provider ownership retains only one; concurrent old
publication cannot authorize changed content because each use revalidates.

Deterministic synthetic two-source test: standalone Load performs **2 metadata
source opens and nonzero record parses**; direct accepting Load plus Blob perform
**0 scan source opens, 0 records, 0 checkpoint decodes/encodes** with identical
output. Digest opens/bytes/passes/enumerations are counted separately by opt-in
Codex SourceWork; zero parse counters do not imply zero source I/O.
Tests include forged hints, fresh-provider fallback, cancellation, concurrent
readers, immutable old snapshots, oversized proof rejection, same-size/mtime
rewrite, replacement, disappearance, outside-root symlink, duplicate appearance,
index addition and title update. Existing Codex lazy-output/tail/index tests pass.
PF-07B must still exercise the complete dedicated WAL/parent/index-path mutation
matrix and concurrent Scan publication gates, rather than infer these from design.

Passed `go test ./...`, `go vet ./...`, `go build ./...`,
`go test -race ./internal/provider/codex ./internal/provider`, frontend `npm test`
(666 passed), `npm run check`, `npm run build`, cached `go tool gofumpt`,
`GOPROXY=off go tool golangci-lint run --new-from-rev=HEAD ./internal/provider/codex/...`
(0 issues), and `git diff --check` on Darwin
arm64 using installed tools. No private stores, forbidden script, installs,
app launches, process termination, commits or pushes. GUI/Windows runtime QA
unperformed. No engine/FTS wiring or checkpoint optimization.


### PF-07A lazy-proof correction and repeated microbenchmark

Removed proof work from ordinary Scan after review: only optional cold calls
establish evidence. `BenchmarkSourceReconciliation` uses 16 generated rollouts,
100 messages each, 212,614 total bytes. Apple M5/Darwin arm64, three runs,
`go test ./internal/provider/codex -run '^$' -bench BenchmarkSourceReconciliation
-benchmem -benchtime=200ms -count=3`, fixture generation excluded:

| Path | ms/op range | allocated B/op (approx) | proof bytes/op | proof passes/enumerations |
|---|---:|---:|---:|---:|
| Ordinary Scan before optional use | 1.654–1.658 | 17,293,000 | 0 | 0 |
| Ordinary Scan after optional use | 1.650–1.704 | 17,293,000 | 0 | 0 |
| Standalone Load | 1.945–2.007 | 18,521,000 | 0 | 0 |
| Optional cold Load | 3.379–3.420 | 18,888,000 | 425,228 | 2 |
| Optional proven warm Load | 0.952–0.958 | 1,404,884 | 212,614 | 1 |

Ordinary Scan is the unchanged pre-PF07 implementation, with no new wrapper;
these before/after labels compare absence/presence of an optional snapshot, not
historical independent binaries. Counters prove no new no-hint byte work; timings
are advisory. Optional cold is materially more expensive; warm avoids unrelated
parsing but still hashes every byte. Digest opens, actual read bytes (including
failed proofs), discovery passes, and index reads are explicitly accounted.
Discovery readability probes and SQLite internal page I/O remain outside digest
byte counters, as do canonical Scan reads (tracked separately by ScanWork).

## PF-07B — Mutation matrix, recovery and larger evidence (2026-10-24)

Implemented the dedicated synthetic Load/Blob matrix and retained-state evidence.
**Parent PF-07 remains partial; M1 stays 6/15 and PF-08 activation remains gated.**
The larger byte-heavy workload is slower even on proven warm hits, so these
results do not justify unconditional integration or a general speedup claim.

Correctness review found a permanent-reuse failure: an immutable stale snapshot
was never refreshed, including when a newly scanned hint differed from its cached
metadata. Validation now checks proof before hint equality, compare-and-clears
only the snapshot actually checked, and returns stale for the one canonical
fallback. The next optional call establishes fresh before/Scan/after evidence.
Cold publication carries a generation so an older in-flight establish cannot
replace newer evidence; a delayed validator cannot invalidate a newer snapshot.
Ordinary Scan and standalone Load/Blob neither invalidate nor establish proof.
An unstable or corrupt source may still prevent proof indefinitely by design.
There is no filesystem/SQLite atomicity claim or active caller cancellation.

Dedicated coverage now includes WAL-only title updates with an open writer,
numeric 9→10 selection, index removal/replacement/corruption, unconventional
index path override and ID mismatch, archive-column removal (raw archived state
restored), authoritative edge removal and parent disappearance/visibility, raw
newer/active-tie/lexical-tie duplicates beneath index timestamp overlays, append
recovery, existing disappearance/replacement/outside-symlink and same-stat rewrite
cases, lazy output and valid/invalid final tails. Each matrix branch compares
canonical standalone output, proves stale Load and Blob independently, rebuilds,
and accepts Load plus a real inline-file Blob without metadata parsing. Corrupt
indexes intentionally cannot retain proof. Deterministic late invalidation and
publication ordering plus concurrent ordinary Scan/validation exercise ownership;
source-count and byte caps reject proof. No production hook or private fixture.

### Isolated repeated larger workloads

Apple M5, Darwin arm64, generated eight-rollout fixtures, approximately 38 and
100 MiB total; mostly 8 KiB ignored world-state records plus a message per file.
This deliberately measures byte-heavy, low-parsing-cost sources, not typical
message-heavy data. Fixture construction and warm seeding excluded. Command:
`go test ./internal/provider/codex -run '^$' -bench '^BenchmarkSourceLarge$'
-benchmem -benchtime=1x -count=3`, isolated from broad tests. These are three
single-operation samples per mode, not steady-state statistics or cold disk I/O.

| Path | 38 MiB ms/op range | 100 MiB ms/op range | allocated MB/op (38 / 100) |
|---|---:|---:|---:|
| Ordinary Scan before optional use | 7.50–7.97 | 17.48–18.17 | 54.30 / 129.03 |
| Ordinary Scan after optional use | 7.30–8.02 | 17.96–18.99 | 54.30 / 129.02 |
| Standalone Load | 19.24–20.62 | 49.63–51.27 | 61.29 / 145.71 |
| Optional cold Load | 46.15–46.37 | 120.71–121.38 | 61.48 / 145.90 |
| Optional proven warm Load | 25.27–26.40 | 67.06–68.14 | 7.09 / 16.78 |
| Changed source + one fallback | 33.41–34.85 | 85.68–90.26 | 61.38 / 145.80 |

Exact full proof bytes/pass: **39,792,696 / 104,808,952**. Warm does one
full digest pass and **zero Scan bytes/records/opens**; cold does two passes plus
one Scan. Standalone and both ordinary Scan modes perform **zero proof work**,
8 Scan opens and **4,824 / 12,680** records (**39,787,872 / 104,796,272**
record bytes). Changed-mode samples append one blank line, invalidate proof and
perform one canonical fallback (8 Scan opens); fallback frequency is 100% for
these changed samples and 0% for stable warm samples. Subsequent cold rebuild
cost is not hidden in these changed samples. The benchmark also supports longer
runs, where alternating stale/rebuild calls are included in the changed average.
Digest counters include unrelated source bytes; Scan bytes count record payload
rather than newline bytes. Neither includes SQLite internal pages, readability
probe opens, or selected transcript parser bytes. Timings include those actual
operations; no claim of zero total I/O is made.

**Concrete activation gate/follow-up:** do not wire PF-08 unconditionally. Warm
hashing is about 25–37% slower here despite lower allocations; cold is >2×
standalone. Above 512 MiB or 4096 sources, no proof is retained and every optional
cold call may repeat proof attempts plus a full Scan. Before PF-08, establish an
explicit workload/size and mutation-rate policy with measured end-to-end benefit,
including cold amortization and over-cap bypass/backoff, or develop stronger
incremental freshness evidence in a separately scoped follow-up. No cheap
mtime-only trust, reduced canonicality, or ordinary-Scan hashing is acceptable.
PF-07B correctness/evidence work is implemented; parent performance acceptance
and integration suitability are not claimed complete.

A final three-repeat 200ms small-fixture rerun measured warm 1.003–1.013 ms
versus standalone 2.069–2.109 ms; ordinary before/after Scan 1.760–1.769 /
1.747–1.788 ms, zero proof work. This advisory rerun was launched while a full
race command was outstanding, so it is **not** part of the isolated acceptance
benchmark evidence above and is not used to justify integration.

### Actual retained heap sample and verification

`go test ./internal/provider/codex -run '^TestSourceRetainedHeapSample$' -v
-count=3`, run separately before benchmarks: after forced GC, keeping Provider
alive and dropping transcript, the 100 MiB/eight-source snapshot retained process
HeapAlloc deltas **52,504 / 15,192 / 18,488 bytes** (final test-cleanup rerun:
47,488 / 13,536 / 12,720 bytes). This process-global subtraction
includes caches/runtime noise; it is neither object attribution, a maximum nor
peak memory evidence. Source bytes are not retained. Existing 4096-source,
512 MiB-byte and 8 MiB serialized-index caps remain; transient discovery/index
allocations occur before bounds, multiple in-flight snapshots can coexist, and
canonical metadata retention is not independently byte-capped.

Passed focused source tests repeated three times, provider/Codex race tests
repeated three times, deterministic recovery/publication tests repeated ten times,
full `go test ./...` and `go test -race ./...`, `go vet ./...`, `go build ./...`, and
frontend `npm test` (666 passed), `npm run check`, `npm run build`, cached
`go tool gofumpt`, changed-line golangci-lint (0 issues), and `git diff --check`.
Frontend emitted Node experimental-loader/MockTimers and bundle-size warnings;
no test/check/build failures. Initial new-test failures
were fixture errors (non-discoverable duplicate directory, Darwin /var alias
comparison, and an invalid index row does not hide a discovered parent); corrected
fixtures/assertions and reruns pass. No installed tooling, private stores,
qa-perf-bench, application launches/kills, engine/FTS wiring, commits or pushes.

## PF-07C — Cost-policy decision and production bypass (2026-10-24)

PF-07C was documented in both trackers before coding (≤4 h package). **Safe
opt-out implemented; parent PF-07 remains partial/blocked, M1 6/15.** No automatic
eligibility policy is justified by the available evidence. PF-08 and dependent
PF-10 remain blocked; **PF-09 is the next independent package**, not implemented
here. This is a negative performance conclusion, not a universal speedup claim.

### Decision and exact supported workload

Cheap discovered source count and stat aggregate size can bound digest cost, but
cannot predict the cost of decoding ignored versus meaningful records, selected
transcript cost, or the number of future unchanged requests needed for amortization.
They also omit unconventional index-added paths unless index semantics are read.
Obtaining density by a new parse defeats cheap eligibility; canonical Scan's
existing request-local counters can describe records but not their decode
complexity or future reuse. Ordinary Scan must not gain extra parsing, hashing or
persistent policy machinery merely to classify this experiment. No cutoff chosen
between the small and large fixtures establishes a portable break-even rule.
Timing-adaptive activation would itself incur costly trial reads and needs further
scope/evidence across workload shapes, I/O and mutation rates.

Production therefore supports **no automatically eligible workload**: New leaves
the private experiment flag off; both optional methods immediately return stale
(after cancellation checking), even if evidence exists. The PF-06 dispatcher then
does exactly one canonical standalone Load/Blob. Zero proof attempts is the retry
bound, including large, over-cap, cold and rapidly changing roots. There is no
cooldown clock, sleep, periodic trial, hidden size preflight, public activation
switch, discovery or SQLite read in the decline. Ordinary Scan is unchanged.
This avoids the regression rather than asserting a learned policy. Any future
activation requires a separately documented measured package and parent acceptance.

The test-only experiment continues to support the measured **16 rollouts × 100
small messages, 212,614 bytes total**, stable unchanged warm Load case. It still
hashes all bytes and is not production eligibility. Experimental Blob may consume
an existing Load-established snapshot but now cannot initiate cold before/Scan/after
proof, including if evidence disappears between calls. Mutation/canonicality matrix
and publication-generation safeguards remain covered. Large experimental retained
heap evidence above remains historical experimental evidence, not production state.

### Isolated repeated evidence

Apple M5/Darwin arm64; synthetic fixtures only; no other test/race work was running.
Setup and experimental warm seeding excluded. Commands (each count=3):

```sh
go test ./internal/provider/codex -run '^$' -bench '^BenchmarkSourceReconciliation/(OrdinaryLoad|ProductionBypass|OptionalCold|OptionalWarm)$' -benchmem -benchtime=300ms -count=3
go test ./internal/provider/codex -run '^$' -bench '^BenchmarkSourceLarge/(38MiB|100MiB)/(StandaloneLoad|ProductionBypass)$' -benchmem -benchtime=3x -count=3
go test ./internal/provider/codex -run '^$' -bench '^BenchmarkSourceBlobPolicy$' -benchmem -benchtime=3x -count=3
```

| Load path | small ms/op | 38 MiB ms/op | 100 MiB ms/op |
|---|---:|---:|---:|
| Standalone | 1.946–1.997 | 22.364–22.471 | 55.821–57.476 |
| Production bypass + fallback | 1.964–1.998 | 22.304–22.435 | 56.002–58.207 |
| Experimental cold | 3.395–3.438 | historical above | historical above |
| Experimental warm | 0.952–0.994 | historical regression above | historical regression above |

Standalone/bypass allocations approximately 18.52 / 61.29 / 145.71 MB/op for
small/38/100. Experimental small cold 18.89 MB/op, warm 1.405 MB/op. Small cold
reads 425,228 proof bytes in two passes; warm 212,614 in one. The roughly 1.4 ms
extra small cold cost needs about two subsequent unchanged warm hits just to repay
on this host/fixture; mutation can erase that benefit. This is not a general
break-even estimate.

| Blob path (real HELLO attachment) | 1 MiB ms/op | 38 MiB ms/op | 100 MiB ms/op |
|---|---:|---:|---:|
| Standalone | 1.261–1.576 | 7.645–8.117 | 18.187–18.999 |
| Production bypass + fallback | 1.103–1.265 | 7.920–7.949 | 17.930–18.456 |

Blob allocations approximately 10.76 / 55.36 / 130.09 MB/op for both paths.
Three operations per large/Blob sample are advisory, not latency distributions or
cold-disk measurements; differences within these ranges are not speedup claims.
Bypass is approximately standalone in these repeated samples.

**No hidden byte work:** every bypass/standalone measured proof counter is zero.
Large Load canonical counts are identical: eight Scan opens, 4,824 / 12,680
records, 39,787,872 / 104,796,272 record bytes per operation. Blob standalone and
bypass both do eight Scan opens. Proof bytes do not include canonical Scan,
selected transcript reads, SQLite pages or readability probes; those ordinary
costs remain, rather than being described as zero I/O. Deterministic tests assert
all five SourceWork counters stay zero on repeated declines (including >512 MiB
sparse source and >4096 sources), production with a retained snapshot, rapid
appends, cancellation and concurrent dispatch. Three appended Load+Blob pairs do
exactly six canonical source opens in a one-file root, with canonical output;
experimental repeated cold Blob calls do no Scan or proof.

### Verification and boundaries

Passed `go test ./internal/provider/codex ./internal/provider -count=1`, focused
source race tests for both packages `-count=3`, `go test ./...`, `go vet ./...`,
`go build ./...`, gofmt and `git diff --check`. Frontend code was untouched;
frontend tests/check/build and full repository race were not rerun for PF-07C
(previous PF-07B results above are historical). No private stores/scripts, installs,
app launch/kill, extra agents, engine/FTS activation, commits or pushes.

## PF-09 — Claude checkpoint summary reuse (2026-10-24)

Implemented only PF-09. PF-07 remains partial/blocked; PF-08/PF-10 remain
blocked. No persisted checkpoint/version or parser changes. A provider-owned
summary caches only the validated unchanged decision and sidecar dependencies,
not decoded metadata, pending usage or pending tool slices. Exact copied raw
checkpoint bytes, Size/ModTime/Offset, transcript file identity and sidecar
path/size/mtime must match. Changed sources decode normally; cold/reset scans
parse normally. New providers validate persisted checkpoints once. The raw copy
prevents caller in-place edits from silently authorizing reuse; no content hash
or duplicate marshal is introduced. Existing best-effort stat-based freshness
semantics remain: same-inode content edits preserving size and mtime (including
sidecars) are not detected; this is not a content-proof feature.

Admission is at most 1024 summaries with raw checkpoints at most 16 KiB each
(16 MiB raw upper bound, plus structs/map/path/FileInfo storage). Oversized and
non-admitted sources use the old decode path. No LRU or parser snapshot is
retained. Discovery prunes removed/reset entries; concurrent scans can reduce
hit rates but cannot match different raw state. Locks guard cache bookkeeping;
caller mutation of ScanState concurrently with Scan remains unsupported.

### Repeated synthetic evidence

Darwin arm64, Go 1.26.8, same host as the immediately preceding baseline.
Generated PF-01 fixture: 64 sources × 32 records, 250,624 bytes. Generation is
outside timed work. Commands before and after:

```sh
go test ./internal/provider -run '^TestSyntheticScanBaseline$' -bench '^BenchmarkSyntheticScan/claude/' -benchmem -benchtime=200ms -count=3
go test ./internal/provider/claude -run 'TestCheckpointReuse' -v -count=3
```

| Mode | Before µs/op (3 runs) | After µs/op (3 runs) | Before → after B/op | Before → after allocs/op |
|---|---|---|---|---|
| cold | 3717, 3775, 3782 | 3860, 3889, 3858 | 67,741,388–67,745,958 → 67,796,368–67,799,104 | 11,733–11,751 → 11,807–11,817 |
| unchanged | 261, 257, 260 | 174, 172, 172 | 188,039–188,066 → 149,523–149,547 | 1337 → 444 |
| one-source append | 371, 369, 366 | 233, 232, 230 | 1,242,363–1,242,488 → 1,204,607–1,204,728 | 1363 → 484 |

Unchanged checkpoint decodes fall **64 → 0**, append **64 → 1**;
encodes/opens/records stay respectively 0/0/0 and 1/1/1. Cold stays
0 decodes, 64 encodes/opens, 2048 records. Approximate median improvements:
34% unchanged and 37% append, with roughly 2% cold overhead. Advisory warm
filesystem timings, not native latency claims. The append benchmark repeatedly
uses its fixed pre-append state; this intentionally measures the same one-record
resume each iteration, not an ever-growing transcript. Historical PF-01 exact
counts above are preserved; current assertions now require the reduced work.

### Retained-state cost

Measured summary struct size: 88 bytes. Basic fixture raw identity: 802 bytes;
125-byte temporary path; accounted payload 1015 bytes per entry excluding map,
allocator and FileInfo backing storage. GC/HeapAlloc deltas for 1024 summaries
with 802-byte synthetic raw identities: **1,105,936 / 1,146,096 / 1,105,920 bytes**.
This isolated measurement includes map/raw allocations but excludes preallocated
paths and a shared FileInfo; it is not whole-provider heap or worst-case usage.
Raw-state retention is capped rather than retaining a potentially huge parsed
checkpoint or its PendingTools allocation. Entry saturation is conservative
admission, so workloads above 1024 sources may see smaller gains.

### Verification and limitations

Passed full `go test ./...`, `go build ./...`, `go vet ./...`, affected
`go test -race ./internal/provider/claude ./internal/provider`, frontend
`npm test`, `npm run check`, `npm run build`, gofmt and diff whitespace checks.
Regressions cover versions (missing/old/future), corrupt and in-place edited raw
state, valid foreign state, reset/new provider, identical-stat inode replacement,
same-size rewrite, truncation, sidecar changes/removal, source removal, admission
bounds and concurrent scans. Existing every-fixture-boundary resume, split
usage/tool state and checkpoint roundtrip tests pass. Prior ScanState immutability
is checked. No private stores, forbidden script, installation, app launch/kill,
Windows package, extra agents, commits or pushes. Native GUI QA is unperformed.
