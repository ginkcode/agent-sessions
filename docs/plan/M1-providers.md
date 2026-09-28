# M1 — OpenCode, Codex and scan/group core: detailed plan

Covers M1-01 … M1-14 in [TASKS.md](../../TASKS.md). The Wails app and UI
(M1-15 … M1-28) are planned separately. The shared contracts in [M0.md](M0.md)
are authoritative: module `github.com/ginkcode/agent-sessions`, `model` types,
`provider.Provider`, `ScanState`/`ScanResult`, `Diagnostics`, `jsonl.Reader`,
`pathutil.GitResolver` and `testutil/golden` are **not** redefined here. Format
background is in [docs/formats.md](../formats.md).

**Boundaries:** no writes to tool stores; no reads of OpenCode `auth.json` or
`credential`, `account`, `control_account` tables, or Codex `auth.json`. Scan
returns **metadata only**; `Load` parses content on demand; `Blob` retrieves
large output/attachments on demand. Each provider uses its M0 `ScanState` and
returns `Changed`, `Removed`, the replacement `State`, and `Diag`. All times
in the model are UTC. Respect context cancellation at least once per source
and every ~1,000 records. Unknown types are diagnostics, not fatal errors.

**Local observations (read-only, 2026-09-27):** the OpenCode DB is 248 MB in
WAL mode, with 64 `session_v2` rows, 60 `session` rows and 2,748
`session_message` rows; the 60 v1 IDs are duplicated in v2. A read-only SQLite
probe took ~0.2 ms to group message roles via the covering index and ~40 ms to
count 3,722 tool items using `json_each` on assistant JSON. Fifteen v2 sessions
have no messages (keep them). Codex `state_5.sqlite` has a `threads` table but
zero rows locally; there are no local rollouts to commit as fixtures.

---

## M1-01 OpenCode read-only SQLite helper (3 h)

`internal/provider/opencode/db.go`; add `modernc.org/sqlite` to `go.mod`.
This helper is also used for the Codex state index in M1-11 (no second driver).

```go
func openReadOnly(path string) (*sql.DB, error)
func hasTable(ctx context.Context, db *sql.DB, name string) (bool, error)
func tableColumns(ctx context.Context, db *sql.DB, name string) (map[string]bool, error)
```

1. Build the DSN with `url.URL{Scheme: "file", Path: absPath}` and `url.Values`:
   `mode=ro&_pragma=busy_timeout(5000)&_txlock=deferred` (URL-encoded when
   serialized). `modernc.org/sqlite` documents `_pragma` as a `PRAGMA ...`
   statement and `_txlock` as `deferred`/`immediate`/`exclusive`;
   `busy_timeout(5000)` is SQLite's 5-second busy timeout. `deferred` is already
   the default, but stating it prevents an accidental future exclusive lock.
   See [modernc driver docs](https://pkg.go.dev/modernc.org/sqlite).
2. `sql.Open("sqlite", dsn)` is lazy: `PingContext` immediately, then set
   `SetMaxOpenConns(1)` and `SetMaxIdleConns(1)` for short sequential reads.
   Close the DB on all paths. Do not set `immutable=1`: the DB and its WAL are
   live; that flag can omit uncheckpointed changes. WAL read-only opens need
   an accessible `-shm` file or permission to create it; report an actionable
   warning if the directory/files make this impossible. Do **not** create a
   shadow copy or run `PRAGMA journal_mode` (a write).
3. Schema probes use only an allowlisted name from code, never user SQL:
   ```sql
   SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?);
   PRAGMA table_info("session_v2"); -- only for a whitelisted identifier
   ```
   M1-11 can share this helper only after moving the unexported functions to
   `internal/provider/sqliteread/` or implementing a tiny read-only wrapper
   there; place it there now (`sqliteread.Open`, `HasTable`, `Columns`) to
   avoid an OpenCode→Codex import. The relative file is
   `internal/provider/sqliteread/db.go`.
4. Use `QueryContext`/`QueryRowContext`, close `Rows` promptly, and never
   hold a transaction across a scan/`Load` or a channel send. Wrap lock, schema,
   and permission failures with the DB path; a busy DB fails this provider only.

**Tests:** temp DB initialized by test code (never the tool's DB): mode=ro
rejects `CREATE TABLE`, `HasTable` true/false, reserved name handling, bad
path/permissions and canceled query. Manual smoke: open/query/close the live
DB while OpenCode is running without `SQLITE_BUSY` or writes. If `-shm` is
missing in a read-only directory, surface the SQLite error instead of silently
using stale data.

**Done when:** read-only smoke succeeds on the live DB, and tests pass.

---

## M1-02 OpenCode detection and storage generations (2 h)

`internal/provider/opencode/opencode.go`, `detect.go`.

```go
type Provider struct {
    root string // paths.Roots.OpenCodeData
    git  *pathutil.GitResolver
}
func New(root string, git *pathutil.GitResolver) *Provider
func (p *Provider) ID() model.AgentID                 // model.AgentOpenCode
func (p *Provider) DisplayName() string               // "OpenCode"
func (p *Provider) Detect(ctx context.Context) (provider.Detection, error)
func (p *Provider) WatchPaths() []string
func (p *Provider) ResumeCommand(m model.SessionMeta) provider.Command
```

1. Probe `<root>/opencode.db` with M1-01 if it exists. Recognize **v2 only
   when both** `session_v2` and `session_message` exist; recognize **v1 only
   when both** `session` and `message` exist (and `part` for `Load`). A partial
   schema is a warning and is skipped, not a crash. Probe
   `storage/session/` for `*.json` under project-ID directories independently.
   Empty legacy directories do not count as session data.
2. `Detection{Present, Roots: []string{root}, Generations: []string{...}}`
   lists generations present in order `v2`, `v1`, `legacy`. An absent root
   returns `Present=false`, no error. A corrupt/existing unreadable DB returns
   a contextual error without trying to read authentication data.
3. `WatchPaths`: only paths that exist among `opencode.db`, its `-wal`, and
   `storage/session`/`storage/message`/`storage/part` (M2 watcher also watches
   parent dirs so it notices files created later). `ResumeCommand` is
   `{Argv: []string{"opencode", "--session", m.Ref.ID}, Dir: m.CWD}`;
   `opencode --help` in v2.0.18 confirms `--session, -s string` continues a
   session. The app quotes argv and cwd for a shell; never build shell text in
   the provider.
4. Implement interface stubs backed by later tasks without special-casing
   v1/legacy in callers.

**Tests:** absent root, all generation combinations, incomplete DB schema,
legacy-only tree, path with spaces. Manual `Detect` reports v2 and v1 here.

**Done when:** detection is accurate and no absent installation errors.

---

## M1-03 OpenCode v2 metadata scan and counts (3 h)

`internal/provider/opencode/scan_v2.go`. Scan is per-session and uses short,
parameterized queries; omit archived filtering. `time_*` values are Unix
milliseconds. `session_v2` has **no index on `time_updated` locally**: for 64
rows a table scan is cheaper than building any index in a read-only DB.

```go
func (p *Provider) scanV2(ctx context.Context, db *sql.DB,
    sinceMs int64, full bool, d *provider.Diagnostics) ([]model.SessionMeta, []string, int64, error)
func modelName(raw sql.NullString) string // {"providerID":"x","id":"y"} → "x/y"
```

**Session query** (`sinceMs` omitted on a first/full scan; subsequent scans
use `>=` to include equal-millisecond boundary rows):

```sql
SELECT s.id, s.project_id, s.parent_id, s.directory, s.title, s.version,
       s.agent, s.model, s.cost, s.tokens_input, s.tokens_output,
       s.tokens_reasoning, s.tokens_cache_read, s.tokens_cache_write,
       s.time_created, s.time_updated, s.time_archived, p.worktree
FROM session_v2 AS s
LEFT JOIN project AS p ON p.id = s.project_id
WHERE s.time_updated >= ?
ORDER BY s.id;
```

A separate cheap **all-ID** sweep supports deletions even with an incremental
cursor: `SELECT id FROM session_v2 ORDER BY id`. Select `MAX(time_updated)`
from `session_v2` once; advance the cursor monotonically. Do **not** only
query updates for deletion detection.

For IDs returned above, batch parameterized `IN (?,...)` queries (≤ 100 IDs
per batch; SQLite variable limits vary). Use the covering
`session_message(session_id,type,seq)` index for conversation counts:

```sql
SELECT session_id,
       SUM(CASE WHEN type='user' THEN 1 ELSE 0 END) AS users,
       SUM(CASE WHEN type='assistant' THEN 1 ELSE 0 END) AS assistants
FROM session_message
WHERE session_id IN (/* bound IDs */) AND type IN ('user','assistant')
GROUP BY session_id;
```

Tool counts need JSON only for assistant rows of those IDs:

```sql
SELECT m.session_id, COUNT(*) AS tools
FROM session_message AS m,
     json_each(m.data, '$.content') AS item
WHERE m.session_id IN (/* bound IDs */) AND m.type='assistant'
  AND json_extract(item.value, '$.type')='tool'
GROUP BY m.session_id;
```

`json_each` measures ~40 ms for **all** 2,531 local assistant rows versus
~0.2 ms for role counts (a full unbounded batch here measured ~55–70 ms).
Keep both SQL aggregates on the scan path rather than materializing 30 MB of
assistant JSON in Go or rereading the whole 248 MB DB; incremental scans
restrict by changed IDs. Invalid `data` JSON should not abort all metadata:
use `json_valid(m.data)` to guard `json_each` (or retry the batch per ID and
warn); still count roles. For first prompt, select the first `user`
row per changed ID by `seq` using `ORDER BY seq LIMIT 1` with the
`session_message(session_id,type,seq)` index. Read only `data.text`, normalize
with `model.OneLine`/`TruncateRunes(...,300)`; never decode user `files.data`
(base64). `Title`: nonempty row title → FirstPrompt → `"(untitled)"`.

Map `model` JSON `providerID/id` (empty/invalid fields: best available id and
warn on malformed JSON; optional `variant` is ignored), `agent`→`AgentName`,
nullable `parent_id`→`ParentID`,
`project.worktree`→`RepoRoot` unless project is `global` or worktree is `/`;
otherwise `git.Resolve(CWD).MainRoot`. `pathutil.NormalizeDir`,
`pathutil.Exists`, `time.UnixMilli`, `Archived = time_archived.Valid`, row
`cost`/token columns and `version` fill M0 `SessionMeta`. `SourcePath` is the
absolute `opencode.db` path; `Ref={AgentOpenCode,id}`. Keep zero-count sessions.

**Tests:** golden metadata for 2 user/3 assistant/4 tool items, no messages,
null model/title, archived row, parent, deleted cwd, invalid JSON, equal-ms
updates, and max cursor. Live sanity: 64 v2 rows with expected counts.

**Done when:** scan of this DB lists 64 metas with correct count breakdown.

---

## M1-04 OpenCode v2 transcript `Load` (4 h)

`internal/provider/opencode/load_v2.go`, `blob.go`. Resolve a ref to v2 first,
then stream rows in sequence order (do not select all JSON in metadata scan):

```sql
SELECT id, type, seq, time_created, data
FROM session_message WHERE session_id=? ORDER BY seq;
```

```go
func (p *Provider) loadV2(ctx context.Context, db *sql.DB,
    id string, meta model.SessionMeta, d *provider.Diagnostics) (*model.Transcript, error)
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error)
```

| Row | Unified mapping |
|---|---|
| `user` | `Message{ID: row.id, Role: user, Time: data.time.created}`; `data.text`→text part. `files[]`→lazy `FileRef` (name, MIME, `Ref`) without copying `files[].data`; pure-file user rows are still messages. |
| `assistant` | One `Message` per row, model from `data.model.providerID/id`, tokens from `data.tokens`, content array in order. `text`→text, `reasoning`→reasoning, `tool`→one paired tool part (`id`, `name`, JSON `state.input`, `state.status`, output from `state.metadata.output` or `state.content[]`). |
| `compaction` | System compaction part with reason/status; summary collapsible, bounded preview + lazy output if large. |
| `synthetic`, `system` | `Message{Role: system, IsMeta: true}` with text/notice (not counted). |
| `idle`, `location-switched` | Ignore; unknown row/content types call `Diag.Unknown`. |

`state.content[]` may contain text, while `metadata.output` already holds the
full result; do not render both as duplicated output. Keep tool output ≤64 KiB
in `ToolCall.Output`; set `OutputTruncated`/`OutputRef` for larger outputs or
`metadata.truncated=true`. A tool with state `error` is a single error tool
part. `data.error` becomes a notice; timestamps prefer JSON milliseconds,
falling back to row `time_created`.

**Blob keys:** `v2:<message-id>:file:<index>` or
`v2:<message-id>:tool:<content-index>` (opaque to UI); requery by
`WHERE id=? AND session_id=?` and extract exactly that slot. A user file may
store base64 in `files[].data`; a v1 file may store a `data:` URI (M1-05).
`FileRef.Ref` contains only the blob key, **never** raw data/base64/URI.
Validate ref agent and session ID, array indexes and types; cap decoded blob
size or stream it with an explicit UI budget. Do not encode any raw URI into
`Transcript` or a diagnostic. For task tools, use
`state.metadata.sessionId` and `parentSessionId` only when they match a known
child session (M1-07) to set `ToolCall.Child`.

**Tests:** golden tool+reasoning+compaction/system/synthetic transcript;
files.data fixture confirms JSON output contains no base64 and `Blob`
reconstructs bytes; large output and invalid content/unknown type. CLI
`show opencode <id> --json` parses the fixture.

**Done when:** v2 fixture transcript (including tools and lazy files) matches
the golden output.

---

## M1-05 OpenCode v1 scan and `Load` (4 h)

`internal/provider/opencode/scan_v1.go`, `load_v1.go`. Implement independent
v1 scan/load for v1-only IDs; merge priority belongs to M1-07. Query rows
from `session` with the **same core columns** as v2 (no `fork_session_id`),
left-joined to `project`; use `WHERE s.time_updated >= ?` for updates and
`SELECT id FROM session` for removal bookkeeping.

```sql
SELECT s.id, s.project_id, s.parent_id, s.directory, s.title, s.version,
       s.agent, s.model, s.cost, s.tokens_input, s.tokens_output,
       s.tokens_reasoning, s.tokens_cache_read, s.tokens_cache_write,
       s.time_created, s.time_updated, s.time_archived, p.worktree
FROM session AS s LEFT JOIN project AS p ON p.id=s.project_id
WHERE s.time_updated >= ? ORDER BY s.id;
```

`message.data.role` contains `user`/`assistant` (not a separate role column).
Count **actual** user prompts, not all user rows: v1 user messages whose parts
are only a `compaction` part, or a synthetic `text` part
(`part.data.synthetic == true`), are not conversational (locally, 16 such
user rows exist).

```sql
SELECT m.session_id,
       SUM(CASE WHEN json_extract(m.data,'$.role')='assistant' THEN 1 ELSE 0 END)
           AS assistants,
       SUM(CASE WHEN json_extract(m.data,'$.role')='user' AND EXISTS (
            SELECT 1 FROM part AS p WHERE p.message_id=m.id
              AND json_valid(p.data)
              AND json_extract(p.data,'$.type') IN ('text','file')
              AND COALESCE(json_extract(p.data,'$.synthetic'),0)=0
           ) THEN 1 ELSE 0 END) AS users
FROM message AS m
WHERE m.session_id IN (/* bound IDs */) AND json_valid(m.data)
GROUP BY m.session_id;
```

For tooltip tool calls:

```sql
SELECT p.session_id, COUNT(*) FROM part AS p
WHERE p.session_id IN (/* bound IDs */) AND json_valid(p.data)
  AND json_extract(p.data,'$.type')='tool'
GROUP BY p.session_id;
```

The local v1 counts differ from naive `role=user` counts on 10 of 45 populated
sessions because synthetic/compaction user messages exist. Both queries are
limited to selected IDs; local full v1 counts take ~10–40 ms. Malformed JSON
rows increment `Diag.ParseErrors`. `model` session column follows M1-03;
`message.data.modelID/providerID` is the fallback for transcript model.

**Load:** query `message` by `session_id ORDER BY time_created,id`, and parts
per message `ORDER BY time_created,id` (or one joined query with a stable
message/part sort). `message.data.role`, `time.created`, `tokens`, `cost` map
to `Message`. `part.data.type` mapping:

- `text`, `reasoning`: text/reasoning part; synthetic user text→`IsMeta`.
- `tool`: single paired tool (`tool`, `callID`, `state.input`,
  `state.output`, `state.status`), cap output and supply `Blob` key.
- `patch`: `PartPatch.Files` from `files[]`; `file`:
  `FileRef{Mime, Name, Ref:"v1:<part-id>:file"}`. **Do not inline** its
  potentially huge `data:` URI; `Blob` validates ownership and decodes only
  on request.
- `compaction`: one system marker (not a human prompt);
  `step-start`/`step-finish`: bookkeeping, ignore. Unknown types: diag.

`session_v2` does not imply a v1-only file; use v1 only when v2 has no matching
ID. Parent and project rules are shared with M1-03.

**Tests:** a small v1-only fixture DB built from SQL (M1-07), with synthetic
and compaction user rows, paired tool, patch, file URI and invalid part.
Metadata and transcript golden tests; `Blob` round-trip.

**Done when:** v1-only fixture scans, counts and loads without counting
synthetic prompts or exposing file URI payloads.

---

## M1-06 OpenCode legacy JSON storage (4 h)

`internal/provider/opencode/legacy.go`. No real legacy session files exist
locally, so fixtures are synthetic and follow [formats.md](../formats.md).

```go
func (p *Provider) scanLegacy(ctx context.Context, prev provider.ScanState,
    d *provider.Diagnostics) (metas []model.SessionMeta, sources map[string]provider.SourceState, err error)
func (p *Provider) loadLegacy(ctx context.Context, id string,
    d *provider.Diagnostics) (*model.Transcript, error)
```

1. Walk **only** `storage/session/<projectID>/<sessionID>.json`. Decode
   session JSON (`id`, `projectID`, `parentID`, `directory`, `title`,
   `time.{created,updated}`, version, tokens/cost as available). File stem is
   the ID fallback. Never walk `storage/session_diff`, `migration`, auth, or
   credentials. Record each absolute session JSON path in `ScanState.Sources`;
   if size+mtime match the previous source, use its checkpointed meta/counts.
2. For changed/new session JSON, glob only
   `storage/message/<sessionID>/*.json`; parse each message using the v1
   `message.data` mapping. For its ID, read
   `storage/part/<messageID>/*.json`; use v1 part mapping/count rule. Parts
   may lack a DB-style `data` wrapper: fields live at the JSON root. Sort by
   `time.created`, then ID. Skip invalid files individually with path warnings.
3. If a message/part is modified while the session JSON file is unchanged,
   still invalidate its checkpoint: store and compare directory/file stamps
   for the message and part subtree, or reread these small legacy directories.
   Never assume only `session.time.updated` changes. Store a checkpoint of the
   finalized meta under the session JSON source path to avoid repeated parses.
4. `RepoRoot`: project JSON under `storage/project/<projectID>.json` if present
   and `worktree` is valid, otherwise `git.Resolve(CWD).MainRoot`; `global`/`/`
   are not repos. Legacy `Blob` keys carry a validated part path/ID; reject
   traversals and escapes (use `filepath.Rel` after resolving symlinks) and
   return bytes only on demand.
5. `Removed`: previous legacy session paths not discovered; this is a file
   deletion, not a DB `time_updated` event. Continue to scan other sessions
   if one JSON file is malformed.

**Tests:** synthetic tree with a parent/child, tool call, file URI, missing
parts, malformed message, updated part without session JSON change, and a
removed session. Golden metadata and transcript. No files created in the tool
root by tests.

**Done when:** legacy-only tree scans/loads incrementally and handles drift.

---

## M1-07 OpenCode merge, ancestry and project roots (3 h)

`internal/provider/opencode/scan.go`, `resolve.go`; fixtures in
`internal/provider/opencode/testdata/` (`v1.sql`, `v2.sql`,
`legacy/...`, golden JSON). `sql` files create **test** DBs in `t.TempDir()`;
never commit a copy of the 248 MB live DB.

```go
func (p *Provider) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error)
func (p *Provider) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error)
```

1. Detect generations and scan newest to oldest: v2 → v1 → legacy. Build
   `byID map[string]model.SessionMeta`, keeping the **first** generation of
   each ID (*as built:* v2 wins unless the v1 row's `time_updated` is
   strictly newer, because an unmigrated 1.x client can keep writing v1); never add counts across generations. `Load` resolves that same
   priority at call time and tolerates a generation disappearing by trying
   the next available generation. Keep one ID→generation/source map per
   provider instance for `Blob`, or recompute with bounded probes when a
   `Load` starts (protect concurrent reads with a mutex).
2. Track the **entire** chosen ID set in a provider-private checkpoint inside
   `prev.Sources[dbPath].Checkpoint` (legacy paths remain individual sources).
   Compare against a fresh ID sweep of v2/v1 and legacy paths. Return IDs no
   longer present in `Removed`; an ID dropping from v2 into still-existing v1
   is `Changed`, not `Removed`. If DB temporarily fails, do **not** report
   all its sessions removed; return an error/diagnostic and retain previous
   state. New/modified rows, and generation changes, go in `Changed`.
3. `ScanState.Cursor` is a **decimal Unix-millisecond max `time_updated`**
   across v2/v1, not a row count or a last-seen ID. On first scan (empty
   cursor) query all rows. On the next scan use `>= cursor` (rather than only
   `>`) to catch ties at millisecond precision; unchanged equal-boundary
   rows may reappear in `Changed`, so compare against checkpointed previous
   metadata when available. Cursor advances to `max(oldCursor, max seen)`.
   Changing a legacy file bypasses the DB cursor; a new/removed DB ID is
   picked up by the all-ID sweep. If timestamps regress, a complete rescan
   requested with zero `ScanState` repairs it; M2 adds persistence/rebuild.
4. `project.worktree` gives the **main repo root**, including sessions whose
   CWD is a linked worktree listed in `worktree(project_id,directory)`.
   Never treat `project_id='global'` or worktree `/` as a repo; use
   `pathutil.GitResolver.Resolve(CWD).MainRoot` for those or missing rows.
   `RepoRoot` is normalized; do not use `fork_session_id` as `ParentID`:
   only `parent_id` denotes subagent/child ancestry. `AgentName` comes from
   session `agent`; if a task tool has `metadata.sessionId`, attach the child
   ref only when the child `parent_id` matches the current session.
5. Sort changed metas and removed refs by `Ref.Key()` for stable tests; aggregate
   parse/unknown diagnostics across generations; a bad legacy file does not
   mask healthy DB sessions.

**Tests:** both SQL generations with duplicate ID (v2 wins), v1-only ID,
legacy-only ID, one migration v1→v2, deletion, same-ms update, 2 nested
children and a linked worktree plus `global`. Live sanity: **64**, not 124,
unique IDs; parent IDs/roots match the local DB. No test uses live content.

**Done when:** all generations merge predictably; no local duplicate IDs.

---

## M1-08 Codex fixtures, sanitized (1 h; blocked on a real sample)

`internal/provider/codex/testdata/rollout-basic.jsonl`,
`internal/provider/codex/testdata/state.sql`, golden outputs. Extend the
existing `cmd/fixture-sanitize/main.go` (M0-06), **not** a new sanitizer.

1. Ask the user for consent to generate a throwaway session. In a **temporary
   directory**, run e.g. `codex exec --skip-git-repo-check "Reply with one
   sentence, then invoke no tools"` with an isolated `CODEX_HOME` and chosen
   auth setup; do **not** run the command as part of a read-only plan. Normal
   `codex exec` persists a rollout; `--ephemeral` does not, so omit it.
   A short interactive `codex` session with one tool call gives richer data.
   Obtain a populated `state_N.sqlite` from the same isolated home if the
   client populated it; otherwise generate a **minimal SQL fixture** with the
   observed `threads`/`thread_spawn_edges` columns, recording that it is
   synthetic. Never commit `auth.json`, logs, or raw user prompts.
2. Sanitize all Codex message-bearing keys recursively: `message`, `text`,
   `summary_text`, `instructions`, `arguments` (parse JSON string if valid),
   `output`, `encrypted_content` (drop), nested `content`/`input`, tool calls,
   `repository_url`, path-bearing fields (`cwd`, rollout_path, local file
   paths) and image/audio data. Keep item `type`, `role`, IDs/call IDs,
   timestamps, model, numeric usage, envelope/ordinal, source shape and line
   order. Replace URLs/secrets, usernames and **all free-form strings** with
   deterministic placeholders rather than relying on only known keys.
3. Make `state.sql` reproduce just the required metadata/spawn rows, replacing
   rollout paths with fixture-relative paths when a test builds its temp DB;
   no binary state DB or private rollout is committed. Include a second
   manually constructed fixture for malformed/truncated last lines and unknown
   item types. Run a secret/path grep and review the sanitized output by hand
   before committing. Assert fixture ID matches `session_meta.id` and DB row.

**Tests:** sanitizer preserves JSONL schema and pairable call IDs without
original text/paths; fixture loads and is accepted by the Codex CLI parser if
possible. Until a real sample is supplied, M1-09 can use synthetic files;
M1-10/11/12 golden acceptance remains **blocked**, not falsely complete.

**Done when:** sanitized rollout + populated-index fixture SQL and goldens
are committed, or explicitly marked blocked awaiting user sample.

---

## M1-09 Codex detection and discovery (2 h)

`internal/provider/codex/codex.go`, `discover.go`.

```go
type Provider struct {
    root string // paths.Roots.Codex
    git  *pathutil.GitResolver
}
func New(root string, git *pathutil.GitResolver) *Provider
func (p *Provider) ID() model.AgentID   // model.AgentCodex
func (p *Provider) DisplayName() string // "Codex"
func (p *Provider) Detect(ctx context.Context) (provider.Detection, error)
func (p *Provider) WatchPaths() []string
func (p *Provider) ResumeCommand(m model.SessionMeta) provider.Command
func (p *Provider) discover(ctx context.Context) ([]string, error)
```

1. Resolve `$CODEX_HOME` or `~/.codex` through M0 `paths`. Discover only
   `sessions/YYYY/MM/DD/rollout-*.jsonl` and
   `archived_sessions/rollout-*.jsonl`. Glob the prescribed depth; skip
   symlinks escaping the root and unreadable files with path warnings.
2. Find `state_<N>.sqlite` by strictly parsing the nonnegative integer `N`
   (not lexical ordering); use the highest existing one. Do not traverse
   `auth.json`/other SQLite files. `Present` if any rollouts **or** a populated
   threads table; an empty state DB alone is not a session. Note the index
   path/generation for M2 diagnostics. A missing root is normal.
3. `SourcePath` is the rollout path, not `state_N.sqlite`. `WatchPaths` includes
   `sessions`, `archived_sessions` and the selected state DB/WAL (if present).
   Resume uses `{Argv: []string{"codex", "resume", m.Ref.ID}, Dir:m.CWD}`;
   local `codex resume --help` (0.156.1) accepts UUID or session name, with
   `--last` optional. Use the UUID and explicit cwd, not `--last`.

**Tests:** synthetic two-day tree, archived file, state_2 vs state_10,
empty root, symlink escape and duplicate UUID path. No local sessions is
expected on this machine.

**Done when:** fixture files are discovered; absent installation is harmless.

---

## M1-10 Codex rollout metadata scan and count resume (3 h)

`internal/provider/codex/scan_rollout.go`. Reuse M0 `jsonl.Reader` and
`SourceState{Size,ModTimeNs,Offset,Checkpoint}`; scan **complete** newline-
terminated records only. A partial final line remains at the old offset and
is retried. Decode a small envelope with `encoding/json` or `gjson`, never
load the whole rollout. Lines are normally
`{ "timestamp": RFC3339, "ordinal"?: n, "type": "...", "payload": {...} }`.
The `type` belongs to a **rollout item**; `response_item.payload.type` is a
second, independent discriminator. Codex's canonical Rust parser avoids a
`serde(flatten)`/arbitrary-precision decimal pitfall; Go `json.RawMessage`
retains raw number tokens for `Load`. Do not decode large tool arguments into
float64 via `map[string]any` and re-encode them.

```go
type checkpoint struct {
    Meta             model.SessionMeta `json:"meta"`
    LastAssistantID  string            `json:"lastAssistantId"`
    // See below: pending function calls and usage are not counted twice.
}
func (c *checkpoint) consume(line []byte, d *provider.Diagnostics)
func (c *checkpoint) finalize(path string, modTime time.Time) model.SessionMeta
```

| Rollout item | Metadata/count action |
|---|---|
| `session_meta` | `payload.id`, `cwd`, `cli_version`, `git.branch`; `payload.timestamp`/line timestamp for creation, optional `source`/`originator`; `git.repository_url` is not a local RepoRoot. |
| `turn_context` | Last nonempty `payload.model`; `cwd` if metadata missing. Do not make a message. |
| `response_item` `message` | `role=user` counts only real user input (`input_text`, image input), not injected `<environment_context>`, `<user_instructions>`, `# AGENTS.md instructions for` context; `role=assistant` counts each logical assistant message, dedupe repeated `id` when present. Developer/system roles do not count. |
| `response_item` `function_call`/`custom_tool_call` | Increment `ToolCalls` once per `call_id` (track at resume boundary); results do not count. |
| `event_msg` `token_count` | Use `info.total_token_usage` as **latest cumulative** usage (not a sum of events); model maps input/output/reasoning/cacheRead and optionally cacheWrite. `user_message`/`agent_message` may be duplicate UI events, **never** count alongside response items. |
| `compacted` | No conversation count; note timestamp. Unknown items: `Diag.Unknown`, skip. |

`FirstPrompt` is first non-contextual user input (`OneLine` and ≤300 runes),
`Title` from index if present (M1-11) else first prompt or `"(untitled)"`;
`CreatedAt` from session meta, `UpdatedAt` latest valid line time or mtime;
`CWD` normalized, `RepoRoot` from `git.Resolve`. `AgentVersion` from
`cli_version`. `Archived` by containing directory unless index overrides.
`ParentID` from index edges (M1-11), with `session_meta.source`/`forked_from_id`
only when confirmed by fixture/source as subagent ancestry. Do **not** treat a
fork's origin as a subagent parent without evidence.

**Resume:** skip identical `(size,mtime)` files; restore checkpoint on append
and seek to `Offset`; rescan after shrink/mtime change without append. Keep
counts and last assistant/call IDs in checkpoint; if identifiers absent,
use line offsets for deduplication. `Removed` is previous rollout paths now
missing (not stale index rows). Sort changed/removed deterministically.
If a legacy envelope-less line is seen, only accept an explicitly recognized
historic shape (`response_item`-like object with top-level `role` and
`content`, or first-line metadata with `id`/`cwd`); otherwise warn and skip,
not a guessed `type` mapping. See Format corrections below: no upstream
proof that a general envelope-less format still exists.

**Tests:** fixture meta, injected-vs-human counts, event duplicates, tool
pairs, cumulative token events, partial last line, append resume equals full
scan, shrink, unknown item and cancellation. Counts must match the default
transcript view, not raw event count.

**Done when:** golden metadata/counts pass and offset resume is exact.

---

## M1-11 Codex `threads` fast path and ancestry (3 h)

`internal/provider/codex/index.go`. Open **only** the highest
`state_N.sqlite` via `sqliteread.Open`. The local 0.156.1 `threads` table has
`created_at_ms`, `updated_at_ms`, `model`, `first_user_message`, `preview`,
`cli_version`, `archived`, `rollout_path`, `cwd`, `title`, `tokens_used`,
`git_branch`; older schemas may omit the `_ms`/model columns. Probe columns
using `sqliteread.Columns` and select only supported columns. `created_at`
and `updated_at` are older **seconds**; prefer `_ms` when non-null. Do not
assume every locally observed optional column exists in every index.

```sql
SELECT id, rollout_path, cwd, title, first_user_message, model,
       tokens_used, archived, git_branch, cli_version,
       created_at_ms, updated_at_ms, created_at, updated_at
FROM threads ORDER BY id;

SELECT parent_thread_id, child_thread_id
FROM thread_spawn_edges ORDER BY child_thread_id;
```

Generate the SELECT using an **allowlist of column identifiers**; missing
optional columns become `NULL AS column_name`. Empty/missing/locked/corrupt
index: fall back to discovered rollout scan and a warning, not an error that
hides valid files. If the index has rows, use it as the fast metadata source:
`id`, title, first user preview, CWD, model, version, timestamps, archived
(`archived != 0`), branch, path. Do not add `tokens_used` into `TokenUsage`:
it is one total, while M0 has directional token fields; get the latter from
rollout token events. **Merge** file-scan `Counts`, detailed tokens,
`RepoRoot` and missing fields into each index row; new or unindexed rollout
files remain visible. The index cannot supply counts, so first scans still
stream rollout JSONL; later unchanged files reuse their M0 checkpoints while
index metadata updates can still emit `Changed`.

`thread_spawn_edges` is authoritative for parent→child; set
`ParentID=parent_thread_id` on a matching child ID, ignore dangling edges.
If a single ID has both active and archived paths, prefer an existing path
matching the index row, otherwise the latest valid rollout; never duplicate
an ID. Delete/archival path changes are updates, not duplicate sessions.
Filter bad `rollout_path` (must resolve inside Codex root or be a known
explicit source); do not open arbitrary absolute paths supplied by a DB row.
All-ID reconciliation uses the discovered files and valid index rows; an
index row pointing to a vanished file becomes a warning and eventual removal.

**Tests:** populated temp DB from `state.sql`, zero-row DB, missing optional
columns, seconds-vs-ms time fallback, corrupt index, dangling edges, archived
file and duplicate ID, changed index title on unchanged rollout. Assert no
credential DB access.

**Done when:** populated index accelerates metadata, and empty/no index uses
rollouts with the same ID set and count breakdown.

---

## M1-12 Codex `Load` from rollout (4 h)

`internal/provider/codex/load.go`, `blob.go`. Validate agent/ID and resolve
only a path found by discovery/index (no `ref.ID` path interpolation). Iterate
`jsonl.Reader` from offset 0; use `json.RawMessage` for nested content/input.

```go
func (p *Provider) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error)
func (p *Provider) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error)
```

| Item | Unified mapping |
|---|---|
| `response_item` `message` | Role user/assistant/system (developer→meta system); `content[]` input/output text→text part, image/file→lazy `FileRef`. Use item id or stable line offset for `Message.ID`, line timestamp for time. Mark known injected context meta; do not duplicate matching `event_msg` messages. |
| `response_item` `reasoning` | `summary[].text`, content (if present)→reasoning part on current assistant turn, or stand-alone assistant message if none. Encrypted reasoning has no readable text; a notice may say unavailable, **never** dump `encrypted_content`. |
| `function_call`/`custom_tool_call` | Tool part on current assistant message with `call_id`, name, arguments as raw `json.RawMessage`, status pending; `function_call_output`/`custom_tool_call_output` with matching `call_id` fills output/status. Unmatched output becomes a notice, not an extra conversation message. |
| `local_shell_call` and other known tool requests | Best-effort tool part with stable call ID and status; unknown response variants→diagnostic. |
| `compacted` | System compaction marker; `message` is bounded summary; do not expand `replacement_history` as new conversation messages. |
| `event_msg` | Only UI-only notices with no response-item counterpart (e.g. turn aborted) and latest token usage; `user_message`/`agent_message` events cannot be rendered again if the response item is present. |

Pair tool calls/results across intervening records via `map[callID]*ToolCall`,
mark still pending when live and `unknown` otherwise. Truncate outputs at
64 KiB and use `OutputRef="rec:<lineStart>:<callID>"`; `Blob` seeks that
line, validates the item type and call ID, then returns the full output.
Images use `FileRef.Ref="rec:<lineStart>:<content-index>"` and lazy decode;
no base64, data URI, encrypted reasoning or full tool payload is placed in
`Transcript`. For a child thread linked by M1-11, attach
`ToolCall.Child` only if a matching spawn call ID is actually available;
`ParentID` alone is insufficient to infer which tool call spawned it.
Recover from malformed **complete** lines with `Diag.ParseErrors`; a partial
trailing line is best-effort for `Load`, never a committed scan checkpoint.
Return a descriptive not-found error if source vanished after scan.

**Tests:** golden response messages/reasoning/tool pair/compaction, duplicates
between event and response items, unknown type, large output `Blob`, injected
context, partial last line, and 10 MB line. `Load.Meta` equals the scan meta
for a static fixture (aside from live state).

**Done when:** a sanitized real Codex rollout renders correctly and lazy
outputs are recoverable without duplicate messages.

---

## M1-13 Concurrent scan orchestrator and in-memory catalog (3 h)

`internal/scan/scan.go`, `catalog.go`. The catalog is in-memory only; M2-01
persists its data. Construct it explicitly; the M1-16 app service queries it.

```go
type Catalog struct { /* mu sync.RWMutex; byKey map[string]model.SessionMeta */ }
func NewCatalog() *Catalog
func (c *Catalog) Apply(result provider.ScanResult)
func (c *Catalog) All() []model.SessionMeta
func (c *Catalog) Get(ref model.SessionRef) (model.SessionMeta, bool)
func (c *Catalog) Children(ref model.SessionRef) []model.SessionMeta

type Runner struct { Providers provider.Set; Workers int; Catalog *Catalog }
type Report struct {
    States map[model.AgentID]provider.ScanState
    Diag   map[model.AgentID]provider.Diagnostics
    Errors map[model.AgentID]error
}
func (r *Runner) Run(ctx context.Context, prev map[model.AgentID]provider.ScanState) Report
```

1. Call `Detect` for each provider under `ctx` and enqueue detected ones into
   a `min(Workers, len(Providers))` worker pool (default e.g. 3). Each worker
   calls `Scan(ctx, prev[p.ID()])`. A missing provider does not cause global
   failure; if it was previously present, retain its state/catalog until a
   successful authoritative scan confirms removals. Each provider uses its
   own DB/files and handles its own internal concurrency.
2. **One owner goroutine** collects results, calls `Catalog.Apply`, stores
   replacement state under that provider ID and merges provider `Diag` into
   the report. On provider failure, retain the previous state and metadata,
   record the error/warning; never apply a partial result or convert an error
   into mass `Removed`. Only the caller persists `Report.States` in M2.
3. `Apply` deletes `Removed` first and upserts `Changed` by
   `SessionRef.Key()`. If a ref appears in both, changed wins. Return **copies**
   from `All`/`Get`/`Children`, sorted by UpdatedAt descending then `Ref.Key()`;
   `Children` requires same agent and `ParentID==ref.ID` (no cross-provider
   linkage). Protect map from concurrent UI reads; leave heavy grouping
   outside the lock.
4. If a provider implements M0 `LiveDetector`, call `Live(ctx)` after a
   successful scan and overlay `Live`/`LiveStatus` by session ID on its
   catalog entries, **including unchanged** sessions. Do not mark absent IDs
   live or erase a known live badge on a transient Live error; report that
   error. M2-10 supplies periodic refresh; M1 uses scan-time info only.
5. Cancellation stops enqueueing, passes `ctx` into work, waits for workers,
   and returns `ctx.Err()` per interrupted provider. A slow provider does not
   serialize faster providers; deterministic catalog/report ordering keeps
   UI and tests stable. Reject duplicate `Provider.ID` in construction or
   report an error rather than racing the same state key.

**Tests:** `provider/providertest.Fake` with slow, fast, failing and live
providers; max active workers bounded; cancellation; two scans with Changed
and Removed; failure retains old catalog; unchanged live update; concurrent
`All`/`Apply` with `-race`.

**Done when:** a slow provider does not block fast results; cancellation and
incremental state merging are race-free.

---

## M1-14 Pure grouping engine (3 h)

`internal/group/group.go`, `group_test.go`. Takes a snapshot from
`Catalog.All()`; no filesystem, provider or Wails imports. `CWDMissing` and
`RepoRoot` are already supplied by providers/M0 path utilities.

```go
type GroupMode string
const (
    DirAgent GroupMode = "dir-agent" // default: directory → agent → sessions
    AgentDir GroupMode = "agent-dir" // agent → directory → sessions
    Flat     GroupMode = "flat"
)
type Kind string // "directory" | "agent" | "session"
type Node struct {
    Key          string             `json:"key"`
    Label        string             `json:"label"`
    Kind         Kind               `json:"kind"`
    Path         string             `json:"path,omitempty"`
    Agent        model.AgentID      `json:"agent,omitempty"`
    Count        int                `json:"count"`
    MessageTotal int                `json:"messageTotal"`
    Children     []Node             `json:"children,omitempty"`
    SessionRefs  []model.SessionRef `json:"sessionRefs,omitempty"`
    Missing      bool               `json:"missing,omitempty"`
}
type Options struct {
    Mode     GroupMode
    Home     string // for display only; pass an absolute normalized home
    FoldRepo bool   // choose RepoRoot if available; richer worktree UI is M3-04
}
func Build(sessions []model.SessionMeta, opts Options) []Node
```

1. Index metas by `Ref.Key()`. Link a child **only if** its parent exists in
   the same agent and the edge is not self-referential/cyclic. Unresolved or
   cyclic children become top-level sessions (never disappear). Preserve the
   parent's group even when a child's CWD differs; leaf children remain
   navigable under the parent. Each session leaf has its singleton
   `SessionRefs`; group nodes can expose descendant refs in display order for
   `ListSessions(groupKey)` (M1-16), deduplicated.
2. Choose group path `CWD` by default; if `FoldRepo && RepoRoot!=""`, choose
   `RepoRoot` (basic API behavior now; cross-worktree resolution and UI toggle
   are completed in M3-04). Empty paths group under a stable `"(unknown
   directory)"` key. Key namespaces: `dir:<normalized-absolute-path>`,
   `agent:<id>`, `dir-agent:<path>:<agent>` (encode lengths or escape separators
   for collision safety), session leaf `session:<Ref.Key()>`. Keys do not use
   shortened labels or ephemeral list position.
3. In `DirAgent`, roots are directories, their children agents, then parent
   session leaves; `AgentDir` swaps the first two levels. `Flat` has only
   top-level session leaves, with nested children underneath. `Children`
   is always the actual visible tree, not a duplicated flat list.
4. `Count` on a directory/agent group is the number of **top-level** session
   leaves in that subtree. On a top-level session leaf it is 1; nested child
   leaves have Count 0 (do not inflate the top-level total).
   `MessageTotal` on a session leaf is its own `Counts.Total()` **plus every
   nested child's** total; on groups it is the sum of root leaves' totals.
   Do not double count child refs if listing a group. `Missing` on a directory
   is based on path missing from its metas (or `CWDMissing` of all sessions);
   on a leaf use its own `CWDMissing`; agent groups are missing only if all
   descendant directories are missing.
5. Label a path equal to Home as `~`, a path under Home as
   `~/<relative/path>`; do not replace `/home/user2` given Home `/home/user`.
   Use `filepath.Rel`/separator boundary, not `strings.Replace`; never
   shorten the stable `Path`/`Key`. Agent labels come from known IDs with
   a human-readable fallback for future providers.
6. Sort session siblings by `UpdatedAt` descending, then `Ref.Key()`; group
   siblings by case-folded display label, tie by stable Key; flat roots by
   recency. Avoid mutating the input slice, and return distinct tree copies.
   Invalid GroupMode falls back to `DirAgent` (or reject at the app boundary).

**Tests:** table for all three modes, parent+two children with differing
CWDs, orphan/cycle, missing existing mix, equal timestamps, zero messages,
Home prefix boundary, empty CWD, repo-fold option and stable keys. Assert
input unchanged and root totals exclude child count but include child
messages exactly once.

**Done when:** all three group trees, navigation refs, totals and missing/`~`
labels match golden/table tests.

---

## Format corrections

These are **notes for a later update to `docs/formats.md`**, not edits to that
file in this task. The public Codex source is authoritative where available;
Codex sample-specific details remain unverified until M1-08.

1. Codex's current `RolloutItem` has more than the five variants listed in
   `formats.md`: `inter_agent_communication`,
   `inter_agent_communication_metadata`, `token_usage_record`, `world_state`,
   `retained_context`, `security_risk_score`, `realtime_item` also exist.
   Continue skipping unknown variants safely. `RolloutLine` has an optional
   `ordinal` and deliberately omits `Deserialize` (upstream requires a
   canonical parser to retain nested decimal precision). Sources:
   [codex-rs/history/src/lib.rs](https://github.com/openai/codex/blob/main/codex-rs/history/src/lib.rs),
   [rollout payload wire types](https://github.com/openai/codex/blob/main/codex-rs/history/src/rollout_payload.rs),
   [canonical parser](https://github.com/openai/codex/blob/main/codex-rs/rollout/src/lib.rs).
2. `compacted.payload` may include `replacement_history` and further resume
   metadata; it is **not just** `{message}`. Do not count the replacement
   history as appended user/assistant turns. Sources:
   [history types](https://github.com/openai/codex/blob/main/codex-rs/history/src/lib.rs),
   [rollout payload](https://github.com/openai/codex/blob/main/codex-rs/history/src/rollout_payload.rs).
3. The locally observed Codex `threads` schema also has older
   `created_at`/`updated_at` integer columns, `source`, `originator`,
   `has_user_event`, `reasoning_effort`, and more; the `_ms` timestamp columns
   are nullable. Probe columns instead of relying on the partial list in
   `formats.md`. Current local DB is **empty**, so no actual Codex session
   timestamps have been verified here. Reference:
   [Codex repository](https://github.com/openai/codex).
4. The statement that envelope-less 2025 rollouts exist is **unconfirmed** by
   the inspected current source; mark it unverified pending a fixture rather
   than asserting a generic historic parser. Current wire format uses a
   tagged `type` plus `payload`. Source:
   [rollout wire types](https://github.com/openai/codex/blob/main/codex-rs/history/src/rollout_payload.rs).
5. OpenCode v1 `message` rows with `role='user'` can contain synthetic text or
   only a compaction part, so counting every role=user row overstates default
   human messages (10/45 populated local sessions differ). OpenCode v2 keeps
   `synthetic` and `compaction` as separate row types. Also `project_id='global'`
   has `worktree='/'`, not a repo. These were observed via read-only queries
   on the local installation; no external URL is needed for local evidence.

## Proposed contract changes

**M3-05 multi-root support only; do not change M0 contracts for M1.** M1
constructs one provider per agent and root, stores `ScanState` per agent, and
deduplicates within that root. To support configured extra roots without
merging two distinct sessions that happen to have the same ID, first amend the
authoritative M0 contracts when M3-05 is implemented:

```go
// model.SessionRef gains an optional discriminator.
Root string `json:"root,omitempty"` // canonical absolute provider storage root

// provider.Provider gains this method, implemented by existing constructors.
Root() string

// provider.Set gains lookup for a specific provider instance.
func (s Set) Resolve(ref model.SessionRef) (Provider, bool)
```

`SessionRef.Key()` preserves the exact `agent:id` when `Root==""` for old
JSON/keys; nonempty roots use an unambiguous separate namespace, e.g.
`"root:"+string(Agent)+":"+strconv.Itoa(len(Root))+":"+Root+":"+ID`.
`Set.Get(agent)` remains the single-root lookup and returns false when
ambiguous; `Set.Resolve` matches agent plus canonicalized root. M3-05 stamps
a nonempty Root on refs from configured extra roots, while an empty Root
continues to denote the default root. The orchestrator's state map then uses
`(Agent,Root)` keys, and grouping links only same-root parents. M2's cache
needs a migration to include Root in the session key and per-root scan state;
`SourcePath` alone is not a complete identity. Until M3 all Root values are
empty; no M1 task relies on this proposal. No other contract changes are
required: M0's Checkpoint/Cursor cover incremental scans, and FileRef/Blob
cover lazy attachments.
