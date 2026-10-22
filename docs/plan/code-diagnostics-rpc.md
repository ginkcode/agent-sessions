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
- No real SSH/WSL identity acquisition or native Windows/Linux runtime QA claimed.
