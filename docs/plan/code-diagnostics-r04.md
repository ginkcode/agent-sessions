# R04 — production discovery/trust resolver seam

Date: 2026-10-22. **R04 complete (offline seam/tests only).** Parent progress
remains **19/28**. CD-22/CD-23/CD-24 remain partial; Stage 1/2 HOLDs remain.

## Implementation

- `internal/codediagnostics/live_discovery.go`: `DiscoverLive` composes the
  existing bounded Go/TS/Svelte metadata discovery with stable concrete provider
  IDs and independent missingTool/unsupported availability. Reviewed search paths
  are bounded to 64 entries. Existing contained walks never promote parents;
  discovery never executes version probes, configuration, package scripts,
  installers, or private-cache searches. Svelte's verified version list is still
  empty. Platform/version support has not expanded.
- Backend-only `LiveRegistration` carries openedDocuments metadata and fixed
  server identity into Manager's existing attachment-wide proposal. Available
  saved checkers and multiple live servers share **one exact complete fingerprint**.
  The Trust database/schema/lookup and revocation semantics are unchanged. No
  subset grants, implicit approvals, per-provider persistent grants or weakening
  of current-root checks were introduced. Missing providers contribute no tool
  and retain their own unavailable status when the other tools are approved.
- `DiscoveryDocuments` binds that same whole tool set to each resolved server,
  checks actual contained paths, and rejects conflicting owners instead of using
  registration order. It copies fixed config slices and keeps provider/policy
  identities independent of the composite grant. SessionManager validates exact
  selected tool/config membership, preflights the whole set on attach/retry and
  rehashes it immediately before launch. Checker run-all selects checkers only;
  checker submissions already consume the complete proposal.
- Existing positional Registration literals in tests were converted to keyed
  fields for the new optional live metadata field; their behavior is unchanged.

## Evidence

`live_discovery_test.go` uses only synthetic files and fake framed launches:
checker + Go + TS under the **same Trust and SessionManager**, missing Svelte,
zero starts before approval, a subset grant cannot start a composite session,
two approved starts/reuse without cross-provider cancellation, checker-only
run-all, rejected argv/config omission, changed fingerprint, removed catalog
permission, contained path rejection and conflicting ownership. The discovery
smoke has a parent go.mod and a nested authorized root, no installed tools and
no fallback; reviewed-search overflow fails closed. Existing adapter discovery
suites cover bounded contained walks, symlink escapes and config metadata-only
handling; no real language server is invoked by this package's tests.

Validation passed:

- `go test ./...`
- `go test -race ./internal/codediagnostics`
- `go tool golangci-lint run ./internal/codediagnostics/...` — 0 issues
- `git diff --check`

No engine package changed, so there is no new-engine lint delta.

## Explicit limits / next ownership

This is a callable production **seam**, not production activation. Engine still
runs its existing checker-only discovery; R05 must supply reviewed host metadata,
use the same registrations for Manager and DiscoveryDocuments, own sessions,
connect openedDocuments notices and teardown, and keep the capability disabled.
No R05 lifecycle wiring, frontend route, automation, native/remote evidence or
real-server execution is claimed. Existing pathname TOCTOU/non-sandbox limits
and adapter runtime/dependency verification limits remain; installed-server
support verification is R11–R13. This does not certify existing wrappers/plugins
as safe or complete the parent acceptance. Production capability flags remain
false. Later work must not approve each server separately on this Trust: doing
so replaces the single project grant and cancels peer leases.
