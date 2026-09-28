# Agent Sessions — Task Breakdown

Derived from [SPEC.md](SPEC.md). Every task is scoped to be **≤ 4 h for a senior
Go/TS developer**. Estimates include writing tests for the task.

**Decided:** Wails **v2** (stable), using GTK3 + `webkit2gtk-4.1` on Linux.
See SPEC §2.

**Assumptions until the spec's open questions (§14) are answered:**
- The frontend is Svelte + TS.
- v1 is read-only, so M4 is optional.
- Codex fixtures are blocked on getting a real sample (M1-08).

**Definition of Done (applies to every task):**
- Code is linted (`golangci-lint`, `svelte-check`).
- Unit or golden tests are added, and `make test` passes.
- No writes to any tool's session store.
- No credential files are read.
- Public types and functions have doc comments where the intent is not obvious.

Legend: **Est** = hours, **Deps** = task IDs that must be done first.

---

## M0 — Foundation & Claude Code provider (≈ 42 h)

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M0-01 | Scaffold repo: `git init`, `go mod`, folder layout per SPEC §3, `Makefile` (`build`, `test`, `lint`, `fmt`), `.golangci.yml`, `.gitignore`, `README` stub | 2 | – | `make lint test` runs green on the empty skeleton. |
| M0-02 | `internal/model`: `SessionMeta`, `Message`, `Part`, `TokenUsage`, `MessageCounts`, `AgentID`/`Role`/`PartKind` enums, JSON tags | 2 | M0-01 | Types compile; JSON round-trip test passes. |
| M0-03 | `internal/provider`: `Provider` interface, `ScanState`, `SessionRef`, registry (`Register`/`All`/`Get`), `Diagnostics` collector | 2 | M0-02 | A fake provider registers and scans in a test. |
| M0-04 | `internal/paths` (Linux): default roots and env overrides (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `XDG_DATA_HOME`, `XDG_CACHE_HOME`, `XDG_CONFIG_HOME`); `paths_darwin.go` stub | 2 | M0-01 | Table tests cover each env override. |
| M0-05 | Path utils: `NormalizeDir` (Clean, EvalSymlinks, trailing `/`), `GitRoot` found by walking up for `.git` (dir or worktree file, no `exec`), memoized; `Exists` check | 3 | M0-01 | Tests with a temp repo, a worktree, a symlinked dir and a missing dir. |
| M0-06 | Fixture tooling: `scripts/sanitize-fixture` copies a real session and redacts message text/paths while keeping structure; golden-test helper with an `-update` flag | 3 | M0-01 | One sanitized Claude fixture is committed; the golden helper is used by a test. |
| M0-07 | Claude: `Detect()` and file discovery (`projects/*/*.jsonl`, `<uuid>/subagents/*.jsonl`) | 2 | M0-03, M0-04 | Discovers all 30 local sessions and their subagent files. |
| M0-08 | Shared JSONL reader: handles huge lines (> 10 MB), tolerates a truncated or partially written last line, supports start-at-offset, and exposes a cheap `"type"` pre-filter before full decode | 3 | M0-01 | Tests cover huge-line, partial-last-line and offset resume; benchmark on the 38 MB file. |
| M0-09 | Claude metadata scan: `cwd`, first non-meta user prompt, title priority (`ai-title` → `summary` → first prompt), model, branch, created/updated, version | 3 | M0-07, M0-08 | Golden metadata matches for fixtures; real `scan` shows sensible titles. |
| M0-10 | Claude message counting per SPEC §6 (user excluding meta/tool_result; assistant merged by `message.id`; tool-call count), returning the resume byte offset | 3 | M0-09 | Counts match a hand-verified fixture; appending lines and resuming from the offset gives the same total as a full rescan. |
| M0-11 | Claude `Load`: user/assistant records → `Message`/`Part`; merge split assistant blocks; pair `tool_use` ↔ `tool_result`; text/reasoning/tool parts | 4 | M0-08, M0-02 | Golden transcript test passes for a fixture containing tool calls. |
| M0-12 | Claude `Load` extras: `compact_boundary` → compaction part, `isMeta`/`attachment` → hidden meta, `isSidechain`, lazy refs to `tool-results/*.txt` (`Truncated=true`) | 3 | M0-11 | Fixture with compaction and meta renders correctly in golden output. |
| M0-13 | Claude subagents: expose `subagents/*.jsonl` as child sessions with `ParentID` | 2 | M0-09 | The parent lists its children; children are excluded from the top level. |
| M0-14 | Claude live detection: read `sessions/<pid>.json` and check whether the pid is alive (`kill(pid,0)`), mapping pid → `sessionId` | 2 | M0-07 | Test with a fake sessions dir; the real current session shows `Live=true`. |
| M0-15 | Claude hardening: fuzz test for the record parser, and a diagnostics count of unknown record types | 3 | M0-12 | `go test -fuzz` runs for 60 s without panic; unknown types are counted, not errors. |
| M0-16 | Headless CLI `agent-sessions-cli`: `scan [--agent] [--json]` (table: agent, cwd, title, msgs, updated) and `show <agent> <id> [--json]` | 3 | M0-10, M0-11 | Works against the real `~/.claude`; `--json` output is valid. |

## M1 — Remaining providers + MVP GUI (≈ 84 h)

### OpenCode

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M1-01 | Read-only SQLite helper (`modernc.org/sqlite`, `mode=ro`, `busy_timeout`, short queries, `HasTable()` schema probe) | 3 | M0-01 | Opens the live 248 MB `opencode.db` while OpenCode runs, with no lock errors. |
| M1-02 | OpenCode `Detect` and generation detection (v2 tables / v1 tables / legacy `storage/`) | 2 | M1-01, M0-04 | Reports which generations are present locally. |
| M1-03 | OpenCode v2 scan: `session_v2` → `SessionMeta` (dir, title, model, tokens, cost, archived, parent) plus SQL message counts from `session_message` | 3 | M1-02 | 64 local sessions listed with counts. |
| M1-04 | OpenCode v2 `Load`: `session_message` (`user`, `assistant.content[]`, `compaction`, `synthetic`, `system`) → `Message`/`Part` | 4 | M1-03 | Golden test passes; sessions containing tools/patches render in the CLI `show`. |
| M1-05 | OpenCode v1 scan and `Load`: `session` + `message` + `part` (text, reasoning, tool, patch, file, compaction; step-start/finish ignored) | 4 | M1-02 | Golden test built from a v1-only fixture DB. |
| M1-06 | OpenCode legacy JSON storage scan and `Load` (`storage/session|message|part`) | 4 | M1-02 | Golden test using a synthetic legacy fixture tree. |
| M1-07 | OpenCode merge: dedupe by id (newest generation wins), `parent_id` children, `project.worktree`/`worktree` → `RepoRoot`; fixture DB built from `.sql` in testdata | 3 | M1-03, M1-05, M1-06 | Local total is 64 with no duplicates; fixture tests pass. |

### Codex

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M1-08 | Obtain a real Codex rollout plus a populated `state_N.sqlite`, then sanitize them into testdata **(blocked: SPEC Q3)** | 1 | M0-06 | Fixtures committed. |
| M1-09 | Codex `Detect` and discovery (`sessions/YYYY/MM/DD/rollout-*.jsonl`, `archived_sessions/`) | 2 | M0-03, M0-04 | Discovers fixture files; returns empty (not an error) when missing. |
| M1-10 | Codex rollout metadata scan (`session_meta`, `turn_context`) and message counts (excluding injected env/instructions), with byte-offset resume | 3 | M1-09, M0-08, M1-08 | Golden metadata test passes. |
| M1-11 | Codex `threads` index fast path (highest `state_N.sqlite`), merged with the file scan; `thread_spawn_edges` → `ParentID`; `archived` flag | 3 | M1-01, M1-10 | Uses the index when populated and falls back to files when it's empty (as on this machine). |
| M1-12 | Codex `Load`: `response_item` (message, reasoning, function_call ↔ function_call_output) and relevant `event_msg` → `Message`/`Part` | 4 | M1-10 | Golden transcript test passes. |

### Core

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M1-13 | Scan orchestrator: run all detected providers concurrently (bounded pool, context cancel), merge results and diagnostics | 3 | M0-03 | A slow fake provider doesn't block the others; cancellation works. |
| M1-14 | Grouping engine (pure Go): Dir→Agent, Agent→Dir, Flat; subagent nesting; missing-dir flag; per-node counts | 3 | M0-05, M0-02 | Table tests cover all 3 modes. |

### GUI (Wails + Svelte)

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M1-15 | Wails **v2** scaffold (`wails init -t svelte-ts`, then upgrade to Svelte 5 + current Vite), Linux build with `-tags webkit2_41`, `make dev` / `make app`, `WEBKIT_DISABLE_DMABUF_RENDERER` fallback | 3 | M0-01 | Empty window launches on this machine; build deps (`libgtk-3-dev libwebkit2gtk-4.1-dev`) documented. |
| M1-16 | App service bound via `options.App.Bind`: `ListGroups(mode, filter)`, `ListSessions(groupKey)`, `GetSessionMeta(ref)`, `GetMessages(ref, offset, limit)`; generated `wailsjs` bindings wrapped by `frontend/src/lib/api.ts` (the only module that imports `wailsjs/*`) | 3 | M1-13, M1-14, M1-15 | Frontend can call each binding through `api.ts`; the generated types compile. |
| M1-17 | Frontend: three-pane shell, resizable splitters (widths persisted), system light/dark theme, app-wide store | 3 | M1-15 | Panes resize; theme follows the OS. |
| M1-18 | Frontend: sidebar group tree (expand/collapse, counts, group-mode toggle, Agents section) | 3 | M1-16, M1-17 | Tree reflects real data in all three modes. |
| M1-19 | Frontend: session list column (virtualized rows with live dot, title, agent badge, **message count**, relative time, hover tooltip with count breakdown and absolute time) | 4 | M1-16, M1-17 | Smooth with 1k+ synthetic rows; counts match CLI output. |
| M1-20 | Frontend: transcript viewer base (virtualized list, user/assistant bubbles, markdown via `marked` + DOMPurify sanitization) | 4 | M1-16, M1-17 | A real session renders; injected `<script>` in content is neutralized. |
| M1-21 | Frontend: code highlighting (shiki, lazy-loaded languages) and copy-code buttons | 2 | M1-20 | Highlighted blocks render; copy works. |
| M1-22 | Frontend part renderers: collapsible reasoning, tool call + result pair (status icon, highlighted input/output), patch/diff view, compaction divider, subagent link (opens child) | 4 | M1-20 | Every `PartKind` has a renderer; checked on real sessions. |
| M1-23 | Frontend: session header (title, agent, model, cwd, branch, msg count, tokens, cost, created/updated) | 2 | M1-19 | Header values match the CLI `show --json`. |
| M1-24 | Progressive loading for big transcripts: paged `GetMessages`, load-on-scroll, first screen before the full parse | 4 | M1-20 | The 38 MB session shows its first screen in < 1 s (SPEC §8). |
| M1-25 | Actions: copy resume command (per provider, including `cd`), reveal source file (`xdg-open` on parent dir) | 2 | M1-23 | Pasted command resumes the session in each tool. |
| M1-26 | Empty, error and loading states (no agents detected, provider failed, session file vanished) | 2 | M1-18, M1-19, M1-20 | Each state is reachable and looks intentional. |
| M1-27 | WebView hardening: strict CSP, external links open in the system browser, no remote resources | 2 | M1-20 | CSP header is set; clicking a link in a transcript opens the browser, not the WebView. |
| M1-28 | MVP QA pass on real data: measure against the SPEC §8 perf targets, then file and fix the top issues | 4 | M1-01 → M1-27 | Checklist signed off; perf numbers recorded in `docs/perf.md`. |

## M2 — Index, search, live updates (≈ 36 h)

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M2-01 | Cache DB (`$XDG_CACHE_HOME/agent-sessions/index.db`, mode `0600`): schema and migrations for sessions, file state (path, size, mtime, offset) and counts | 3 | M1-13 | Migrations run idempotently; the file is created with `0600`. |
| M2-02 | Incremental indexing: skip unchanged files, resume from offset on append, rescan fully on shrink; startup paints from cache first, then refreshes | 4 | M2-01, M0-10, M1-10 | Warm start reaches first paint in < 300 ms; an appended session updates its count. |
| M2-03 | OpenCode incremental refresh via `time_updated > last_seen` | 2 | M2-01, M1-07 | Only changed sessions are re-read (verified by test). |
| M2-04 | FTS5 index of titles and message text, updated incrementally with the M2-02/03 pipeline | 4 | M2-02 | The FTS table stays in sync after append and delete. |
| M2-05 | Search API: `Search(query, filter)` → hits with snippet, session ref and message index; prefix and phrase queries | 3 | M2-04 | Tests cover ranking and snippet highlighting. |
| M2-06 | Search UI: `/` focuses the search box; results list with snippets; clicking a result opens the session, scrolls to the message and highlights the match | 4 | M2-05, M1-24 | Jumping to the matched message works in large sessions. |
| M2-07 | fsnotify watcher: watch provider roots (recursive for Claude/Codex dirs; the DB/WAL for OpenCode) with debounce, mapping events to targeted rescans | 4 | M2-02 | A new message in an active session is reflected within about 1 s. |
| M2-08 | Push backend changes to the UI (`runtime.EventsEmit` with the `OnStartup` ctx, subscribed through `api.ts`), reactively updating tree counts, list rows and the header | 3 | M2-07, M1-18, M1-19 | UI updates without manual refresh. |
| M2-09 | Live tail in the transcript view (auto-append; "jump to latest" pill when scrolled up) | 3 | M2-08, M1-24 | Watching an active session shows new messages. |
| M2-10 | Live badge refresh (periodic pid check, plus OpenCode/Codex activity heuristics based on recent updates) | 2 | M0-14, M2-08 | Badge disappears within 5 s of the agent exiting. |
| M2-11 | Diagnostics panel: provider roots, detected generations and versions, parse-error and unknown-type counts per provider | 3 | M1-13, M1-16 | Panel shows real counts; errors link to the file. |
| M2-12 | "Clear cache and rebuild" action | 1 | M2-01 | The cache is recreated and the UI reloads. |

## M3 — Polish & power features (≈ 35 h)

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M3-01 | Backend filters: agent, date range, model, archived, has-subagents, live-only | 3 | M1-16 | Filter tests pass; the counts in the tree respect filters. |
| M3-02 | Filter UI (chips and popover), persisted per session of the app | 3 | M3-01 | Filters apply instantly and survive a restart. |
| M3-03 | Sort options: updated, created, message count, tokens/cost | 2 | M1-19 | Sort stays stable and persists. |
| M3-04 | Repository folding: group by `RepoRoot`, collapsing subdirs and worktrees (`.claude/worktrees/*`, OpenCode `worktree`) | 3 | M1-14, M1-07 | This machine's worktree sessions collapse under the main repo. |
| M3-05 | Config file (`config.toml`): load, validate, defaults, save; root overrides and multiple roots per provider | 3 | M0-04 | Invalid config produces a readable error; overrides take effect. |
| M3-06 | Settings UI (providers on/off, roots, terminal command, theme, default grouping, hidden dirs) | 3 | M3-05 | Changes persist and apply without a restart. |
| M3-07 | "Open terminal in cwd" with a configurable command and auto-detected default | 2 | M3-05 | Works with at least `gnome-terminal`, `kitty` and `x-terminal-emulator`. |
| M3-08 | Export to Markdown (respecting the meta/reasoning toggles) | 3 | M1-16 | Exported file reads cleanly; golden test passes. |
| M3-09 | Export to JSON (unified model) and standalone HTML | 3 | M3-08 | Both formats open correctly; HTML is self-contained. |
| M3-10 | Keyboard navigation: `j/k`, `Enter`, `Esc`, `/`, `g g`/`G`, pane focus switching; shortcut help overlay | 3 | M1-19, M1-20 | All shortcuts work and are listed in the overlay. |
| M3-11 | View toggles: show meta/system, show reasoning, raw-JSON-per-record inspector | 3 | M1-22 | Toggles persist; raw JSON matches the source record. |
| M3-12 | Stats view: messages, tokens and cost per dir, per agent and over time (read `dataviz` guidance first) | 4 | M2-01 | Charts render from index data; totals match the list. |

## M4 — Destructive actions (opt-in) (≈ 10 h, pending SPEC Q1)

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M4-01 | Linux trash per the freedesktop Trash spec (`~/.local/share/Trash`, `.trashinfo`), with a macOS stub | 3 | M0-04 | Trashed files show up in the file manager's Trash and can be restored. |
| M4-02 | Delete/archive for Claude and Codex (trash the session file, subagent dir and tool-results); confirmation dialog; refuse if the session is live; setting to enable destructive actions | 4 | M4-01, M2-10 | Actions are disabled by default; the live guard is tested. |
| M4-03 | OpenCode delete/archive through the `opencode` CLI/API (investigate the supported path; no direct DB writes) | 3 | M1-07 | Session is removed via the official path, or the action is hidden if none exists. |

## M5 — macOS & distribution (≈ 20 h)

| ID | Task | Est | Deps | Done when |
|---|---|---|---|---|
| M5-01 | `paths_darwin.go`: cache in `~/Library/Caches`, config in `~/Library/Application Support`; verify tool roots on macOS | 2 | M0-04 | Path tests pass on macOS. |
| M5-02 | macOS build and fix WKWebView differences (fonts, scrolling, clipboard, `open -R`, terminal launch) | 4 | M5-01, M1-28 | Full MVP checklist passes on macOS. |
| M5-03 | Universal binary (arm64 + amd64) and `.dmg` packaging | 3 | M5-02 | `.dmg` installs and runs on both architectures. |
| M5-04 | Code signing and notarization pipeline (credentials stored in CI secrets) | 4 | M5-03 | Gatekeeper opens the app without warnings. |
| M5-05 | Linux packaging: AppImage and `.deb` (declaring the webkit2gtk-4.1 dependency) | 4 | M1-28 | Installs and runs on a clean Ubuntu VM. |
| M5-06 | CI (GitHub Actions): lint, test and fuzz-smoke on Linux; build artifacts for Linux and macOS on tag | 3 | M0-01 | CI is green; tagged release produces artifacts. |

---

## Summary

| Milestone | Tasks | Est. hours |
|---|---|---|
| M0 Foundation + Claude | 16 | 42 |
| M1 Providers + MVP GUI | 28 | 84 |
| M2 Index, search, live | 12 | 36 |
| M3 Polish | 12 | 35 |
| M4 Destructive (opt-in) | 3 | 10 |
| M5 macOS & distribution | 6 | 20 |
| **Total** | **77** | **≈ 227 h** |

**Critical path to the MVP:**

M0-01 → M0-02 → M0-03 → M0-07 → M0-08 → M0-09 → M0-10 → M1-13 → M1-16 → M1-20 → M1-24 → M1-28

**Parallelizable after M0-03:**
- OpenCode (M1-01…07), Codex (M1-08…12), Claude (M0-07…15) and the GUI scaffold (M1-15, M1-17) can each go to a different developer.
- Frontend work can start against a mocked `M1-16` binding layer.
