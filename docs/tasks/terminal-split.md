# Split terminals (2026-10-25)

Request: show two or more shells at once in the built-in terminal, as VS
Code's terminal does.

Model: a tab is a group of one to four panes; each pane is one terminal (one
PTY), as every tab is today. The backend is unchanged. `terminals.tabs` stays
the flat list of panes in start order, so session links, attention, alerts,
`tabFor`, `show(id)` and closing keep working on pane ids; `active` is the
focused pane. Groups (`terminals.groups`) own the tab-strip and pane order and
are not persisted (terminals do not survive a restart).

## TS-01 — Store: groups of panes (complete)

- Every launch (shell, resume, new session, handoffs) adds a group, as a tab
  today. A process that exits before it is shown, or a stale reply, adds none.
- `split(id, defaultCwd, host)` starts a shell after the source pane in its
  group, in the source's directory when it runs on the selected host (else the
  panel's default). At most 4 panes per group; when the source or its group is
  gone when the shell starts, or the group is full, the shell gets its own
  group. Its first size is the source pane's, divided along the split axis.
- Closing a pane focuses its neighbour in the group; an empty group goes and its
  neighbour is shown; the last pane hides the panel. Running agents still ask
  before closing, one at a time across a group.
- Every pane of the shown group is visible, so all of them count as viewed for
  attention and notification clearing, not only the focused one.
- Number shortcuts select groups. The stored panel layout stays
  `{open, height, position}`.

Verification: store tests cover groups per launch, split placement, start
order, directory per host, first size along each dock axis, the 4-pane limit, a
source closing mid-start, neighbour focus and proportions on close, exits in a
hidden tab, exited-before-shown and stale starts, number selection, `show` of a
pane in a hidden tab, pane resizing limits, closing a split tab with an agent,
and drops to a given pane; session-link tests cover viewing every pane of the
shown tab. Terminal, session-link and alert tests pass; check 0 errors.

## TS-02 — Panel layout (complete)

Panes of the shown group sit side by side with the panel at the bottom and
stacked with it on the left, separated by the shared splitter (each pane keeps
at least ~120 px wide or 60 px tall; terminals refit when the drag ends). The
pane views are one flat list, so changing groups never remounts a terminal and
loses its scrollback. With more than one pane the focused one has an accent
edge; clicking or typing in a pane focuses it. A group's tab shows each pane's
segment (status, agent, label, host, exit) with the focused one emphasized;
clicking a segment focuses that pane. Closing a tab closes its group.

Verification: panel render tests cover a single pane (no splitter, no
segment emphasis, each dock and maximize state) and a split tab docked at the
bottom and on the left (one tab per group, segments in pane order with the
focused one marked, one splitter, every pane view mounted, shares as flex
growth, the other group's pane hidden). Terminal, session-link and alert tests
pass; check 0 errors. Not run in the app: refitting side-by-side panes and
dragging the splitter.

## TS-03 — Controls and shortcuts (complete)

"Split terminal" in the panel header and the tab and terminal context menus;
"Close Terminal" in the terminal menu closes one pane. Shortcut ⌘\ on macOS
and Ctrl+Shift+5 elsewhere (VS Code's; Ctrl+\ stays with the terminal, where it
is SIGQUIT; Windows may map Ctrl+Shift+digit to input-language switching).
Dropped files go to the pane under the drop point.

The header button's icon divides the way the panes will (side by side, or
stacked when docked left); it and the menu items are disabled, with a reason,
once a tab holds 4 terminals. A split from the tab menu splits that tab's
focused pane. The shortcut acts only while the panel is open with a terminal
and no dialog is over it.

Verification: shortcut tests cover ⌘\, Ctrl+Shift+5 on QWERTY and AZERTY,
AltGr, Ctrl+\ left to the terminal on every platform, and the terminal
passing the shortcuts to the app; panel render tests cover the header button
and its full-tab state; source checks cover the App handler's conditions and
the terminal menu's items. Full frontend tests pass; check 0 errors.

Out of scope: moving panes between tabs, keyboard pane navigation, persisting
layouts.

## TS-04 — Regression gate (complete)

Full frontend tests, check, build and diff check.

Result: `npm test` 741 pass, 0 fail; `npm run check` 0 errors (the existing
tsconfig warning); `npm run build` succeeds (the existing chunk-size warning);
`git diff --check` clean.

Not exercised without the app, to check by hand: split twice and type in each
pane; drag the splitters, docked at the bottom and on the left; maximize;
close panes and whole tabs; let a shell exit; split a resumed agent; ⌘\ or
Ctrl+Shift+5 while typing in a terminal; drop a file on each pane. Ctrl+Shift+5
may be taken on Windows by input-language switching.
