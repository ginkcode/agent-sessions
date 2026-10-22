# Code diagnostics: TypeScript adapter and version spike (CD-23)

Status: **partial / blocked** — adapter seam and observed spike with automated tests; no
production route, no host-approved server, no UI/native QA. Parent plan:
[code-diagnostics.md](code-diagnostics.md). Pattern: [code-diagnostics-gopls.md](code-diagnostics-gopls.md).
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

| Wrapper | TypeScript | Decision |
|---|---|---|
| `@vtsls/language-server` 0.3.0 | 5.9.3 | **Observed, provisionally listed** in `supportedVtslsVersions` so the adapter has one verified pairing. **Not verified through the Go adapter/real session, not on `PATH`, no approved install.** Treat as "none verified for production". |
| anything else, other package name, TypeScript missing | — | **Unsupported** / `missingTool` |
| not darwin/arm64 | — | **Unsupported** |

`typescript-language-server` and the Svelte server are **not verified at all** (CD-24 owns Svelte).
Versions are read from package metadata next to the resolved entry point
(`@vtsls/language-server/package.json`, sibling `typescript/package.json`); the server is **not
executed** to probe a version.

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
- **Package discovery** (`discoverTSPackages`): bounded rooted walk (4096 entries, depth 32, 48
  packages, 192 config files). A directory with `package.json`, `tsconfig.json` or `jsconfig.json` is
  a package root; nested-only roots are fine. `node_modules` is skipped silently; hidden directories
  and symlinks are skipped ⇒ `partial`. `extends`/`references` are never read or followed, so
  out-of-root projects are never touched. Monorepo selection is limited to these counts.
- **Binary resolution** (`resolveVtsls`): contained `node_modules/.bin/vtsls` (symlink must resolve
  inside the root; project-local = executable project content, flagged `ProjectLocal`) then reviewed
  absolute `PATH` directories. Fixed argv `["--stdio"]`, explicit env (`HOME`, `PATH` with the reviewed
  node dir), bound through the CD-02 tool fingerprint with `package.json`/`tsconfig`/`jsconfig` identities.
  No `npx`, package scripts, shell, install or download.
- **Availability** (independent of Go/Svelte/checker providers): `missingTool` (no wrapper, no node, no
  TypeScript package), `unsupported` (platform, other package, unknown version pair, unreadable metadata,
  no contained package). Unavailable plans offer no server and report partial coverage.
- **Normalization** reuses the CD-22 normalizer (`normalizeLSPPublication`, provider-prefixed
  attribution `typescript/<source>`), `TSBridge` mirrors `GoplsBridge`: matching-version pushes replace
  the document set (empty clears), stale/future versions are rejected by the manager, unversioned ⇒
  uncertain, out-of-root ⇒ `OutsideRootNotice`, malformed ⇒ bounded partial notice, unowned files
  (e.g. `tsconfig.json`) ignored.
- **Coverage** `openedDocuments`, `externalChanges: "unwatched"`; no push is not "clean".

## Test labeling

- `TestTSEvidenceReplay` replays **observed** vtsls payloads from `evidence.json`.
- Everything using `tsHost`, the fake session server (`TestTSBridgeFreshnessSynthetic`) or fake
  package trees is **synthetic** and proves adapter handling only, not real-server behavior.

## Not done — required to complete CD-23

1. An **approved, installed** LSP wrapper on the host (`PATH` or project-local), e.g. vtsls or
   typescript-language-server, with its TypeScript dependency; then **re-run the spike** driving it
   through the Go adapter (not Python), recording versions, argv, nonzero-exit/crash behavior, and
   whether any version publishes `version`.
2. Decide whether a server-specific strategy can promote unversioned pushes beyond `uncertain`.
3. Production wiring: no `Registration`/`SessionManager`/`Notifications` caller,
   `liveCodeDiagnostics` stays unadvertised, `TSPlan.Provider()` is not consumed by the manager.
4. Manual QA: edit/fix an unsaved TS error with panel/marker coherence and editor undo.
5. Monorepo/`extends` semantics against a real server, load/size tests, and Windows/Linux platforms.
6. `typescript-language-server` compatibility is entirely unverified.
