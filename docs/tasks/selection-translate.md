# Selected-text menu and contextual translation (2026-10-24)

Right-clicking a transcript message offers Copy, Translate and Print / Save as
PDF. With text selected the app returned to the native menu, which offers little
in the desktop webview. A selection inside one message's rendered body now gets
its own menu: **Copy** (the selection) and **Translate to {language}**. It has no
Print/Save and no whole-message translation actions.

A short selection is ambiguous on its own, so the sentence(s) around it go to the
model as context. Only the selected text is translated. The result appears in a
popover at the selection; the message itself is not changed, and nothing is kept
after the popover closes.

Invariants:
- No selection → the existing message menu is unchanged.
- A selection crossing messages, touching the header or its controls, or outside
  the rendered body keeps the native menu.
- Selection actions never call or mutate the whole-message translations store.
- Context is bounded (8 KiB UTF-8 client-side, checked again by the backend).
- Automated tests do not establish native selection, focus or placement
  behavior; report them separately.

## SL-01 — Selection capture helper (complete)

`frontend/src/lib/selectionTranslation.ts`: validate one noncollapsed range whose
endpoints are both inside the message body; build rendered text from visible text
nodes with block boundaries; locate the selection by range offsets (not text
search); expand to the intersecting sentences with `Intl.Segmenter` or a
punctuation fallback; crop context without splitting code points. Snapshot text,
context and anchor rectangle at `contextmenu` time, because the menu takes focus
before an action runs.

## SL-02 — Backend/API (complete)

`translate.Client.TranslateSelection` sends a selection-specific system prompt
and the selection/context as separate JSON fields in the user message, with byte
bounds. The shared HTTP handling is factored out; whole-message requests are
unchanged. `App.TranslateSelection`, the frontend API/adapter/mock and the
unguarded (local) operation list gain the new call.

## SL-03 — Menu and popover (complete)

`MessageBubble` opens the selection menu from the snapshot. A nonmodal popover
shows loading, the escaped result or an error with Retry, and Copy/Close. It is
dismissed by Escape, outside clicks and scrolling. A request generation ignores
late replies after close, retry, a new selection or teardown.

## SL-04 — Regression gate (complete)

Selection/context unit tests, API/mock payload tests, component render tests,
stale-reply tests, Go request/bounds tests, and README/SPEC notes about context
being sent. Full frontend tests/check/build, Go tests/vet and diff check.

## Results

Frontend: selection tests 9/9 cover popover dialog/loading markup, repeated text, ordered range offsets, inline
markup, paragraphs and code, element endpoints, CJK fallback, 8 KiB UTF-8 crop
without broken surrogates, rejection of cross-message, control, hidden and
whitespace selections, menu items with no Print/Save, stale request guard, Wails
argument forwarding/error normalization, the unguarded local operation and mock
payloads. Full frontend suite 721/721 PASS; check has 0 errors and the existing
`ignoreDeprecations` warning; build passes with the existing >500 kB chunk
warning.

Backend: `go test` and `go vet` pass for `internal/translate` and
`internal/app`, including request payload/prompt, escaped instruction-like text,
legacy whole-message request regression, byte boundaries, redaction, local
settings and translation while remote. Only local `httptest` servers were used.

Not verified in the native app: selection preservation on right-click, popover
placement and focus, and a real provider call. Manual checklist: select a word
mid-sentence → right-click → Copy and Translate; Escape/outside click/scroll
close the popover; a selection across two messages shows the native menu; the
message menu still includes Print / Save as PDF.
