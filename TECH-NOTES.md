# Technical notes

Platform quirks that cost time to find. Check here before changing the
affected code.

## macOS fullscreen shortcut — exit needs investigation

Control+Command+F is the standard macOS fullscreen toggle in apps that support
native fullscreen. It may be handled by AppKit/the window menu before the
WKWebView receives a keyboard event.

User-reported desktop behavior (not yet independently reproduced):

- Control+Command+F enters fullscreen.
- Pressing it again does **not** exit fullscreen.
- The user observed that the shortcut already entered fullscreen; whether that
  came from native handling or the app's frontend handler is not yet confirmed.

The frontend now recognizes Control+Command+F on macOS in
`frontend/src/lib/fullscreen.ts` (previously Command+Shift+F). It calls the Wails
window fullscreen APIs. Non-macOS platforms keep F11. Reserve Control+Shift+F
for the planned Go to File directory-content search.

Follow-up investigation:

- Test a fresh desktop build, with focus in the file viewer, editor, and terminal,
  both before and after entering fullscreen.
- Check whether each keypress reaches `App.svelte` and whether
  `WindowIsFullscreen` reflects the actual native window state.
- Check native menu/AppKit handling and possible competing native/frontend
  toggles before choosing a fix. Do not assume the frontend sees both presses.
- Verify enter/exit with the green window button or native menu as a comparison.
- Keep the intentional Escape-does-not-exit-fullscreen behavior separate from
  this issue. Unit tests cover frontend logic, not native shortcut delivery.

No fullscreen-exit fix has been implemented or verified for this report.

## Mouse side buttons (back / forward)

The app moves back and forward between sessions with the mouse's side
buttons. In the DOM these are `MouseEvent.button` 3 (back) and 4 (forward),
but only some webviews report them that way, so each platform takes a
different path. All paths end in the same navigation as Alt+←/→ and ⌘[ / ⌘].

| Platform | Webview | What the page sees | How the app handles it |
|---|---|---|---|
| Windows | WebView2 | `mousedown`/`mouseup` with button 3/4 | DOM handler in `frontend/src/App.svelte` (`onMouseButton`, `navigationButton` in `frontend/src/lib/navHistory.ts`). Not yet confirmed on real hardware. |
| Linux | WebKitGTK (GTK3) | A **left** click (`button=0`, `buttons=0`, `which=0`) plus `auxclick`, identical for back and forward | GTK hook in `cmd/agent-sessions/mousenav_linux.c` takes GDK buttons 8/9 before WebKit sees them |
| macOS | WKWebView | Nothing usable | `NSEvent` local monitor in `cmd/agent-sessions/mousenav_darwin.m` |

The native hooks emit the Wails event `nav:mouse` with `"back"` or
`"forward"` (`mouseNavigateEvent` in `cmd/agent-sessions/mousenav.go`).
The frontend subscribes through `subscribeMouseNavigate` in
`frontend/src/lib/api.ts`. The shared Go side is
`cmd/agent-sessions/mousenav_native.go`.

### Linux

- GDK numbers the side buttons 8 (back) and 9 (forward), not 4/5. GDK's
  4–7 are the scroll wheel.
- WebKitGTK passes them to the page as a left-button press, so without the
  hook a side button also acts like a left click in the page. The hook
  consumes both press and release and navigates on release.
- The hook is attached to the `WebKitWebView` found by walking the
  toplevel windows, retried every 100 ms until Wails has created it.
- cgo: a Go file with `//export` can't define C functions in its preamble,
  so the C code lives in a `.c` file. Go honours its `//go:build` line.

### macOS

- Without a mouse utility, `NSEvent` reports the side buttons as
  `NSEventTypeOtherMouseDown`/`Up` with `buttonNumber` 3 (back) and 4
  (forward).
- **Logi Options+ never sends these.** It turns each side-button press into
  a horizontal **swipe** (`NSEventTypeSwipe`), the same event as a
  three-finger trackpad swipe. Each press arrives as a swipe with
  `deltaX = 0`, then one with `deltaX = +1` (back) or `-1` (forward). This
  matches Safari's convention.
- So the monitor handles both: buttons 3/4, and swipes with a non-zero
  `deltaX`. Zero-delta swipes are passed on. A side effect is that a
  three-finger trackpad swipe also navigates, as it does in Safari.
- Other mouse utilities (SteerMouse, BetterMouse, Mac Mouse Fix,
  SensibleSideButtons) may remap the buttons differently again, for example
  to ⌘[ / ⌘] keystrokes, which the keyboard shortcuts already cover.

### Windows

- WebView2 should report buttons 3/4 to the page like Chromium, and the
  DOM handler calls `preventDefault` so WebView2 doesn't try its own page
  navigation. This still needs testing on a real machine.
- Logitech's Windows software may remap the buttons too; check what the
  app receives before assuming the handler is broken.

### How to debug

The page often can't tell you what happened, because the event never
reaches it. Log at the native layer instead:

- **macOS:** add a second `NSEvent` local monitor with `NSEventMaskAny`
  that `NSLog`s each event's `type`, `buttonNumber`, `deltaX` and
  `keyCode` (skip mouse moves and drags), build with `wails build`, run the
  binary inside the `.app` from a terminal and press the buttons. A plain
  `go build` doesn't link: Wails adds frameworks such as
  UniformTypeIdentifiers.
- **Synthetic presses on macOS:** a small Swift tool that posts
  `CGEvent(mouseType: .otherMouseDown/.otherMouseUp, mouseButton: 3 or 4)`
  with `postToPid` tests the button path. It doesn't reproduce what a
  remapping utility sends, so always also test with the real mouse.
- **Linux:** a headless WebKitGTK probe can synthesise `GdkEventButton`
  with `gtk_main_do_event` and log what the page receives (for example
  under `broadwayd` with `GDK_BACKEND=broadway`).
