# Agent Sessions — Specification (draft v0.1)

A local desktop GUI to discover, browse, and manage sessions created by agentic
coding tools (Claude Code, Codex CLI, OpenCode, …).

- **Platforms:** Linux first, macOS second. Windows: out of scope.
- **Language:** Go (backend) + web frontend via Wails.
- **Principle:** local-only, read-only by default. The app never writes to a
  tool's own session store unless the user explicitly triggers a destructive
  action (see §7).

---

## 1. Goals / Non-goals

### Goals
1. Auto-detect installed agents and scan all their existing sessions.
2. Present sessions grouped by **working directory** and by **agent type**.
3. Render full session content (user/assistant messages, reasoning, tool calls
   and results, diffs, subagents, compaction markers).
4. Fast on large histories (hundreds of MB, single transcripts > 40 MB).
5. Survive format drift: unknown record types/fields are skipped, never fatal.
6. Pluggable providers so new agents (Gemini CLI, Aider, Copilot CLI, Cursor…)
   can be added without touching the core.

### Non-goals (v1)
- Starting/driving agents from within the app (only "copy/launch resume command").
- Syncing sessions across machines or any network access.
- Editing transcripts.

---

## 2. Tech stack

| Layer | Choice | Why |
|---|---|---|
| App shell | **Wails v2** (Go ↔ WebView) | Native binary, Go backend, HTML/CSS frontend. Rich rendering (markdown, code highlighting, diffs) is much easier in a WebView than in Fyne/Gio. Uses WebKitGTK on Linux, WKWebView on macOS, so the same code runs on both. |
| Frontend | Svelte + TypeScript + Vite (alt: React) | Lightweight; good fit for virtualized lists. |
| Markdown / code | `marked` + `shiki` (or `highlight.js`) | Transcripts are markdown-heavy. |
| SQLite (read) | `modernc.org/sqlite` (pure Go) | No cgo/sqlite version coupling; open tool DBs with `mode=ro`. |
| Own index/cache | SQLite + FTS5 at `$XDG_CACHE_HOME/agent-sessions/index.db` | Fast startup + full-text search. |
| File watching | `fsnotify` | Live updates of active sessions. |

**Linux build deps:** `gcc`, `pkg-config`, `libgtk-3-dev`, `libwebkit2gtk-4.1-dev`
(build with `-tags webkit2_41` on distros that ship only 4.1).

> **Decision (2026-09-27): Wails v2.** v2 is the stable line; v3 is beta, and
> moving from v2 to v3 later is a port rather than a version bump. To keep that
> port cheap:
> - Only `internal/app` and `cmd/agent-sessions` import Wails.
> - The frontend reaches the backend through one thin `frontend/src/lib/api.ts`
>   wrapper around the generated bindings and events. Components never import
>   `wailsjs/*` directly.
>
> v2 specifics:
> - Services are bound with `options.App{Bind: []interface{}{svc}}`, which
>   generates TypeScript into `frontend/wailsjs/go/...`.
> - Go → UI events use `runtime.EventsEmit(ctx, name, data)`, with the `ctx`
>   captured in `OnStartup`.
> - v2 supports a single window, which is enough for the three-pane layout.

---

## 3. Architecture

```
cmd/
  agent-sessions/        # Wails GUI entrypoint
  agent-sessions-cli/    # headless: `scan`, `show <id>`, `--json` (parser debugging, tests)
internal/
  model/                 # unified Session / Message / Part types
  provider/              # Provider interface + explicit provider Set
    claude/
    codex/
    opencode/
  index/                 # cache DB, incremental rescan, FTS
  watch/                 # fsnotify → provider-specific invalidation
  paths/                 # per-OS default locations, env overrides
  app/                   # Wails-bound service (the API the frontend calls)
frontend/
testdata/<provider>/     # sanitized fixture sessions (golden tests)
```

### 3.1 Provider interface & 3.2 Unified data model

The authoritative definitions live in
[docs/plan/M0.md](docs/plan/M0.md) (M0-02 `internal/model`, M0-03
`internal/provider`). In short:

- **`Provider`:** `Detect`, `Scan(ctx, prev ScanState)` (incremental: returns
  `Changed`/`Removed` sessions plus a new resume state), `Load` (full
  transcript on demand), `Blob` (lazy large outputs and images), `WatchPaths`,
  and `ResumeCommand`. `LiveDetector` is an optional extra.
- **`SessionMeta`:** identity, cwd/repo root, title, model, timestamps,
  `Counts` (user / assistant / tool calls), tokens, cost, and archived/live flags.
- **`Message` → `Part`:** kinds `text | reasoning | tool | patch | file |
  compaction | notice`. A tool call and its result are merged into one `tool`
  part. Large outputs and images are never inlined; they are referenced by a
  blob key.

Verified on-disk formats are documented in [docs/formats.md](docs/formats.md).

---

## 4. Provider specs (verified on this machine, 2026-09-27)

### 4.1 Claude Code
- **Root:** `$CLAUDE_CONFIG_DIR` or `~/.claude`.
- **Sessions:** `projects/<encoded-cwd>/<session-uuid>.jsonl`, one JSON record per line.
  - `<encoded-cwd>` is **lossy** (`/` and `.` → `-`). Never decode it; take
    `cwd` from the records.
  - Side data: `<uuid>/subagents/*.jsonl` (subagent transcripts → child
    sessions), `<uuid>/tool-results/*.txt` (large tool outputs, lazy-load).
- **Record `type`s seen:** `user`, `assistant`, `attachment`, `system`
  (e.g. `subtype: compact_boundary`), `ai-title`, `last-prompt`, `mode`,
  `cost-state`, `queue-operation`, `atis-latch`. Ignore unknown types.
- **Parsing rules:**
  - Assistant records are split per content block, so merge by `message.id`.
  - Thread via `uuid` / `parentUuid`. `isSidechain`, `isMeta` flags.
  - Title priority: last `ai-title.aiTitle` → `summary` record → first non-meta user text.
  - Model: `message.model`; tokens: `message.usage`.
- **Live detection:** `~/.claude/sessions/<pid>.json` contains `sessionId`,
  `cwd`, `pid`. The session counts as live if the pid is alive.
- **Scale:** 306 MB total here, largest file 38 MB. The metadata scan must
  stream the file and must not build the whole transcript in memory.
  - Message counts need one pass over every line. Use a fast line filter that
    checks the `"type"` prefix before full JSON decoding.
  - Cache the count together with the byte offset where the scan stopped.
    Sessions are append-only, so a changed file resumes from that offset
    instead of rescanning.
- **Resume:** `claude --resume <id>` (run in `cwd`).

### 4.2 Codex CLI
- **Root:** `$CODEX_HOME` or `~/.codex`.
- **Sessions:** `sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`, plus
  `archived_sessions/`.
  - Line 1: `session_meta` (id, cwd, cli_version, git info). Then
    `turn_context`, `response_item` (`message`, `reasoning`, `function_call`,
    `function_call_output`, …), and `event_msg` (token counts, etc.).
- **Index DB:** `state_<N>.sqlite` (pick the highest N), table `threads`: `id`,
  `rollout_path`, `cwd`, `title`, `first_user_message`, `model`,
  `tokens_used`, `archived`, `git_branch`, `created_at_ms`, `updated_at_ms`.
  Use it for fast metadata when populated, and fall back to scanning rollout
  files. `thread_spawn_edges` gives parent→child.
- ⚠️ No Codex sessions exist on this machine (the `threads` table is empty and
  there is no `sessions/` dir). **We need a sample rollout file for fixtures.**
- **Resume:** `codex resume <id>`.

### 4.3 OpenCode
- **Root:** `$XDG_DATA_HOME/opencode` or `~/.local/share/opencode` (the same
  path is used on macOS).
- **Three storage generations (v2 and v1 are read; legacy is detected only, M1-06):**
  1. `opencode.db` → `session_v2` + `session_message` (`type` ∈ user,
     assistant, compaction, synthetic, system, idle, …; `data` is JSON with a
     `content[]` of text/reasoning/tool entries). *(v2.x)*
  2. `opencode.db` → `session` + `message` + `part` (part types: text,
     reasoning, tool, patch, file, step-start/finish, compaction). *(v1.x)*
  3. Legacy JSON files: `storage/session/<projectID>/<id>.json`,
     `storage/message/<sessionID>/*.json`, `storage/part/<messageID>/*.json`.
  - Dedupe by session id: the v2 row wins unless the v1 row's `time_updated`
    is strictly newer (Scan and Load share this rule). Here all 60 v1
    sessions also exist among the 64 in v2.
- Metadata comes directly from the session row: `directory`, `title`,
  `parent_id` (subagents), `model`, `agent`, `cost`, `tokens_*`,
  `time_archived`, `summary_additions/deletions/files`. `project.worktree`
  gives the repo root; `worktree` table maps worktree dirs.
- DB is 248 MB with an active WAL. Open with `mode=ro`, keep queries short,
  and never hold long read transactions.
- **Resume:** `opencode --session <id>` (run in `directory`).

---

## 5. Grouping, filtering, search

- **Group modes (toggle):**
  - Directory → Agent → Sessions (default)
  - Agent → Directory → Sessions
  - Flat, sorted by recency
- **Directory normalization:** `filepath.Clean`, resolve symlinks, strip trailing `/`.
- **"Fold by repository" option:** group by git toplevel so that subdirectories
  and worktrees (`.claude/worktrees/*`, OpenCode `worktree` table,
  `git worktree list`) collapse under the main repo.
- Directories that no longer exist on disk are shown dimmed with a "missing" tag.
- **Filters:** agent, date range, model, has-subagents, archived, live-only.
- **Sort:** last updated (default), created, message count, tokens/cost.
- **Search:** FTS5 over title and message text, with results showing snippets.
  Clicking a result opens the session scrolled to the matching message.
- Subagent/child sessions are nested under their parent, not listed as top-level.

---

## 6. UI

Three-pane layout:

```
┌──────────────┬─────────────────────────────┬──────────────────────────────────┐
│ Search [...] │ Filters ▾  Sort ▾           │  Title · agent badge · model     │
├──────────────┼─────────────────────────────┤  cwd · branch · 128 msgs         │
│ ▾ ~/ws/app   │ ● Fix login bug  128💬  2m  │  tokens · cost                   │
│   Claude (4) │   Add tests       42💬  1d  │  [Copy resume cmd] [Open term]   │
│   OpenCode(2)│   Refactor API     9💬  3d  │──────────────────────────────────│
│ ▸ ~/ws/cli   │                             │  👤 user message (markdown)      │
│ ▸ ~/ws/site  │                             │  🤖 assistant text               │
│              │                             │   ▸ reasoning (collapsed)        │
│ Agents       │                             │   ▸ tool: bash `make test` ✓     │
│  Claude  34  │                             │   ▸ patch: 3 files +40 −12       │
│  Codex    0  │                             │  ── context compacted ──         │
│  OpenCode 64 │                             │   ▸ subagent: Explore (→ open)   │
└──────────────┴─────────────────────────────┴──────────────────────────────────┘
```

- **Session list row (column 2):** live dot, title, agent badge (in flat and
  agent-less group modes), **message count**, and relative updated time. The
  full timestamp shows on hover.
- **Message count definition:** the number of conversation messages shown in
  the transcript by default:
  - human user prompts, excluding meta/injected records and tool results
  - assistant messages, merged per message id (Claude splits them per content block)

  Tool calls/results, reasoning, and bookkeeping records are not counted.
  Subagent messages are counted on the subagent session, not the parent. The
  hover tooltip gives a breakdown: `user 12 · assistant 116 · tool calls 340`.

- The transcript view is **virtualized**, so sessions with 10k+ records stay smooth.
- Tool call and result are paired and collapsed by default, with syntax-highlighted input/output.
- Toggles: show meta/system messages, reasoning, and raw JSON per record (for debugging).
- Live sessions show a green dot, and their transcript auto-updates (tail).
- Light and dark theme that follows the system. Keyboard navigation: `/`
  search, `j/k` list, `Enter` open.

---

## 7. Actions

| Action | Milestone | Notes |
|---|---|---|
| Copy resume command | M1 | Per-provider command plus `cd <cwd>`. |
| Open terminal in cwd | M3 | Configurable terminal command (`x-terminal-emulator`, `gnome-terminal`, `kitty`, …; `open -a Terminal` on macOS). |
| Reveal source file | M1 | `xdg-open` / `open -R`. |
| Export session | M3 | Markdown, JSON (unified model), HTML. |
| Delete / archive | M4, opt-in | Destructive. Requires confirmation. Claude files are moved to the OS trash (not `rm`). Codex uses `codex delete`, and OpenCode deletes v2 copies via `opencode session delete` and v1 rows via one SQL transaction; both are permanent and need the separate `allow_permanent_delete` opt-in. Never run while a session is live or recently updated. |

---

## 8. Performance & robustness

- **Incremental index:** key file-based sessions by `(path, size, mtime)` and
  re-parse only changed files. Append-only JSONL files (Claude, Codex) resume
  from the last scanned byte offset, which keeps message counts cheap to
  maintain. If the file shrank, rescan it fully.
- **OpenCode:** query `time_updated > last_seen`. Message counts come from SQL
  aggregates:
  - `session_message` rows with type `user`/`assistant`
  - v1 fallback: `message` rows grouped by role
- **Codex:** count `response_item` messages with role `user` (excluding
  injected environment/instructions context) and role `assistant`.
- **Targets (≈300 MB history):** cold first scan < 5 s, warm start < 300 ms to
  first paint, opening a 40 MB transcript < 1 s to first screen (stream and
  render progressively).
- Scan providers concurrently with a bounded worker pool.
- Per-record parse errors are counted and surfaced in a "diagnostics" panel,
  never crashing the scan.
- Record provider format versions (`version`, `cli_version`) to diagnose drift.

## 9. Privacy & security

- No network calls and no telemetry.
- Never read credential files (`.credentials.json`, `auth.json`, OpenCode
  `credential`/`account` tables).
- Treat transcript content as untrusted when rendering: sanitize markdown/HTML
  and allow no script execution in the WebView.
- The cache DB contains transcript text. Store it with `0600` permissions and
  offer "clear cache".

## 10. Configuration

`$XDG_CONFIG_HOME/agent-sessions/config.toml`:
- Per-provider enable flag and root path overrides (supporting multiple roots,
  e.g. several `CLAUDE_CONFIG_DIR`s).
- Default grouping, hidden directories, terminal command, theme.

## 11. macOS port (M5)

- Paths are mostly identical (`~/.claude`, `~/.codex`, `~/.local/share/opencode`).
  Centralize them in `internal/paths` with `_darwin.go` / `_linux.go`.
- Cache at `~/Library/Caches/agent-sessions`, config at `~/Library/Application Support/agent-sessions`.
- Build a universal binary (arm64 + amd64). Code signing and notarization are needed for distribution.
- Live-pid check: Linux reads `/proc/<pid>` and matches the start time to guard pid reuse; macOS needs an equivalent (M5-01/02).

## 12. Testing

- Golden tests per provider: `testdata/<provider>/*` → expected unified JSON.
- Fixtures cover every OpenCode generation, Claude compaction/subagents/split
  assistant blocks, and Codex with and without the `threads` index.
- A fuzz test for JSONL parsers (truncated or partially written last line,
  which is normal for live sessions).
- The CLI `agent-sessions-cli scan --json` is used in CI without a display.

## 13. Milestones

| # | Scope |
|---|---|
| M0 | Repo scaffold, unified model, provider interface, Claude provider, headless CLI, fixtures. |
| M1 | OpenCode + Codex providers; Wails app with grouping tree, session list, and transcript viewer; copy resume / reveal file. **← MVP** |
| M2 | Cache index + FTS search, fsnotify live updates, live badges, diagnostics panel. |
| M3 | Filters/sort polish, export, open-terminal, repo folding, keyboard nav, stats (tokens/cost per dir/agent). |
| M4 | Opt-in destructive actions (trash/archive). |
| M5 | macOS build, signing, packaging (Linux: AppImage/.deb; macOS: .dmg). |

## 14. Open questions

1. **Scope of "manage":** is read-only and resume enough for v1, or is delete/archive needed early?
2. **Frontend framework:** Svelte (recommended) or React?
3. **Codex samples:** can you produce a real Codex session for fixtures?
4. **More agents later?** Gemini CLI, Aider, Copilot CLI, Cursor, … (these affect how generic the model must be).
5. **Distribution:** personal use only, or packaged releases?
