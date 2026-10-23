# Code diagnostics frontend (CD-10 / CD-11 / CD-12 / CD-13 / CD-14 / CD-15)

> **Update 2026-10-23:** Tools setup now offers a review step and one Install click for each missing gopls, vtsls or Ruff, on local and SSH/WSL hosts, and attaches again once after an install completes. Svelte setup is still not offered. See the [editor (Zed) model](code-diagnostics-zed-model.md).

## Diagnostics UI polish (screenshot feedback, 2026-10-23)

This change came from a user screenshot. It touches frontend presentation only:
backend, `liveDocuments.ts` and store protocol state are unchanged. Progress stays
at **19/28** with no task counted as done.

- **Location wording** (`problemNavigation.ts` `problemLocationStatus`,
  `fileLevelFinding`). A finding with no range, or a 0:0 range whose message reads
  like a build-constraint exclusion, is now *file-level*. It is never called stale.
  A truly outdated location starts with "Stale: …" and no longer uses "stale or
  unverified; location remains in Problems only". Source projection
  (`sourceDiagnostics.ts`) gives each message a `kind`. An out-of-range position
  reads "Position is outside the displayed text; shown in the list only."
- **Recovery action.** `FileViewer` passes `sourceRecovery` to `SourceView`, which
  shows it only when stale findings exist. If the file's live document failed and
  can reopen, the button reads **Reopen live sync** (the existing
  `codeDiagnostics.reopenDocument`). Otherwise it reads **Refresh results** (the
  existing `codeDiagnostics.refresh()`). Each button says what it does and says that
  nothing is saved. It never approves, installs, saves or runs on its own.
- **Compact Source list.** Shows severity counts, a "N not current" tag, and per row
  a severity pill, Ln/Col, a short source label and a collapsed long message. The
  list is 12px and scrolls inside a bounded height so the source area keeps its
  space. Text stays escaped.
- **Problems panel.** Severity filter checkboxes are grouped with a gap and use
  capitalized labels. Rows are a pill/location/message/meta grid. Details are capped
  at `min(38vh, 360px)`.
- **Code Checks settings** (`codeCheckLabels.ts`). Human tool names ("Go language
  server (gopls)", "Go syntax", "Go vet"), status pills, and consistent modes ("LSP ·
  live opened Go files", "saved Go files · automatic", "saved files · on demand").
  Live-sync failures are grouped, with a Reopen button per file. Coverage and host
  details sit under disclosures. gopls install/check appears only for the local
  host. Remote hosts get an explicit "not available for remote hosts" reason, and
  TS/Svelte setup is stated as not offered.

Evidence: new `frontend/test/code-check-ux.test.mjs` plus adapted `problems`,
`source-diagnostics` and `code-check-status` tests. Validation: **479/479 frontend
tests**, `npm run check` 0 errors (existing tsconfig warning), build passed
(existing chunk advisory). Caveats: these are server-rendered markup and pure-state
checks only, with no native GUI run. Build-exclusion detection is a message
heuristic. Remote detection uses the `ssh:`/`wsl:` host-ID prefix.

## Compact Go to File status (UX simplification, 2026-10-23)

This change came from user feedback: the always-visible Code Checks/setup text at
the top of Go to File took up most of the dialog. It changes only the layout. Store,
backend, trust, managed-install and live gating are unchanged, and the overall
progress stays at **19/28** with no task counted as done.

- **No top panel.** `GoToFileDialog` no longer mounts `CodeChecks` above the body.
  The only diagnostics UI by default is a single bottom status button in
  `ProblemsPanel` (`aria-expanded`, `aria-controls` → details region, a polite
  `role="status"` label). Each dialog mount starts collapsed, so no open state is
  remembered.
- **Concise status** (`codeCheckSummary.ts`): error/warning/info counts, or a short
  state such as "Code checks starting…", "Checking…", "Code checks: approval
  needed", "Code checks unavailable", "Code checks disconnected" or "No checker
  available". "No problems reported" appears only when the project is approved, the
  attachment is ready and a checker is available, and it gets "(partial)" when
  coverage is partial; only then is the status dot green. The tooltip covers scope
  and caveats (project only, includes unlisted files, partial or truncated) and
  never shows host identities.
- **Progressive disclosure.** Clicking the status shows the issue list (severity
  filters, previous/next, file groups) and a separate **Settings** /
  **Set up code checks** toggle, which has its own `aria-expanded`. That toggle
  mounts `CodeChecks`, which now holds approval, run, cancel, revoke, retry,
  live-document recovery and the optional gopls setup inside a collapsed `<details>`
  that opens only while a proposal or installation is active. When there is nothing
  to list and approval or setup is needed, opening the status also reveals the
  settings. Every approve or install still takes its own button press, and an
  installation still needs a reviewed proposal. Raw host IDs and proposal
  fingerprints appear only under a collapsed **Technical details**; the install
  proposal still names the target host identity next to the Install button.
- **Show/hide toggle.** Like the terminal button, an icon button at the right of
  the status bar shows or hides the details (`aria-pressed`, accent while shown),
  as does Ctrl+Shift+M while Go to File is open. The editor drops CodeMirror's own
  Mod-Shift-m lint panel binding so the shortcut always means this panel. Each
  opening still starts hidden.
- **Short approval card.** The approval buttons come right after one line saying
  how many tools will run with the user's permissions. The full risk explanation
  and the tool list sit under a collapsed **What will run and what approval
  means**. The list shows each program once: nested workspaces repeat the same
  executable under hashed IDs, so rows are merged by executable, with every
  configuration listed and an "N workspaces" pill.
- **Collapsed checker list.** The Checkers list is a disclosure whose summary is
  one line of status counts ("2 ready", "4 unsupported", "partial coverage"). It
  opens by itself only when a checker offers an Install button or has failed, so
  those actions are never hidden.
- **Red file names.** A file name in the tree and in search results turns red for
  a finding of *any* severity, including hint and information (`issueCounts`). This
  also applies to the selected row. The Git class and Git letter stay, and the row
  tooltip adds "N reported code issue(s)". Hint and information rows also get
  screen-reader text, because the error/warning badge labels only itself. Folders
  above a file with findings get a subtle dotted red underline. Existing
  error/warning badges and counts are unchanged.

Evidence: `frontend/test/code-check-status.test.mjs` (default markup has no verbose
top panel or host hash, the status sits below the body with collapsed and
controlled semantics, concise counts, settings appear only when disclosed, no
automatic approval or install, no false clean claim in unavailable, off, unapproved,
disconnected or failed states, and red names in the tree, the selected row and
search for non-error severities). Existing CodeChecks, managed-tools, integration
and Problems tests were adapted. Validation: **466/466 frontend tests**, `npm run
check` 0 errors (existing tsconfig warning), build passed (existing chunk advisory;
the lazy `fileEditor` chunk is kept), `git diff --check` clean. These are
server-rendered markup and pure-state checks only. Native paint, focus, theme and
screen-reader verification is still CD-17, and no native GUI run is claimed.

## CD-15 editor lint

Overall progress is **14/28** (CD-01…CD-08 + CD-10…CD-15); CD-09 remains blocked.
The editor-only lazy runtime uses `@codemirror/lint` state effects and CD-14's
verified saved-range projection. Dirty/conflicted/uncertain drafts and invalid,
stale or document-basis findings never receive saved markers. Every document
transaction (including undo) clears markers immediately; current revision-bound
props must reapply. Cache restoration sanitizes lint state before display without
losing history, selection or scroll. No diagnostic changes edit text or save files.
There is no separate lint gutter: like the viewer, the editor marks the line
numbers of lines with markers (bold, dotted underline), so switching between
viewing and editing keeps the gutter width and does not move the text.

412 frontend tests pass; check/build pass with existing warnings. Native tooltip,
gutter, keyboard/screen-reader and theme QA remain CD-17. See
[CD-15 evidence](../tasks/CD-15.md#completion-evidence). No next task started.

## CD-14 source indicators

Overall progress is **13/28** (CD-01…CD-08 + CD-10…CD-14); CD-09 remains blocked.
Verified clean saved revisions alone receive UTF-16 source overlays. A pure projection
rejects invalid coordinates, preserves overlapping/exclusive/empty/EOF locations,
and keeps file-level, stale and uncertain findings as attributed plain text messages.
CSS Highlights walk verified source text nodes without altering escaped syntax HTML,
find offsets or selection. Accessible gutter labels coexist with Git/line selection;
keyboard activation opens messages. Unsupported WebViews retain gutters/messages.
Diff, blame, rendered previews and images never receive source overlays. Directory,
active-path and attachment gates prevent cross-file markers; empty replacement clears.
No CodeMirror static imports. See [CD-14 evidence](../tasks/CD-14.md#completion-evidence).
Validation: **409/409 frontend tests**, check 0 errors (existing configuration warning),
build passed with existing chunk advisory and lazy editor chunk. Native paint/theme,
wrap/layout, focus and screen-reader QA remains CD-17; no next task started.

## CD-13 tree counts

Overall progress is **12/28** (CD-01…CD-08 + CD-10…CD-13); CD-09 remains blocked.
Cached shared exact groups now feed file and component-ancestor count maps, summary,
Problems and inline selectors. Every file contributes once per ancestor, independent
of expansion, filtering, listing truncation or tabs. Provider originals survive
shared grouping and single-provider clearing. Tree/search badges expose severity,
stale/uncertain and partial/truncated context independently of Git and ignored tags;
no discovery changes or diagnostic-only nodes are introduced. See
[completion evidence](../tasks/CD-13.md#completion-evidence). Frontend validation:
**403/403 tests**, check 0 errors (existing configuration warning), build passed
(existing chunk-size advisory). Native layout/theme/screen-reader QA remains CD-17.

## CD-12 Problems navigation

Overall progress is **11/28** (CD-01…CD-08 + CD-10…CD-12); CD-09 remains blocked.
The shared collapsible panel groups exact findings, filters severities locally,
retains source attribution and supports native button activation plus scoped arrow
and wrapping next/previous navigation. It is a sibling of the mounted viewer.
`problemNavigation.ts` keeps UTF-16 exclusive ranges separate from line-only blame,
validates revision/freshness/draft eligibility and maps source text-node offsets.
Viewer navigation uses kept tabs and captures request/tab/path/host/root/connection/
attachment generation identity through load/render and lazy editor mount. Dirty or
unverified saved ranges stay panel-only; no saves, reloads or destructive guards are
bypassed. Markdown/SVG/diff locations switch to source. CodeMirror selection is a
selection-only transaction preserving document/undo history.

See [CD-12 completion evidence](../tasks/CD-12.md#completion-evidence). Frontend
validation: **399/399 tests**, check 0 errors (existing config warning), build passed.
Node async/store/controller/SSR and keyboard-handler fixtures are not native QA;
focus/paint/scroll and native activation verification remain CD-16/CD-17 work.

## CD-11 integration

Completed local approval/check/status controls; overall progress **10/28**
(CD-01…CD-08 + CD-10…CD-11). CD-09 production remote identity remains blocked.
The dialog calls asynchronous `show(dir)` and `hide()` on visibility/route changes,
with no check execution on attach, approval, dismissal, save or context changes.
`CodeChecks.svelte` consumes shared selectors and store-owned mutation gating,
including ack/event-gap ownership and immediate fail-closed revocation.
See [CD-11 completion evidence](../tasks/CD-11.md#completion-evidence) for tests
and limitations. Manual keyboard/focus QA has not been performed.


## Dependency exception and release boundary

CD-10 proceeds locally against the committed CD-01 contract and CD-09 RPC
contract/transport recovery tests. **CD-09 remains blocked** on production SSH/WSL
authenticated host identity acquisition and launch binding. Nothing here activates
remote capabilities or treats picker aliases as stable identities. Old/identity-less
remotes expose unsupported diagnostics while file browsing/editing continues.

## Ownership and integration

`frontend/src/lib/stores/codeDiagnostics.svelte.ts` owns only attachment/results
state. It has no FilesStore, CodeMirror, source-text, draft, revision-counter, tab,
selection or history ownership. Existing scanner `Diagnostics` are unchanged.
`codeDiagnostics.ts` defines distinct transport types and pure exact-dedup/count/
saved-basis eligibility helpers. Wails methods pass the stable `expectedHostId`
on every call and normalize errors without changing existing APIs. The browser
mock has a synthetic approval/check/revoke lifecycle, not executable discovery.

Later CD-11 dialog wiring must call `attach(dir, connectionState)` on explicit
project lifecycle, and `hide()`/`detach()` on hide/replacement/disposal. Reopen or
reconnect always attaches anew. Subscribe-before-attach and connection-event
cleanup are internal; explicit `disconnect()` is also available to the route
owner. Connection epoch guards apply independently to replies and callbacks.
No component is wired yet; no check runs automatically on attach or approval.

Full replacement snapshots are authoritative. Run/cancel acknowledgements never
install findings or roll back newer provider runs; previous run IDs in retained
failed/canceled sets are intentionally allowed by the contract. Multipart slices
are concatenated by provider/origin/scope/run identity and published atomically.
Exact duplicates are ignored; metadata/part conflicts, gaps, five-second assembly
timeout, and count/escaped-byte overflow discard buffers and resnapshot. Recovery
is coalesced with at most two requests per attachment until explicit `refresh()`;
there is no unbounded automatic retry. Errors retain stale findings. Equal
resnapshots resolve uncertainty; lower snapshots cannot roll back newer events.
Only explicit newer revoked snapshots may transition an owned generation.

Projections expose providers/coverage/truncation, exact-deduplicated findings with
per-provider source/basis attribution, default error/warning counts and component-
bounded descendant totals. Saved inline eligibility requires current freshness,
a verified equal revision and a clean displayed file; actual range/text validation
remains the renderer's responsibility. File-level and uncertain findings remain
panel/count eligible. Live/document inline support is not advertised.

## Validation (macOS)

- `npm test`: all frontend tests pass, including compiled real rune-store races,
  actual CD-01 Go multipart fixtures, early/duplicate/reversed events, gaps,
  conflicting parts, timeout, recovery exhaustion, encoded size bounds, empty
  replacement, stale/cancel retention, run acknowledgement, revoke races,
  epoch/host/reconnect/old-callback isolation and browser mock lifecycle.
- Wails mapping tests cover every Stage 1 method, exact host/argument order,
  absent bindings and normalized string/structured errors.
- Existing draft/history tests still pass; no editor imports added. Production
  build retains the separate lazy `fileEditor` chunk.
- `npm run check`: zero errors; existing `ignoreDeprecations: "6.0"` warning remains.
- `npm run build`: pass; existing large chunk advisory remains.
- `go test ./internal/codediagnostics/... ./internal/engine/... ./internal/app/... ./internal/rpc/...`: pass.
- Same relevant Go suite with `-race`: pass.

No real-host activation, UI, native platform QA, LSP or document sync is claimed.
