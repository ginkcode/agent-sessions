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
