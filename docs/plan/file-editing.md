# Go to File — lightweight file editing

Status: FE-01–FE-12 implemented on branch `editor` (commits `11ca1cd`,
`82e00ca`, `6e5581a` and the release-review fixes). Editing is exposed in the
UI. Automated regression coverage is in place and the release review (FE-14) is
done, with its findings fixed. Manual desktop and Linux/Windows/SSH/WSL QA
(FE-13) has not been performed and blocks release.

## Goal and scope

Add an explicit Edit mode to Go to File for quick project-file changes, not a
full IDE. Keep the existing source viewer, Markdown/image previews, and Git diff
as the default read-only experience.

First version includes:

- Existing UTF-8 text files, including empty files, Markdown, and SVG source.
- CodeMirror 6: syntax highlighting, line numbers, indentation, undo/redo,
  find, and word wrap. Lazy-load the editor and language support.
- Explicit Save and Discard actions (both leave edit mode); Cmd+S / Ctrl+S.
- Dirty-tab indicators, retained drafts, and guarded destructive transitions.
- Revision-checked saves and an explicit conflict-resolution flow.
- Local, WSL, and SSH access; older remote servers remain read-only.

Revised by user decision: there is no separate Done action. Save and Discard
both leave edit mode; Cmd/Ctrl+S saves and keeps editing.

Out of scope: new files, rename/delete, Save As, autosave, disk-backed draft
recovery, merge editor, filesystem watching, LSP/autocomplete, formatting, and
diagnostics. Binary/image/oversized files remain read-only.

Follow-up scope: [code-diagnostics.md](code-diagnostics.md) proposes project
checks and live language-server diagnostics as a separate staged feature. The
exclusions above describe this editing release, not that follow-up.

Drafts stay in memory, not local storage or another persistent cache: project
files may contain secrets. Normal close/quit must be guarded, but a crash or
forced termination can lose drafts. Do not log file bodies.

## Current integration points

- `frontend/src/lib/components/common/GoToFileDialog.svelte`: dialog lifecycle,
  Escape handling, lock, presentation mode, and file picker.
- `frontend/src/lib/components/common/go-to-file/FileViewer.svelte`: current
  viewer, previews, Git diff, find, and toolbar.
- `frontend/src/lib/components/common/go-to-file/FileTabs.svelte`: tab close,
  middle-click, and bulk-close context actions.
- `frontend/src/lib/stores/files.svelte.ts`: tabs, asynchronous reads, refresh,
  and per-directory saved state. Currently refresh and reopen reread tabs;
  connection changes call `files.reset()`. These must become draft-aware.
- `frontend/src/lib/api.ts` and `frontend/src/lib/types.ts`: frontend contracts.
- `internal/files/files.go`: bounded UTF-8 reads and root-contained resolution;
  no write operation exists. Current text-read limit is 5 MiB.
- `internal/engine/{backend,service,engine}.go`: backend interface and routing;
  the service restricts reads to known session directories.
- `internal/rpc/` and `internal/app/`: remote transport, desktop bindings,
  capabilities, offline behavior, and connection lifecycle.

## Behavioral contract

| Action | Behavior |
|---|---|
| Edit | Load the editor and promote a temporary preview tab to a kept tab. |
| Switch tabs | Retain draft, undo history, cursor/selection, and scroll position. |
| Save (button) | Submit an explicit snapshot, then return to viewer. A failed or conflicted save, or typing during the save, keeps the editor open. |
| Cmd/Ctrl+S | Submit an explicit snapshot and stay in edit mode. |
| Type during save | Allow typing; edits after the submitted snapshot remain dirty. |
| Discard (button) | Confirm when dirty, drop the draft and return to viewer; reread from disk after a conflict or uncertain save. |
| Reload (error bar) | Confirm when dirty, reload from disk, and reset editor history; stay in edit mode. |
| Close a tab | Dirty files require Save / Discard / Cancel. |
| Bulk-close tabs | Coordinate one flow; no overlapping prompts or unawaited saves. |
| Refresh | Refresh the listing and clean tabs; never replace dirty drafts. |
| Hide the dialog | Preserve drafts in memory; reopen must not reread over them. |
| Change directory | Preserve drafts in the existing per-directory cache. |
| Switch host / clear state | Guard any operation that would discard drafts, including cached directories. |
| Disconnect | Keep drafts; block saves until the correct host is available. |
| Quit / close window | Guard normal exits with unsaved drafts. |

Editor shortcuts take priority while editing. Cmd/Ctrl+F uses CodeMirror search;
Alt+Z uses the existing wrap preference. Escape may close editor search but must
not discard a draft. Preserve the dialog-lock behavior. Presentation mode and
rendered previews remain read-only; leaving edit mode requires the same dirty
check as Discard.

After save, update the saved baseline, size/revision, Git listing status, and
saved-file diff. Do not refresh away drafts in other tabs. Git markers describe
saved disk content, not speculative unsaved changes.

## Save and conflict contract

Finalize exact exported names in FE-01 before implementation. Intended shape:

```ts
saveFile(dir, path, text, expectedRevision)
```

- A revision is an opaque hash of the original file bytes, not mtime/size alone.
- The save request requires a revision; no unchecked force-write flag.
- The backend validates the known directory, containment, existing regular-file
  status, UTF-8/text eligibility, and input/output byte limits independently of
  frontend controls. Account for JSON expansion and RPC frame limits.
- Initial edit limit: 500,000 UTF-8 bytes, below the viewer's 5 MiB read limit.
  Viewing larger text still works. Enforce the same edit limit in backend and UI.
- Preserve executable permissions, UTF-8 BOM, uniform LF/CRLF style, and the
  intended final newline. Do not normalize bytes merely by entering edit mode.
  Mixed line endings and unsupported newline conventions stay read-only in v1.
- Refuse writes through symlinked child paths in v1 (including directory
  components). A canonicalized project root itself may be a symlink. Audit
  Windows junction/reparse behavior and hard links before enabling writes.
- **Revised by user decision:** write in place, not by replacement. The user
  selected this after the strict replacement gate refused macOS provenance
  attributes. Open the existing inode without truncation; write the snapshot,
  truncate to its final length, then sync. This preserves inode identity and
  existing ownership, permissions, ACLs, streams and extended attributes without
  copying them. Hard links are allowed: all aliases see the write, including
  aliases outside the project. A crash, full disk or I/O failure can leave partial
  contents; do not claim atomic saves or rollback. Keep the frontend draft on
  failure. No-op saves do not write or change mtime.
- Serialize writes to a target within this process. Use race-resistant path
  handling where available; resolving a pathname once is not a complete defense
  against symlink substitution.
- Return a structured success/conflict result that survives RPC and Wails;
  do not identify conflicts by matching error-message strings.

On conflict, retain the draft and offer Reload from disk, Overwrite…, or Cancel.
Reload requires confirmation before losing the draft. Overwrite requires an
explicit confirmation and retries against the revision just observed. If that
revision changes again, show another conflict. Deleted/replaced/ineligible files
are not silently recreated or overwritten.

Revision checks protect against ordinary stale saves, but cannot exclude an
unrelated writer in the final check/replace interval. Document this limitation;
do not claim cross-process compare-and-swap or locking guarantees.

A disconnected save can have an unknown outcome. Do not automatically replay it
or mark the draft clean. On reconnect, reread the correct target to reconcile:
if its bytes equal the submitted snapshot, acknowledge that snapshot as saved;
otherwise preserve the draft and require the normal revision/conflict flow.

## Task breakdown

### Initial implementation findings

- Reads now return `revision: "sha256:<hex>"` from exact raw text bytes and
  optional `edit: { eligible, reason?, newline?, bom? }`. Text remains unchanged.
  Older servers may omit both fields. Newline values are `lf`, `crlf`, or `none`.
- All files remain ineligible for now: supported text has `safety_pending`;
  over-limit text has `too_large`, and mixed/bare-CR text has
  `unsupported_newlines`. Binary/image/over-viewer-limit reads omit metadata.
  FE-02 is not complete until path/protection/platform eligibility is connected.
- `os.Root` is available and already used by downloads. Use rooted operations
  for saves, but do not mistake root containment for a no-symlink guarantee.
  Windows junctions can report `ModeIrregular`, not `ModeSymlink`.
- Existing atomic-write helpers do not preserve all project-file metadata.
  Windows replacement ACL/stream behavior and macOS ACL detection need explicit
  implementation and tests before either platform advertises edit eligibility.
  Split FE-03 into path/protection and Unix metadata/replacement subtasks before
  starting; split FE-04 similarly if necessary. No unsafe fallback is allowed.
- The service knows permitted session directories, but unusually broad working
  directories can expose tool stores/credentials. Build protected-target checks
  from host-local configured roots and protected names. Existing viewer credential
  exposure is a separate pre-existing issue; do not enable editing around it.
- Same-host reconnect currently calls `files.reset()` and clears all cached tab
  state. FE-10 must separate connection reload from draft disposal, not merely
  add a prompt to explicit host switches.
- Wails supports `OnBeforeClose`, but no hook is wired. On Windows the hook can
  run on the UI thread: never block it waiting for a frontend response. Prefer
  preventing close, emitting a request, then explicitly authorizing a second
  quit after the draft coordinator succeeds. Design out the first-edit/close
  synchronization race before marking FE-11 complete.
- Each app window is a separate process; only its own drafts need a close guard.
  OS session shutdown/forced termination is not reliably covered by ordinary
  Wails close hooks and must remain an explicit limitation.

### Progress and validation

#### Current implementation (supersedes replacement-era findings below)

- User explicitly chose **in-place saves** after reviewing the crash/full-disk
  tradeoff. FE-03b's hard-link refusal and metadata-copy gate and FE-03c/FE-04's
  atomic replacement tasks are superseded by the revised save contract above.
- `edit_target.go` opens existing files through rooted handles, checks component
  identities, bounds reads and rejects symlink/irregular paths and unsupported
  text. Canonical roots are checked against the protection policy again.
- `edit_write.go` implements revision-checked in-place saves, exact BOM/newline
  serialization, no-op saves, write/truncate/sync, and a bounded global save lock.
  Global serialization handles hard-link/root aliases without a leaking lock map.
  Waiting for the lock is cancellable. Identity/path/policy are revalidated before
  mutation. External writers remain outside this lock; partial writes remain an
  accepted limitation. Windows runtime metadata/handle behavior still needs QA.
- Engine reads complete eligibility with protected configured **and default**
  tool/app roots plus an O_RDWR open without mutation. Low-level format-only
  reads report `not_checked` rather than the old `safety_pending` reason.
- FE-05 implementation is wired through backend/service/engine, app, offline
  backend, RPC and CLI capability, frontend API/types and browser mock. Old remote
  servers omit `saveFile`; the client refuses saves before transport. Tests cover
  actual RPC saved/conflict results, unknown directories, protected stores and
  remote/offline routing without local fallback.
- Save backend committed on branch `editor` as `11ca1cd`.
- FE-07 implementation added: lazy runtime/language imports, per-tab immutable
  CodeMirror state/selection/scroll in a WeakMap, live wrapping/read-only state,
  search and save keys, and safe async mount cleanup. No inactive editor DOM.
- Native close backend added: editing must arm the guard before mounting; once
  armed, every close is deferred to a frontend decision. One-shot request IDs
  reject stale approvals; the callback never blocks the UI thread. Frontend must
  freeze input throughout close approval. Unit tests cover cancellation, replay
  and concurrent requests. Desktop-tag Go build/test passes on macOS; actual
  native close interaction remains untested.
- FE-06 draft state implemented and tested: immutable save snapshots, later
  typing retained as dirty, read/refresh/reopen guards, hidden-directory saves,
  host/epoch validation, and conservative refusal to reset/close unresolved drafts.
  `fileEditing.ts` handles editor normalization and byte limits. Failed/stale
  reloads preserve drafts and history; uncertain saves are never replayed.
- A serialized accessible file-decision prompt and destructive-action coordinator
  are implemented. Tab and bulk close paths use the coordinator; native request
  subscription and browser unload guard are installed. The coordinator freezes
  editor input via its busy state.
- Lifecycle: `files.connectionChanged(host)` keeps editors, drafts and caches on
  same-host reconnect, invalidating late reads and refreshing clean tabs and the
  listing. Explicit host switches go through `fileActions.changeHost`, which
  resolves drafts and stays busy until routing completes. Uncertain saves are
  reconciled by reading only and comparing exact submitted bytes (captured
  BOM/newline format); a match advances the baseline without touching later
  typing, a mismatch becomes a conflict. Successful saves refresh only the
  visible listing; a listing failure is not reported as a save failure.
- Conflicts: Resolve… offers Reload (confirmed) or Overwrite (confirmed). The
  overwrite retries against the observed revision via normal CAS and may
  conflict again.
- FE-08 viewer controls exposed: Edit (awaits `enableFileEditing()` to arm the
  native close guard and revalidates tab/dir/host/epoch), Save / Cmd-Ctrl+S,
  Discard, Done, dirty/saving status, read-only reasons, and conflict/uncertain
  error bar. Preview, presentation, Git diff and viewer find hide while editing;
  the editor renders ahead of deleted/empty/error states. Typing is frozen only
  during coordinated prompts and quit, not during ordinary saves or disconnects.
- Release review (FE-14): no body/credential logging, no unchecked revision, no
  silent loss through previews, directory changes, dialog hiding or reset.
  Findings fixed:
  - A save failing after writing began (write/truncate/sync) now returns
    `files.ErrSaveIncomplete` (RPC code `save_incomplete`); the frontend marks
    it uncertain and reconciles by reading, so a draft undone to the baseline
    stays protected. An unresolved conflict also counts as unsaved, since after
    a partial write the draft is the only intact copy.
  - A lazily loaded editor no longer takes focus while a prompt is open; the
    prompt keeps focus inside itself (document `focusin`) and blocks the
    browser's Cmd/Ctrl+F/S/W defaults.
  - `App.SaveFile` takes the host the file was read from and refuses the save,
    unwritten (`ErrSaveHostChanged`), unless the same routing snapshot used for
    the call is that host.
  - Escape regression from the fullscreen safeguard (`9e2f01a`) fixed in
    `6e5581a`: handlers use `keyHandled(e)` instead of `defaultPrevented`.
- Earlier validation: 296 frontend tests pass (drafts, coordinator, prompt,
  editor, viewer controls); Svelte check has 0 errors and the existing tsconfig
  warning; production build passes; full Go suite and desktop-tag tests pass.
- Bundle check: CodeMirror lives only in the lazy `fileEditor` chunk
  (~324 kB, ~105 kB gzip) with language modules in separate chunks; the main
  `index` chunk contains no CodeMirror code, so viewing never loads the editor.
  Responsiveness near the 500 KB limit is not yet measured.
- Latest validation (after the review fixes): 299 frontend tests, Svelte check
  (0 errors, existing tsconfig warning), production build, `go vet ./...`,
  `go test ./...`, race tests for app/files/rpc and desktop-tag tests pass.
- Pending: native desktop interaction QA (editing, close/quit prompts, real
  CodeMirror DOM focus/search/scroll), Linux/Windows filesystem behaviour, and
  SSH/WSL disconnect-during-save scenarios.
- Validation: full `go test ./...`; race tests for files/engine/RPC/app; repeated
  file-save race tests; Linux/Windows cross-platform vet; frontend's 239 tests;
  `npm run check` (0 errors, existing ignoreDeprecations warning). No native
  Linux/Windows or desktop close QA claimed. `npm audit` reports an existing
  high-severity source-map-js dev dependency advisory, not a CodeMirror advisory.

#### Earlier incremental notes (historical)

- FE-01: audit complete, contract/policy decisions still partial (unchecked below).
- FE-02: raw-byte revision and text-format metadata implemented. Added table,
  read integration, byte-boundary, and JSON compatibility tests.
- FE-03a: standalone protected-path preflight implemented, including broad-root
  and symlink-alias protection, missing configured roots, case variants, and
  parent-traversal regressions. Service integration and write-time checks remain.
- Save contracts and pure serialization implemented in `edit_save.go`: LF editor
  snapshots restore original BOM/CRLF, preserve intended final newline, and
  enforce serialized byte limits. Saved/conflict results are structured; no
  actual disk-write operation or editing UI is enabled.
- Latest checks: `go test ./...`, `go test -race ./internal/files/`,
  `go vet ./internal/files/`, and `git diff --check` passed. Found the installed
  Node toolchain outside PATH; `npm run check` now runs with zero errors and one
  tsconfig warning (`ignoreDeprecations: "6.0"`). Platform save QA is still pending.
- Validation: `go test ./internal/files/`, `go vet ./internal/files/`, and
  `go build ./...` passed using the installed Go 1.27.2 toolchain. Go is not on
  the shell PATH. Frontend type checking could not run because Node/npm are not
  available on PATH. No native platform save/close QA has been performed.

Every task is scoped to at most four engineering hours, including focused tests.
Split a task further before implementation if it exceeds that size. Estimates
are provisional; platform-safe replacement and native close guards are the main
uncertainties. Checkboxes record actual status; unrun manual QA stays unchecked.
Since the user chose in-place writes, "replacement"/"temporary file" items below
are satisfied by the in-place contract (same inode, truncate, sync) instead.

| ID | Task | Est | Depends on | Status |
|---|---|---|---|---|
| FE-01 | Finalize contracts and filesystem safety decisions | 3 h | — | Done |
| FE-02 | Add edit eligibility and revision metadata to reads | 3 h | FE-01 | Done |
| FE-03a | Add protected-target and rooted path preflight | 3 h | FE-01 | Done |
| FE-03b | Add target identity, hard-link and Unix metadata checks | 4 h | FE-02, FE-03a | Done (in-place) |
| FE-03c | Implement revision-checked Unix replacement and locking | 4 h | FE-03b | Done (in-place) |
| FE-04 | Implement and verify Windows safe replacement | 4 h | FE-03 | Code done; Windows QA pending |
| FE-05 | Wire backend, RPC, desktop, capabilities, and mocks | 4 h | FE-03, FE-04 | Done |
| FE-06 | Add draft-aware tab state and save snapshot handling | 4 h | FE-02, FE-05 | Done |
| FE-07 | Integrate lazy-loaded CodeMirror and retained editor state | 4 h | FE-06 | Done; real-DOM QA pending |
| FE-08 | Add editing toolbar, shortcuts, and viewer transitions | 3 h | FE-07 | Done |
| FE-09 | Add dirty-tab and coordinated bulk-close guards | 3 h | FE-06, FE-08 | Done |
| FE-10 | Protect drafts across host transitions and disconnects | 4 h | FE-05, FE-06, FE-09 | Done; SSH/WSL QA pending |
| FE-11 | Guard native window close and normal app quit | 4 h | FE-09 | Done; native QA pending |
| FE-12 | Add conflict UI and targeted post-save refresh | 4 h | FE-05, FE-08, FE-09 | Done |
| FE-13 | Regression coverage and cross-platform QA | 4 h | FE-10, FE-11, FE-12 | Automated done; manual pending |
| FE-14 | Document behavior and complete release review | 2 h | FE-13 | Done; findings fixed |

Total: approximately 50 hours including platform hardening and QA; revise after
FE-01. Independent frontend/backend work can overlap once contracts are settled.

### FE-01 — Contracts and filesystem safety decisions

- [x] Define revision, eligibility/read-only reason, request, and structured
  success/conflict types, including how conflicts expose the observed revision.
- [x] Define byte-preserving editor serialization: BOM, LF/CRLF, empty files,
  final newline, and unsupported newline detection.
- [x] Audit reusable Go safe-write/path helpers, Windows replacement APIs,
  hard-link detection, ACL/metadata handling, and native window/quit hooks.
- [x] Define protected targets: project editing must not bypass existing rules
  against writes to tool session stores or credential files when a known
  directory is unusually broad. Identify enforcement points and tests.
- [x] Record concrete decisions here and split any oversized downstream task.

Done when: implementation contracts are unambiguous, no safety issue is silently
left to frontend validation, and unsupported filesystem cases have explicit
read-only/error behavior.

### FE-02 — Revision and eligibility metadata

- [x] Extend Go and frontend content types with revision and edit eligibility.
- [x] Compute revisions from the same bytes returned by a text read.
- [x] Describe text-format/size restrictions without weakening viewer support.
- [x] Connect path, protected-target, and platform eligibility checks; remove
  `safety_pending` only when the complete save safety policy is enforceable.
- [x] Test empty files, BOM, LF/CRLF, mixed endings, binary, size boundaries,
  invalid UTF-8, symlink paths, and unsupported/protected targets.

Done when: an eligible read carries enough metadata to open and serialize an
editor without an incidental content change; older responses remain viewable.

### FE-03a / FE-03b / FE-03c — Path safety and Unix safe saves

FE-03a preflight is implemented in `internal/files/edit_path.go`, with tests.
It is deliberately not authorization for a subsequent pathname-based write.

- [x] Add metadata-only `EditPolicy.Check`, protected names/configured roots,
  rooted component checks, and regression tests. Reject `..` components before
  normalization in targets, project roots, and policy roots; otherwise cleaning
  could conceal symlink traversal or protect the wrong configured directory.
- [x] Wire configured host-local roots into service read eligibility and saves.
- [x] FE-03b: implement rooted target opening/identity checks, hard-link refusal,
  and supported Unix permission/ownership/ACL/extended-attribute policy.
- [x] FE-03c: add bounded, revision-checked save support under `internal/files/`.
- [x] Revalidate target identity, path safety, eligibility, and permissions.
- [x] ~~Use a same-directory temporary file and safe replacement~~ Superseded by
  the chosen in-place write (same inode, truncate, sync); metadata is preserved
  because the inode is kept. No temporary files are created.
- [x] Serialize competing in-process saves to the same canonical target.
- [x] Test stale revisions, concurrent saves, deleted/replaced files, traversal,
  symlink components, hard links, no-op saves, cancellation, and failed writes.

Done when: successful saves return the exact new baseline/revision and failures
do not truncate the original or silently create a missing target.

### FE-04 — Windows safe saves

- [x] Implement Windows save semantics without a delete-then-write gap (in-place
  write of the existing file; code and cross-compile vet only).
- [ ] Verify ACL/read-only attributes, shared/open handles, path casing,
  junctions/reparse points, hard links, Unicode paths, and CRLF/BOM retention.
- [ ] Test failure cleanup and verify actual behavior on Windows, not only a
  cross-compile. Reuse platform test helpers for unsupported environments.

Done when: supported Windows targets satisfy the same safety contract as Unix;
unsupported cases fail clearly and keep the original intact.

### FE-05 — End-to-end save API

- [x] Extend backend interface, service known-directory checks, engine routing,
  app binding, RPC params/client/dispatcher, and offline implementations.
- [x] Add a save capability and hide/disable editing for older remote servers.
- [x] Update frontend API, browser mocks, test backends, and binding types.
- [x] Verify structured conflicts across both transports and bounded payloads.
- [x] Test local/remote routing, unknown directories, unsupported capability,
  disconnected behavior, and protected-target rejection on the backend.

Done when: local, SSH, and WSL use the same validated operation; remote saves
cannot accidentally write the equivalent path on the local host.

### FE-06 — Draft-aware tab state

- [x] Add baseline/revision, draft, dirty, editing, saving, and save-error state.
  Keep CodeMirror-specific objects out of serializable API data.
- [x] Promote preview tabs on edit and preserve drafts in per-directory state.
- [x] Make refresh, reopen, and asynchronous reads respect drafts and edits.
- [x] Save an immutable snapshot; typing while saving remains dirty relative to
  that snapshot. Undoing to the baseline clears dirty state.
- [x] Key pending operations by host, directory, tab identity, and generation;
  ignore stale responses without moving a draft to another target.
- [x] Test tab changes, read/save races, failed saves, refresh, and reopen.

Done when: navigation and refresh cannot silently replace an unsaved buffer.

### FE-07 — CodeMirror component

- [x] Add `FileEditor.svelte` with lazy-loaded CodeMirror 6 and a bounded set of
  lazy language modules; plain text is the fallback.
- [x] Add theme, line numbers, indentation, history, search, and wrap support.
- [x] Retain per-tab editor state/selection/scroll across switches and dialog
  hide/show; dispose it when a tab is discarded or genuinely closed.
- [x] Avoid mounting an editor DOM for every inactive tab; avoid rebuilding
  editor state from scratch on each keystroke.
- [x] Test mount/unmount cleanup, tab switching, undo, and draft synchronization.

Done when: editor state stays intact across navigation and the normal viewer
path does not eagerly load the editor bundle.

### FE-08 — Editing controls and shortcuts

- [x] Add Edit, Save, Discard, and Done editing with accessible labels and
  clear saving/error/read-only states. Empty files must expose Edit.
- [x] Route Cmd/Ctrl+S and Cmd/Ctrl+F correctly; prevent app/browser shortcut
  conflicts and preserve Alt+Z, Escape, dialog locking, and focus restoration.
- [x] Keep previews, presentation, and Git diff read-only; guard mode changes
  that leave a dirty editor. Do not render unsaved source as a saved-file diff.
- [x] Keep editing available after a successful save without losing history.

Done when: keyboard and mouse editing flows work without changing read-only
viewer behavior or silently discarding edits.

### FE-09 — Tab close coordination

- [x] Add dirty indicators, including accessible unsaved-state text.
- [x] Route close button, middle-click, Cmd/Ctrl+W, and all context-menu close
  actions through a common Save / Discard / Cancel coordinator.
- [x] Preflight bulk actions with one coordinated decision; cancel leaves tabs
  open, failed saves retain affected drafts, and no prompt overlaps another.
- [x] Reuse the coordinator for Done editing and discard confirmation.

Done when: every tab-removal path is guarded and tested, including inactive tabs.

### FE-10 — Host lifecycle and uncertain saves

- [x] Guard explicit host switches and resets that would clear drafts,
  including drafts in cached directories; cancel preserves the current context.
- [x] Preserve drafts on unexpected disconnect and prevent writes to a newly
  selected host using an old tab's path/revision.
- [x] Handle reconnect and stale connection generations without applying late
  save/read replies to the wrong state.
- [x] Reconcile unknown save outcomes against the submitted snapshot without
  automatic replay; later typing must remain dirty.

Done when: a disconnect or host transition cannot lose or misroute a draft.

### FE-11 — Native close and quit guards

- [x] Connect draft protection to actual Wails window-close and normal quit
  paths, not only browser `beforeunload`.
- [x] Coordinate Save / Discard / Cancel across all cached drafts; save failure
  or cancellation keeps the window open. Handle repeated close requests.
- [x] Provide appropriate browser-development unload protection and clean up
  listeners. Do not promise protection against forced termination.
- [ ] Verify shortcut quit, window close, and normal application quit on the
  supported desktop platforms; account for multiple app windows.

Done when: ordinary native exits cannot silently discard drafts, and clean
windows close without an unnecessary prompt.

### FE-12 — Conflict handling and saved-file refresh

- [x] Add conflict UI retaining the draft, with Reload, Overwrite…, and Cancel.
- [x] Confirm destructive reload/overwrite; retry overwrite against the observed
  revision. Another disk change must conflict again.
- [x] Keep deleted/replaced/protected targets out of the overwrite path.
- [x] On successful save, update the exact submitted baseline and targeted Git
  metadata; do not reread dirty tabs or overwrite edits made during the save.
- [x] Distinguish save success from a subsequent Git refresh failure.

Done when: conflicts are explicit, recoverable, and tested across RPC and local
bindings; disk refresh failures do not misreport a completed write as failed.

### FE-13 — Regression and platform QA

- [x] Run Go tests, frontend tests, Svelte checks, and applicable lint/build
  checks; add regression tests for the complete edit/save lifecycle.
- [ ] Verify source/Markdown/SVG viewing, images, find, wrapping, Git diff,
  presentation, dialog lock, terminal-file opening, and tab context actions.
- [ ] Manually verify local macOS/Linux/Windows and SSH/WSL edit/save flows,
  stale conflicts, permissions, disconnect during save, and native close guards.
- [ ] Check editor load/bundle cost, responsiveness near the edit limit, and
  cleanup when many tabs/directories are opened and closed.
- [x] Record actual checks and untested platform cases; do not mark unrun QA as
  passing. Split extended QA if the task exceeds four hours.

Done when: acceptance scenarios pass on tested platforms and any remaining
platform gaps are explicit release blockers or documented limitations.

### FE-14 — Documentation and release review

- [x] Update README with editing shortcuts, explicit-save behavior, eligibility,
  conflict handling, remote compatibility, and draft-loss limitations.
- [x] Record final contracts, deviations, task status, and QA evidence here.
- [x] Review changes for accidental credential/body logging, writes outside the
  project boundary, silent draft loss, and unsupported safety claims.

Done when: documentation matches shipped behavior and all unresolved blockers
are identified before release.

## Acceptance scenarios

1. Edit an empty file or a source/config/Markdown/SVG text file, save locally or
   remotely, and verify exact disk bytes and updated Git status.
2. Switch tabs/directories, hide/reopen the dialog, and refresh without losing
   draft text, undo history, cursor, or selection.
3. Close one/many dirty tabs or attempt host switch/quit: Save / Discard / Cancel
   works; save errors never discard the remaining drafts.
4. Let another process modify the file: ordinary save refuses the stale
   revision; reload and confirmed revision-checked overwrite both work.
5. Type during a save: successful response acknowledges only the submitted
   snapshot, leaving subsequent edits dirty.
6. Disconnect during a remote save: preserve the draft, never write locally,
   and reconcile the result after reconnect without blindly replaying it.
7. Read-only, oversized, unsupported-encoding/newline, link, and protected files
   remain viewable where previously supported but cannot bypass backend checks.
8. Older remote servers still support viewing without advertising editing.
