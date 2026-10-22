# Backend result manager (CD-07)

Implemented headless ownership only in `internal/codediagnostics/manager*.go`.
No engine/App bindings, RPC methods/capabilities, UI, automatic execution, or live
language-server support are enabled.

## Integration seams and ownership

- Construct one `Manager` with a fresh authenticated connection ID and the same
  connection-owned `Trust`/shared `Supervisor`. Attach accepts `AuthorizedRoot`
  from catalog authorization, contract version 1 and backend-owned registrations.
  Attachment metadata discovery never launches a process. Duplicate project
  attachments in one owner return busy; detached projects attach with fresh IDs.
- `CheckerAdapter` is trusted Go code, not client input. `GoVetPlan` and
  `SveltePlan` implement `RunApproved`; no production registry is wired yet.
  Every command uses the **complete attachment tool proposal** for supervisor
  lease checks, avoiding per-adapter fingerprints invalidating each other's
  approval. Existing standalone `Run` methods retain their single-plan behavior.
  Adapters re-enumerate their own configuration and validate supervisor handles.
- Run validates ownership and approval, commits checking state, then dispatches
  bounded adapter work. Repeat calls coalesce; backend policy can supersede.
  Manager context/run identity and trust lease validation gate final publication
  atomically under the owner lock. No external emitter callbacks run there.
- Cancel removes the current run before cancellation. Latest completed cancellation
  is idempotent; obsolete IDs return staleContext, never target newer work. There
  is no unbounded run history. Delete cancels all attachment work before clearing
  that path, avoiding a growing deleted-path tombstone ledger.
- Revoke clears queued old-generation events, changes generation and clears sets
  before trust cancellation/durable removal. Even durable-removal failure returns
  the empty revoked snapshot plus error. Detach/invalidate require reattach.
  Close is the disconnect/shutdown hook: removes ownership first, closes trust
  and supervisor, joins adapter workers outside the lock, and is repeatable.
  Callers must use these manager lifecycle hooks rather than directly changing
  its trust owner. Independently revoked trust still fails final publication.

## Results, budgets and events

Only qualifying `Replace && phase == idle && failure == nil` outcomes replace the
checker-owned project set, including explicit empty arrays. Partial successful
coverage replaces with partial scope; failed/canceled/parser-incomplete runs
retain old findings stale. Findings are validated for path containment and scalar
shape; malformed ranges become file-level. Saved revisions survive snapshots;
missing proof cannot be upgraded to current. Stage 1 rejects document-basis
findings. `DocumentIdentity.MatchesPublication` reserves strict version/lifecycle
identity checks only; it does not implement document operations or LSP publication.

Provider availability, phase, freshness, coverage, ownership and approval remain
separate. Provider sets are ordered by ID, findings by path/range/message with
stable severity/code/source/basis tie-breaks. `CountFindings` projects exact
path/range/severity/message/code equivalence without destroying attribution.

Downward-injectable bounds: 8 attachments, 32 outstanding adapter calls (including
canceled cleanup), 32 providers, 10,000 findings, 7 MiB **escaped encoded finding**
bytes, 8 MiB encoded snapshots. A bounded heap chooses a deterministic prefix from
adapter candidates. Across providers, retention keeps the provider-ordered prefix.
UTF-8 message shortening and dropped findings explicitly mark partial/truncated;
shortened strings and retained prefixes do not hold discarded large backing data.
Discovery metadata is rejected above 128 KiB or a quarter of the event budget;
expanding coverage metadata is reduced to explicit partial scope. These are
conservative narrower limits than the 1 MiB metadata reservation, so repeated
metadata fits bounded event parts. At most 64 attempted project identities per
connection prevent Trust's project ledger growing through attach/detach churn;
a new connection is required at that bound. Backend-only Trust use outside this
manager must enforce its own ownership bounds.

Snapshots are deep copies. Every committed transition increments a safe sequence;
exhaustion invalidates attachment rather than wrapping. Events are full replacement
snapshots, split only between findings (explicit empty sets preserved), at most
32 parts, with a 1024-byte reserve inside each 1 MiB event cap for transport framing.
CD-09 must validate its actual encoded RPC envelope. Pull-based `DrainEvents`
transfers at most two updates; buffers enforce a combined 16 MiB encoded limit.
Overflow explicitly requests resnapshot; no callbacks, replay log or raw output
history is retained. Consumers must assemble a whole update, check sequence/context
and fetch `GetSnapshot` on the gap flag, not apply partial updates. Queue overflow
and part-generation failure are recovery signals, not successful empty runs.

## Evidence and limitations

Deterministic channel-gated fake adapters cover findings → empty, failure/cancel
retaining stale, superseded completion last, cancel-old/new-run isolation, revoke,
detach/reattach, generation invalidation, deleted-file late completion, shutdown,
coalescing and bounded pending calls. A supervisor-backed fake proves approval,
process cancellation, revoke/reapproval and publication integration without an
installed checker. Independent provider sets retain attribution with shared counts.
Concurrent snapshot/event drain tests run under the race detector. Limit tests
exercise escaped/control-heavy JSON, UTF-8 message size, finding/encoded snapshot
budgets, frame boundaries, event gap recovery, copies and sequence exhaustion.
Reserved document guards test old versions, unversioned results and reopen IDs.

Validation on macOS arm64:

- `go test ./internal/codediagnostics -run 'TestManager|TestReserved' -count=10`
- `go test -race ./internal/codediagnostics -count=3`
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3 run ./internal/codediagnostics/...`
- `go test ./...`

No diagnostic bodies are logged or persisted. Trusted adapter code must return
promptly on cancellation and bound its parsing; the manager cannot forcibly stop
an arbitrary Go adapter goroutine. Trust hashing/filesystem operations retain the
CD-02 cancellation and hostile-writer race limitations. No GUI, remote delivery,
Windows/Linux native runtime QA, or real-tool end-to-end claim is made here.
