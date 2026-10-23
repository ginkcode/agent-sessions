# NAV-02 — Navigation over the wire

- Status: ✅ Done (2026-10-24)
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

## Outcome

- `LiveDocuments.Navigate`, `engine.CodeNavigationBackend` and the app
  binding `NavigateCode(context, documentId, version, op, position,
  expectedHostId)`, routed through `liveRoute` with the generation check
  afterwards. Navigation is its own interface, so the diagnostics backend
  contract did not change.
- RPC: `navigateCode` is a live method with the ownership check and a
  separate `codeNavigation` capability, set only with
  `liveCodeDiagnostics`. Both sides refuse an older peer before any work.
  The client checks that the answer is for the asked op, document and
  version (otherwise `staleContext`) and validates it: location count,
  ranges, project or absolute external paths with no `..`, hover kind,
  size and UTF-8.
- Frontend: `api.navigateCode`, the result types, the mock (word-based
  definition, references and hover over its open documents) and
  `CodeDiagnosticsStore.navigate`. It waits up to 2 s for typing to sync,
  asks about the synced version, drops answers for a file or attachment
  that moved on, and returns a reason instead of throwing
  (`codeNavigation.ts`). The reasons name what is missing and do not
  promise a setup button.
- Tests: `internal/rpc/code_navigation_test.go`, the local live,
  live-route, unsupported and offline app tests, and
  `frontend/test/code-navigation.test.mjs`.
