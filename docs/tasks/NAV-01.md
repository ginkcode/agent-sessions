# NAV-01 — Language server navigation requests

- Status: ✅ Done (2026-10-24)
- Estimate: 4 h
- Dependencies: CD-20, CD-22 (live sessions for open documents)
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Ask the running language server for definitions, references and hover text
at a position in an open document, with bounded and validated answers.

## Design

- Client capabilities in `initializeParams`: `textDocument.definition`
  (`linkSupport`), `textDocument.references`, `textDocument.hover`
  (`contentFormat: ["markdown", "plaintext"]`). These describe what the client
  understands, not what runs, so they are not part of the approval policy and
  do not require approving again.
- `negotiateCapabilities` records `definitionProvider`, `referencesProvider`
  and `hoverProvider`.
- `SessionDocuments.Navigate(ctx, op, documentID, version, position)` in a new
  `lsp_navigation.go`. The document must be open at exactly that version, so
  positions match what the server has. The owner is found as for document
  sync; for Python, Pyright answers (Ruff has no navigation).
- Answers are normalized into locations `{path, range}` with UTF-16 ranges.
  `Location` and `LocationLink` are both accepted (the link's target
  selection range is used). Paths inside the project root are relative;
  paths outside are kept absolute and marked `external` for NAV-03.
- Hover returns Markdown or plain text, capped at 64 KB; references at 2,000
  locations with `truncated`. A request times out after 10 s.
- Unsupported operation, not running, stale version and timeout each return
  their own error code.

## Done when

- Fake-server tests cover each operation, both location forms, outside-root
  results, version mismatch, missing provider capability, caps and timeout,
  and that the approval policy hash does not change.

## Outcome

- `initializeParams` always declares `textDocument.definition` (with
  `linkSupport`), `references` and `hover` (Markdown, then plain text).
  `ServerConfig.Policy()` is unchanged, which a golden test checks.
  `negotiateCapabilities` records `Definition`, `References` and `Hover`.
- The work is split across two layers:
  - `Manager.Navigate(ctx, context, documentId, version, op, position)`
    checks the document, version and position under the manager lock, then
    picks the route. When several servers own the file, it uses the one that
    offers the most navigation, so Python goes to Pyright and not Ruff. Ruff
    answers hover only while Pyright is not running.
  - The request itself runs outside the lock, through the new
    `NavigationProvider` seam, which `SessionDocuments.Navigate` implements.
- Answers are normalized into `{path, range, external}`:
  - Accepted: `Location`, `Location[]` and `LocationLink[]`. A link uses its
    target selection range, or its target range when there is none.
  - Dropped: duplicates, non-file URIs, bad ranges, and paths under the
    root that don't resolve inside it (a missing file or an escaping link).
  - Capped at 2,000 locations, with `truncated`.
  - Hover reads MarkupContent, MarkedString and arrays of them. A
    language-tagged string becomes a fenced block. The text is capped at
    64 KB on a UTF-8 boundary.
- The 10 s timeout uses the session's injectable clock, and the server gets
  `$/cancelRequest`.
- Error codes:
  - `invalidArgument`: unknown operation or a position outside the text.
  - `staleVersion`: version mismatch, or the server answered
    ContentModified.
  - `notRunning`: the session is stopped.
  - `resyncRequired`: the server restarted and hasn't seen the document, or
    the route is broken.
  - `unsupported`: no provider capability, or no navigation seam.
  - `timeout`, `canceled`, `busy`, `toolFailed`: the server's own errors
    (`toolFailed` never includes its text).
- Deviation: Pyright ran with `pyright.disableLanguageServices: true`, which
  makes it answer nothing to navigation. It is now `false`. The settings are
  part of the approval policy, so Python projects ask for approval once more
  after this update. Organize imports and tagged hints stay off.
- Tests: `lsp_navigation_test.go` covers:
  - a fake server for each operation and both location forms;
  - outside-root and dropped places;
  - the reference and hover caps;
  - stale version, position and operation checks that never reach the
    server;
  - server errors, the fake-clock timeout with cancel, and not running;
  - route choice between Ruff and Pyright, and the missing capability;
  - capability negotiation and the unchanged policy.
