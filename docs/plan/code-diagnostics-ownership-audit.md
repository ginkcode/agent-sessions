# R01 — live diagnostics ownership and freshness decision

Date: 2026-10-22. **R01 complete (audit/decision only). R02 implemented (offline ownership seams/tests); R03 implemented (offline conservative transport-lifetime URI isolation/tests).**
The map and exposed outcomes below record the pre-R02 audit; the decisions remain
controlling. See [R02 implementation evidence](../tasks/CD-26.md#r02-concrete-ownership-2026-10-22)
for corrected ownership boundaries and [R03 evidence](../tasks/CD-26.md#r03-conservative-uri-isolation-2026-10-22) for conservative rejection of reused-URI pushes.
Parent progress remains **19/28**; CD-22…CD-26 remain partial. This is the
controlling ownership/freshness decision for R02/R03, superseding any broader
matching-version or close/reopen assurance in earlier evidence. No capability,
production registration, automation, executable probe, or body logging is added.

## Authoritative ownership map (actual implementation audited)

All Go paths below are under `internal/codediagnostics/` unless qualified.
These are distinct identities; none may stand in for another.

| Boundary | Current identity / code | Loss or required invariant |
|---|---|---|
| Host/project authorization | `AuthorizedRoot`, `Project{HostID, Root}`; `Manager.Attach` in `manager.go` revalidates root and disallows two attachments of the same project in one manager | Canonical host/root, not picker alias, package directory or URI alone. |
| Client result epoch | `manager_types.go`: `Context{ConnectionID, AttachmentID, Generation}`; `Manager.lookup` guards client calls | Result epoch is not session generation or approval. Path publication takes only Project; detach/re-attach can select a new epoch unless lifecycle isolation also holds. |
| Approval / fixed server | `ServerConfig.Tool/Policy` in `lsp_session.go`, fingerprint and Trust preflight; adapter constants `gopls`, `typescript`, `svelte` | Backend-owned concrete provider, fixed config and current grant; never trust a client/server-body provider label. |
| Session reuse | `lsp_session.go:keyOf`: `(Project, cfg.ProviderID, cfg.Policy())`; each `lspSession` has its own opaque generation | Policy includes exact server configuration; package count does not create extra sessions. A result epoch change alone does not isolate a reused transport. |
| Document routing | `lsp_documents.go:SessionDocuments.server` resolves `(root,path)` to `DocumentServer{Config,LanguageID}` and current session | Resolver knows concrete provider/policy, but `DocumentProvider.Session` returns only generation/capabilities. `documentState` retains only path/version/text/generation/broken, not provider/policy. R02 must preserve and verify the resolved owner. |
| Language assignment | `go_lsp.go:GoplsPlan.Documents`: `.go`; `svelte_lsp.go:DocumentOwner/OwnedDocuments`: `.svelte` → Svelte; JS/TS including `x.svelte.ts` → TypeScript, contained package checks | One synchronization owner per displayed document. Go is a separate resolver; no production combined resolver exists. Independent checkers may report on the same file, without becoming synchronization owners. |
| App document lifecycle | `documents.go:OpenDocument`: attachment docs keyed by documentId, one open entry per path, initial version exactly 1; `ChangeDocument` strictly increases versions | IDs distinguish frontend lifecycles, but never enter LSP. Closed-ID FIFO is only 256 entries; its comment “never reused” is not an unbounded guarantee. R03 must not rely on this FIFO for URI isolation. |
| Wire synchronization | `lsp_documents.go:Apply`: open sends URI/language/version/text, change sends URI/version/changes, close sends URI only | No documentId, attachment/result epoch or per-open nonce on wire. Same URI + version 1 can describe multiple opens on one transport. Notify success is delivery, not diagnostic completion. |
| Notification provenance | `lsp_session.go:handle`: `SessionNotification{Project,ProviderID,Generation,Method,Params}` from session-owned config | Envelope preserves concrete provider/generation, not document lifecycle. Current-session checks cannot disambiguate two opens in the same generation. |
| Adapter normalization | `go_lsp.go:GoplsBridge.Handle`, `ts_lsp.go:TSBridge.Handle`, `svelte_lsp.go:SvelteBridge.Handle` check project/provider/method; normalizer resolves contained URI and prefixes `Diagnostic.Source` | TS/Svelte additionally filter their file ownership; Svelte rejects unverified virtual coordinates. All bridges call `goplsSink.PublishPathDiagnostics(Project,generation,path,version,diagnostics)` **without provider**. Source attribution is not set ownership (empty sets have no Source). |
| Manager publication | `documents.go:PublishPathDiagnostics` searches current project attachment and current open path; `publishLocked` rechecks provider.Session current generation, mirror generation/broken state and optional exact version | These guards reject absent paths, wrong generations/versions, but path lookup assigns the **new** documentId to an old same-URI/version push. `PublishDocumentResults` with an explicit old ID is not the actual adapter route. |
| Live replacement | `documents.go:publishLocked` constructs `ProviderID: "lsp"`; replacement matches only `Origin == "lsp" && Scope.DocumentID == documentID` | Concrete identity is lost. Two legitimate providers on different documents are mislabeled as one; the API cannot express independently owned sets for the same document. Current distinct session generations incidentally reject some cross-owner pushes; that is not a provider contract. |
| Checker replacement | `manager_results.go:apply`: concrete registered ID + `Origin: "checker"`, project scope; replacement matches provider + checker origin | Preserve this separation. Live empty publication must not clear/relabel checker results. `manager.go:prepare` is checker-only (`savedProject`, available requires CheckerAdapter); live plan `Provider()` metadata is not wired. |
| Close / invalidation | `documents.go:dropDocument` removes all LSP sets for closing ID, records bounded closed ID, then best-effort didClose; `releaseDocuments` also used on invalidation | Ending a lifecycle may intentionally remove all its live sets, but must not remove another document or checker. It does not cancel old diagnostic computation in the server. |
| Snapshot/event/frontend | `manager_types.go` ResultSet carries provider/origin/scope; `frontend/src/lib/codeDiagnostics.ts:resultSetKey` includes provider/origin/scope; `liveDocumentEligibility` trusts producer freshness plus matching documentId/version/synced/range | Existing consumer keys can distinguish providers. They cannot recover a dropped provider or detect a backend-rebased old push whose Basis is rewritten to the new ID. R02 must preserve identity through snapshots/events; R03 must prevent false certification at publication. |

## Findings and precise evidence limits

1. **Exposed R02 bug, by code inspection:** concrete adapter provenance is discarded
   at `goplsSink`; manager emits generic `lsp` regardless of adapter. A gopls finding
   may retain `Source: gopls/compiler` while its ResultSet says `ProviderID: lsp`.
   This is not fixed by source-prefixing, deduplication, or frontend grouping.
2. **Exposed R03 freshness bug, by deterministic code trace:** open A at `a.go`, v1;
   close A; open B at `a.go`, v1 in the same generation; deliver A's delayed v1
   push through GoplsBridge. Path lookup finds B; both generation checks and exact
   version check pass; valid coordinates get `Freshness: current`, `Basis: B/v1`.
   An old empty v1 push can also clear B's set. These are required failing regression
   schedules for R03, not a claimed passing guard. Text equality or range validity
   cannot establish provenance (choose different texts with identical valid ranges).
3. `live_lifecycle_integration_test.go:TestLiveLifecycleFramedReplacementAndGuards`
   closes **v2**, reopens **v1**, then sends **v2** (lines 148–160). That passes
   because versions differ; it does not test delayed v1 after a v1 reopen. The
   explicit-old-ID tests also do not exercise adapter path rebinding.
4. Existing crash/retry test proves a changed session generation, and invalidation
   tests prove rejection while revoked/disconnected/closed. They do not prove
   freshness after reattachment/reapproval on a reused still-live transport.
5. Existing frontend two-source tests prove consumer separation, not backend
   concurrent concrete-provider routing. No runtime reproduction was executed for
   this audit; exposed outcomes above follow the inspected branches directly.

## Decision for R02 — concrete ownership, not source heuristics

- Thread a backend-owned concrete provider identity from resolved document owner
  and SessionNotification through adapter sink, manager publication and ResultSet.
  Bind owner identity to the document lifecycle (including policy/session identity),
  and reject mismatched ownership **before** replacement, including empty batches.
  Do not accept ownership from `Diagnostic.Source`, message, URI extension alone,
  or a caller-supplied client provider ID. Keep session generation checks as well.
- Replacement identity is attachment epoch + provider + origin + full scope
  `(kind,path,documentId)`; checker project scope remains independent. Empty means
  replace only this owner's set, not “clear all live findings”. Ordinary routing
  still has exactly one LSP synchronization owner per document; a two-provider
  test must not manufacture duplicate didOpen streams to satisfy coverage.
- Two approved providers can run concurrently on different owned files in one
  project (Go + TS, TS + Svelte); saved checkers can overlap any file. Reject a
  non-owner attempting publication on another owner's path even if it claims a
  matching version. No broad cross-provider diagnostic deduplication.
- R02 may change seams/tests only; it does not solve URI freshness, add live
  registration/discovery/production startup, or permit activation. `openedDocuments`
  registration/session owner is still R04/R05 work.

## Decision for R03 — conservative provable freshness

**Default: never certify matching-version freshness on a reused URI in a reused
session without isolation.** A new app documentId, result epoch, sleep/debounce,
flush/didClose delivery, empty push, save, or matching coordinates is not isolation.
LSP publishDiagnostics has no documentId and no per-open acknowledgment token.

The conservative implementation target is **discard ambiguous publications**
(including empty batches) for a URI reused across lifecycles on the same session
transport. Retain explicit uncertainty/limited coverage and no current markers;
a discarded empty must not imply clean. R03 may instead retain ambiguous findings
only as explicitly uncertain, range-free panel data, but must demonstrate that old
empty publications cannot erase a newer certified set or certify all-clean.
Dropping is the simpler default. Unversioned publications remain uncertain and
range-free even on an isolated lifecycle; no freshness upgrade is authorized.

Freshness can resume only with a provably isolated new server/transport generation,
old callbacks rejected independently, actual displayed text synchronized to that
new session, and matching publication version. Restart/reopen must account for
all other documents sharing that provider session; never silently lose drafts,
reuse another provider's session, or bypass trust/restart limits. R03 need not
implement eager restarts: retaining uncertainty/unavailability is acceptable.
A monotonic wire-version translation could be a future separately proven strategy,
but the current reset-to-1 contract alone provides no such proof.

Ambiguity tracking must survive close, delete/recreate, detach/re-attach and
reapproval while a session is reused, and be scoped to actual provider/policy/
session + canonical URI. Bounded bookkeeping must fail closed on exhaustion
(e.g. disable certification for the remaining session), never forget an old URI
and silently treat it as first-open. Closed-ID FIFO eviction must not re-enable
freshness. A different attachment generation alone is insufficient. URI aliases
that resolve to the same wire URI must not evade the rule.

## Deterministic regression schedules / implementation exits

Use existing fake launch, framed transport and notification-completion channels
in `live_lifecycle_integration_test.go`; barriers acknowledge processing, not
freshness. No sleeps to assert absence, installed servers, body logging or native
claims. For each rejected push assert unchanged snapshot sequence and sets. For
accepted replacement assert concrete provider, origin, full scope, Basis, range,
freshness and matching snapshot/event payloads. These are specifications, **not
new passing tests**; do not skip or invert an unsafe assertion to make CI green.

| ID / owner | Ordered schedule | Required outcome / current evidence |
|---|---|---|
| O1 / R02 | Approve fake Go + TS sessions; open `a.go` and `b.ts`; publish each, then Go empty; concurrently retain a saved Go checker set on `a.go`. Repeat pushes in reverse order with completion barriers. | Concrete `gopls`/`typescript` sets and independent checker attribution; Go empty clears only Go document set. Current generic ID fails concrete attribution. |
| O2 / R02 | Mixed package: open `App.svelte`, `util.ts`, `store.svelte.ts`; repeat attach/reuse; publish Svelte and TS; Svelte empty; deliver Svelte push on TS-owned path and TS push on Svelte path. Include an empty wrong-owner push through manager seam. | One didOpen stream per file, two reused provider sessions, no cross-provider relabel/clear, no duplicate package sessions; virtual URI never gets source coordinates. Existing resolver tests only partly cover this route. |
| O3 / R02 | Seed two owned result sets through supported publication seams (if multiple independent result providers are supported), one checker set and another document; replace/empty one exact key. | All other keys unchanged; if non-owner live provider is unsupported, assert rejection rather than adding a second synchronization owner. Check snapshots and emitted events, not only source strings. |
| F1 / R03 | Open A `a.go` v1 text `aa`; hold its push; close A; open B same URI v1 text `bb`; release old v1 finding with valid `0:0–0:1` range. Repeat with old empty, including after a B finding arrives. | No old push becomes B/current or clears a certified B set. **Current code accepts and re-bases it**. Same text in A/B must not be a shortcut either. |
| F2 / R03 | On first lifecycle, current v2 finding; deliver older v1, future v3, wrong generation and explicit closed/unknown ID; deliver unversioned finding. | Mismatches cannot replace; unversioned is uncertain/no range. Existing framed and document tests cover these guards; preserve them. |
| F3 / R03 | Crash after A v1; join disposal; retry approved session; synchronize displayed text in new generation; replay old envelope with old generation, then matching new push. | Old generation rejected, new isolated push eligible. Existing crash/retry test covers v2 reopen; add same URI/v1 lifecycle variant without faking a new generation on the old callback. |
| F4 / R03/R05 | Hold push; revoke/disconnect/detach; replay; reapprove/reattach and open new ID same URI/v1. Try both reused transport (where allowed) and genuinely new transport. | Inactive epoch rejects; reused transport stays ambiguous; isolated new transport may certify after synchronization. Existing invalidation tests cover only the inactive portion. |
| F5 / R03/R10 | Close/delete/recreate same path/v1; churn beyond closed-ID/ambiguity budget; deliver earliest held v1 callback. Also reopen after idle grace vs within grace. | No FIFO expiry or reconciliation guesses restore freshness; reused session conservative, expired/isolated generation rejects old pushes. Authoritative deletion wiring remains R09/R10, not R01. |
| F6 / R03/R07 | Forward F1 output through snapshot/events to real store and `liveDocumentEligibility` for B/v1. | No current marker, including valid ranges and synced B. Consumer matching ID/version cannot repair a falsely certified producer. Production editor route remains R07/R14. |

## Completion and validation

R01 exit satisfied: inspected concrete Go/TS/Svelte adapters, manager, session and
document seams, checker replacement, existing lifecycle tests and frontend
eligibility; recorded one ownership map, exposed bugs, conservative R02/R03
policy and deterministic schedules. No implementation/test code changed, so no
code test run is claimed. Documentation validation: `git diff --check`.
Parent release HOLDs and live capability omission remain unchanged. R02 was next
at audit completion; R02 and R03 are now implemented separately as linked above.
The exposed outcomes and pre-fix evidence limits above remain historical audit
findings, not the current implementation. R03 deliberately discards reused-URI
pushes rather than claiming matching-version freshness.


## R04 follow-up (2026-10-22)

[Offline discovery/trust seam implemented](code-diagnostics-r04.md): concrete
live registrations can join the checker proposal and resolve one contained
owner per file under a shared exact composite Trust grant. Session policy keys
remain concrete; grants do not become subset/provider grants. The historical
map above predates this seam. Engine/session-owner lifecycle wiring remains R05;
no production activation or parent completion is implied. Progress **19/28**.
