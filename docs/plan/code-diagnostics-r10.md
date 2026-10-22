# R10 — probe-driven reconciliation wiring (2026-10-22)

**Complete (offline): reopen/refresh/pre-read probe wiring only.** Parent progress stays
**19/28**; CD-25 and CD-26 remain partial; live flag false; release HOLDs unchanged.

## Implemented

- Files store probes each displayed tab before reconcile/read (`probe`), guarded by
  host, connection, epoch, tab, read-sequence, edit version/revision and `saving`.
  Confirmed leaf `absent` marks `tab.diskAbsent`, keeps tab/text/draft/undo version/
  CAS revision/uncertain state (no close, no save), supersedes older reads, notifies the
  bridge (`diskState`) to close the live document and block publication, then calls the
  owned backend invalidation. Retained diagnostic paths without tabs are probed in
  batches of 32 and invalidated on confirmed absence.
- `unknown`, unsupported bindings, probe errors and read errors never delete anything;
  a failed read no longer clears displayed content. A late/old read error is dropped.
- Observed absent→present clears the absent mark and re-tracks the draft as a **new**
  lifecycle (new documentId, version 1); the old id is closed and its pushes fail. Same
  content recreation is handled only when absence was actually observed. No rename is
  inferred from revisions; confirmed in-app rename keeps its existing hook.
- Backend: additive `InvalidateCodeDiagnosticsPath` (Engine → App → RPC → frontend) with
  context ownership, catalog + root revalidation; `Manager.DeletePath` now also sends
  best-effort `didClose` for deleted live docs. See the [contract](code-diagnostics-contract.md#r10-additive-path-invalidation-2026-10-22).

## Tests

Go: `TestDeletePathClosesLiveAndClearsEveryProvider` (live + two checker sets cleared,
old URI/documentId/generation rejected after recreation), engine context/escape/
foreign/old-generation/removed-catalog checks, framed RPC ownership, App fail-closed.
Frontend (`live-documents.test.mjs`): absent with dirty+uncertain text, unknown, same-
content recreation fresh lifecycle, overlapping probes, host change, late read error.

## Validation

`go test ./...` pass; `go test -race` on codediagnostics/engine/rpc/app pass; scoped
`golangci-lint --new-from-rev=HEAD` 0 issues; `npm test` 449/449; `npm run check` 0
errors (existing deprecation warning); `npm run build` pass.

## Limits

Probe→invalidate is not atomic; content revision cannot show identical-content swaps
without an observed absence. Probes run only on reopen/refresh/reconcile, not
continuously (`unwatched` stays). Absent files' tabs are not auto-closed. No real-server,
native, remote or manual draft QA. R11 not started.
