# Code diagnostics: gopls adapter and supported-version decision (CD-22)

Status: **partial** — spike and adapter seam complete with automated tests; no production
route, no UI/native QA. Parent plan: [code-diagnostics.md](code-diagnostics.md).
Fixtures: `internal/codediagnostics/testdata/gopls/evidence.json`. Code:
`internal/codediagnostics/go_lsp.go`.

## Supported-version decision

| gopls | Built with | Platform | Decision |
|---|---|---|---|
| `v0.24.0` (`golang.org/x/tools/gopls`) | go1.27.2 | darwin/arm64 | **Supported** (observed) |
| anything else, `(devel)`, other module path | — | any | **Unsupported** until a spike adds it |
| any version | — | not darwin/arm64 | **Unsupported** (platform not verified, as for go vet) |

The version is read from the binary's embedded build info (`debug/buildinfo`); the server
is **not executed** to probe it. The Go toolchain used for package loading was go1.26.8
(`GOTOOLCHAIN=local`). Pull-only servers are unsupported: push is the baseline.

## Spike method (2026-10-22)

Installed `gopls` only (nothing installed or downloaded), driven over stdio with
`gopls serve`, against **disposable synthetic modules in a temp directory** (never user
projects or this repository). Environment: reviewed `HOME`/`GOCACHE`/`GOMODCACHE` in the
temp tree, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOENV=off`, `GOWORK=off` (and `go.work`
explicit once for comparison). Client advertised only `general.positionEncodings:
["utf-16"]`, no `workspaceFolders`, and answered every server request with
`-32601`. The source contained a non-BMP character on the error line.

## Observations

- **Initialize**: `textDocumentSync = {openClose: true, change: 2, save: {}}` (incremental
  sync; `save` object ⇒ didSave without text), no `positionEncoding` in the result (UTF-16
  default, accepted by CD-19), **no `diagnosticProvider`** (push only). `serverInfo.name` is
  `gopls`. No server→client requests arrived during initialization with these client
  capabilities.
- **Push versions**: `didOpen v1` ⇒ one `publishDiagnostics` with `"version": 1`; full-text
  `didChange` v2 ⇒ `version: 2`, `diagnostics: []` (**empty publication clears**); break
  again v3 ⇒ `version: 3` with the finding. Publications for open documents carry
  `version`.
- **`didClose` publications are unversioned** and repeat the on-disk findings. They are
  not a clear and must not be treated as fresh for any open document.
- **Coalescing**: three back-to-back `didChange` (v2–v4) produced a single publication,
  version 4. Intermediate versions are skipped, so absence of a publication for version N
  is normal; the adapter never waits on a specific version.
- **Ranges**: zero-based UTF-16, exclusive end (non-BMP literal produced a 5-column span
  covering the surrogate pair). `source: "compiler"`, `code` is a string
  (`IncompatibleAssign`), `codeDescription.href` present (ignored).
- **Notifications other than diagnostics** (`window/showMessage`, `window/logMessage`) arrive
  during load. They are dropped; bodies are never logged or retained.
- **Workspace layouts** (each published diagnostics for the opened file): root `go.mod` +
  nested `sub/go.mod`; nested module only (no root `go.mod`); `go.work` using `./in` and
  `../out`. With `GOWORK=off` the sibling module outside the root is never configured.
- **Shutdown**: `shutdown` ⇒ `null`, `exit` ⇒ exit code 0.

## Freshness model implemented

1. **Matching version only.** `Manager.PublishPathDiagnostics` (new, path→open document,
   same guards as `PublishDocumentResults`) rejects wrong generation, wrong version,
   broken, closed or never-opened documents; late and future versions cannot clear or
   replace a newer set. Matching pushes replace the document's `lsp` set, empty included.
2. **Unversioned ⇒ uncertain** and panel-only (ranges removed); it is never `current`.
   This covers gopls `didClose` echoes. No synchronization strategy that would upgrade an
   unversioned push has been tested, so none is claimed.
3. **Ranges are re-proven** against the mirrored displayed text (CRLF, non-BMP, inside a
   surrogate pair); malformed/unprovable ranges become file-level findings with
   `invalidRange`. Reversed, negative, incomplete or non-integer ranges are rejected the same way.
4. **Attribution** is `source = "gopls/<server source>"` (or `gopls`), distinct from go vet.
   `severity` 1–4 map to error/warning/information/hint, missing ⇒ warning, others dropped.
   Provider ownership is `origin: "lsp"`; no cross-provider deduplication is done.
5. **URIs** must be authority-free `file:` URIs, bounded, without query/fragment/opaque.
   A well-formed URI outside the root (or a symlink escape) becomes provider status
   (`OutsideRootNotice`), never a link, and nothing is read. Malformed URIs/payloads yield a
   bounded partial notice. Missing files are ignored.

## Workspace detection (contained)

Bounded rooted walk (4096 entries, depth 32, 24 modules, 128 config files; symlinks,
hidden, `_`, `vendor`, `node_modules`, `testdata` skipped ⇒ partial). The authorized root is
never promoted to a parent module, repository or `go.work`; nested-only roots are
supported. `go.work` `use` entries and local `replace` targets leaving the root (including
absolute paths) set `partial` with the outside-root notice. gopls always runs with
`GOWORK=off`, so out-of-root workspace members are never configured; coverage is reported as
`openedDocuments`, `partial: true`, `externalChanges: "unwatched"`.

## Availability (independent of go vet)

`DiscoverGopls` → `missingTool` (no gopls on the reviewed PATH, or no Go toolchain),
`unsupported` (platform, version, foreign/unreadable binary, no contained module),
`available`. Fixed argv `["gopls","serve"]` (`serve`, no flags from input), explicit
environment, `Command.Policy` bound through CD-02 approval, config identities =
`go.mod`/`go.sum`/`go.work`. gopls is never installed, downloaded or version-probed by
execution. Trust is not a sandbox: gopls and the Go toolchain may still read dependencies,
write caches and (despite `GOPROXY=off`) are not guaranteed network-free.

## Session policy

CD-19's configuration is used unchanged: UTF-16 only, no workspace/text-document/window
capability advertised, `workspace/applyEdit`, `executeCommand`, registrations (file
watchers) and the rest rejected with explicit errors. A new `SessionOptions.Notifications`
hook forwards only `textDocument/publishDiagnostics` (raw, bounded params) from the current
generation; `GoplsBridge.Handle` normalizes and publishes through the manager.

## Coverage and failure states

- Coverage is **opened documents only**. Unopened files are not continuously synchronized
  (file watching is unsupported ⇒ `unwatched`). Initialization success or an empty push is
  not "clean Go".
- Crash/disconnect ⇒ session `failed`/`disconnected`, retry capped (CD-19); open documents
  fail changes and then require `resyncRequired` (CD-20). A missing executable fails launch
  with a bounded error (no server text).

## Not done / caveats

- **Not wired to production.** `liveCodeDiagnostics` stays unadvertised: no `Registration`,
  `SessionManager`, `SessionDocuments`, `Notifications` hook or `DiscoverGopls` caller
  exists in the app/engine/RPC layers, no host PATH/cache policy owner, no mapping of
  `GoplsPlan.Provider()` into the manager snapshot (the manager has no LSP provider
  registration or `openedDocuments` coverage path), and notices are surfaced only via a
  callback. A complete route is follow-up work (CD-25/CD-26 integration or a new task).
- **Real gopls through this Go adapter was not run.** The spike used a Python stdio driver
  against disposable modules; the recorded publications are replayed through the production
  normalizer in `TestGoplsEvidenceMatchesSupportedVersion`. No UI/native draft QA
  (introduce/fix a type error in a draft, undo history) was performed, and go vet + gopls
  coexistence is by distinct provider/origin/source only, not exercised end-to-end.
- Only v0.24.0 on darwin/arm64 is supported. Behavior with `go.work` honored
  (`GOWORK` unset) was not adopted.
- Server-side limits (very large publications, many open files) rely on transport/limits
  from CD-18/CD-19 and `maxPublished`; no gopls load test was run.
