# NAV-03 — Read-only external definitions

- Status: ✅ Done (2026-10-24)
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

## Outcome

- `Manager.Navigate` records an answer's external paths as grants on the
  attachment, but only if the attachment is still current and approved once
  the server answers. There are at most 256 grants, oldest dropped first.
  They end on detach, revoke, invalidation and the max-sequence path.
- `files.ReadExternal` reads an absolute path with no `..` steps. It resolves
  links and runs both the path and its target through the edit policy's
  refusal rules (`EditPolicy.refuse`), so it refuses credentials, agent data
  and the store. It never sets Edit or Changes. `Engine.ReadNavigationFile`
  checks the grant first. The method travels over RPC as
  `readNavigationFile`. It is gated on the navigation capability, and the
  client re-checks size, UTF-8 and path.
- Frontend:
  - The store's `readNavigationFile` drops answers for an attachment that has
    moved on.
  - `files.openExternal` opens a tab marked `external`. It is read through
    the grant, kept out of the live documents and the tree, and cannot be
    edited.
  - The tab shows a lock and its home-shortened path. The viewer says
    "Read-only: outside the project" and hides edit, diff, blame, checks and
    link following.
- Deviation: navigation from inside an external file is view-only for now.
  Opening external files as live documents needs owner routing outside the
  root; NAV-04 revisits it.
