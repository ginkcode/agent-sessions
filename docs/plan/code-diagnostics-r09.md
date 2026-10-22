# R09 — contained saved-disk probe (2026-10-22)

**Complete: contract + read-only probe only.** Ownership audit reviewed before
implementation; R03's transport-lifetime URI ambiguity policy still controls.
Progress remains **19/28**; CD-25 remains partial. R10 integration is not implemented,
production `liveCodeDiagnostics` remains false/unadvertised, and both release HOLDs
remain unchanged.

## Decision and implementation

Existing list/read APIs provide neither authoritative absence nor external mutation
identity. Git status, failed reads, equal revisions and path similarity cannot
prove rename. Add optional `probeFiles`, rather than reinterpret existing errors or
create a recursive watcher. The [authoritative v1 additive contract](code-diagnostics-contract.md#r09-additive-saved-disk-probe-2026-10-22)
now specifies `present` + raw-byte content revision, leaf-confirmed `absent`, or
sanitized `unknown`. Content identity is explicitly **not file-object identity**:
identical-content recreation between observations cannot be detected. Absence is an
observation, not a destructive action or rename event.

- `internal/files/probe.go`: 32 explicit canonical paths, 4096 bytes/path, 5 MiB/file,
  8 MiB total read budget; no enumeration, parent widening, bodies in response,
  process starts or filesystem writes. Rooted handles, symlink rejection, transient
  file/directory identity rechecks, canonical root/alias recheck and cancellation.
  Missing ancestors, inaccessible/nonregular files and read failures stay unknown.
- Service authorizes exact catalog directory first; Engine, Backend, RPC client/
  dispatcher and App expose the probe. App guards captured host/backend/connection
  generation before accepting replies. Offline hosts cannot fall back to Local.
- Frontend adds types, optional native binding and explicit unsupported mock/old-
  binding fallback only. Demo reads synthesize data and never fabricate absence.
  No files-store consumer/lifecycle code changed: dirty/uncertain tab retention and
  `unwatched` coverage continue through existing reconciliation tests.
- No inode or body persistence, logging or telemetry; no diagnostic contract
  required-field change, version bump or live capability change.

## Evidence

- `internal/files/probe_test.go`: present → absent → recreated revision, rename
  represented as independent observations, identical-byte recreation limitations,
  missing ancestors, dangling/escaping links, directories, unreadable mode-000
  files (conditional assertion if the OS permits privileged reads), cancellation,
  invalid/duplicate/oversized batches and file read limit.
- `internal/engine/probe_files_test.go`: catalog-first authorization even for invalid
  requests; parent/subdirectory/unrelated roots rejected.
- `internal/rpc/probe_files_test.go`: actual framed RPC/Engine/files route for
  delete/recreate, content revisions, escape/catalog rejection; live flag false.
- `internal/app/probe_files_test.go`: fake backend proves wrong-host requests do
  not dispatch and same-host connection-generation changes discard absent replies.
- `frontend/test/file-probe-api.test.mjs`: fake binding forwards root/paths/host,
  preserves delete/recreate/inaccessible observations and old-binding/mock unknown.
  Full frontend regression includes existing dirty/uncertain draft, late-host read,
  overlapping refresh/reload and unwatched coverage tests.

## Validation

- `go test ./...` — pass.
- `go test -race ./...` — pass.
- `go tool golangci-lint run --new-from-rev=HEAD ./internal/files/... ./internal/engine/... ./internal/app/... ./internal/rpc/...`
  — 0 changed-line issues (run before committing R09).
- Broader package lint was also attempted: existing unrelated baseline violations
  remain; these were not modified. Initial new test errorlint finding corrected.
- `npm test` — 447/447 pass.
- `npm run check` — 0 errors; existing `ignoreDeprecations: "6.0"` warning.
- `npm run build` — pass; existing chunk-size warning.
- `git diff --check` — pass.

## Limits / R10 handoff

This is not an atomic filesystem snapshot or durable external mutation identity.
Checks detect observed swaps, not adversarial swap-away-and-back/in-place writes
preserving metadata. No deterministic filesystem swap schedule or cross-platform
native evidence is claimed here. R10 must discard superseded request/tab/attachment
replies, preserve drafts/undo/CAS uncertainty, invalidate deleted-path checker/live
sets without closing dirty tabs, and obey R03 on reappearance. Missing parents are
intentionally conservative unknown, not a recursive deletion claim. No probe result
permits save/delete/rename on disk. No watches or continuous-clean coverage are
claimed. Real-server/native/remote/manual draft QA remains outstanding.
