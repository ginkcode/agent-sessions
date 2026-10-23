# NAV-02 — Navigation over the wire

- Status: ⬜ Not started
- Estimate: 3 h
- Dependencies: NAV-01
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Make navigation requests available to the frontend for local and remote
projects, with the same host and connection checks as live documents.

## Design

- `engine.LiveCodeDiagnosticsBackend` gains `NavigateCode`; the app binding
  `NavigateCode(expectedHostId, context, documentId, version, op, position)`
  uses `liveRoute`/`liveResult`.
- RPC method `navigateCode` in the live method list with the ownership check;
  capability `CodeNavigation`, set only when live diagnostics are.
- Frontend: `api.navigateCode`, types, and a `codeNavigation` store helper
  that takes the current document basis from `liveDocuments` and returns
  "unavailable" with a reason (not approved, server not running, file not
  owned by a server) instead of throwing.

## Done when

- App, RPC and frontend API tests cover routing, the stale host or epoch
  rejection, the capability flag and the reasons.
