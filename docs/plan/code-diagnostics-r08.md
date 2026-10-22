# R08 — failure, recovery and bounded coverage UI

Date: 2026-10-22. **Complete, offline frontend only.** Parent progress remains
**19/28**; CD-22…CD-24/CD-26 remain partial. Production live capability remains
false/unadvertised; both release HOLDs unchanged. No R09 implementation.

## Implementation and recovery contract

- Actual diagnostics store projects retained findings stale for failed/canceled/
  disconnected or unavailable providers, and bridge synchronization failures.
  Counts/badges retain findings, Problems shows stale attribution and partial/
  truncation status, while shared source/editor eligibility removes markers.
  Truncation is partial coverage, not an all-clear. Provider failure does not
  change browsing readiness or discard drafts.
- Wiring now carries identity-only bridge failure status to CodeChecks. Only
  allowlisted error codes are rendered, never raw sync errors or document text.
  Provider notices/failures and diagnostic text remain escaped Svelte text.
- Check Project/retry selects savedProject checkers only, including mixed-provider
  attachments. There is no live provider restart RPC, and the UI does not pretend
  the checker run endpoint can restart a language server.
- Explicit “Reopen live synchronization” uses existing close/open operations,
  current displayed text, a fresh documentId/version 1 and at most two lifecycle
  recoveries. Attachment token, current approval, path and old documentId are
  rechecked; pending revoke/route replacement makes it inert. No commands, replay,
  backend restart API, body logs or persistence were added. Existing automatic
  resync recovery remains bounded and cannot bypass current approval/context.
- Backend review: LiveDocuments.EnsureSession calls SessionManager.Attach on a
  subsequent open (R05), so terminal session failure may create a new transport.
  A new frontend documentId alone does **not** isolate the URI in a surviving
  transport: R03 conservatively rejects reused-URI findings/empties until a new
  isolated session. The UI explicitly states this limitation; status refresh is
  read-only, not provider retry. Failed lifecycle findings remain stale after a
  successful reopen until authoritative publication replaces them.

## Evidence

`frontend/test/live-lifecycle-integration.test.mjs` traverses real FilesStore,
CodeDiagnosticsStore and bridge, CodeChecks/Problems SSR, source projection and
CodeMirror EditorState projection. Covers provider crash with retained current-
looking input, unavailable peer, truncation/partial status, escaped hostile text,
retained badge counts with absent markers, browsing/draft retention, bridge error
redaction, fresh identity/text resend, bounded recovery, wrong-ID and pending-
revoke guards, and rejected superseded open/change promises after replacement.
Existing checker retry tests now assert explicit saved-checker selection.

Validation: frontend `npm test` 445 passed; `npm run check` 0 errors, one existing
ignoreDeprecations warning; `npm run build` passed with existing large-chunk warning
and lazy fileEditor chunk preserved. `git diff --check` passed. No Go files changed;
Go/race/lint not rerun for this frontend-only package.

These are offline component/state tests, not native paint/focus or real-server
recovery evidence. Full Engine/App/RPC route is R14; native/manual QA remains R16.
No activation, remote matrix, R09 reconciliation or parent completion claimed.
