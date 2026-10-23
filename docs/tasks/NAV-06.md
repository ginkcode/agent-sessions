# NAV-06 — References list

- Status: ✅ Done (2026-10-24)
- Estimate: 3 h
- Dependencies: NAV-04
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Find References shows every use in a list you can walk through.

## Design

- The results open in the left pane's Search mode as a result set titled
  "References to `name`", grouped by file like search matches, with the line
  previews read from the files (drafts from memory). This reuses one result
  list, its keyboard handling and F4/Shift+F4.
- "Back to search" restores the previous search text and results.
- Truncated answers say so.

## Done when

- Store tests cover building the result set, previews from drafts, and
  returning to the search; server-render tests cover the title and groups.

## Outcome

References now temporarily occupy the existing Search pane, grouped by file,
with the symbol name, truncation notice and Back to search. The previous search
text, results and keyboard position are preserved. Existing click, Enter and
F4/Shift+F4 navigation uses the exact server range, including multiline and
read-only external locations. Replacement is blocked for references.

Previews prefer in-memory drafts; host reads are limited to four at a time.
Failed reads leave an explicitly unavailable preview and a navigable location.
Project changes and leaving the references view invalidate pending reads.

Validation: 637 frontend tests pass; svelte-check reports 0 errors and the
pre-existing tsconfig warning. Native GUI interaction remains unverified.
