# GIT-01 — Repository status

- Status: ✅ Done (2026-10-24)
- Estimate: 3 h
- Dependencies: none
- Plan: [editor-essentials.md](../plan/editor-essentials.md)

## Goal

Report the repository state the Changes view needs: branch, upstream and
every changed file with its staged and unstaged state kept apart.

## Design

- `files.GitRepoStatus(ctx, root) (RepoStatus, error)` from
  `git status --porcelain=v2 --branch -z --untracked-files=all`:
  - branch name (or detached HEAD and its short hash), upstream, ahead and
    behind counts, whether there is any commit yet;
  - per entry: path, original path for renames and copies, index state,
    worktree state, and `conflict` for unmerged entries;
  - a merge, rebase, cherry-pick or revert in progress (from the files in
    the Git directory), so the view can say so.
- Paths are relative to the project directory; entries outside it (when the
  project is a subdirectory of the repository) are left out, as the tree
  does today. At most 5,000 entries, with `truncated`.
- Not a Git repository, or no `git`, returns `ok: false` with the reason.
- `engine`, `rpc` (method `gitStatus`, capability `GitChanges`), app binding
  `GitStatus(dir)`, frontend API and types.

## Done when

- Tests with temporary repositories cover staged, unstaged, both, untracked,
  renamed, deleted, conflicted, detached HEAD, no commits, upstream ahead and
  behind, a subdirectory project and a non-repository.

## Outcome

- Implemented porcelain v2 NUL-delimited status with separate index/worktree
  states, rename/copy origins, conflicts, branch/upstream counts, detached and
  unborn HEAD, and a 5,000-entry truncation flag. Repository-root metadata is
  available, but entries and rename origins cannot expose outside-project paths.
- Detects merge, rebase, cherry-pick and revert state in each worktree's Git
  directory. Missing Git and non-repositories return `ok: false` with a reason;
  command errors and cancellation remain errors.
- Wired known-directory authorization through the engine, optional RPC
  `GitChanges`/`gitStatus`, selected-host app binding, frontend API/types and mock.
  Older backends reject unsupported calls without local fallback.
- Temporary-repository tests cover all requested states, upstream divergence,
  unusual filenames, linked worktree operations and subdirectory rename
  boundaries; parser tests cover copy origins, submodule XY and the entry cap.
  Engine authorization, RPC compatibility/roundtrip and frontend API tests pass.
- Validation: `go test ./...`, `go build ./...`, `go vet ./...`, frontend
  `npm run check`, `npm test` (640 passed), and `npm run build` passed. Existing
  frontend warnings remain: `ignoreDeprecations` configuration and large bundles.
