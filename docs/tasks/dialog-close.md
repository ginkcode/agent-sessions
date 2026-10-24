# Centered dialog close buttons (2026-10-24)

Go to File's text × depends on font metrics and appears off-center. Replace
modal header close glyphs with a shared centered SVG button, keeping its 28px
square, 4px radius, themed hover style and all existing close behavior.

## DC-01 — Shared control and migration (complete)

All ten controls now use the shared component. Existing handlers, accessible
names, GoToFile end alignment and PortForward focus class are retained. SSR
fixtures load the real control centrally. Updated the Delete dialog assertion
that previously required a literal text glyph. Focused dialog/focus tests: 40/40
PASS (including the new shared-control coverage).

Add DialogCloseButton with a symmetric 16px SVG, explicit grid centering, zero
padding, accessible label/title, native disabled prop and visible focus ring.
Migrate GoToFile, GlobalSearch, ManageSettings, PortForward, Import, Export,
Handoff, DeleteConfirm, HostEnv and MediaViewer's close only. Preserve labels,
handlers, GoToFile end alignment and PortForward's initial-focus hook. Remove
obsolete close-only CSS. Footer actions, tabs, popovers and footer-only dialogs
are out of scope.

## DC-02 — Regression gate (complete)

New shared-control tests cover default/custom accessible name and tooltip,
native disabled state, SVG geometry, class forwarding/alignment, compiled
centering/focus/hover CSS and all ten migrations with critical callback/focus
hooks. Full frontend suite 727/727 PASS. Check: 0 errors, existing
`ignoreDeprecations` warning. Build PASS with existing >500 kB chunk warning;
diff check clean. No native app launched; optical appearance remains unverified.

Use the actual shared control in SSR dialog fixtures. Test button semantics,
SVG geometry, disabled state, layout/focus hooks and migration coverage. Run
focused dialog/focus tests, all frontend tests, check/build and diff checks.
Native optical centering remains unverified unless exercised in the app.
Manual check: hover/focus/click/keyboard in light/dark, narrow headers, and
unchanged Escape/backdrop/busy-operation behavior.
