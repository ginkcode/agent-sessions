# Implementation task details

Task briefs for **Go to File code diagnostics**. **Progress: 19/28 complete**
(CD-01: contract and tested fixtures; CD-02: backend trust and path policy;
CD-03: bounded checker supervisor; CD-04: installed-checker format evidence;
CD-05: contained Go vet adapter; CD-06: contained Svelte adapter and tested conservative outcomes;
CD-07: headless result manager with bounded lifecycle/snapshot/event tests;
CD-08: Engine/App bindings, owned metadata discovery and offline routing;
CD-10: standalone frontend API/store with atomic reconciliation and mock race tests;
CD-11: local dialog approval, explicit check controls and honest status/coverage;
CD-12: grouped/filtered Problems and guarded draft-safe UTF-16 source/editor navigation;
CD-13: shared exact-deduplication file/tree badges and component ancestor counts;
CD-14: verified source gutter/range indicators and plain-text file/stale messages;
CD-15: lazy CodeMirror lint with verified saved ranges and draft/cache freshness guards;
CD-16: deterministic checker/manager/RPC and real-store controls/panel/picker/source/editor integration evidence;
CD-18: bounded LSP stdio transport, fake-server tests only;
CD-19: safe LSP session lifecycle, fake server/process/clock only;
CD-20: bounded versioned document operations across backend transports;
CD-21: frontend document lifecycle bridge, automated tests only).
CD-09 remains blocked on authenticated production remote identity activation;
CD-17 is partial/blocked: [non-GUI preflight and blocked native matrix](../plan/code-diagnostics-qa.md), Stage 1 HOLD; CD-21 (frontend document lifecycle bridge) is complete with automated tests only; CD-22 (gopls adapter) is partial: spike/adapter seam with automated tests, no production route or manual QA ([decision doc](../plan/code-diagnostics-gopls.md)); CD-23 (TypeScript adapter) is partial/blocked: adapter seam and synthetic tests, no host-approved LSP server verified ([doc](../plan/code-diagnostics-typescript.md)); CD-24 (Svelte adapter) is partial/blocked: adapter seam, ownership policy and synthetic tests, no Svelte language server verified ([doc](../plan/code-diagnostics-svelte.md)); CD-25 is partial (read-only revision reconciliation and fake-session guards; external rename/delete reconciliation and production wiring incomplete, not counted); CD-26 is partial ([deterministic fake lifecycle integration and blockers](CD-26.md); live retention/ownership/status gaps and manual UI smoke remain, not counted); CD-27 is partial/blocked ([live QA matrix](../plan/code-diagnostics-live-qa.md), focused fake preflight only; all live cells blocked/untested, Stage 2 HOLD, not counted); CD-28 is blocked: Step 1 prerequisites unmet, both release gates HOLD, no dependency exception; no automation implemented/enabled, off by default. CD-16 is automated coverage, not native CD-17 QA. CD-10 used an explicit dependency exception against
committed RPC contracts/tests, not remote activation. Local controls are wired;
production remote diagnostics are not enabled. Manual keyboard/focus QA is not claimed. The authoritative wire contract is
[contract v1](../plan/code-diagnostics-contract.md). See [the implementation plan](../plan/code-diagnostics.md) for shared
behavior, architecture, trust rules, and release criteria, and
[the task tracker](../../TASKS.md#follow-up--go-to-file-code-diagnostics-proposed)
for feature status.

## Remaining work and totals

[Actionable remaining-work backlog](code-diagnostics-remaining.md) splits CD-22…CD-26
acceptance gaps into ≤4h packages, followed by tool/native gates and automation last.
It distinguishes independent offline coding from approval/tool/environment blockers;
no installs, activation or dependency exception are authorized.

Actual parent totals: **28 tasks = 19 complete + 7 partial + 2 blocked + 0 not started**.
The partial parents are CD-17 and CD-22…CD-27 (including partial/blocked QA/tool work);
the blocked parents are CD-09 and CD-28. Packages do not change the denominator.

## How to use these briefs

- Finalize contracts in CD-01 before implementation; API names elsewhere remain
  provisional until that task is complete. Keep the plan and dependent briefs
  synchronized when a contract changes.
- Respect listed dependencies. Frontend presentation tasks can run in parallel
  after CD-10 using fixtures/mocks based on CD-01. Dependency ranges such as
  `CD-05…CD-15` include both endpoints and every task between them.
- The frontend presentation tasks share `FileViewer.svelte` and range helpers;
  coordinate ownership of those edits even when the task graph permits parallel work.
- Estimates include focused tests and are capped at four developer hours per
  task. Split and re-estimate work before starting if exploration shows it will
  exceed that limit; do not silently omit acceptance requirements.
- Likely file lists identify integration points, not instructions to create
  duplicate infrastructure. Recheck the current code before making changes.
- Follow the repository Definition of Done in TASKS.md. Mark implementation,
  automated testing, and manual QA accurately; planned QA is not a passing result.
- Code diagnostics are separate from existing session-provider diagnostics.
  Checking must not modify source files or persist drafts/diagnostic payloads.

## Cross-cutting implementation notes

- Existing `FileLineSelection` is one-based and line-only. Diagnostic range
  navigation needs an explicit UTF-16 column-aware path; do not silently change
  line-selection behavior used by blame and normal source selection.
- `FileEditState.version` invalidates cached editor state; it is not the monotonic
  document version required for live diagnostic freshness.
- Preserve lazy editor loading. Adding diagnostic display to the read-only
  viewer must not statically import CodeMirror into the main application bundle.
- Current Node/SSR tests do not prove client focus, scrolling, or native WebView
  layout. The Stage 1/2 QA tasks must record those checks separately.

## Estimate summary

| Stage | Tasks | Developer effort |
|---|---|---|
| 1 — Explicit project checks | 17 | 62 h |
| 2 — Live opened-file diagnostics | 10 | 40 h |
| 3 — Optional automatic project checks | 1 | 4 h |
| **Total** | **28** | **106 h** |

These are planning estimates, not elapsed-time commitments. Tool-version spikes
and platform QA may uncover follow-up tasks that must be estimated separately.

## Stage 1 — Explicit project checks

The first release uses approved, host-local tools and an explicit Check Project
action. It does **not** automatically execute tools when the dialog opens.

| Task | Detail | Estimate |
|---|---|---|
| [CD-01](CD-01.md) | ✅ Done — shared contracts and tested protocol fixtures | 3 h |
| [CD-02](CD-02.md) | ✅ Done — project execution trust and authorization | 4 h |
| [CD-03](CD-03.md) | ✅ Done — bounded checker process supervisor | 4 h |
| [CD-04](CD-04.md) | ✅ Done — checker output/version spikes and tested sanitized fixtures | 3 h |
| [CD-05](CD-05.md) | ✅ Done — contained Go vet adapter and fixture/fake-supervisor tests | 3 h |
| [CD-06](CD-06.md) | ✅ Done — contained Svelte-check adapter and fixture/fake-supervisor tests | 4 h |
| [CD-07](CD-07.md) | ✅ Done — headless result manager, bounded snapshots/events and lifecycle races | 4 h |
| [CD-08](CD-08.md) | ✅ Done — Engine/App bindings and offline behavior | 3 h |
| [CD-09](CD-09.md) | ⛔ Transport evidence complete; identity activation blocked ([split estimates](CD-09-identity.md)) | 4 h |
| [CD-10](CD-10.md) | ✅ Standalone frontend store/API, bounded atomic reconciliation and mock race tests (CD-09 remote activation still blocked) | 4 h |
| [CD-11](CD-11.md) | ✅ Local approval, explicit check/cancel/retry controls, honest summary/coverage; remote CD-09 blocked | 3 h |
| [CD-12](CD-12.md) | ✅ Problems panel and draft-safe UTF-16 range navigation; native QA deferred | 4 h |
| [CD-13](CD-13.md) | ✅ Shared exact-deduplication file/tree badges and ancestor counts; native layout QA deferred | 3 h |
| [CD-14](CD-14.md) | ✅ Verified source gutter/range indicators and file/stale messages; native paint/layout QA deferred | 4 h |
| [CD-15](CD-15.md) | ✅ Lazy CodeMirror lint, verified ranges and draft/cache freshness guards; native QA deferred | 4 h |
| [CD-16](CD-16.md) | ✅ Deterministic Stage 1 integration regressions; remote identity blocked, native QA not claimed | 4 h |
| [CD-17](CD-17.md) | 🟡 Partial/blocked — non-GUI QA preparation only; native matrix unperformed, Stage 1 HOLD | 4 h |

## Stage 2 — Live opened-file diagnostics

Services start asynchronously for approved projects. Opened-file coverage is
not a promise that the entire workspace has been checked.

| Task | Detail | Estimate |
|---|---|---|
| [CD-18](CD-18.md) | ✅ Bounded LSP stdio transport (fake-server evidence; transport only) | 4 h |
| [CD-19](CD-19.md) | ✅ LSP protocol and process lifecycle (fake-server evidence; no adapters) | 4 h |
| [CD-20](CD-20.md) | Document operations across backend transports | 4 h |
| [CD-21](CD-21.md) | Frontend document lifecycle bridge | 4 h |
| [CD-22](CD-22.md) | Go language-server adapter | 4 h |
| [CD-23](CD-23.md) | TypeScript language-server adapter | 4 h |
| [CD-24](CD-24.md) | Svelte language-server adapter | 4 h |
| [CD-25](CD-25.md) | External-change and file-watch reconciliation | 4 h |
| [CD-26](CD-26.md) 🟡 Partial | Fake-server lifecycle integration; retention/ownership/status and UI smoke gaps remain (not counted) | 4 h |
| [CD-27](CD-27.md) 🟡 Partial/blocked | [Blocked live matrix](../plan/code-diagnostics-live-qa.md); focused fake preflight only, no live measurements; Stage 2 HOLD (not counted) | 4 h |

## Stage 3 — Optional automatic project checks

| Task | Detail | Estimate |
|---|---|---|
| [CD-28](CD-28.md) ⛔ Blocked | Both release gates HOLD; no dependency exception; automation absent/off-default, all acceptance unchecked | 4 h |

Additional languages and checker adapters require separately scoped tasks after
feedback from the first two stages; they are not included in CD-28.

## Editor essentials

Briefs for [editor-essentials.md](../plan/editor-essentials.md): project
search, agent sessions, code navigation and Git changes. Status is tracked in
each brief and in [TASKS.md](../../TASKS.md#follow-up--editor-essentials).

| Area | Tasks |
|---|---|
| Project search | [PS-01](PS-01.md) engine · [PS-02](PS-02.md) wire · [PS-03](PS-03.md) pane · [PS-04](PS-04.md) replace engine · [PS-05](PS-05.md) replace UI |
| Agent sessions | [AG-01](AG-01.md) launch · [AG-02](AG-02.md) entry points · [AG-03](AG-03.md) tab links · [AG-04](AG-04.md) turn state · [AG-05](AG-05.md) attention · [AG-06](AG-06.md) alerts |
| Code navigation | [NAV-01](NAV-01.md) LSP requests · [NAV-02](NAV-02.md) wire · [NAV-03](NAV-03.md) external files · [NAV-04](NAV-04.md) viewer · [NAV-05](NAV-05.md) editor · [NAV-06](NAV-06.md) references |
| Git changes | [GIT-01](GIT-01.md) status · [GIT-02](GIT-02.md) writes · [GIT-03](GIT-03.md) branches · [GIT-04](GIT-04.md) pane · [GIT-05](GIT-05.md) diffs · [GIT-06](GIT-06.md) sync |
