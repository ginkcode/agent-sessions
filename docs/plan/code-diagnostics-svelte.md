# Svelte-check adapter evidence (CD-06)

Implemented backend primitives only: `internal/codediagnostics/svelte*.go`.
No capability, manager, RPC, UI, automatic checks or CD-07 work is enabled.

## Supported policy

- macOS arm64 only; exact installed svelte-check 4.7.6, Node 22.23.2,
  Svelte 5.57.1 and TypeScript 5.9.3. Installed package metadata is read without
  execution; approved Node/checker version probes require exact CD-04 output.
- Bounded deterministic contained package traversal (4096 entries, depth 32,
  12 supported packages), no parent promotion or discovery symlink following.
  Excluded directories, missing local tsconfig and skipped/capped scope are partial.
- Narrow layout: Svelte declared in dependencies/devDependencies, explicit local
  JSON tsconfig and svelte.config.js. JSONC, extends, references, plugins and
  external path settings are unsupported. This deliberately does **not** support
  this repository's inherited frontend tsconfig yet. No config is evaluated during
  discovery/tests. No dependency/config edits are needed to pass tests.
- Installed checker entry, version helper, bundled implementation, compiler and TS
  implementation, direct checker dependency entry/metadata identities, discovered
  manifests/locks/configs are bound with Node and exact command/environment policy
  through the existing fingerprint. Unresolvable/escaping identities decline support.
  Re-enumeration before/after runs catches configuration creation/removal.
  Imported config/plugin and transitive runtime internals are not sandbox-attested;
  approval still permits executable configuration. Broader identity coverage requires
  a separately evidenced policy, not a name-only fallback.
- Only direct absolute Node plus JS entry and fixed arguments; no .bin launcher,
  shell, npx, scripts, installation/download fallback, NODE_OPTIONS/NODE_PATH,
  watch, incremental, fix or tsgo. Explicit replacement environment and supervisor
  no-download controls; existing project/backend serialization and trust leases.

## Results and tests

Verified CD-04 machine-verbose fixtures drive parser tests: START, JSON records,
matching final summary and exit semantics, clean/warnings/errors, malformed/missing
summary and file-level results. Config failures (including normal error records plus
COMPLETED), config/dependency errors and any stderr fail conservatively. Failures,
cancellation, limits or mixed package failure never permit an empty replacement;
previous provider results must be retained stale by the future manager.

Contained paths, bounded saved reads/messages/findings/encoded results, UTF-16
exclusive coordinates including CRLF/non-BMP boundaries, invalid range fallback,
and out-of-root notices follow the existing contract helpers. Saved basis is always
uncertain without revision/snapshot proof; ranges must remain panel-only on drafts.

Synthetic metadata trees and fake supervisor processes cover nested/multiple
packages, runtime/version/dependency rejection, traversal bounds, symlink exclusion,
exact argv, changed config identities, partial and mixed failures, cancellation,
output limits and unsafe coordinates. No real checker, arbitrary project config,
package script or download was executed by tests.

Validation: targeted diagnostics race tests, scoped diagnostics golangci-lint, and
full `go test ./...` pass. Native Linux/Windows/remote execution and application
integration/manual WebView QA remain downstream release gates.

---

# Svelte language-server adapter and TypeScript ownership (CD-24)

Status: **partial / blocked** — adapter seam with synthetic tests; **no Svelte language server
verified** (2026-10-22). No production route; `liveCodeDiagnostics` remains unadvertised. Code:
`internal/codediagnostics/svelte_lsp.go`, tests `svelte_lsp_test.go`, evidence
`testdata/svelte-lsp/evidence.json`. Pattern: [typescript](code-diagnostics-typescript.md),
[gopls](code-diagnostics-gopls.md).

## Spike result: none verified

Read-only inventory; nothing installed, downloaded, or executed.

| Location | Found |
|---|---|
| `PATH` (`svelteserver`, `svelte-language-server`) | none |
| `frontend/node_modules` | `svelte-check` 4.7.6 (checker CLI, **not** LSP), `svelte` 5.57.1; no `svelte-language-server`, `svelte2tsx`, `typescript-svelte-plugin` |
| Zed private cache `languages/*` | basedpyright, bash, eslint, gopls, json, package-version, ruff, rust-analyzer, tailwindcss, vtsls, yaml — **no Svelte server** |
| Zed `extensions/installed/svelte` 0.2.11 | grammar/config/wasm only; `extensions/work/svelte` is empty (server would be downloaded on demand; not done) |
| Zed prettier cache | `prettier-plugin-svelte`, svelte 5.57.2 — formatter only |

Unobserved (so unrecorded, not assumed): initialize capabilities, position encoding, whether
pushes carry `version`, virtual-document URIs/mapping, crash behavior, required runtime.
`svelteserver --stdio` is the *expected* fixed argv from public convention, not an observation.
The vtsls observation (CD-23: pushes unversioned) must not be extrapolated to the Svelte server.

**Supported versions: none.** `supportedSvelteLSVersions` is empty, so `DiscoverSvelteLS` reports
`unsupported` for any discovered server until a spike adds a (server, svelte, typescript) triple.

## Implemented (automated, synthetic)

- **Detection** (`discoverSveltePackages`): bounded rooted walk (4096 entries, depth 32, 48 packages,
  192 configs). A directory is a Svelte package if it contains `svelte.config.{js,mjs,cjs,ts}`
  (existence only; never imported/evaluated — a test fixture that would throw proves this) or a
  `package.json` declaring `svelte`/`@sveltejs/kit` (parsed as bounded JSON data). Companion
  package/tsconfig/jsconfig files bind into the CD-02 fingerprint. No parent promotion;
  `node_modules` skipped; hidden dirs and symlinks skipped ⇒ partial.
- **Resolution** (`resolveSvelteServer`): contained `node_modules/.bin/svelteserver` (symlink must stay
  in-root; flagged `ProjectLocal`, executable project content under trust), then reviewed absolute
  `PATH` dirs. Fixed argv `["--stdio"]`, replacement env (`HOME`, `PATH` with reviewed node dir), policy
  string includes server/svelte/typescript versions. Versions come from sibling package metadata,
  never by executing. No npx/scripts/shell/install/download.
- **Availability** independent of svelte-check (CD-06), TypeScript and Go: `missingTool` (server, node,
  svelte/typescript package), `unsupported` (platform, foreign package, unverified version, unreadable
  metadata, no package). Unavailable plans offer no server and report partial coverage.
- **Ownership** (`DocumentOwner`, `OwnedDocuments`): `.svelte` → Svelte provider (`languageId svelte`);
  `.ts/.mts/.cts/.tsx/.js/.mjs/.cjs/.jsx` → TypeScript provider, including `store.svelte.ts`
  (last extension decides); everything else (e.g. `.go`, `tsconfig.json`) → none. A file is routed to
  exactly one provider, so no duplicate didOpen/change/close stream; the Svelte document manager
  refuses TS files (`unsupported`) and the bridge ignores TS-owned paths. Each provider requires a
  contained package covering the file, and one unavailable provider never affects the other.
- **Session keying**: existing `keyOf` = (project, provider, adapter policy). One Svelte and one
  TypeScript session per authorized root regardless of package count; packages are coverage, not keys
  (no folder-count inflation). Tested.
- **Normalization** (`SvelteBridge`) reuses the CD-22 normalizer, attribution `svelte/<source>`.
  Only real, contained, in-root `.svelte` source coordinates are accepted. Non-`file` URIs
  (virtual documents) produce `SvelteVirtualNotice` (limited coverage) and no link/offsets;
  out-of-root ⇒ `OutsideRootNotice`; malformed ⇒ bounded partial notice. **Virtual-document mapping is
  unsupported** (nothing verified).
- **Freshness/replacement**: matching-version push replaces only that document's LSP set (empty
  clears it; sibling Svelte documents, TS and checker sets untouched); stale/future versions rejected
  by the manager; unversioned ⇒ `uncertain`, ranges dropped (no tested sync strategy). Absence of a
  push is never "clean". Coverage `openedDocuments`, `externalChanges: unwatched`.

## Test labeling

Everything in `svelte_lsp_test.go` is **synthetic** (fake binaries, package trees, fake session
server; version strings such as `9.9.9-synthetic`). It proves adapter handling, not Svelte server
behavior. `TestSvelteEvidenceRecordsNoneVerified` pins the empty verified list.

## Not done — required to complete CD-24

1. An **approved, installed** `svelte-language-server` (host `PATH` or project-local) with its
   `svelte` and `typescript` packages and a compatible Node runtime.
2. Re-run the spike through the Go adapter: record version triple, argv/runtime, initialize
   capabilities and position encoding, whether `publishDiagnostics` carries `version`, behavior on
   template/script/style errors, Unicode/CRLF columns, virtual/generated URIs, crash and
   missing-dependency behavior; add sanitized fixtures; add the triple to `supportedSvelteLSVersions`.
3. Decide whether any verified virtual-document mapping exists; otherwise keep it unsupported.
4. Mixed Svelte/TS behavior against real servers (shared `typescript-svelte-plugin` vs. separate TS
   server; potential overlapping findings need attribution, not suppression).
5. Production wiring (no `Registration`/`SessionManager`/notification caller consumes these plans).
6. Manual QA: edit/fix an unsaved Svelte error, switch view/editor/tabs, retained undo/draft state.
7. Non-darwin/arm64 platforms.
