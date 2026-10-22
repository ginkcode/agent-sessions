# Implementation task details

Task briefs for **Go to File code diagnostics**. **Progress: 8/28 complete**
(CD-01: contract and tested fixtures; CD-02: backend trust and path policy;
CD-03: bounded checker supervisor; CD-04: installed-checker format evidence;
CD-05: contained Go vet adapter; CD-06: contained Svelte adapter and tested conservative outcomes;
CD-07: headless result manager with bounded lifecycle/snapshot/event tests;
CD-08: Engine/App bindings, owned metadata discovery and offline routing);
CD-09…CD-28 are not started. No user-facing
diagnostics support has shipped. The authoritative wire contract is
[contract v1](../plan/code-diagnostics-contract.md). See [the implementation plan](../plan/code-diagnostics.md) for shared
behavior, architecture, trust rules, and release criteria, and
[the task tracker](../../TASKS.md#follow-up--go-to-file-code-diagnostics-proposed)
for feature status.

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
| [CD-10](CD-10.md) | Frontend diagnostics store and API | 4 h |
| [CD-11](CD-11.md) | Approval, check controls, and summary | 3 h |
| [CD-12](CD-12.md) | Problems panel and range navigation | 4 h |
| [CD-13](CD-13.md) | Tree badges and aggregate counts | 3 h |
| [CD-14](CD-14.md) | Read-only source-view diagnostics | 4 h |
| [CD-15](CD-15.md) | CodeMirror lint integration | 4 h |
| [CD-16](CD-16.md) | Project-check integration regression tests | 4 h |
| [CD-17](CD-17.md) | Cross-host/platform QA and Stage 1 gate | 4 h |

## Stage 2 — Live opened-file diagnostics

Services start asynchronously for approved projects. Opened-file coverage is
not a promise that the entire workspace has been checked.

| Task | Detail | Estimate |
|---|---|---|
| [CD-18](CD-18.md) | Bounded LSP stdio transport | 4 h |
| [CD-19](CD-19.md) | LSP protocol and process lifecycle | 4 h |
| [CD-20](CD-20.md) | Document operations across backend transports | 4 h |
| [CD-21](CD-21.md) | Frontend document lifecycle bridge | 4 h |
| [CD-22](CD-22.md) | Go language-server adapter | 4 h |
| [CD-23](CD-23.md) | TypeScript language-server adapter | 4 h |
| [CD-24](CD-24.md) | Svelte language-server adapter | 4 h |
| [CD-25](CD-25.md) | External-change and file-watch reconciliation | 4 h |
| [CD-26](CD-26.md) | Fake-server end-to-end regression tests | 4 h |
| [CD-27](CD-27.md) | Real-server QA and Stage 2 gate | 4 h |

## Stage 3 — Optional automatic project checks

| Task | Detail | Estimate |
|---|---|---|
| [CD-28](CD-28.md) | Opt-in background project checks on open/save | 4 h |

Additional languages and checker adapters require separately scoped tasks after
feedback from the first two stages; they are not included in CD-28.
