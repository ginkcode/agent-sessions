# Code diagnostics — remaining-work split

Date: 2026-10-22. **R01 audit, R02 concrete ownership and R03 URI isolation complete; R04 offline discovery/trust seam and R05 internal owner route complete; R06–R20 not started.** Parent progress remains **19/28**:
19 complete, 7 partial (CD-17, CD-22…CD-27), 2 blocked (CD-09, CD-28), 0 untouched
not-started tasks. Partial/blocked parents are not counted complete. These package
IDs subdivide remaining acceptance work; they do not add to the 28-task denominator.

This supplies the follow-up split required by [CD-22](CD-22.md#not-met--not-claimed)
and the [task split rules](README.md#how-to-use-these-briefs). Each estimate includes
focused tests/evidence and is **≤4 developer hours**. Re-estimate/split before coding
if a package exceeds its cap; never omit acceptance to fit. Full regression and
native evidence have separate packages. **R01 is complete (audit/decision only); R02
is complete (offline ownership seams/tests); R03 is complete (offline conservative URI isolation/tests); R04 is complete (offline discovery/trust seam/tests); R05 is complete (internal Engine/session owner route, offline tests; production disabled); R06–R20 are not started.** The [R01 ownership audit](../plan/code-diagnostics-ownership-audit.md)
is authoritative for R02/R03 ownership and conservative reused-URI freshness policy;
its exposed bugs are not implemented fixes or passing regression claims.

## Execution boundaries

- **Code** means safe independent, offline coding/fake tests against existing seams
  and dependencies, not a claim that a partial parent dependency is done. No new
  dependency exception or production activation is approved here. Assign/review
  scoped work explicitly; keep live capability unadvertised and automation absent.
- **Approval/environment** means a real prerequisite for the named execution or
  sign-off, not a reason to stop unrelated local fixes. No installs/downloads are
  authorized. Missing tools stay unavailable; private editor caches are not approved
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
| R06 / CD-26 | 4 h | R02; Code | Apply bounded live retention/normalization and escaped snapshot/RPC budgets, finding/message caps and observable truncation/partial status (including shortened adapter messages). Boundary/hostile-escape/many-document tests prove bounded storage and no false all-clean result; do not raise caps. |
| R07 / CD-22…CD-24, CD-26 | 4 h | R02/R03; Code | Wire live source/editor marker eligibility and navigation, preserving lazy editor import, UTF-16 ranges, cached drafts/undo and no diff/blame/preview overlays. Component/store tests distinguish saved checker, current versioned live and unversioned uncertain results. Native paint/undo remains R16. |
| R08 / CD-22…CD-24, CD-26 | 3 h | R05/R06/R07; Code | Add provider crash/failure/retry and partial/truncation UI; retained sets stale, panel/badges/markers agree, hostile text escaped, unavailable provider does not block browsing. Test status propagation through actual store/components, not callbacks alone. |
| R09 / CD-25 | 3 h | Code; may run independently after ownership review | Define/add authoritative contained disk absence/identity reconciliation contract; distinguish confirmed deletion from permission/transient read failure and rename from unproven disappearance. Fake API tests cover delete/recreate and inaccessible paths; preserve dirty/uncertain tabs and explicit `unwatched` fallback. No recursive watcher scope expansion. |
| R10 / CD-25, CD-26 | 4 h | R03/R05/R09; Code | Wire authoritative external delete/rename/recreate into reopen/refresh/pre-check, clear deleted checker/live sets and reject old URI/generation pushes. Tests cover same-path recreation, refresh races, dirty/uncertain drafts and late reads; failed reads never guess deletion or overwrite text. |

## Verified tools, full-route tests and cleanup

| ID / parent | Est. | Prerequisite / boundary | Work and verifiable exit |
|---|---|---|---|
| R11 / CD-22 | 3 h | R04/R05; approval/environment | Run approved preinstalled gopls through Go adapter (existing v0.24.0 Python spike is insufficient). Record fixed argv/version, initialize/sync/UTF-16, versioned/unversioned push/empty/crash metadata in sanitized fixtures and support decision. No raw traffic/bodies persisted. |
| R12 / CD-23 | 4 h | R04/R05; approval/environment | Obtain approved preinstalled TS LSP wrapper/runtime/TS pair (raw tsserver is not LSP; private Zed vtsls observation is provisional). Spike through Go adapter, record compatibility/push uncertainty/failure fixtures and supported/unsupported decision; leave unavailable if none provided. |
| R13 / CD-24 | 4 h | R04/R05, R12 ownership contract; approval/environment | Obtain approved preinstalled Svelte LS + packages (svelte-check is not LS). Verify Go adapter sync/push/virtual URI/crash behavior and source-only mappings, mixed TS ownership and independent clears. Change empty version support list only on verified evidence. |
| R14 / CD-26 | 4 h | R02…R08, R10; Code, no installed server required | Extend full approved fake attachment through Engine/App/RPC, actual adapters/owner and frontend consumers. Cover trust denial, two providers, current/unversioned/late pushes, empty clears, reconciliation, failures, old capability omission, bounds/truncation and sequence gaps. Tests must traverse the new route, not merely compose old seams. |
| R15 / CD-26 | 4 h | R05/R14; Code | Add deterministic owned process/waiter/timer/goroutine cleanup instrumentation and repeated cancel/crash/revoke/disconnect/idle/shutdown tests. Run offline Go/race + scoped lint and frontend test/check/build regression with existing dependencies; record blocked commands honestly. No global process matching or platform leak claim. |

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
