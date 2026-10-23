# Code diagnostics: TypeScript adapter and version spike (CD-23)

> **Inline fix 2026-10-23:** vtsls omits publication versions. Instead of removing
> those ranges, the backend now retains validated TypeScript ranges and adds
> `basis.observedVersion` (the mirror version at receipt). Freshness stays
> `uncertain`; this is not server confirmation. The frontend can draw them only
> over that same synced document lifecycle/version, hiding them on edits, pending
> sync, reopen or disconnect. A late unversioned server result can still refer to
> earlier text: this is best-effort editor positioning, not a freshness guarantee.
> Go/Ruff version handling is unchanged. This supersedes the panel-only policy
> below. Covered by fake-server and frontend source-projection tests; native QA pending.


> **Update 2026-10-23:** Any `@vtsls/language-server` (project `node_modules`, login-shell PATH or the app's managed copy) is accepted at any version, the workspace TypeScript is used when present, and remote hosts are supported. See the [editor (Zed) model](code-diagnostics-zed-model.md).

Status: **partial**. The production local live route discovers vtsls 0.3.0 with
TypeScript 5.9.3 from metadata alone. The real server passed a smoke test through
the Go adapter (2026-10-23). Not done: no approved install on this host, no manual
editor QA, remote stays fail-closed. Parent plan: [code-diagnostics.md](code-diagnostics.md).
Pattern: [code-diagnostics-gopls.md](code-diagnostics-gopls.md).
Fixtures: `internal/codediagnostics/testdata/typescript/evidence.json`. Code:
`internal/codediagnostics/ts_lsp.go` (+ shared normalizer in `go_lsp.go`).

## Inventory (2026-10-22, read-only; nothing installed or downloaded)

| Candidate | Found | Verdict |
|---|---|---|
| `typescript-language-server`, `vtsls`, `svelteserver`, `vscode-*-language-server`, `tsc`, `tsserver` on `PATH` | none | not available |
| `frontend/node_modules/.bin/tsserver` / `tsc` (TypeScript 5.9.3) | raw tsserver, speaks `Content-Length` but **not LSP** | unsupported (out of scope) |
| `frontend/node_modules` LSP wrappers (`@vtsls`, `typescript-language-server`, `vscode-languageserver*`) | none; `svelte-check` 4.7.6 is a checker CLI, not a server | not available |
| mise node 22.23.2 global modules | only `corepack`, `npm` | not available |
| **Zed-managed private install** `~/Library/Application Support/Zed/languages/vtsls` | `@vtsls/language-server` **0.3.0** with `typescript` **5.9.3** (`node_modules/.bin/vtsls`) | LSP-speaking, **but an editor's private cache, not host-approved tooling and not on `PATH`** |

The Zed copy is the only LSP-speaking TypeScript server found. It was **only executed
read-only against a disposable synthetic project** to record behavior; the app must never
depend on another editor's private directory.

## Supported-version decision

| Wrapper | Language service | TypeScript | Node | Decision |
|---|---|---|---|---|
| `@vtsls/language-server` 0.3.0 | `@vtsls/language-service` 0.3.0 | 5.9.3 (bundled with the wrapper) | ≥ 18 | **Verified** through the Go adapter on darwin/arm64 (policy `vtsls-v2:0.3.0:ts5.9.3:darwin/arm64:…`) |
| any other version, mismatched service, other package name | | | | **Unsupported**, with the found versions in the notice |
| missing wrapper, TypeScript or service; no usable `node` | | | | **missingTool** ("Not installed"), with a manual install command |
| version-manager shim only (mise/asdf/volta/nodenv) | | | | **Unsupported**; shims are never executed |
| not darwin/arm64 | | | | **Unsupported** |

Versions come from package metadata only: the wrapper's `package.json`, then
`@vtsls/language-service` and `typescript` resolved the way Node would (nested for
global npm installs, flattened for project and editor installs). Node's version
comes from `<prefix>/include/node/node_version.h`. Nothing is executed to probe a
version. `typescript-language-server` and Svelte servers are unverified (CD-24 owns Svelte).

## Observed behavior (vtsls 0.3.0 / TS 5.9.3, Python stdio driver)

Executable `…/node_modules/.bin/vtsls`, argv `--stdio`, runtime `node` (v22.23.2), minimal env.
Client advertised UTF-16 and `publishDiagnostics.versionSupport: true`, no workspace folders;
server requests were answered `-32601`.

- **Initialize**: `serverInfo {vtsls, 0.3.0}`; `textDocumentSync = {openClose: true, change: 2, save: false}`
  (incremental); no `positionEncoding` (UTF-16 default); **no `diagnosticProvider`** ⇒ push only.
- **Pushes are unversioned.** Every `publishDiagnostics` lacked `version`, even with
  `versionSupport` advertised. Under CD-01/CD-21 rules this makes **all vtsls results `uncertain`,
  panel-only (ranges removed)**; no server-specific strategy to upgrade them was tested.
- Open (error) ⇒ push with finding; fix edit ⇒ empty push (**clear**); break again ⇒ finding;
  `didClose` ⇒ empty push. Range column 22 on a line containing a non-BMP character is a UTF-16
  column; `source: "ts"`, numeric `code` (2322), severity 1.
- Config publications: a `tsconfig.json` received a push (including "No inputs were found"
  errors when `include` matched nothing) although never opened. The bridge ignores non-JS/TS paths.
- Only `window/logMessage` notifications otherwise; no server→client requests. Bodies dropped.
- `shutdown` ⇒ `null`, exit code 0. Missing-binary/crash handling is the CD-19 behavior, covered with
  fakes only.

## Implemented (automated)

- **Ownership** `TSLanguageID`: `.ts/.mts/.cts` → `typescript`, `.tsx` → `typescriptreact`,
  `.js/.mjs/.cjs` → `javascript`, `.jsx` → `javascriptreact`. `.svelte`, `.go`, `.json` and
  everything else are **not owned** (CD-24 refines Svelte). `TSPlan.Documents` also requires the file
  to sit under a discovered contained package root; there is no parent promotion.
- **Package discovery** (`discoverTSPackages`, on the walker `discoverProjectDirs` shared with the
  [Ruff adapter](code-diagnostics-python.md)): bounded rooted walk (4096 entries, depth 32, 48
  packages, 192 config files). A directory with `package.json`, `tsconfig.json` or `jsconfig.json` is
  a package root; nested-only roots are fine. `node_modules` is skipped silently; hidden directories
  and symlinks are skipped ⇒ `partial`. `extends`/`references` are never read or followed, so
  out-of-root projects are never touched. Monorepo selection is limited to these counts.
- **Binary resolution** (`resolveVtsls`): contained `node_modules/.bin/vtsls` (its link must resolve
  inside the root; a project-local install is executable project content, flagged `ProjectLocal`),
  then reviewed absolute directories. The server is launched as `node <entry> --stdio` with the real
  `node` binary, never the `vtsls` script, so shebang/`env` lookup cannot pick another runtime. All of it
  is bound through the CD-02 composite fingerprint. No `npx`, package scripts, shell, install or download.
- **Availability** (independent of Go/Svelte/checker providers): `missingTool` (no wrapper, no node, no
  TypeScript package), `unsupported` (platform, other package, unverified versions, Node < 18 or unverifiable,
  shims only, no contained package). Unavailable plans offer no server and report partial coverage.
- **Normalization** reuses the CD-22 normalizer (`normalizeLSPPublication`, provider-prefixed
  attribution `typescript/<source>`), `TSBridge` mirrors `GoplsBridge`: matching-version pushes replace
  the document set (empty clears), stale/future versions are rejected by the manager, unversioned ⇒
  uncertain, out-of-root ⇒ `OutsideRootNotice`, malformed ⇒ bounded partial notice, unowned files
  (e.g. `tsconfig.json`) ignored.
- **Coverage** `openedDocuments`, `externalChanges: "unwatched"`; no push is not "clean".

## Go adapter smoke (2026-10-23)

`TestLocalLiveRealVtslsSmoke` (`internal/engine/code_diagnostics_vtsls_smoke_test.go`)
is opt-in. It runs only when `AGENT_SESSIONS_VTSLS_DIR` and `AGENT_SESSIONS_VTSLS_NODE_DIR`
are set, and was run against the Zed copy above. It uses a disposable synthetic
module (no dependencies, no project scripts) and a temp `HOME`. The run goes through
`DiscoverLocalLive`, the composite approval, `HostSessionLaunch`, Engine
open/change/close and the shared normalizer:

- Invocation `node <…>/@vtsls/language-server/bin/vtsls.js --stdio`. The env is
  exactly `HOME`, `PATH=<node bin>:/usr/bin:/bin` and `NODE_DISABLE_COMPILE_CACHE=1`.
  No shell, `npx` or shim.
- Opening before approval returns `approvalRequired`, and no process starts.
- Two requirements were found and fixed. First, vtsls asks `workspace/configuration`
  for the empty section (the whole configuration). The fixed settings answer turns
  automatic type acquisition off, tsserver logging off, plugin paths empty, and
  never switches to a workspace TypeScript. Second, vtsls sends diagnostics only to
  a client that declares `textDocument.publishDiagnostics`. Both are bound into the
  approval policy and enabled only for TypeScript; gopls' initialize is unchanged.
- Unsaved `a.ts` and `b.mjs` drafts containing `export const x = ;` each produced
  "Expression expected." (1109, error) in under 1 s. The results were unversioned,
  so they are uncertain, carry no range and are shown in the panel and tree only.
  The fixed draft cleared them in about 0.5 s.
- Drafts were never written. The temp `HOME` stayed empty: no typings cache and no
  compile cache.

## Production wiring (2026-10-23)

`DiscoverLocalLive` replaces `DiscoverGoLive` on the local route. It registers
gopls, vtsls, Svelte as an unsupported placeholder, and Ruff for Python
([code-diagnostics-python.md](code-diagnostics-python.md)), each with independent
availability. vtsls is searched for in project `node_modules/.bin`, then in the
reviewed executable directories: `PATH`, plus mise's `node/latest/bin` for
Finder-launched apps. It is never searched for in editor caches. Its approval
inputs are `node` plus six toolchain files (wrapper entry and package, service,
tsserver, its implementation, and the TypeScript package), together with
package/tsconfig/jsconfig identities (≤ 128). Projects without
`package.json`/`tsconfig.json`/`jsconfig.json` show a muted "unsupported" instead
of a missing tool. Remote hosts still have no live route.

## Test labeling

- `TestTSEvidenceReplay` replays **observed** vtsls payloads from `evidence.json`.
- `TestLocalLiveRealVtslsSmoke` is the **real-server** run. It is opt-in and skipped by default.
- Everything using `tsHost`, the fake session server or fake package trees is
  **synthetic**. It proves adapter handling only, not real-server behavior.

## Not done

1. No approved install on this host: production reports "Not installed". Install
   with `npm install -g @vtsls/language-server@0.3.0` (mise Node 22 puts it in
   `node/latest/bin`, which is searched), then do manual editor QA: edit and fix
   an unsaved TS error, check that the panel and tree agree, and check that editor
   undo still works.
2. No server-specific strategy promotes unversioned pushes beyond `uncertain`.
3. Monorepo/`extends` semantics against a real server, load/size tests, and
   Linux/Windows platforms are untested.
4. `typescript-language-server` is unverified. There is no managed installer for vtsls.
