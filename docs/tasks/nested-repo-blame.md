# Blame in nested repositories (2026-10-24)

Report: opening a directory that is not a Git repository but contains several
repositories, line blame never appears.

Cause: the viewer shows blame only when the listing came from Git
(`files.listing.git`). A non-Git root is listed by walking the tree, so `git` is
false and blame is hidden for every file, even inside nested repositories.
`files.BlameFile` already discovers Git from the file's own directory, so the
backend can answer; only the gate is wrong.

## NB-01 — Report nested repositories in walked listings (complete)

While walking a non-Git root, record each directory holding a `.git` entry
(a directory, or a file for worktrees and submodules) as
`Listing.Repositories` (sorted, slash-separated, relative). The walk still skips
`.git` directories and adds no Git commands or extra file-system reads. A
truncated walk may miss repositories past the limit; their files simply keep no
blame. Git listings are unchanged. Older servers omit the field, which keeps
today's behavior.

## NB-02 — Show blame inside nested repositories (complete)

Add `repositories` to `FileListing`, keep it immutable in listing snapshots, and
show blame when the listing came from Git or the selected file is inside a
listed repository (path-segment prefix match, so `app` does not cover
`apps/x`). Other conditions (preview, external files, Git-basis tabs,
capability, disconnected) are unchanged. Blame errors are still reported per
line by the backend. Test the Go walk (directory and file `.git`, nested
levels, no `.git` descent), the pure gate helper and the wiring; run Go and
frontend tests, check, build and diff checks.

Out of scope: changed-line markers in nested repositories (`gitChanges` runs
`git diff` from the root) and Git status in walked listings.

Verification: Go `./...` PASS (`go vet ./internal/files` clean); frontend
732/732 PASS; check 0 errors (existing tsconfig warning); build PASS (existing
chunk-size warning); diff check clean. The existing
`TestBlameFileRepositoriesBelowWorkspace` already covers backend blame below a
non-Git root. Not exercised in the native app or against a remote host; remote
hosts need the rebuilt server bundle to report repositories.
