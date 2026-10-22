# Code diagnostics contract v1 (CD-01)

Status: authoritative protocol specification for CD-02 onward. This specifies
future behavior; it does not advertise an implemented capability. Change this
reference and its fixtures before changing wire behavior. Additive optional
fields are compatible; changes to required fields or semantics require a new
`contractVersion`. Existing `provider.Diagnostics` / `Backend.Diagnostics()` are
unrelated and unchanged.

Parent: [implementation plan](code-diagnostics.md). Task briefs:
[CD-01](../tasks/CD-01.md), [task index](../tasks/README.md).
Fixtures and validation: `internal/codediagnostics/testdata/contract/` and
`internal/codediagnostics/contract_test.go`. The fixtures use synthetic identities
and source only, never real project contents or secrets.

## R09 additive saved-disk probe (2026-10-22)

Contract version remains **1**: no existing diagnostics field or required method
changes meaning. Optional read-only `probeFiles` is an independent files RPC;
`Backend.ProbeFiles(ctx, dir, paths)` / App `ProbeFiles(dir, paths, host)` returns
`{root, observations:[{path,state,revision?,reason?}]}`. Unsupported/missing methods
or transport failures are **unknown**, never deletion. The frontend binding is
optional; old bindings and the synthetic demo mock return `unknown/unsupported`.
The RPC client may return method-not-found from an old host; consumers must retain
uncertainty rather than reinterpret the error. No live capability is enabled.

- Authorize the exact catalog directory **before** filesystem access. Canonicalize
  that root only, never search/widen to a parent. Reject the entire batch for empty,
  duplicate, noncanonical/absolute/escaping/backslash/NUL paths, >32 paths, or
  >4096 bytes per path. Paths are slash-separated, relative regular-file names.
- Open through `os.Root`; reject symlink leaves and ancestors (including contained
  and dangling links). Missing root/ancestor, nonregular targets, permission or
  transient errors are `unknown/unavailable`, not missing. Only ENOENT at the leaf
  below verified existing directories yields `absent`. Recheck directory/root
  identity and catalog alias; observed swaps invalidate observations. Inodes are
  used only transiently for these checks, never exposed, persisted or telemetered.
- `present` requires a bounded stable raw-byte read, regular-file identity/size/
  mtime checks around it and a `sha256:` content revision identical to file-read
  revision semantics. Limit 5 MiB/file and 8 MiB/batch (+one sentinel byte);
  remaining over-budget observations are `unknown/limit`. Cancellation is
  `unknown/cancelled`; root/alias replacement is `unknown/changed`. No OS error
  strings, source bodies or excerpts appear in responses/logs/persistence.
- `absent` confirms **path absence at observation**, not a historical delete event,
  ongoing absence, rename destination, or permission to discard a draft. An old
  absent path plus a new present path (even equal revisions) is **not rename**.
  Identical-content delete/recreate between observations is indistinguishable from
  uninterrupted presence. Revisions identify bytes, not file objects, LSP opens,
  sessions or freshness; no external mutation ID is invented.
- This is not an atomic batch or filesystem lock. External mutations after checks,
  in-place writes preserving metadata and swap-away-and-back races cannot be fully
  proven absent. Future consumers must guard request/connection/attachment/tab
  epochs and revalidate before acting; no destructive filesystem operation is
  authorized by this probe. App captures the host/backend/connection generation
  and rejects a superseded reply, including same-host reconnects.
- R09 supplies the contract/probe only. R10 must integrate reopen/refresh/pre-check,
  invalidate confirmed deleted-path results without deleting dirty/uncertain tabs,
  preserve drafts/undo/CAS state on unknown/errors, distinguish reappearance from
  proven rename, and retain R03 URI ambiguity rules. Existing reconciliation remains
  unchanged and adapters retain explicit `unwatched` coverage. No watcher or scan,
  save, process startup, lifecycle mutation or continuous-sync claim is added.

Executable synthetic wire evidence: `internal/rpc/probe_files_test.go`; bounded
backend/path tests: `internal/files/probe_test.go`; host epoch tests:
`internal/app/probe_files_test.go`; frontend fake API: `frontend/test/file-probe-api.test.mjs`.

## 1. Identity and scalar rules

All JSON keys use lower camel case. Required arrays serialize as `[]`, never
`null`. Optional fields are omitted, not `null`. Unknown additive fields are
ignored. All text is valid UTF-8; no HTML/Markdown rendering or actionable links.

| Field | Type | Meaning |
|---|---|---|
| contractVersion | integer, exactly 1 | Wire contract negotiated by attach |
| hostId | opaque string | Stable backend host identity, not a display label |
| connectionId | opaque string | Fresh for each connection/client ownership lifetime, including Local |
| root | absolute host-native string | Canonical authorized root resolved on the project host |
| attachmentId | opaque random string | Connection-owned project subscription token |
| generation | opaque random string | Fresh result epoch; replaced on revoke/restart/invalidation |
| runId | opaque random string | Latest checker invocation for one provider |
| sequence | safe integer | Attachment-wide monotonically increasing committed update number |
| documentId | opaque random string | Fresh for each document open lifecycle; prevents close/reopen version collisions |
| version | safe integer, starts at 1 | Monotonic version of actual displayed text within documentId |
| revision | opaque string | Saved-file content revision, not mtime; reuse files revision semantics |

Opaque IDs are nonempty, at most 256 UTF-8 bytes, and must be compared exactly.
Tokens do not replace authorization. Safe integers lie in `[0, 9007199254740991]`;
no floating values, NaN, uint64 overflow, or string-number coercion. On sequence
or version exhaustion, invalidate/reopen the lifecycle rather than wrap.

Project identity is `(hostId, root)`; live result ownership additionally requires
`connectionId`, `attachmentId`, and `generation`. A desktop routing epoch remains
an extra local guard, not a replacement for wire identity. Attachments are
connection-owned; shared cached work must not publish to another owner's token.
CD-02 defines the concrete stable host identifiers, canonicalization policy,
grant persistence location, and executable/configuration fingerprint. It must
not substitute a friendly host label or frontend path normalization.

## 2. Coordinates, paths, diagnostics

`Position = {line, character}`: safe nonnegative integers, zero-based line and
UTF-16 code-unit column. `Range = {start: Position, end: Position}` uses an
exclusive end, ordered start <= end; zero-width ranges are allowed. CRLF is one
line separator and neither CR nor LF contributes a column. Tabs count as one
UTF-16 unit, not visual columns; non-BMP characters count as two units. Adapters
must convert byte/code-point/one-based positions. LSP uses negotiated UTF-16 or
an explicit supported conversion, never an assumed encoding.

`path` is a nonempty slash-separated project-relative path, at most 4096 UTF-8
bytes: no absolute/drive/UNC prefix, backslash, empty component, `.`/`..`
component, colon (including drive prefixes), NUL or control characters. No case folding in the frontend. Host-side
resolution verifies canonical containment, including symlinks. URI decoding and
host/scheme checks happen before producing this path. Never navigate a raw tool
URI. Out-of-root findings become a bounded provider notice, not a fake in-root
file. The project root itself is not a diagnostic path.

A diagnostic has required `path`, `severity`, `message`, `source`, `freshness`,
and `basis`, and optional `range`, `code`, `locationIssue`:

- `severity`: `error | warning | information | hint`.
- `message`: nonempty plain text, maximum 8192 UTF-8 bytes.
- `source`: nonempty tool/rule source, maximum 256 bytes.
- `code`: optional nonempty string, maximum 256 bytes; numeric tool codes convert
  to decimal text. Never treat code as a URL or command.
- `freshness`: `current | stale | uncertain`. Current means only that the basis
  is verified; it does not assert workspace-wide coverage.
- `basis`: `{kind: "saved", revision?: string}` or
  `{kind: "document", documentId: string, version?: integer}`. A saved result
  without a verified revision and a document result without a verified version
  cannot be current. External checker reads can race writers: only mark current
  when adapter/supervisor evidence verifies the analyzed snapshot, otherwise
  use uncertain. A post-run content hash alone is insufficient proof.
- Omit `range` for file-level findings. Invalid tool ranges become a file-level
  finding with `locationIssue: "invalidRange"`; do not invent line 1. Validate
  against the actual basis text before using inline coordinates. A previously
  valid range that no longer maps stays panel-only and is invalidated/staled.

Ownership is carried by the surrounding result set, not repeated on each finding.
`ResultSet = {providerId, origin, scope, diagnostics, runId?}` where:

- `providerId` is a stable adapter/configuration identifier inside the project,
  maximum 256 bytes, not a human label or executable command.
- `origin`: `checker | lsp`.
- `scope`: `{kind: "project"}` for checker or
  `{kind: "document", path, documentId}` for LSP.
- Checker sets require `runId`; LSP sets omit it. LSP findings must match the
  document scope path/documentId. Closed document IDs cannot publish.

Exact display equivalence is `(path, range-or-absent, severity, message,
code-or-absent)`. Keep raw provider sets and attribution `(providerId, source)`;
removing one provider must not remove another's equivalent finding. For equivalent
entries, inline display uses only a basis eligible for the displayed text;
fresh/stale entries must not lend freshness to each other. Counts deduplicate
regardless of source and include error/warning by default; filters do not change
global counts. File-level findings count. Folder totals count each descendant
finding once using path components, independent of expansion/search/listing caps.

## 3. Provider state and coverage

`ProviderState` has required `providerId`, `availability`, `phase`, `coverage`,
`freshness`, `truncated`, optional `runId`, `notice`, `failure`.

- availability: `approvalRequired | available | missingTool | unsupported | disabled`.
- phase: `idle | starting | checking | failed | canceled | disconnected`.
- freshness: `current | stale | uncertain` (result-set summary, not inline proof).
- coverage: `{kind: "savedProject" | "openedDocuments", roots: string[],
  partial: boolean, externalChanges: "reconciled" | "unwatched" | "watching"}`.
  Roots are contained relative directories (`""` denotes the authorized root).
  Watching means only supported registrations, not complete filesystem awareness.
- truncated: boolean; when true, coverage.partial must be true.
- notice: optional plain text, maximum 2048 bytes; use for missing dependencies,
  out-of-root findings, unsupported watchers, or scope limits.
- failure: optional `{code, message}`, using bounded error codes/messages below.

Approval state is separate: `{state: "required" | "approved" | "revoked",
mode?: "session" | "persistent", fingerprint?: string}`. Only approved states
include mode/fingerprint; fingerprints are opaque, bounded like IDs. UI derives
checking/partial/stale summary from these independent dimensions, not one success
boolean. Zero findings means “No reported problems” with scope, never “all clean”.

## 4. API surface

Names below are final for v1; Go exports PascalCase equivalents and `api.ts`
uses the same lower camel case. App wrappers include `expectedHostId` on every
call to reject routing changes before dispatch; headless operations use the
connection's actual host identity. JSON-RPC method names match this table.
Capability flags are `codeDiagnostics` (Stage 1) and `liveCodeDiagnostics`
(Stage 2), both optional false when absent. Stage 1 reserves document shapes but
must not advertise live support. Unsupported calls fail before starting a process.

`Context = {connectionId, attachmentId, generation}` is required as `context`
on every attached request and response/update. Backend looks up the owned root;
requests cannot change it. No operation takes an arbitrary executable/argv.

| Method | Parameters (in addition to App expectedHostId) | Result |
|---|---|---|
| attachCodeDiagnostics | `{dir, contractVersion: 1}` | `{project: {hostId, root}, context, snapshot}` |
| detachCodeDiagnostics | `{context}` | `{context}` acknowledgement |
| getCodeDiagnosticsSnapshot | `{context}` | `Snapshot` |
| approveCodeDiagnostics | `{context, mode: "session" | "persistent", fingerprint}` | `Snapshot` |
| revokeCodeDiagnostics | `{context}` | New-generation `Snapshot` |
| runCodeDiagnostics | `{context, providerIds?: string[]}` | `{context, runs: [{providerId, runId}]}` |
| cancelCodeDiagnostics | `{context, runs: [{providerId, runId}]}` | `{context}` acknowledgement |
| openCodeDiagnosticDocument | `{context, path, documentId, version: 1, text, revision?}` | `{context, documentId, version}` |
| changeCodeDiagnosticDocument | `{context, documentId, version, text}` | `{context, documentId, version}` |
| saveCodeDiagnosticDocument | `{context, documentId, savedVersion, revision}` | `{context, documentId}` |
| closeCodeDiagnosticDocument | `{context, documentId}` | `{context, documentId}` |

Attach authenticates the known directory before canonical discovery; it performs
no executable version probes before approval. Initial snapshot includes bounded
metadata discovery and an approval proposal fingerprint when discovery completes:
`snapshot.proposal?: {fingerprint, tools: [{providerId, executable, configPaths}]}`.
Executable/config paths are display-only host metadata, never frontend commands.
If metadata is still pending the proposal is absent; approval cannot proceed.
Approval recomputes/compares the proposal, rejecting stale fingerprints.
Approval does not itself launch Stage 1 checks. Not now makes no approval call.

Run with omitted providerIds targets all available checker providers; an explicit
empty list is invalid. Retry is run with that provider ID, not a separate method.
Run acknowledgement follows committed checking status; result events may arrive
before the acknowledgement. Repeated runs coalesce with current work or create a
new run ID; only the latest provider run can commit. Cancel is idempotent for a
known completed/canceled run and cannot cancel a newer run. Unknown foreign run
IDs fail; attachments/tokens never authorize another caller's cancellation.
Detach/close are idempotent within their owning connection's lifecycle; retain
bounded tombstones or return staleContext without causing work. No mutation is
silently retried across reconnects, including approval.

Frontend sends full text with change; backend converts to full/incremental LSP
sync as negotiated. Versions strictly increase (not necessarily consecutive);
repeated version with identical text is idempotent, different text is conflict.
Save sends no second body. Flush change before didSave and preserve later typing;
if savedVersion is older than live version, the backend uses the known saved
snapshot only if retained, otherwise omits includeText or returns resyncRequired.
Do not claim the latest draft was saved. Failed/uncertain saves send no didSave.
Read-only text can open; oversized files stay viewable with live unavailable.

Errors use `{code, message}` internally and in provider failures. RPC maps these
to ResponseError with a stable string `data.code`; App/API normalize to the same
code. Codes: `unsupported`, `disconnected`, `unauthorized`, `approvalRequired`,
`approvalChanged`, `staleContext`, `invalidArgument`, `notFound`, `busy`,
`limitExceeded`, `resyncRequired`, `toolFailed`, `canceled`. Message max 2048 bytes,
plain sanitized text; no bodies/raw stderr. Tool findings/nonzero diagnostic exit
codes are not toolFailed. Attach wrong contractVersion is unsupported.

## 5. Snapshots, events, atomic replacement

`Snapshot = {contractVersion: 1, context, sequence, approval, providers,
resultSets, proposal?}`. Sequence starts at zero. Provider IDs and scope keys are
unique. Successful run atomically replaces that provider's project set, including
empty. Failed/canceled runs keep the prior set, marking findings/provider stale.
LSP publication replaces exactly its document set, including empty diagnostics.
Document close/delete removes its LSP set and stales/removes checker findings as
appropriate; deletion clears known deleted paths. Rename closes old URI and
invalidates old coordinates; reopening requires a fresh documentId.

Event name (RPC and Wails): `codeDiagnostics:updated`. Event payload:
`{contractVersion: 1, context, sequence, updateId, partIndex, partCount, snapshot}`.
Each update is a full replacement snapshot split into bounded parts. A part's
snapshot carries identical context/sequence/approval/provider/proposal metadata,
plus a disjoint slice of resultSets. Large sets may be split across parts with
the same provider/origin/scope/runId; concatenate diagnostics for that key, never
replace one fragment independently. Empty sets remain explicit. Findings appear
once per update. partIndex starts at zero; partCount >= 1. Ignore exact duplicate
parts; conflicting duplicates invalidate assembly and require resnapshot.

For get/attach responses the snapshot must fit the snapshot byte cap; no partial
response paging. Provider retention budgets below ensure this is possible. Event
chunks are a delivery optimization, not permission to retain unbounded results.

Subscribe before attach. Buffer early events by context until attach resolves,
then apply its snapshot and only higher-sequence updates in order. Drop older or
equal snapshots; never roll back newer events. Commit assembled updates only;
never expose half a checker run. An expected next sequence is last + 1; gaps,
assembly timeout (5 s), conflicting parts, or buffer overflow discard assembly,
mark resyncing, and fetch a snapshot. A resnapshot can jump sequence; queued events
<= snapshot.sequence are dropped. During resync retain labeled stale results.
Do not treat an incomplete update as a successful empty result.

Revoke invalidates generation before canceling work and returns/emits an empty
new-generation snapshot with revoked approval. Only a matching revoke response
or an event for the same owned attachment with a newer snapshot explicitly
carrying revoked approval may transition generation. Restart/invalidation instead
requires reattach (staleContext); unsolicited unrelated generations are ignored.
Disconnect clears live context and marks frontend retained results disconnected/
stale. Reconnect always attaches anew, even for the same path. No old event may
revive approval or results. Engine shutdown/detach invalidates ownership first.

## 6. Bounds and security responsibilities

Fixed v1 defaults (raise only with measured evidence and contract update):

| Resource | Limit |
|---|---|
| Retained findings | 10,000 per attachment across providers |
| Retained encoded Snapshot | 8 MiB using Go encoding/json escaping, including metadata |
| Encoded event envelope per part | 1 MiB including JSON-RPC envelope where used |
| Event parts / buffered updates | 32 parts per update / 2 updates per attachment |
| Providers / coverage roots / proposal tools | 32 / 128 per provider / 32 |
| Proposal config paths | 128 per tool, each <= 4096 UTF-8 bytes |
| Document text | 5 MiB UTF-8 before JSON escaping (viewer limit); editing retains its own lower limit |
| Checker captured stdout+stderr | 10 MiB combined; displayed stderr <= 2048 sanitized bytes |
| Checker timeout / parallelism | 120 s / 1 process per project, 2 per backend |

Reserve at least 1 MiB of the snapshot cap for metadata; apply a total 7 MiB
encoded finding budget, not merely a count cap. Retain a deterministic bounded
prefix ordered by provider/scope/path/range/message, mark impacted provider
truncated/partial, and report limit status. Overlong messages are UTF-8 safely
shortened with a notice and partial status; never silently treat discarded output
as success. Metadata exceeding its reservation makes a provider partial or
unavailable rather than emitting an invalid frame. Never split a finding across
parts. Buffers enforce both count and byte caps (16 MiB total assembled/buffered
snapshot data per attachment); overflow triggers resnapshot with bounded retry.

An 8 MiB escaped snapshot plus a small RPC response envelope stays below the
existing 64 MiB RPC cap. A 5 MiB text payload can expand to 30 MiB under Go JSON
escaping, also below that cap; reject oversized encoded requests independently.
Do not reuse newline app RPC framing for LSP Content-Length transport (CD-18).

Service: known-directory authorization, ownership checks, host-side root lookup.
Trust layer: current grants and fingerprint before every executable launch.
Supervisor: argv-only host-local execution, process-tree cleanup, time/output
limits, no source-fix flags or implicit tool downloads. Adapter: tool-version
parsing, contained discovery, path/coordinate conversion, honest coverage.
Manager: run/document/generation guards, bounded atomic retention and sequencing.
RPC/App: owner-scoped delivery, expected host guard, encoded frame bounds.
Frontend: epoch/context/sequence checks, safe text rendering, draft freshness and
navigation guards. Neither trust nor containment is a sandbox for tool plugins.
No protocol bodies, drafts, findings, or raw process output are logged/persisted;
explicit persistent grants contain identity/fingerprint only.

## 7. Race/assertion matrix

The fixture suite records schedules here; downstream tasks implement executable
state-machine tests at their own seams (CD-07/CD-09/CD-10/CD-16/CD-26).

| Schedule | Required assertion |
|---|---|
| Event 2 arrives before attach snapshot 1 | Buffer, attach, commit 2; snapshot 1 never overwrites 2 |
| Snapshot 2 resolves after event 3 | Ignore snapshot 2; keep 3 |
| Run A completes after retry B | A cannot replace B or clear B's findings |
| Cancel A races new B | Cancel affects A only; B remains active |
| Failure/cancel after successful findings | Prior set retained stale; no empty-success publication |
| Success with zero findings | Explicit empty provider set clears old findings |
| Two providers report equivalent finding, one clears | Count once; surviving source attribution remains |
| Reconnect same host/root | New connection/attachment; all old replies/events rejected |
| Revoke during queued/running work | Generation invalidated first; no late publication/launch |
| Update parts 1,0,1 arrive | Commit once after complete; exact duplicate harmless |
| Gap, missing part, conflicting duplicate or overflow | Keep stale display, bounded resnapshot, never half-commit |
| didChange v3 then publication v2 | No v2 draft underlines; v3 remains authoritative |
| Unversioned publication after typing | Uncertain panel-only result, never verified current range |
| Close/reopen same path with version 1 | New documentId rejects old lifecycle publications |
| Save v2 while user has typed v3 | didSave describes v2 only; v3 remains dirty/current live text |
| Disk changes during checker run | No current basis without snapshot proof; no draft save |
| Rename/delete or host switch during navigation | Invalidate old path/request; preserve drafts |

## 8. Explicitly deferred decisions

- CD-02 resolved: [trust addendum](code-diagnostics-trust.md) defines stable host/root
  policy, persistence/fingerprint schema, ownership, cancellation seam and approval
  copy. Authenticated transport identity acquisition/wiring remains CD-08/CD-09.
- CD-04 resolved the initial narrow format/version targets in the
  [checker spike](code-diagnostics-checkers.md): executable probes, direct-launch
  policy, exit interpretation and explicit snapshot/platform evidence limitations.
  Production adapters and native Windows/Linux verification remain downstream.
- CD-18/CD-19: LSP frame/request limits, safe handler details and restart caps
  (must fit these outer budgets; do not advertise unimplemented capabilities).
- CD-22…CD-24: actual supported LSP versions/wrappers, push publication/version
  behavior, Go/TS/Svelte document ownership and any validated freshness strategy.
- CD-25: bounded watcher implementation or explicit unwatched/stale fallback.

These owners may narrow tool support without changing v1. Any changed wire
semantics, limits, or operation names must update this reference and fixtures.
