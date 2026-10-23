# NAV-03 — Read-only external definitions

- Status: ⬜ Not started
- Estimate: 3 h
- Dependencies: NAV-02
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Going to the definition of `fmt.Println`, a module dependency or a Python
package opens its source, read-only, even though it is outside the project.

## Design

- When a navigation answer has external locations, the backend records those
  absolute paths as granted for that live attachment (at most 256, oldest
  dropped). `ReadNavigationFile(context, path)` reads only a granted path,
  with the normal text read limits, and refuses credential and agent data
  names like the edit policy does.
- The viewer opens it in a tab marked read-only with the path shown in full
  (home shortened to `~`). No editing, Git, blame or diagnostics in that tab.
- Navigation from inside an external file works when the server owns it,
  otherwise the tab is view-only.

## Done when

- Tests cover granted and refused paths, the grant limit, protected names and
  the end of grants when the attachment ends.
- Server-render tests cover the read-only tab.
