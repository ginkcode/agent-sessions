# R07 — current live source/editor ranges

Date: 2026-10-22. **Complete, offline frontend evidence only.** Parent progress
remains **19/28**; CD-22…CD-24 and CD-26 remain partial. R08 is not implemented.
Production `liveCodeDiagnostics` remains false; both release HOLDs are unchanged.

## Implementation

- The existing live bridge publishes identity-only change invalidations to the
  diagnostics store. `liveBasis(path)` is available only on the current approved,
  live-capable attachment; synchronous typing/debounce, replacement, interruption,
  pending revoke and attachment invalidation remove eligibility immediately.
- FileViewer passes the displayed path's confirmed document/version/sync basis to
  shared source projection and the lazy editor. Saved checker eligibility still
  requires a clean matching saved revision, independently of live eligibility.
  Unversioned, uncertain, stale, wrong-document and wrong-version entries are
  ineligible; equivalent groups select a verified original without changing any
  provider-owned basis/freshness.
- Navigation uses the same eligibility and rechecks after rendering and when the
  lazy editor mounts. It does not save/reload drafts to make a range eligible.
  Source overlays remain excluded from diff, blame and rendered preview.
- The existing UTF-16/exclusive-end validator, multiline and EOF projection,
  CodeMirror transaction-time clear and editor cache/history are reused. No
  CodeMirror runtime import is added to stores or read-only viewer code.

## Evidence

- `live-lifecycle-integration.test.mjs`: actual FilesStore, diagnostics store and
  bridge feed current/old/future/unversioned/wrong-document results through source
  projection, SourceView SSR and CodeMirror EditorState projection. Typing clears
  before debounce; pending revoke and disconnect remove basis without draft reset;
  a wrong URI cannot produce an editor marker.
- `problems.test.mjs`: actual FileViewer navigation block with real FilesStore
  accepts current live ranges over drafts, rejects wrong IDs/versions/unversioned
  data, and rechecks sync after the queued render boundary.
- `file-editor.test.mjs`: actual editor controller/state clears live lint on a text
  transaction and rejects mismatched/unsynced bases; remount retains undo history
  without restoring invalid lint. Existing saved checker and cache tests pass.
- `source-diagnostics.test.mjs`: live UTF-16 multiline/empty-line/EOF projection,
  surrogate-boundary rejection and no saved-checker upgrade over a dirty draft.

Validation: `npm test` **442 passed**, `npm run check` **0 errors, 1 existing
ignoreDeprecations warning**, `npm run build` passed (existing large-chunk warning;
separate lazy fileEditor chunk retained). No Go files changed, so no Go run.
`git diff --check` passed.

These are offline component SSR, actual editor-state/controller and queued
navigation tests, not native paint/focus/selection/undo certification. Native UI
and manual draft evidence remain R16; full Engine/App/RPC route remains R14.
Backend R03 conservative reused-URI rejection is not replaced by frontend matching
IDs/versions: the frontend cannot repair a producer that falsely certifies a push.
No real language servers, remote matrix, automation or failure/retry UI work is
claimed.
