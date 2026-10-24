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
