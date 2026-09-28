# Session storage formats (verified 2026-09-27)

Reference for provider implementers. Everything here was verified on a real
machine (Ubuntu 26.04, Claude Code 2.1.280, OpenCode 2.0.16) unless marked
**unverified**. Formats drift, so parsers must skip unknown record types and
fields.

All timestamps are converted to `time.Time` (UTC) in the unified model.

---

## Claude Code

**Root:** `$CLAUDE_CONFIG_DIR` or `~/.claude`

```
projects/<encoded-cwd>/<session-uuid>.jsonl          main transcript
projects/<encoded-cwd>/<session-uuid>/subagents/
    agent-<agentId>.jsonl                            subagent transcript
    agent-<agentId>.meta.json                        {"agentType","description","toolUseId",...}
projects/<encoded-cwd>/<session-uuid>/tool-results/<id>.txt   large tool outputs
sessions/<pid>.json                                  one per running process
```

- `<encoded-cwd>` is lossy (`/` and `.` → `-`). **Never decode it.** Use the `cwd` field in records.
- Credentials live in `.credentials.json`. **Never read it.**

### Records (one JSON object per line)

Common fields: `type`, `uuid`, `parentUuid`, `timestamp` (RFC3339 string), `sessionId`, `cwd`, `gitBranch`, `version`, `isSidechain`, `isMeta`.

| `type` | Meaning / fields used |
|---|---|
| `user` | `message.content` is either a **string** or an array of blocks: `text`, `tool_result {tool_use_id, content (string or blocks), is_error?}`, `image`. `toolUseResult` holds structured tool output (shape varies per tool). |
| `assistant` | `message.{id, model, content[], usage}`. Blocks: `text`, `thinking {thinking}`, `tool_use {id, name, input}`. **One API message is split across several records, one block each, all sharing `message.id`.** On the verified data (228 files, 29,884 records), records sharing an id were always consecutive among assistant records. Other record types may appear between them, but an id never reappears after a different id. |
| `attachment` | Injected context (environment etc.). Treat as meta and hide by default. |
| `system` | `subtype: "compact_boundary"`, `compactMetadata {trigger, preTokens, postTokens}` → compaction marker. Other subtypes → notice. |
| `ai-title` | `aiTitle`. May repeat; **last one wins**. |
| `summary` | `summary` (older versions; none present locally). Title fallback. |
| `last-prompt`, `mode`, `cost-state`, `queue-operation`, `atis-latch` | Bookkeeping. Ignore (`cost-state` may be used later for cost). |

**`usage`:** `input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`, `output_tokens_details.thinking_tokens`. Sum per unique `message.id`, not per record.

### Message counting rule (SPEC §6)

- **user:** a `type=user` record that is not `isMeta` and has at least one `text`/`image` block (or string content). Records containing only `tool_result` blocks are not counted.
- **assistant:** the number of distinct `message.id` values. Because records sharing an id are consecutive, counting works by comparing each id with the previous one.
- **toolCalls:** the number of `tool_use` blocks.

### Subagents

- Subagent transcripts have `isSidechain: true`, the parent's `sessionId`, and an `agentId`.
- `meta.json.toolUseId` links the subagent to the parent's `tool_use` block, which is where the transcript renders the "subagent → open" link.
- Child session id: `<parentSessionId>/agent-<agentId>`.

### Live sessions: `sessions/<pid>.json`

```json
{"pid":1284494,"sessionId":"…","cwd":"…","startedAt":1790514010247,
 "procStart":"27032322","kind":"interactive","status":"idle","updatedAt":1790516113562, …}
```

- **Live** means the pid exists **and** `procStart` equals field 22 (starttime) of `/proc/<pid>/stat`. This guards against a reused pid.
- `status` (`idle`/`busy`) can be shown as a badge.

---

## OpenCode

**Root:** `$XDG_DATA_HOME/opencode` or `~/.local/share/opencode`

- `opencode.db` is SQLite in WAL mode (248 MB locally).
- `storage/` holds legacy JSON.
- `auth.json` and the `credential`/`account`/`control_account` tables hold credentials. **Never read them.**
- All `time_*` columns and JSON `time.*` values are Unix **milliseconds**.

### Generation 2 (v2.x): `session_v2` + `session_message`

`session_v2` columns used:

| Group | Columns |
|---|---|
| Identity | `id`, `project_id`, `parent_id`, `fork_session_id` |
| Location | `directory` |
| Display | `title` (nullable), `version` |
| Metadata | `agent`, `model` (JSON `{"id","providerID","variant"}`) |
| Usage | `cost`, `tokens_input`, `tokens_output`, `tokens_reasoning`, `tokens_cache_read`, `tokens_cache_write` |
| Change summary | `summary_additions`, `summary_deletions`, `summary_files` |
| Timestamps | `time_created`, `time_updated`, `time_archived` |

`session_message(id, session_id, type, seq, time_created, data JSON)`. Order by `seq`.

| `type` | `data` |
|---|---|
| `user` | `{text, time, files?, agents?}` |
| `assistant` | `{agent, model, content[], cost, tokens, time, finish, error?}`. `content[]` items: `text {text}`, `reasoning {text, time}`, `tool {id, name, state:{status, input, content[] (text blocks), metadata:{output, exit, truncated}}, time}`. **Each tool block already contains its own result.** |
| `compaction` | `{status, reason, summary}` → compaction marker (summary collapsible) |
| `synthetic` | `{text, time}`: auto-injected user text. Treat as meta. |
| `system` | `{text, time}`: environment injection. Treat as meta. |
| `idle`, `location-switched` | Bookkeeping. Ignore. |

- **Counts:** user = rows with `type='user'` **whose `text` is not synthetic filler** (in v1, some `role='user'` rows carry only compaction or synthetic text; 16 of 45 populated local sessions were affected); assistant = rows with `type='assistant'` (v1: `role='assistant'`); toolCalls = number of `tool` items in assistant content.
- Counts are computed with SQL (`json_each`) or in Go during scan.

### Generation 1 (v1.x): `session` + `message` + `part`

- `session`: same core columns as v2, but `title` is NOT NULL.
- `message.data`: `{role, time, agent, modelID, providerID, cost, tokens, parentID, finish, error?}`.
- `part.data.type`:
  - `text {text}`
  - `reasoning {text}`
  - `tool {tool, callID, state:{status, input, output, metadata, title, time}}`
  - `patch {hash, files[]}`
  - `file {mime, filename, url}` (`url` may be a huge `data:` URI; load lazily)
  - `compaction`
  - `step-start` / `step-finish` (ignore)
- **Locally, all 60 v1 sessions also exist in v2** (same ids). Prefer v2.

### Legacy JSON (pre-DB): **unverified locally**

- `storage/session/<projectID>/<sessionID>.json`
- `storage/message/<sessionID>/<messageID>.json`
- `storage/part/<messageID>/<partID>.json`

Same JSON shapes as the v1 `data` columns plus ids. `storage/session_diff/` and `storage/migration` are not sessions.

### Projects

- `project(id, worktree, vcs, name)`: `worktree` is the repo root. `id='global'` has worktree `/`, which means no repo.
- `worktree(project_id, directory)` lists extra worktree dirs of a project.

---

## Codex CLI: **unverified locally (no sessions on this machine)**

**Root:** `$CODEX_HOME` or `~/.codex`

- `sessions/YYYY/MM/DD/rollout-<YYYY-MM-DDThh-mm-ss>-<uuid>.jsonl`
- `archived_sessions/rollout-*.jsonl`
- `state_<N>.sqlite`: pick the highest N; `state_5` exists locally.
- `auth.json` holds credentials. **Never read it.**

### Rollout lines (expected, per public Codex source)

Each line is `{"timestamp": "...", "type": "...", "payload": {...}}`:

| `type` | `payload` |
|---|---|
| `session_meta` | `{id, timestamp, cwd, originator, cli_version, instructions?, git?:{commit_hash, branch, repository_url}}` (first line). Flattens `SessionMeta` + optional `GitInfo`; may carry `parent_thread_id`, `forked_from_id`, `source` (a `SessionSource` enum; `SubAgent(ThreadSpawn{parent_thread_id, depth, …})` marks subagent ancestry), `history_mode`. |
| `turn_context` | `{cwd, model, approval_policy, sandbox_policy, effort?}` |
| `response_item` | `{type:"message", role:"user"\|"assistant"\|"developer", content:[{type:"input_text"\|"output_text", text}]}`, `{type:"reasoning", summary:[{type:"summary_text", text}], encrypted_content?}`, `{type:"function_call", name, arguments (JSON string), call_id}`, `{type:"function_call_output", call_id, output}`, `{type:"custom_tool_call"…}`, `{type:"local_shell_call"…}` |
| `event_msg` | `{type:"token_count", info:{total_token_usage:{input_tokens, cached_input_tokens, output_tokens, reasoning_output_tokens, total_tokens}}}`, `{type:"user_message", message}`, `{type:"agent_message", message}`, … |
| `compacted` | Compaction marker; payload may also include `replacement_history`, `guardian_history`, `resume_metadata`. **Do not render `replacement_history` as new conversation messages.** |

- **User messages whose text starts with `<environment_context>` or `<user_instructions>` are injected context.** Treat them as meta and do not count them.
- **Lines:** the envelope may also carry an optional `ordinal: n`. The Rust canonical parser deliberately avoids plain `Deserialize` to keep nested decimal precision — keep `json.RawMessage` for `arguments`/`input` in Go and never round-trip them through `float64`.
- **Rollout item types are broader than the five above** (`inter_agent_communication`, `token_usage_record`, `world_state`, `retained_context`, `security_risk_score`, `realtime_item`, …). Skip unknown ones via diagnostics.
- Older rollouts (2025) may lack the `{type,payload}` envelope — **unconfirmed in current source**; only accept an explicitly recognized historic shape, otherwise warn and skip. **Confirm with real fixtures (task M1-08).**
- `codex migrate-rollouts` (verified on CLI 0.156.1) indicates newer "paginated thread history" storage in `state_N.sqlite`; rollouts stay JSONL, but probe schema rather than assuming a fixed layout.

### `state_5.sqlite` → `threads` (verified schema, 0 rows locally)

Columns (probe at runtime; presence varies by version): `id`, `rollout_path`, `created_at` (seconds) / `created_at_ms`, `updated_at` (seconds) / `updated_at_ms` (nullable), `cwd`, `title`, `first_user_message`, `preview`, `model`, `model_provider`, `tokens_used`, `archived`, `archived_at`, `git_branch`, `git_sha`, `git_origin_url`, `cli_version`, `agent_nickname`, `agent_role`, `name`, `is_pinned`, `source`, `originator`, `has_user_event`, `reasoning_effort`. Prefer `_ms` when non-null.

`thread_spawn_edges(parent_thread_id, child_thread_id, status)` gives parent → child links.
