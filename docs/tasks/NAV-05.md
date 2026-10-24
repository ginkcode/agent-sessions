# NAV-05 — Navigation in the editor

## Hover documentation follow-up (2026-10-24)

- HD-01 complete: shared inline Markdown recognizes basic named/numeric entities
  and punctuation escapes once, preserving code literals, safe links and raw-HTML
  neutralization. Destinations normalize before validation/attribute escaping;
  source NULs cannot forge renderer tokens. Focused renderer/navigation/editor
  tests: 62/62 PASS. An initial new test had invalid JavaScript backtick quoting;
  corrected the fixture and reran successfully.
- HD-02 complete: Markdown hovers adapt indentation-delimited documentation
  admonitions to a titled blockquote, including four `&nbsp;` indentation tokens
  emitted by language servers. Fenced examples, plaintext, malformed directives,
  body/dedent boundaries and ordinary transcript Markdown remain unchanged.
  Compact blockquote styles match in viewer and editor. Synthetic Python class
  documentation verifies the signature, link and escaped attribute names together.
  Focused renderer/navigation/editor tests: 64/64 PASS; final full frontend suite:
  708/708 PASS (includes an additional hover-only scope regression). Check: zero
  errors, existing invalid `ignoreDeprecations` warning. Production build PASS,
  existing >500 kB chunk warning. `git diff --check` PASS. No app launch: native
  tooltip appearance and pointer behavior remain unverified.

The reported class hover displays literal `&nbsp;`, backslashes around attribute
names and `!!! abstract "Usage Documentation"`. The first two are standard inline
Markdown gaps; the third is a provider documentation extension. No backend or
hover lifecycle changes, dependencies, private data or app launch are needed.

- Status: ✅ Done (2026-10-24)
- Estimate: 3 h
- Dependencies: NAV-04
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

The same navigation while editing, against the draft the server has.

## Design

- `fileEditor.ts`: a `hoverTooltip` extension and a mod-click handler
  (`posAtCoords`), plus F12/Shift+F12 keys, added in `freshState`. Callbacks
  come through `FileEditorOptions`.
- Requests use the live document basis (`documentId`, `version`) only when
  the draft is synced; while a change is still pending, the request waits for
  the next sync (up to 1 s) instead of asking at a stale position.
- Results reuse the viewer's opening, picker and history.

## Done when

- Tests cover the synced and pending-sync cases and the keymap entries.
- Manual check still needed: tooltip placement in CodeMirror.

## Outcome

Implemented Cmd/Ctrl+click definitions with CodeMirror coordinate APIs,
F12/Shift+F12 through its keymap, and 400 ms `hoverTooltip` hovers. Callbacks
are read through the current owner, including on cached editor states. Hovers
use the shared safe Markdown/plaintext renderer, fit within the editor and
close on edits, scrolling, clicks and Escape. Definitions and references use
the viewer's result handling and history.

Draft requests wait up to 1 s for synchronization and ask only about the exact
unchanged displayed text. Changed drafts, resets, tabs and paths invalidate
requests, including while waiting for sync or an answer. Even before a newer
version is assigned, pending typing now makes an in-flight answer stale.
History records the current editor/source selection rather than an old landing,
and blocked/frozen history steps no longer consume entries.

Tests cover the real CodeMirror keymap, UTF-16 positions, modifier-click word
boundaries, hover creation/lifetime, cached owners, synced/pending/timeout and
stale requests. A real live-document bridge test verifies the request waits
for the exact inserted draft to be acknowledged. Frontend validation with the
concurrent NAV-06 integration: 637 tests pass; svelte-check has 0 errors and the
existing tsconfig warning. No app was launched; native tooltip placement and
pointer timing remain a manual check.
