# Code diagnostics RPC (CD-09)

Stage 1 operations now use protocol-v1 additive `codeDiagnostics` and
`liveCodeDiagnostics` flags plus optional `codeDiagnosticsHostId` handshake
metadata. Live is always false. Client calls fail before writing if project
checks or verified host metadata are absent. Existing browse/edit RPC remains
unchanged.

## Identity limitation — intentionally fail closed

The current SSH launcher does not expose authenticated host-key + resolved
endpoint/security-domain identity, and the WSL launcher does not expose Windows
installation/account + immutable distribution installation GUID. Neither the
picker label nor the headless engine's local identity is a substitute. Production
headless servers therefore **do not advertise diagnostics yet**. The explicit
`RemoteCodeDiagnosticsHostID(context.Context)` backend seam must be implemented by
an authenticated transport owner before activation; client environment variables,
labels, and initialize assertions are not accepted as that authority. Unsupported
platform identity remains unsupported. Synthetic verified identities exercise the
complete Stage 1 wire operations in tests without enabling production execution.

## Ownership and delivery

Each server owns an attachment/context registry. Every attached request is
checked before backend dispatch. Engine/manager still enforce root authorization,
approval, run identity, and generation. Canceling an RPC wait does not cancel
checker work; explicit cancel/revoke/detach or connection expiry does. EOF, parent
cancellation, idle timeout, and Close close transport handles, join handlers,
detach attachments, and reset the diagnostics owner/session grants.

Notifications are admitted only for owned contexts. A dedicated writer permits
at most 64 queued parts, two pending update IDs per attachment and 16 MiB encoded
pending data per connection (stricter than per attachment). Actual marshaled
JSON-RPC envelopes, including newline and Go HTML escaping, must fit 1 MiB.
Overflow/oversize closes the connection rather than silently dropping updates;
reconnect must attach anew. Manager retention and snapshot/event splitting remain
the canonical 8 MiB/32-part implementation. Dequeued writes recheck ownership;
clients independently suppress detached/foreign/older-generation events. Early
attach events have bounded 64-part/16 MiB buffering. Revoke transitions require
explicit revoked approval and increasing sequence; late revoke replies cannot
roll back newer state.

The RPC client forwards **parts**, not partial result sets. Atomic assembly,
sequence-gap/missing-part/conflicting-part recovery and snapshot reconciliation
remain CD-10's frontend store responsibility, as specified in CD-01. Overflow at
the transport layer disconnects; manager overflow delivers a full replacement
snapshot at its latest sequence, so the consumer observes the gap and requests a
snapshot. No frontend store or live/document operation was added.

## Evidence

- `go test ./...` — pass on macOS.
- `go test -race ./...` — pass on macOS.
- Focused RPC tests use non-executable fake checker metadata and actual pipe-pair
  requests for unknown roots, unapproved/approved work, foreign contexts,
  empty-provider rejection, explicit cancellation, revoke/new generation,
  disconnect expiry, scoped notifications, escaped envelope limits, bounded
  slow-peer admission, idle expiry, optional old flags and zero-write guards.
- Scoped `go tool golangci-lint run --new-from-rev=HEAD ./internal/rpc/... ./internal/app/...`
  checks new code; full package lint also reports pre-existing unrelated findings.
- `code_diagnostics_recovery_test.go` adds actual pipe RPC evidence: complete
  multipart contents (including escaped strings, duplicates and out-of-order parts),
  deliberately missing-part metadata, skipped sequence updates followed by latest
  authoritative snapshot RPC, and stale-sequence suppression. Tests observe parts,
  never implement or claim frontend atomic assembly, timeout/conflict detection or
  automatic retry policy; those remain CD-10.
- A gated real pipe writer deterministically fills the 64-part notification queue;
  the next admission errors and disconnects, expires the active fake checker, and
  rejects further snapshots. A fresh connection to the same host/root requires a
  new attachment/approval, rejects old tokens/late events and can resnapshot. The
  existing two-update admission and escaped-envelope tests cover other limits.
- Follow-up validation: `go test ./...` and `go test -race ./...` pass on macOS.
  Full `go tool golangci-lint run ./...` still fails on 112 pre-existing findings
  outside this change; `go tool golangci-lint run --new-from-rev=HEAD
  ./internal/rpc/... ./internal/app/...` passes (0 issues). Focused diagnostics
  RPC tests also pass 20 consecutive race-enabled runs.
- No real SSH/WSL identity acquisition or native Windows/Linux runtime QA claimed.
  [Explicitly split identity activation work](../tasks/CD-09-identity.md) records
  launcher evidence, trust requirements and four-hour work packages. CD-09 remains
  blocked. Progress is 10/28 for CD-01…CD-08 + CD-10…CD-11 only; frontend work proceeded
locally against committed contracts/tests under an explicit dependency exception.
CD-09 is not counted complete and production remotes remain inactive.
