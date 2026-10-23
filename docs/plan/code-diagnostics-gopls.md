# Code diagnostics: gopls adapter and supported-version decision (CD-22)

> **Update 2026-10-23:** Any gopls build is accepted; a managed gopls is built with the host's Go at the release that Go supports; remote hosts are supported. See the [editor (Zed) model](code-diagnostics-zed-model.md).

Status: **partial** — spike and adapter seam complete with automated tests; **local
experimental production route active in the desktop app (2026-10-23)** with a real
installed-gopls smoke through the production adapter; remote stays fail-closed; no UI/native
QA. Parent plan: [code-diagnostics.md](code-diagnostics.md).

## Local experimental activation (2026-10-23)

**Route.** Only the desktop `NewApp()` local Engine passes
`engine.WithLocalLiveCodeDiagnostics()`, and only on darwin/arm64. `NewAppWithService`,
test Apps and the headless `agent-sessions-cli serve` Engine do not, so remote hosts stay
fail-closed twice: the missing option, and rpc `initializeDiagnostics`, which still requires a
verified SSH/WSL identity (CD-09). `App.ConnectionState()` for Local now reports
`codeDiagnostics` (Stage 1 surface + host ID) and `liveCodeDiagnostics` (Stage 1 + document
surface + `LiveCodeDiagnosticsReady()`). The frontend keeps the local file capabilities and
merges only these two, with live requiring Stage 1. The capability means that the route exists.
It does not mean gopls is installed.

**Discovery (metadata only, `internal/engine/code_diagnostics_local_live.go`).** The search
directories are the inherited PATH, then `~/go/bin`, `~/.local/bin`, `$GOPATH/bin`,
`/opt/homebrew/bin`, `/usr/local/go/bin` and `/usr/local/bin`. Only absolute, clean entries
are kept, de-duplicated, up to 64. GOROOT is the parent of the `bin` directory of the
first resolved regular executable `go`, and gopls is searched in `GOROOT/bin` first. The
caches are `UserCacheDir/go-build` and `~/go/pkg/mod`. Nothing is executed, installed or
downloaded. `cd.DiscoverGoLive` registered gopls plus TypeScript and Svelte as independent
`unsupported` opened-documents providers. (Since 2026-10-23, `cd.DiscoverLocalLive` replaces it
and also discovers vtsls; see [CD-23](code-diagnostics-typescript.md#production-wiring-2026-10-23).
Svelte is still unsupported.) A missing gopls or Go
gives `missingTool`, and any version other than v0.24.0 gives `unsupported`. Both are provider
statuses, not errors. The supported list was not widened. A managed install
(`managedtools.ReviewInstalledGo`) still needs Go ≥1.27 that an operator has reviewed. This
change does not provision it; it prefers an existing supported install.

**Approval.** `ServerConfig.Toolchain` binds `GOROOT/bin/go`, plus `GOROOT/VERSION` and
`GOROOT/go.env` when they are regular files (policy `gopls-toolchain-v1`), into the same
composite fingerprint as gopls, `go.mod`/`go.sum`/`go.work` and the checker. Changing any of
them requires approval again. Approving execution is still separate from discovering tool
metadata, and a launch happens only after per-project approval and a lease check.

**Launch.** `cd.HostSessionLaunch` (darwin/linux) starts the absolute fingerprinted
executable with the fixed argv `serve`, the complete adapter environment (`GOTOOLCHAIN=local
GOPROXY=off GOSUMDB=off GOENV=off GOWORK=off`, reviewed HOME/GOCACHE/GOMODCACHE/GOROOT), the
authorized root as working directory, `os.Pipe` stdio and its own process group. Terminate
sends SIGKILL to the whole group.

**Saved checks vs live diagnostics.** *Check Project* (go vet, Stage 1) runs only on saved
files on disk and parses go vet stderr. That parser was not extended. Live diagnostics send
the displayed **unsaved** text of opened Go files to gopls through `didOpen`/`didChange` and
accept only versioned publications that match the document version. Drafts are never saved.
The two providers stay distinct (`go` vs `gopls`) and are not deduplicated. The CodeChecks
copy now says so whenever live is advertised. Otherwise it keeps "Only saved files are
checked". It also said TypeScript and Svelte live diagnostics were not enabled; since
2026-10-23 the copy names the live languages actually offered, and only Svelte is not enabled.

**Evidence (2026-10-23, darwin/arm64).** Host gopls is `v0.24.0` (`golang.org/x/tools/gopls`,
built with go1.27.2), at the mise install `…/go/1.26.8/bin/gopls`. The toolchain is go1.26.8
darwin/arm64, and the GOROOT has `VERSION` and `go.env`.

- `TestLocalLiveRealGoplsSmoke` (internal/engine) runs on a disposable synthetic temp module
  (`go 1.21`). It uses the production `discoverLocalLive` + `HostSessionLaunch` + Engine owner:
  - Attach produced a proposal with 4 inputs (gopls, bin/go, VERSION, go.env), and open was
    refused before approval.
  - After session approval, a draft v1 with a syntax error got the versioned diagnostic
    `expected ';', found 'EOF'` (error, current, range 3:0–3:0) in about 160 ms. A
    `codeDiagnostics:updated` event carried it.
  - Changing to the fixed text (v2) got an empty publication that cleared the set in about
    20 ms. After close, the on-disk file was unchanged (never saved).
- `TestLocalLiveRouteCapabilitiesApproveOpenAndEvent` (internal/app) checks that the local
  `ConnectionState` capabilities are true. It then goes through attach, proposal, refusal
  before approval, approve, open the draft, the real SessionManager/LSP transport/gopls bridge,
  a framed fake-server push, a `codeDiagnostics:updated` event, change, and the empty clear.
  `TestLocalCapabilitiesWithoutLiveRoute` checks that a non-desktop Engine omits live.
- `TestDiscoverGoLiveBindsToolchainAndHoldsOtherLanguages` (now `TestDiscoverLocalLiveBindsToolchainAndHoldsOtherLanguages`),
  `TestHostSessionLaunchFixedArgvAndProcessGroup` (grandchild killed with the group) and the
  engine discovery unit tests (`localExecutableDirs`, `localGoRoot`, missing tool ⇒ status).
- Frontend tests cover the local capability merge and the CodeChecks live/saved copy.

**Remaining holds.** There is no native UI/manual QA (a draft edit in the real window, undo
history, panel/inline markers), so the acceptance item about not losing drafts stays open.
Remote live stays disabled (CD-09). TypeScript and Svelte stay unsupported (CD-23/CD-24). The
Stage 1 and Stage 2 release HOLDs are unchanged. Linux and Windows local live stay disabled
by the platform gate. The GOROOT binding is an enumerated identity, not attestation of the
standard library or module cache.
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

- *(Superseded 2026-10-23 for the local desktop route; see "Local experimental activation"
  above. Remote is still not wired.)* **Not wired to production.** `liveCodeDiagnostics` stays unadvertised: no `Registration`,
  `SessionManager`, `SessionDocuments`, `Notifications` hook or `DiscoverGopls` caller
  exists in the app/engine/RPC layers, no host PATH/cache policy owner, no mapping of
  `GoplsPlan.Provider()` into the manager snapshot (the manager has no LSP provider
  registration or `openedDocuments` coverage path), and notices are surfaced only via a
  callback. A complete route is follow-up work (CD-25/CD-26 integration or a new task).
- *(Superseded 2026-10-23: `TestLocalLiveRealGoplsSmoke` runs real gopls through this
  adapter.)* **Real gopls through this Go adapter was not run.** The spike used a Python stdio driver
  against disposable modules; the recorded publications are replayed through the production
  normalizer in `TestGoplsEvidenceMatchesSupportedVersion`. No UI/native draft QA
  (introduce/fix a type error in a draft, undo history) was performed, and go vet + gopls
  coexistence is by distinct provider/origin/source only, not exercised end-to-end.
- Only v0.24.0 on darwin/arm64 is supported. Behavior with `go.work` honored
  (`GOWORK` unset) was not adopted.
- Server-side limits (very large publications, many open files) rely on transport/limits
  from CD-18/CD-19 and `maxPublished`; no gopls load test was run.
