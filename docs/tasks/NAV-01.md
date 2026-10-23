# NAV-01 — Language server navigation requests

- Status: ⬜ Not started
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
