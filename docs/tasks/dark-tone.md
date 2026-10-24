# Native-tone dark surfaces (2026-10-24)

Target: #2C2C2B, matching raw screenshot title-bar pixels RGB (44,44,43).
The initial choice matched all large surfaces. The user clarified that dark mode
should retain lighter/darker surface layers, equivalent to the light-mode hierarchy.
The native title bar remains OS-managed; its appearance can vary with focus and
system settings.

## DT-05 — Theme-matched count tooltip in light mode (complete)

Follow-up report: light mode still showed the wrong theme, because the base chip
was inverted (a #0b1220 chip on the light list). Make the base tooltip a light
elevated surface: `--bg-primary` background, `--text-primary` text,
`--border-color` hairline and a soft shadow, with role colors at least 4.5:1 on
white (user #2563eb, assistant #7c3aed, tool calls #047857, errors #dc2626). The
DT-04 screen-dark surface and tints remain, now also overriding tool calls
(#34d399). Update the regression test; run frontend tests, check, build and diff
checks.

Verification: frontend 728/728 PASS; check 0 errors (existing tsconfig
warning); build PASS (existing chunk-size warning); diff check clean. The test
rejects the inverted chip and asserts ≥4.5:1 for every role color and text on
both tooltip surfaces. Native appearance not exercised.

## DT-04 — Session-list count tooltip (complete)

Report: hovering a session row's message count in dark mode shows a light chip
("user N · assistant N · tool calls N"). The tooltip inverted the theme
(`--text-primary` background), which suits light mode but yields a #f2f2f2 chip
in dark mode with pastel role colors at ~1.7–2.5:1 contrast.

Keep the light-mode inverted chip unchanged. In screen dark mode, render it as an
elevated dark surface: `--bg-tertiary` background, `--text-primary` text, a
`--border-color` hairline and a deeper shadow. Lighten the role colors only in
dark mode so each is at least 4.5:1 on #383837 (user #93c5fd, assistant #c4b5fd,
tool calls #34d399, errors #fca5a5). Add a regression test for the override and
the contrast; run frontend tests, check, build and diff checks.

Verification: frontend 728/728 PASS; check 0 errors (existing tsconfig
warning); build PASS (existing chunk-size warning); diff check clean. The test
asserts the light chip is unchanged and readable, the screen-dark override, and
≥4.5:1 for every role tint and text on #383837. Native appearance not exercised.

## DT-03 — Restore layered surfaces (complete)

Clarification: use #242423 for the primary content/terminal canvas, #2c2c2b for
secondary panels/message cards, and #383837 for tertiary controls. This restores
the light-mode surface hierarchy without reintroducing a blue/purple cast.
Update terminal fallback and regression expectations; verify text contrast on
all three layers. Light mode and semantic colors remain unchanged.

## DT-01 — Palette update (complete)

Updated shared dark tokens, terminal fallback and the screen-dark command-block
exception. Editor surfaces already use shared tokens; no duplicate palette added.

Set dark primary/secondary surfaces to #2c2c2b, tertiary controls to #383837,
and muted text to #b0b0ad. Update the terminal canvas fallback and the dark-only
Import command-block exception. Leave light/print, ANSI output colors, accents,
selection, borders, semantic/status colors and syntax/diff colors unchanged.

## DT-02 — Regression gate (complete)

Focused palette/terminal tests 35/35 PASS; full frontend tests 724/724 PASS.
`npm run check`: 0 errors, existing `ignoreDeprecations` warning. Production
build passes with the existing >500 kB chunk warning; diff check is clean.
Palette tests assert the target tokens, preserved light tokens and selected
semantic colors, primary/secondary/muted text contrast of at least 4.5:1 on both
base and control surfaces, screen-dark command override, and token-driven editor
surfaces. Terminal tests cover dark fallback/CSS precedence/cursorAccent while
preserving ANSI black. Native visual matching has not been exercised.

Test dark tokens, text contrast, unchanged light tokens, terminal canvas/CSS
precedence/cursor background and preserved ANSI black. Run frontend tests,
check/build and diff checks. Native visual matching is unverified unless tested.
Manual follow-up: compare titlebar, sidebar, list, transcript, terminal and editor
in dark/system-dark; inspect controls, hover and selection; verify light mode.

DT-03 verification: frontend 724/724 PASS; check 0 errors (existing tsconfig
warning); build PASS (existing chunk-size warning); diff check clean. Text
contrast passes on all three layers. Native appearance not exercised.
