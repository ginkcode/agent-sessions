# CD-27 — Live diagnostics QA preparation and blocked release record

Date: 2026-10-22. Status: **Partial / blocked; Stage 2 release HOLD**.
Progress remains **19/28**; this preparation is not a completed QA task or a
reduced-platform approval. Stage 1 [CD-17 HOLD](code-diagnostics-qa.md) also remains.

## Evidence boundary

Inspected source commit: `0d2d11162343a79bb0b5ef227f4a6ea196ff9b6f` on
`feature/code-diagnostics`; working tree initially clean. No runnable app build,
app version, approved QA root, native client session or remote identity was
established. Shell host is macOS; it is not an interactively tested client.
No browser/computer tools, real SSH/WSL target or other native platform QA is
available here. No servers, project configurations, package scripts, installers
or downloads were executed for this task; no personal projects or session data
were used. Only documentation is changed.

### Non-GUI preflight actually run

| Command (repository root) | Observed result | Meaning |
|---|---|---|
| `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test ./internal/codediagnostics -run TestLiveLifecycle -count=1` | Pass, package time 0.155 s | Existing deterministic fake lifecycle tests only; downloads/toolchain fetching disabled. Not an installed-server, performance, native or remote pass. |

The full CD-26 suite was **not rerun** for CD-27. Its recorded Go/race, frontend
438/438, check/build and repeated fake lifecycle results remain attributed to
[CD-26](../tasks/CD-26.md#evidence-and-precise-remaining-gaps-2026-10-22), not new
live evidence. Before executing the live matrix, rerun that suite in a prepared
QA environment with existing dependencies and approval for any configuration
execution; do not fetch tools/dependencies implicitly.

The CD-17 desktop build previously failed at macOS native link with undefined
`_OBJC_CLASS_$_UTType` (arm64). It was **not retried or fixed** here. CLI/SSR and
fake tests do not bypass that failure or prove WebView focus, layout or undo.

## Prerequisites / release blockers

Each blocker must be resolved and evidenced, not hidden by enabling a flag.

| ID | Required before affected live runs / sign-off |
|---|---|
| P1 — production route | Register concrete Go/TS/Svelte adapters and own approved host-local executable resolution, session creation/reuse/reopen/teardown, manager `openedDocuments` mapping and provider notices/status. The Go adapter has not been exercised against real gopls; Python spikes are not the Go production route. Keep `liveCodeDiagnostics` unadvertised until an end-to-end supported route is safe. |
| P2 — live UI | Wire live viewer/editor marker eligibility and navigation, panel/badge attribution and coverage, crash/failure/retry UI and production provider-status propagation that marks retained results stale. Stage 1 saved-file markers and store tests do not establish live consumers or native panel/marker agreement. |
| P3 — CD-26 identity / bounds | Replace hardcoded live provider ID `lsp` with concrete provider ownership and prove concurrent server/checker replacement. Resolve close/reopen same URI/version ambiguity: a late version-1 push cannot prove a new documentId; do not certify it fresh. Apply bounded live finding retention and escaped snapshot budgets with observable partial/truncation status, including shortened adapter messages. Add exhaustive owned process/waiter/goroutine/timer accounting; current fake disposal is not platform leak proof. |
| P4 — server approval / versions | Obtain explicit approval for preinstalled host/project executables and disposable contained fixtures; record fixed argv, runtime/packages, fingerprint, initialize/sync/encoding, push version behavior and crashes through the Go adapters. Go spike: gopls v0.24.0 darwin/arm64 only. TS: no approved installed LSP wrapper verified; private Zed vtsls 0.3.0 + TS 5.9.3 observation is provisional and unversioned, not production support. Svelte: installed server missing, no version verified; `svelte-check` 4.7.6 is a checker, not an LSP server. Obtain approved preinstalled `svelte-language-server` plus Svelte/TypeScript packages and verify before changing its empty support list. No installs as part of this task. |
| P5 — reconciliation | Complete CD-25 authoritative external rename/delete/delete-recreate discovery and deleted checker-set clearing through the production route without guessing deletion from read failures. Preserve dirty/uncertain drafts. All three adapter seams are `unwatched`; reject watches and visibly mark unopened-file coverage stale until reconciliation, never continuous synchronization. |
| P6 — native environment | Runnable native builds and an interactive operator, including separate investigation of the macOS UTType link failure; real Linux and Windows WebViews and Windows/WSL distro. Provide disposable clean/finding/missing-tool/unsupported fixtures with Unicode/CRLF, nested roots and owned descendants. |
| P7 — remote identity / compatibility | Complete CD-09 authenticated canonical SSH/WSL host/root identity activation (see [identity split](../tasks/CD-09-identity.md)), then supply approved disposable remote targets, installed host-side tools and controlled older servers. Client SSH executable presence is not a remote identity or connectivity test. Never copy projects locally or tunnel LSP to simulate remote QA. |

## Prioritized host/platform × server matrix — all live cells unperformed

**B/U = blocked / untested**, never passed or an observed product failure.
Every server entry includes installed supported, missing-tool/dependency and
unsupported-version scenarios. P1–P5 apply to every live server cell; P6 applies
to every native cell and P7 to all remote cells. Rows list additional blockers.
All other client/host OS pairings are explicitly **blocked / untested** (no
environments nominated); this is a priority matrix, not Cartesian certification.

| Priority; client / project host | gopls | TypeScript LSP | Svelte LSP | Additional prerequisite |
|---|---|---|---|---|
| P0 macOS / local macOS | **B/U** | **B/U** | **B/U** | Runnable macOS build/operator; gopls spike alone is insufficient; TS approval and Svelte installation absent |
| P1 Linux / local Linux | **B/U** | **B/U** | **B/U** | Native Linux desktop, installed approved servers and version evidence |
| P1 Windows / local Windows | **B/U** | **B/U** | **B/U** | Native Windows desktop, approved Windows tools/paths and process-tree measurement |
| P1 supported client / SSH Linux | **B/U** | **B/U** | **B/U** | Real nominated target, authenticated identity and remote-side installed tools |
| P2 supported client / SSH macOS | **B/U** | **B/U** | **B/U** | Same, plus host-specific versions/cleanup evidence |
| P2 supported client / SSH Windows (if supported) | **B/U** | **B/U** | **B/U** | Establish supported transport/server pairing first; no support claim here |
| P1 Windows / WSL Linux distro | **B/U** | **B/U** | **B/U** | Real Windows/distro, Linux-side tools and canonical WSL identity |
| P1 supported client / older SSH server | **B/U** | **B/U** | **B/U** | Controlled older server omitting live capability; test browse/edit, not server startup |
| P1 Windows / older WSL server | **B/U** | **B/U** | **B/U** | Controlled older distro server omitting live capability; browse/edit compatibility |

## Scenario matrix — all native/real-server observations unperformed

Apply **every row** to every applicable host/server cell above. None has a live
pass, measured value or GUI failure observation; inapplicable combinations must
be justified individually at execution time, not silently treated as passed.

| Scenario / exact future action | Status | Prerequisites / evidence to capture |
|---|---|---|
| Open fresh root; choose Not now; session grant; Always allow; restart; revoke; change executable/config fingerprint | **Blocked / untested** | P1/P4/P6 (+P7 remotely). Time dialog/search readiness; record zero starts before approval, persistence scope, renewed approval and owned teardown. |
| Open read-only file; introduce/fix unsaved error; save/discard/reload; switch cached tabs; close/reopen dialog | **Blocked / untested** | P1–P4/P6. Record actual displayed-text sync, draft/selection/undo retention and unchanged disk before explicit save. |
| Navigate panel → source/editor; keyboard/accessible messages; compare badges/counts; open diff/blame/Markdown/image previews | **Blocked / untested** | P2/P6. Plain-text hostile-message rendering, matching attribution, no preview overlays, correct UTF-16 exclusive-end Unicode/CRLF ranges. |
| Compare saved checker results while dirty with matching-version, stale/future and unversioned live publications; send empty document set | **Blocked / untested** | P1–P4/P6. Sanitized metadata-only instrumentation; immediate uncertain-range invalidation, independent provider replacement, “No reported problems” not all-files clean. |
| Close/reopen same path/version with delayed old push; switch host/root/generation; sequence gap/resnapshot | **Blocked / untested** | P3/P7 for remote. Resolve document identity ambiguity first; never present uncertain pushes as fresh. |
| External edit while hidden/dirty; reopen/refresh/pre-check; external rename/delete/recreate | **Blocked / untested** | P5 plus production route. No overwrite of drafts, no inferred deletion on failed read, explicit unwatched/unopened stale coverage. |
| Crash during request; retry/restart cap; revoke; disconnect/reconnect; host switch; shutdown with late publications | **Blocked / untested** | P1–P3/P6/P7. Failure UI, retained stale status, no cross-host repopulation or draft loss; owned descendants stop. |
| Repeat opens/tabs; hide then reopen before/after proposed 2-minute grace; open third project under two-project backend limit | **Blocked / untested** | P1/P3/P6. Record exact timings, process IDs/counts, CPU/RSS baseline/startup/idle/expiry/teardown and descendants; no duplicate servers or leak claim without measurements. |
| Large fixture / bounded remote latency; many findings, oversized escaped messages and transport/retention limits | **Blocked / untested** | P3/P6/P7. Record traffic counts/bytes, latency, responsiveness, truncation/partial feedback; metadata proves no hidden didOpen of all files. |
| Missing/unsupported binaries/runtime/dependencies or pull-only server | **Blocked / untested** | P1/P2/P4/P6. Independent unavailable status; no fallback install/download/package script, no blocked browsing. |
| Older remote omits live flag; browse/edit/save/close | **Blocked / untested** | P6/P7. No live controls/startup; ordinary browsing/editing preserved. Fake old-remote tests are not this native observation. |
| Safe-request/path/trust limits with controlled fixtures | **Blocked / untested** | P1–P4/P6. Retain existing fake denial tests for applyEdit/commands/root expansion; do not destructively probe user projects. Confirm sanitized bounded errors, no raw protocol/body persistence. |

### Reproduction and recording protocol (planned, not executed)

1. Rerun CD-26 regression identifiers: Go full/race suites and repeated
   `TestLiveLifecycle`, frontend tests/check/build and scoped lint as recorded in
   CD-26, using existing dependencies only. Record any blocked command separately.
2. For each selected cell record app commit/stamped version/build mode, client OS
   and WebView, project host OS/architecture/transport, authenticated host/root
   identity (sanitized), approved disposable root and fingerprint, server absolute
   executable/version/fixed argv and runtime/package versions. Resolve tools on
   the project host only after approval. Do not execute project config for discovery.
3. Use contained synthetic files with deliberate findings and clean equivalents;
   follow each scenario action in order, recording observed pass/fail/blocked,
   timings and sanitized counters. For metadata instrumentation record provider,
   generation/document/version, counts/coverage and bytes, never bodies, raw LSP,
   source excerpts or diagnostic dumps. No measured performance values exist here.
4. Capture before/after owned-process/descendant counts and CPU/RSS using that
   platform's available tools, including shutdown/revocation/disconnect. Do not
   globally kill matching processes. Attach evidence IDs and follow-up defect
   tasks for each failure; retain unknowns as untested.
5. Split/re-estimate remaining work beyond four hours. Release owner must review
   actual supported-version and platform evidence before any reduced-scope decision.

## Release decision

**HOLD Stage 2/live diagnostics.** No real-server/native/remote matrix cell has
passed, and no startup/idle/teardown measurement was taken. The only new pass is
a focused fake-server regression, not CD-27 acceptance. P1–P7 and CD-26 remaining
identity/bounds/status gaps block sign-off; CD-17 Stage 1 HOLD remains unchanged.
Keep production live capability disabled; do not advertise any newly verified
platform/server combination. CD-27 remains partial/blocked, not counted. **CD-28
is not started** and must not bypass either release gate.
