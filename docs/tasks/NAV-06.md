# NAV-06 — References list

- Status: ⬜ Not started
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
