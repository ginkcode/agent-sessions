# Code diagnostics — remaining-work split

> **Update 2026-10-23:** the [editor (Zed) model](../plan/code-diagnostics-zed-model.md) drops the version pins of R11/R12, discovers servers in the project, the login-shell PATH and app-managed installs (gopls, Ruff, vtsls with one Install click), and implements SSH/WSL diagnostics with a host-side identity. R18 now needs only real SSH/WSL QA; no real remote session or managed download has been run.

Date: 2026-10-22. **R01 audit, R02 concrete ownership and R03 URI isolation complete; R04 offline discovery/trust seam and R05 internal owner route and R06 bounded live retention complete; R07 current live UI eligibility and R08 failure/recovery/coverage UI complete; R09 contained disk probe/contract complete; R10 probe-driven reconciliation wiring complete (offline); R14 full-route fake-attachment tests complete (offline; frontend leg is a Go-recorded replay); R15 owned cleanup instrumentation/repeated lifecycle tests complete (offline; no leak found); R11 complete locally 2026-10-23 (installed gopls v0.24.0 through the production local route; remote/native not covered); R12 partial 2026-10-23 (vtsls verified through the Go adapter and wired into local discovery; no approved install on this host, no manual QA); Python live diagnostics via Ruff 0.15.20 added 2026-10-23 outside the R-plan ([evidence](../plan/code-diagnostics-python.md); real-server smoke through the Go adapter, detected on this host, no manual QA); R13 environment-blocked (no approved Svelte server); R16–R20 not started.** Parent progress remains **19/28**:
19 complete, 7 partial (CD-17, CD-22…CD-27), 2 blocked (CD-09, CD-28), 0 untouched
not-started tasks. Partial/blocked parents are not counted complete. These package
IDs subdivide remaining acceptance work; they do not add to the 28-task denominator.

This supplies the follow-up split required by [CD-22](CD-22.md#not-met--not-claimed)
and the [task split rules](README.md#how-to-use-these-briefs). Each estimate includes
focused tests/evidence and is **≤4 developer hours**. Re-estimate/split before coding
if a package exceeds its cap; never omit acceptance to fit. Full regression and
native evidence have separate packages. **R01 is complete (audit/decision only); R02
is complete (offline ownership seams/tests); R03 is complete (offline conservative URI isolation/tests); R04 is complete (offline discovery/trust seam/tests); R05 is complete (internal Engine/session owner route, offline tests; production disabled); R06 is complete (bounded live normalization/retention, offline tests); R07 is complete (offline current live source/editor eligibility and navigation tests); R08 is complete (offline failure/recovery/coverage UI); R09 is complete (bounded read-only saved-disk observation contract/probe); R10 is complete (offline reconciliation wiring); R14 is complete ([evidence](../plan/code-diagnostics-r14.md)); R15 is complete ([evidence](../plan/code-diagnostics-r15.md)); R11 is complete locally; R12 is partial (no approved install, no manual QA); R13 is blocked on an approved Svelte server; R16–R20 are not started.** The [R01 ownership audit](../plan/code-diagnostics-ownership-audit.md)
is authoritative for R02/R03 ownership and conservative reused-URI freshness policy;
its exposed bugs are not implemented fixes or passing regression claims.

**UX note (2026-10-23):** the Go to File diagnostics UI was simplified to a compact
bottom status with disclosed details and settings, and file names now turn red for
any severity ([evidence](../plan/code-diagnostics-frontend.md#compact-go-to-file-status-ux-simplification-2026-10-23)).
This is a layout change only. It completes no R-package or parent task, progress
stays **19/28**, and native QA remains CD-17.

## Execution boundaries

- **Code** means safe independent, offline coding/fake tests against existing seams
  and dependencies, not a claim that a partial parent dependency is done. No new
  dependency exception or production activation is approved here. Assign/review
  scoped work explicitly; keep live capability unadvertised and automation absent.
- **Approval/environment** means a real prerequisite for the named execution or
  sign-off, not a reason to stop unrelated local fixes. The user subsequently approved
  implementing [explicit consent-based managed installation](tool-installation.md);
  that independent backlog narrowly supersedes the blanket no-download policy only
  after actual host-scoped Install consent. No real install is authorized by an
  offline coding task or execution approval. Missing tools stay unavailable until
  separately installed; private editor caches are not approved
  production tools. Obtain explicit approval for preinstalled executables/config
  execution and disposable fixtures before real-server runs.
- [CD-17 Stage 1 HOLD](../plan/code-diagnostics-qa.md#release-decision-and-remaining-work)
  and [CD-27 Stage 2 HOLD / P1–P7](../plan/code-diagnostics-live-qa.md#prerequisites--release-blockers)
  remain unchanged. Mock passes are not native, real-server or remote evidence.

## Prioritized coding packages

| ID / parent | Est. | Prerequisite / boundary | Work and verifiable exit |
|---|---|---|---|
| R01 / CD-22, CD-24, CD-26 | 3 h | **Complete 2026-10-22; audit only** | [Ownership map, code-trace evidence, R02/R03 policy and deterministic schedules](../plan/code-diagnostics-ownership-audit.md). Concrete identity loss and same URI/version late-push false freshness exposed; no fixes/activation claimed. |
| R02 / CD-22…CD-24, CD-26 | 4 h | **Complete 2026-10-22; offline only** | Concrete session-envelope provider + bound document provider/policy survive publication and exact-key replacement/snapshots/events. `live_ownership_test.go` covers concurrent checker + Go/TS and TS/Svelte, wrong-owner findings/empties, reverse order, equivalent attribution and session reuse. [Evidence/caveats](CD-26.md#r02-concrete-ownership-2026-10-22); R03 ambiguity remains, no activation. |
| R03 / CD-25, CD-26 | 4 h | **Complete 2026-10-22; offline only** | Transport-lifetime canonical URI history rejects ambiguous findings and empties; bounded history exhaustion fails closed for every path publication until a new session. Deterministic same URI/v1 reopen, identical/different text, repeated paths, reapproval/new attachment owner, crash/retry/old generation, alias and budget tests pass. [Evidence/caveats](CD-26.md#r03-conservative-uri-isolation-2026-10-22). No activation or R04 work. |
| R04 / CD-22…CD-24 | 4 h | **Complete 2026-10-22; offline only** | Production-callable bounded `DiscoverLive`, live metadata registration and contained `DiscoveryDocuments` resolver; same exact attachment-wide checker + multi-live approval with current-root/fingerprint preflight and exact selected-policy membership. Fake launches prove zero starts before composite approval/current identity and independent provider availability. [Evidence/caveats](../plan/code-diagnostics-r04.md). No Engine activation, real tool run or R05 lifecycle wiring. |
| R05 / CD-22…CD-25 | 4 h | **Complete 2026-10-22; internal/offline only** | Engine internal composite discovery + LiveDocuments session owner; openedDocuments snapshots/notices, displayed draft sync/reopen, stale retained failures, revoke/reset/idle teardown. Real Manager/session framed fake and Engine failing-launch route tested. [Evidence and route limits](../plan/code-diagnostics-r05.md). Production constructor remains checker-only and live advertisement disabled; full Engine/App/RPC route remains R14. |
| R06 / CD-26 | 4 h | **Complete 2026-10-22; offline only** | Bounded live normalization and sorted prefix selection before retention; attachment-wide 10k / 7 MiB findings and 8 MiB escaped snapshot cap including document identities. Atomic adapter loss/shortening status and provider truncated/partial notices; hostile escaping, oversized batches/fields/ranges, many-document/provider, empty/failure and actual JSON event envelope tests. [Evidence/caveats](CD-26.md#r06-bounded-live-retention-2026-10-22). No cap increases, live activation or R07/UI work. |
| R07 / CD-22…CD-24, CD-26 | 4 h | **Complete 2026-10-22; offline frontend only** | Current approved live bridge identity reaches source/editor projection and async navigation; typing/debounce/revoke/host interruption invalidate immediately. Actual store/SourceView SSR/editor state/controller/navigation tests distinguish saved checker, current live and uncertain/unversioned/wrong document/version/URI data; UTF-16/multiline/EOF and lazy editor/cache preserved. [Evidence/caveats](../plan/code-diagnostics-r07.md). Native paint/undo remains R16; no activation or R08. |
| R08 / CD-22…CD-24, CD-26 | 3 h | **Complete 2026-10-22; offline frontend only** | Actual store/CodeChecks/Problems/source/editor failure retention and partial/truncation propagation; redacted bridge sync failures with bounded current-approval/fresh-document recovery, checker-only retry and honest same-URI/session limits. Hostile text escaped, unavailable peers do not block drafts/browsing; superseded promises guarded. [Evidence/caveats](../plan/code-diagnostics-r08.md). Production live flag false; R09 untouched. |
| R09 / CD-25 | 3 h | **Complete 2026-10-22; contract/probe only, after ownership review** | Bounded catalog-first contained `probeFiles` across files/Engine/App/RPC; explicit present/leaf-absent/unknown, raw-byte content revisions (not mutation identities), sanitized failures and host-generation guards. Backend/framed RPC/fake API delete/recreate/inaccessible/escape tests; existing dirty/uncertain tabs and `unwatched` fallback unchanged. [Evidence/limits](../plan/code-diagnostics-r09.md). R10 lifecycle wiring not implemented; live flag false. |
| R10 / CD-25, CD-26 | 4 h | **Complete 2026-10-22; offline only.** [Evidence/limits](../plan/code-diagnostics-r10.md) | Wire authoritative external delete/rename/recreate into reopen/refresh/pre-check, clear deleted checker/live sets and reject old URI/generation pushes. Tests cover same-path recreation, refresh races, dirty/uncertain drafts and late reads; failed reads never guess deletion or overwrite text. |
| W01 / CD-25 | 4 h | **Complete 2026-10-23; local only.** [Evidence/limits](../plan/code-diagnostics-project-watch.md) | User-requested revision of the "no watchers" policy. Adds an approved, bounded, local fsnotify project watcher and the in-process `go-syntax` saved-file provider, so unopened `.go` syntax errors show in counts and red names. Running gopls gets contained `didChangeWatchedFiles`. Clean tabs reconcile read-only. Fake plus real-tempdir fsnotify tests. 2026-10-23 fix: approval was blocked in the Finder-launched app (minimal PATH hid mise's Go), so discovery now searches mise's Go install and go-syntax has an analyzer-only approval ([fix](../plan/code-diagnostics-project-watch.md#fix-disabled-approval-buttons-in-the-app-2026-10-23)). |

Follow-ups from W01 (not started, each ≤4h):
- W02: saved-file syntax for TS/Svelte through an in-process parser. This needs an approved dependency; none is installed.
- W03: refresh `go vet` or gopls type diagnostics for unopened files after watched saves. This needs a debounced, approved checker trigger, which overlaps the CD-28 gate.
- W04: let go-syntax work without an executable checker proposal (a standalone approval surface).
- W05: demo/mock support for `watching`, plus manual and native UI QA of the watcher (R16/R17).
- W06: remote host watching once CD-09 identity is active.

## Verified tools, full-route tests and cleanup

| ID / parent | Est. | Prerequisite / boundary | Work and verifiable exit |
|---|---|---|---|
| R11 / CD-22 | 3 h | R04/R05; approval/environment | Run approved preinstalled gopls through Go adapter (existing v0.24.0 Python spike is insufficient). Record fixed argv/version, initialize/sync/UTF-16, versioned/unversioned push/empty/crash metadata in sanitized fixtures and support decision. No raw traffic/bodies persisted. | **Done 2026-10-23 (local): `TestLocalLiveRealGoplsSmoke` runs installed gopls v0.24.0 (go1.27.2 build; toolchain go1.26.8) through the production local discovery/launcher/Go adapter on a disposable module. A draft syntax error got a versioned diagnostic, then the fixed draft got an empty clear; nothing was saved. [Evidence](../plan/code-diagnostics-gopls.md#local-experimental-activation-2026-10-23). Remote and native QA are not covered.**
| R12 / CD-23 | 4 h | R04/R05; approval/environment | Obtain approved preinstalled TS LSP wrapper/runtime/TS pair (raw tsserver is not LSP; private Zed vtsls observation is provisional). Spike through Go adapter, record compatibility/push uncertainty/failure fixtures and supported/unsupported decision; leave unavailable if none provided. | **Partial 2026-10-23: [evidence](../plan/code-diagnostics-typescript.md#go-adapter-smoke-2026-10-23); vtsls 0.3.0/TS 5.9.3 verified via opt-in smoke on an explicitly supplied private copy and discovered by the production local route; no approved install here (shows Not installed), no manual QA.** |
| R13 / CD-24 | 4 h | R04/R05, R12 ownership contract; approval/environment | Obtain approved preinstalled Svelte LS + packages (svelte-check is not LS). Verify Go adapter sync/push/virtual URI/crash behavior and source-only mappings, mixed TS ownership and independent clears. Change empty version support list only on verified evidence. |
| R14 / CD-26 | 4 h | R02…R08, R10; Code, no installed server required | Extend full approved fake attachment through Engine/App/RPC, actual adapters/owner and frontend consumers. Cover trust denial, two providers, current/unversioned/late pushes, empty clears, reconciliation, failures, old capability omission, bounds/truncation and sequence gaps. Tests must traverse the new route, not merely compose old seams. | **Complete 2026-10-22 (offline): [R14 evidence](../plan/code-diagnostics-r14.md); frontend leg is a recorded-Go-sequence replay, not a live cross-language pipe.**
| R15 / CD-26 | 4 h | **Complete 2026-10-22; offline only.** [Evidence/limits](../plan/code-diagnostics-r15.md) | Add deterministic owned process/waiter/timer/goroutine cleanup instrumentation and repeated cancel/crash/revoke/disconnect/idle/shutdown tests. Run offline Go/race + scoped lint and frontend test/check/build regression with existing dependencies; record blocked commands honestly. No global process matching or platform leak claim. Result: no leak found; checker Supervisor/pump/RPC handlers uncounted. |

## Native gates and automation — last

| ID / parent | Est. | Prerequisite / boundary | Work and verifiable exit |
|---|---|---|---|
| R16 / CD-17, CD-26, CD-27 | 4 h | R07/R08/R14/R15; native build/operator | Investigate macOS UTType link failure with installed SDK/tooling, record runnable build or precise remaining blocker; perform fake-server native UI smoke only if runnable. Record focus/keyboard, panel/marker/coverage agreement, draft/selection/undo retention. If investigation consumes budget, split UI smoke before proceeding; no installs/workaround assumed approved. |
| R17 / CD-17, CD-27 | 4 h **per assigned host/server cell** | R11…R16 as applicable; approval/environment | Assign one named local macOS/Linux/Windows matrix cell per package, with installed supported tools and operator. Execute linked Stage 1/2 scenario rows and startup/idle CPU/RSS/process/teardown measurements; record build/versions/pass/fail/untested. Instantiate/split each cell before work; a single pass never certifies other hosts/servers. |
| R18 / CD-09, CD-17, CD-27 | 4 h **per assigned remote cell** | [CD-09 identity packages](CD-09-identity.md), applicable R11…R17; real target/operator | Complete authenticated SSH/WSL identity acquisition/binding via the existing ≤4h split, then separately assign real SSH, Windows/WSL and older-server QA cells. Record host-side tools, reconnect/cleanup/compatibility; synthetic identity or cross-compilation cannot unblock activation. Split each cell before work. |
| R19 / CD-17, CD-27 | 2 h | Actual R17/R18 evidence; release owner | Reconcile both matrices and parent acceptance items; list unresolved failures/untested cells. Owner records separate Stage 1 and Stage 2 pass/HOLD or explicit evidenced reduced-scope decisions. Security/data-loss/race failures still block release; do not promote partial tasks from docs alone. |
| R20 / CD-28 | 4 h | **Both release gates cleared and explicit task authorization** | Only then revisit [CD-28](CD-28.md), re-estimate its original scope before coding. Independent off-default on-open/on-save opt-ins must not derive from trust; test confirmed saves, coalescing/cancel/bounds and stale rejection. Until then: blocked, no automation/preferences/triggers implemented or enabled, all acceptance unchecked. |

Completion of any package is evidence toward its parent, **not automatic parent
completion**. Review every original acceptance item, including manual draft QA for
CD-22…CD-24, reliable reconciliation for CD-25 and full-route/cleanup/UI evidence
for CD-26. Record actual findings in parent/evidence files when work is performed.
