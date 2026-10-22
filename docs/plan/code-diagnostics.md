# Go to File — project code diagnostics

Status: **19/28 complete**: CD-01…CD-08, CD-10…CD-16 and CD-18…CD-21 are complete (contract,
backend trust/checkers/manager, Engine/App bindings, frontend API/store, local approval/check/status controls, draft-safe Problems navigation, shared file/tree diagnostic badges, verified source/editor indicators and deterministic Stage 1 integration tests).
CD-09 RPC contracts/tests are committed, but authenticated production SSH/WSL
identity activation remains **blocked**. CD-10 proceeded locally with an explicit
dependency exception; it does not complete CD-09 or activate remotes.
CD-17 is **partial / blocked**: [non-GUI preflight and blocked native QA matrix](code-diagnostics-qa.md), with **Stage 1 HOLD**; CD-22 is **partial** ([gopls spike/adapter seam](code-diagnostics-gopls.md), automated tests only; not counted in progress, no production route or manual QA); CD-23 is **partial / blocked** ([TypeScript adapter seam](code-diagnostics-typescript.md); no approved installed LSP server, synthetic tests, not counted); CD-24 is **partial / blocked** ([Svelte adapter seam](code-diagnostics-svelte.md); no Svelte language server verified, synthetic tests, not counted); CD-25 is partial (read-only revision reconciliation and fake-session guards; external rename/delete reconciliation and production wiring incomplete, not counted); CD-26 is partial ([fake lifecycle integration evidence and blockers](../tasks/CD-26.md); live retention/ownership/status and manual UI smoke gaps remain, not counted); CD-27 is partial/blocked ([live QA matrix](code-diagnostics-live-qa.md); focused fake preflight only, all live cells blocked/untested, Stage 2 HOLD, not counted); CD-28 is blocked: Step 1 release prerequisites unmet, both gates HOLD; no dependency exception, no automation implemented/enabled, off by default. CD-21's frontend document lifecycle bridge is inert unless `liveCodeDiagnostics` is advertised (never yet in production); the Stage 1 hold, CD-09 and CD-17 do not block it (explicit dependency exception, as for CD-20). Local dialog controls are wired with asynchronous
identity/capability discovery; unsupported remotes expose status only, never execution controls.
Manual keyboard/focus QA and all CD-17 native matrix checks have not been performed.
Frontend policy/evidence: [CD-10](code-diagnostics-frontend.md). Trust policy and approval copy: [CD-02 addendum](code-diagnostics-trust.md).
Execution semantics: [CD-03 supervisor](code-diagnostics-supervisor.md).
Version/invocation evidence and limitations: [CD-04 spike](code-diagnostics-checkers.md).
Go adapter policy and validation: [CD-05 evidence](code-diagnostics-go-vet.md).
Svelte adapter policy and validation: [CD-06 evidence](code-diagnostics-svelte.md).
Manager lifecycle, limits and validation: [CD-07 evidence](code-diagnostics-manager.md).

Authoritative shared contracts: [code-diagnostics-contract.md](code-diagnostics-contract.md).
Operation names and wire semantics there supersede provisional examples below.

## Goal

Provide Zed-like error and warning feedback when Go to File opens a project:
fast dialog opening, file/folder badges, a project summary, navigable problems,
and inline diagnostics in both the source viewer and editor. Keep Go to File
lightweight; this is not a commitment to build a full IDE.

This extends the diagnostics/LSP exclusion in [file-editing.md](file-editing.md).
It does not replace the existing session-provider parsing diagnostics exposed by
`Backend.Diagnostics()`; use distinct code-diagnostics names and types.

## Delivery stages

1. **Project checks:** shared backend/store contracts, trust approval, an explicit
   Check Project action, Problems panel, badges, and source/editor markers.
   Start with `go vet` and `svelte-check` adapters, covering this repository's
   backend and frontend. Each adapter is independently available or unavailable.
2. **Live diagnostics:** backend-owned language servers, initially `gopls`, then
   TypeScript and Svelte servers. Analyze opened documents, including unsaved
   drafts; start services on dialog open only after project approval.
3. **Broader coverage:** opt-in automatic project checks on open/save, followed
   by additional checker adapters (e.g. golangci-lint, ESLint, Ruff) and languages.

Stage 1 is an explicit-check release, not yet the complete automatic-on-open
experience. Stage 2 supplies live feedback; Stage 3 adds opt-in project-wide
checking on open. Do not silently imply all files were analyzed by a language
server. Code completion and symbol navigation, formatting, quick fixes, tool installation,
remote WebSockets, and arbitrary custom shell commands are outside this plan.

## User experience

### Opening and checking

- Open the dialog immediately; no tool discovery or process startup on the
  critical rendering path. Discover supported configurations asynchronously.
- Before approval, show “Enable code checks for this project”. Explain that
  language servers/checkers can execute project configuration and plugins.
- Offer Enable for this session, Always allow this project, and Not now.
  Approval is scoped to host identity and canonical project root, not its label.
- With approval, Stage 1 exposes Check Project; Stage 2 starts relevant language
  services in the background. Never install or download a missing tool.
- Distinguish disabled, approval required, unsupported, tool missing, starting,
  checking, idle, partial, failed, disconnected, and stale results. A successful
  check with findings is not a tool failure.
- The summary reads, for example, “3 errors · 7 warnings”, with checking/stale
  and coverage status nearby. “No reported problems” must not mean “all clean”.
- Cancel stops a running project check; Retry retries a failed provider.

### Display and navigation

- File badges show error/warning counts; folders aggregate their descendants
  without double-counting. Keep Git status visible and independent.
- A collapsible Problems panel groups by file and supports severity filtering,
  keyboard navigation, and next/previous problem actions.
- Each problem shows severity, message, source, optional code, and location.
  Clicking opens the file and selects/reveals the diagnostic range.
- File-level problems without a range still appear in the panel and badges.
- Source view shows gutter indicators and range highlighting/messages; the
  editor uses `@codemirror/lint` with the same normalized diagnostic data.
- Render messages as text, never HTML. Severity must not rely on color alone;
  controls and diagnostic messages must be accessible by keyboard.
- Diagnostic navigation must preserve drafts and obey existing tab/close guards.
- Git diff, blame, rendered Markdown, and images do not receive source-coordinate
  overlays. Navigate to source for a problem in a previewed file.

## Architecture and integration

```text
GoToFileDialog / FilePicker / FileViewer / ProblemsPanel
                        ↕
          CodeDiagnosticsStore (Svelte)
                        ↕
       api.ts → App → Backend → engine service
                        ↕
       internal/codediagnostics manager (on project host)
                  ├── checker adapters
                  └── language-server sessions
```

Use a separate frontend `codeDiagnostics.svelte.ts` store, rather than adding
process/diagnostic state to `files.svelte.ts`. The files store remains the owner
of tabs, drafts, revisions, and selected directory; publish explicit lifecycle
hooks into diagnostics. Proposed backend package: `internal/codediagnostics`.

Integration points:

- `frontend/src/lib/components/common/GoToFileDialog.svelte`: attach/detach,
  summary, approval/status controls, Problems panel layout.
- `frontend/src/lib/components/common/go-to-file/FilePicker.svelte`: badges.
- `.../FileViewer.svelte`, `SourceView.svelte`, `FileEditor.svelte`, and
  `frontend/src/lib/fileEditor.ts`: navigation and diagnostic decorations.
- `frontend/src/lib/api.ts` and `types.ts`: transport-neutral contracts.
- `internal/engine/{backend,service,engine}.go`: context-first routing and
  known-directory authorization before any discovery or execution.
- `internal/app/`: Wails bindings, events, offline behavior, application shutdown.
- `internal/rpc/`: capability negotiation, operations, scoped notifications,
  disconnect cleanup, and compatibility with older servers.

Local, SSH, and WSL checks run where the project files live, using that host's
installed tools. Do not copy projects locally or tunnel a frontend LSP client.

## Shared contracts (finalize in CD-01 before implementation)

### Project and result identity

- Project key: backend connection identity + canonical authorized root.
- Every attachment has an opaque session token and generation. Reconnect,
  revocation, restart, or project replacement invalidates older generations.
- Every provider run has a run ID. Documents have monotonic versions plus a
  saved revision when relevant. Requests/events carry enough identity to reject
  late results from another host, attachment, run, or document version.
- Root discovery may find nested Go modules or frontend packages, but must stay
  inside the authorized root. Do not silently expand a session subdirectory to
  a parent repository or open arbitrary additional LSP workspace folders.

### Diagnostic model

Normalize into project-relative path, optional range, severity
(error/warning/information/hint), message, source, optional code, provider ID,
origin (checker/LSP), and freshness metadata. Ranges use zero-based lines and
UTF-16 columns, with exclusive ends, matching CodeMirror's string indexing.
Adapters convert byte/code-point/one-based locations. Negotiate LSP UTF-16 or
explicitly convert the negotiated encoding; test CRLF and non-BMP Unicode.

Checker and server data are separate provider-owned result sets. A successful
checker run atomically replaces that provider's prior set, including an empty
set; failed/canceled runs retain old results marked stale. LSP publish replaces
one document's set, including empty publications. Clear deleted files, closed
server sessions, revoked projects, and invalidated generations explicitly.

Deduplicate only exact equivalent findings across providers (path, range,
severity, message, code), preserving source attribution. Similar findings are
not necessarily duplicates. Default counts cover error/warning severities.

### Operations and events

Proposed operations (names are provisional): attach/detach project diagnostics,
get snapshot, approve/revoke checks, run/cancel project check, and
open/change/close diagnostic document. Save is coordinated through document
change plus didSave, not a second independent text owner.

Snapshots/events include provider availability, coverage, run state, result
sets, and sequence/generation IDs. Attach plus snapshot/sequence reconciliation
must avoid an initial snapshot racing newer events. Use bounded batches within
RPC frame limits; signal truncation/partial results and allow resnapshot after
sequence gaps. Never silently drop overflow and report success.

Add separate remote capability flags for project checks and live diagnostics.
An old remote server simply omits them; hide unsupported controls without
breaking browsing/editing. No new feature should reuse provider.Diagnostics.

## Project checker execution

- Adapters define detection, fixed executable/argv, working directory, tool
  version support, output parsing, accepted diagnostic exit codes, and coverage.
- Stage 1 adapter spikes must verify installed-version output formats and
  nonzero-exit behavior before implementing production parsers. `go vet` is not
  a complete Go lint suite; Svelte coverage depends on package configuration.
- Do not use shell interpolation, `npx` downloads, package-script execution, or
  implicit dependency installation. Report missing dependencies as unavailable.
- Resolve approved executables on the project host; project-local binaries are
  executable project content and require the same trust approval.
- Disk checks analyze saved files only. Label their results “saved files”; if
  a tab is dirty, do not place those ranges over the draft. Keep them in the
  panel with stale/saved-file context. Never save drafts to run a checker.
- Start with one checker process per project and two per backend. Coalesce
  repeated requests; superseded runs cannot publish over the latest run.
- Proposed defaults: 120 s checker timeout, 10 MiB captured output, 10,000
  findings per project. Bound stderr and individual messages too. Expose partial
  status when limits are hit; validate limits against actual RPC framing.
- Checkers may invoke toolchains that write build caches. “Read-only checks”
  means no app-requested source fixes, not sandboxing or a no-write guarantee.

## Language-server execution

- One managed session per authorized root + server configuration on each host;
  reuse it across tabs. Initially limit to two active projects per backend.
- Implement bounded stdio JSON-RPC framing and requests, initialize/initialized,
  shutdown/exit, cancellation, timeouts, and required server-request responses.
  Existing app RPC framing must not be assumed compatible with LSP framing.
- Stage 2 baseline is push diagnostics. For each initial server, verify that
  supported versions publish diagnostics; pull-only servers are unsupported
  until a later adapter implements pull diagnostics and refresh handling.
- Explicitly deny workspace/applyEdit and server-initiated commands. Supply only
  allowlisted configuration responses and reject workspace expansion outside
  the approved root. No automatic code actions or source edits.
- didOpen sends the actual displayed text; didChange sends debounced drafts
  (initial target 300 ms), respecting negotiated full/incremental sync.
  Read-only opened files also participate. No hidden opening of every file.
- Save, discard, reload, rename, delete, and tab close must reconcile document
  text/version/URI consistently with the files store. Never overwrite drafts.
- Versioned publications are accepted only for the matching document snapshot.
  For unversioned publications, never claim verified draft freshness: invalidate
  ranges on edits and show uncertain results separately until a server-specific
  synchronization strategy is tested. Avoid misleading stale underlines.
- Disk changes caused by other tools/agents are reconciled on dialog reopen,
  explicit refresh, and before rerunning checks. Stage 2 must either support
  bounded server file-watching registrations or document their absence and
  mark unopened-file coverage stale; do not claim continuous workspace sync.
- Hide dialog: retain services briefly (proposed 2-minute idle grace), then stop.
  Reopen during grace reuses them and reconciles changes. Detach idle projects
  under the active-project limit without dropping frontend drafts.
- Disconnect, backend switch, approval revocation, and app shutdown stop owned
  services/checkers and invalidate publications. Kill child process trees using
  platform-specific handling; cap restart attempts and offer explicit retry.

## Trust and privacy

Directory authorization and execution approval are separate checks. Persist only
explicit “Always allow” grants, keyed by stable host identity + canonical root.
Session grants expire on disconnect/app exit. Revocation stops work immediately.
Changes to approved executable/configuration identity require renewed approval
before automatic execution; define the exact fingerprint in CD-02.

This is not a sandbox: even fixed tools can execute repository plugins or read
referenced dependencies outside the root. Explain that at approval time. The
app itself validates diagnostic paths/URIs and does not reveal out-of-root file
contents; out-of-root findings become provider status, not navigable file links.

Document bodies and raw protocol traffic must not be logged or persisted.
Diagnostic messages can contain source excerpts or secrets: keep results in
memory, avoid automatic telemetry, and sanitize/bound stderr shown in errors.
Do not auto-send draft bodies to external services; this plan uses host-local
processes only. Installation and network behavior of third-party tools remain
part of the approval warning, not a guarantee the app can enforce.

## Task breakdown

CD-01…CD-08, CD-10…CD-16 and CD-18…CD-21 are complete; CD-09 is blocked on production remote identity activation; CD-17 partial/blocked; CD-22, CD-23 and CD-24 partial (not counted); CD-25 is partial (read-only revision reconciliation and fake-session guards; external rename/delete reconciliation and production wiring incomplete, not counted); CD-26 is partial ([fake lifecycle integration evidence and blockers](../tasks/CD-26.md); live retention/ownership/status and manual UI smoke gaps remain, not counted); CD-27 is partial/blocked ([live QA matrix](code-diagnostics-live-qa.md); focused fake preflight only, all live cells blocked/untested, Stage 2 HOLD, not counted); CD-28 is blocked: Step 1 release prerequisites unmet, both gates HOLD; no dependency exception, no automation implemented/enabled, off by default. CD-10's local contract/test dependency exception is explicit above. Estimates include focused automated tests; each task
is at most four developer hours. Re-estimate adapter spikes before committing
to server-version support. Split tasks that exceed four hours before coding.
The [TASKS.md](../../TASKS.md) feature section tracks these IDs and status.
Actual totals: **28 = 19 complete + 7 partial + 2 blocked + 0 not started**
(partial: CD-17, CD-22…CD-27; blocked: CD-09, CD-28).
The [remaining-work backlog](../tasks/code-diagnostics-remaining.md) splits
CD-22…CD-26 gaps into ≤4h packages and separates offline coding from approved
installed-tool/native/remote prerequisites. It grants no dependency exception,
installation, capability activation or automation. Both release gates remain HOLD.
Individual implementation briefs, test cases, and acceptance checklists are in
[docs/tasks](../tasks/README.md).

| ID | Stage | Task / done when | Est | Dependencies |
|---|---|---|---|---|
| CD-01 | 1 | Finalize diagnostic/range, identity, replacement, coverage, operation/event contracts; JSON fixtures and race cases specified | 3 h | — |
| CD-02 | 1 | Define trust identity/fingerprint and UX; implement session/persistent grants, revocation, and path authorization tests | 4 h | CD-01 |
| CD-03 | 1 | Checker supervisor with argv-only execution, bounds, cancellation, process-tree cleanup, and fake-process tests | 4 h | CD-02 |
| CD-04 | 1 | Spike go vet/svelte-check versions and formats; commit sanitized output/exit-code fixtures and supported-version decisions | 3 h | CD-01 |
| CD-05 | 1 | Go vet adapter detects contained modules, parses fixtures, distinguishes findings from failures | 3 h | CD-03, CD-04 |
| CD-06 | 1 | Svelte-check adapter detects contained packages, parses fixtures, reports missing dependencies/partial coverage | 4 h | CD-03, CD-04 |
| CD-07 | 1 | Backend manager owns provider result sets, run generations, snapshots, limits, and stale transitions | 4 h | CD-01, CD-03 |
| CD-08 | 1 | Engine/App bindings and offline behavior enforce known-directory and trust checks | 3 h | CD-02, CD-07 |
| CD-09 | 1 | RPC capabilities, operations, scoped bounded events, disconnect cleanup, and old-server tests | 4 h | CD-08 |
| CD-10 | 1 | Frontend store/API reconcile snapshots, events, host switches, generations, and truncation using mocks | 4 h | CD-01, CD-09 |
| CD-11 ✅ | 1 | Local approval controls, Check/Cancel/Retry, provider availability, summary and coverage status | 3 h | CD-10 |
| CD-12 | 1 | ✅ Problems panel with grouping/filtering, keyboard access, and range navigation preserving drafts | 4 h | CD-10 |
| CD-13 | 1 | ✅ Tree badges/folder totals and shared exact-deduplication semantics tested (403 frontend tests; native QA deferred) | 3 h | CD-10 |
| CD-14 | 1 | ✅ Verified source gutter/range indicators and plain-text file/stale messages tested (409 frontend tests; native paint/layout QA deferred) | 4 h | CD-10 |
| CD-15 | 1 | ✅ Lazy CodeMirror lint, shared verified UTF-16 ranges, cached-tab and draft freshness guards (412 tests; check/build passed; native QA deferred) | 4 h | CD-10 |
| CD-16 | 1 | Integration tests for findings/empty/failure/cancel, dirty drafts, output caps, and stale event races | 4 h | CD-05…CD-15 |
| CD-17 | 1 | 🟡 Partial/blocked: [non-GUI preflight and unperformed native matrix](code-diagnostics-qa.md); Stage 1 HOLD | 4 h | CD-16 |
| CD-18 | 2 | ✅ Bounded LSP stdio transport with framing/request/cancel tests using a fake server (local transport only; no adapters) | 4 h | CD-07 |
| CD-19 | 2 | ✅ LSP initialization, sync negotiation, safe request handling, shutdown/restart/idle lifecycle (fake server/process/clock; no adapters, no live capability) | 4 h | CD-18 |
| CD-20 | 2 | Versioned document open/change/save/close operations across Backend/App/RPC, capability and size tests | 4 h | CD-09, CD-19 |
| CD-21 | 2 | Frontend document lifecycle bridge for read/edit/save/discard/reload/rename/delete/close | 4 h | CD-15, CD-20 |
| CD-22 | 2 | gopls adapter/version spike, workspace detection, push diagnostics and freshness tests | 4 h | CD-19, CD-21 |
| CD-23 | 2 | TypeScript server adapter/version spike, package roots, executable discovery and diagnostic tests | 4 h | CD-19, CD-21 |
| CD-24 | 2 | Svelte server adapter/version spike, TS ownership rules and diagnostic tests | 4 h | CD-23 |
| CD-25 | 2 | Reopen/refresh/external-change reconciliation, file-watch support or explicit stale fallback | 4 h | CD-22…CD-24 |
| CD-26 | 2 | End-to-end fake-server tests: Unicode, versions, clear events, crashes, disconnects, limits and cleanup | 4 h | CD-21…CD-25 |
| CD-27 | 2 | 🟡 Partial/blocked: [live QA matrix](code-diagnostics-live-qa.md), focused fake preflight only; no real-server/native/remote QA or resource measurements; Stage 2 HOLD | 4 h | CD-26 |
| CD-28 | 3 | ⛔ Blocked: both release gates HOLD; all acceptance unchecked; no dependency exception or automation, off-default | 4 h | CD-17, CD-27 |

Later adapters/languages get separate scoped tasks after Stage 1/2 feedback;
they are not implicitly included in CD-28. Frontend CD-11…CD-15 can proceed in
parallel against the CD-01 contract and CD-10 mocked store.

## Release acceptance criteria

### Stage 1

- Dialog renders without waiting for checks; no process runs without approval.
- Supported installed checkers produce navigable errors/warnings for unopened
  files within their declared scope. Missing tools/failure/partial/truncation are
  visibly different from a successful check with no reported problems.
- Successful reruns remove fixed findings; failed/canceled/superseded runs do
  not incorrectly clear or replace newer results.
- Badges, panel counts, and inline markers agree. Dirty drafts never get saved
  by a check or decorated with saved-file ranges as if current.
- Local/remote behavior matches; old servers continue to browse/edit normally.
- Tests cover malicious paths, hostile messages, unauthorized directories,
  revoked grants, process cancellation, event races, Unicode, and resource caps.

### Stage 2/3

- Approved supported projects start relevant services asynchronously on open.
- Opened-file syntax/type/lint feedback updates on edits according to the
  selected server's capabilities, without losing draft or undo state.
- Opening/closing repeatedly does not leak processes, duplicate servers, or
  apply findings to another project/host. Tool crashes do not crash the app.
- Project-wide and opened-file coverage remain distinguishable, including
  unsupported file watching and unversioned-publication freshness limitations.
- Stage 3 automatic checker execution is opt-in and never makes dialog opening
  synchronous. No continuous full-project lint process runs on each keystroke.

## Decisions to validate before implementation

The defaults above form the proposed baseline, not claims of existing support.
CD-01/CD-04 and the server spikes must confirm:

- Supported tool versions and installed-binary discovery on GUI-launched hosts.
- Multi-module/monorepo discovery limits and TS/Svelte server ownership.
- Trust fingerprint scope and user-visible handling of configuration changes.
- Performance/resource defaults against a large repository and remote latency.
- Which servers support reliable publication freshness and file-watch behavior.
