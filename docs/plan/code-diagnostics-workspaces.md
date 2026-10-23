# Nested diagnostics workspaces (2026-10-23)

## Goal

Opening a known session directory does not require Git. A directory may contain
independent projects such as `api/` (FastAPI), `web/` (Next.js), and
`processing/` (Python), without a repository at the opened root.

The opened parent remains the authorization, trust, result-path and revocation
boundary. Nested project discovery must never authorize an arbitrary filesystem
path, promote the root to an ancestor, or follow symlinks outside that boundary.
An arbitrary parent that is not a catalog session directory/repository remains
unavailable to the existing file viewer and diagnostics API.

## Intended behavior

- Discover contained project markers with bounded scanning; skip dependency,
  virtual-environment, generated and cache trees. Git metadata is not required.
- Python projects use their own `pyproject.toml`/`pyrightconfig.json` and
  `.venv`/`venv` interpreter, with Ruff and Pyright receiving the same file.
- JavaScript/TypeScript projects use their local server/TypeScript installation
  and package/tsconfig configuration. Avoid treating every secondary tsconfig
  within a package as an independent project.
- Select the deepest applicable project for a file, independently per language.
  Files outside marked projects retain a bounded root-level fallback.
- Keep semantic provider IDs (`ruff`, `pyright`, `typescript`) stable; one
  provider may contribute several server configurations and coverage roots.
- Distinct server configurations have distinct contained working directories,
  LSP initialize roots and approval identities. Server downloads are still shared
  per host rather than copied into projects.
- Every server uses the same complete parent-level approval proposal. A child
  tool, interpreter or bound configuration change requires renewed approval.
- Missing or failed projects must be reported without silently routing their
  files through a sibling project's environment.

## Implementation

- `discoverWorkspaceLive` (`workspace_adapters.go`) runs one rooted scan of at
  most 4096 entries and depth 32. It skips `node_modules`, `.venv`, `venv`,
  `__pycache__`, `site-packages`, `.tox`, `.nox`, `.git`, `.next`, `dist`,
  `build`, other dot-directories and symlinks; anything unscanned marks coverage
  partial. When no boundary below the root exists, legacy single-root discovery
  runs unchanged. Go (`gopls`) discovery is not affected.
- Python boundaries: `pyproject.toml`, `pyrightconfig.json`, `setup.cfg`,
  `requirements.txt`, or a `.venv`/`venv` containing `pyvenv.cfg`.
  JavaScript/TypeScript boundaries: `package.json`, `tsconfig.json`,
  `jsconfig.json`. A tsconfig without its own `package.json` stays with its
  enclosing package, and packages of an npm `workspaces` root stay with that
  root. pnpm workspace files are not recognized, so each pnpm package gets its own instance.
- Each boundary is discovered through a contained discovery-only root and gets
  its own server configuration: `Directory` (cwd and initialize
  `rootUri`/`workspaceFolders`), interpreter or TypeScript SDK, and config paths
  translated back to parent-relative paths. A markerless root instance covers
  sources outside every boundary. A boundary owning no scanned source file
  (e.g. a folder holding only `package.json`/`package-lock.json`) gets no
  instance, unless the scan was truncated by its budget.
- The approval fingerprint hashes each distinct executable and config file once,
  so instances sharing one Node/vtsls/Pyright install count it once toward the
  512 MiB aggregate hashing budget. Repeating each instance's copy made
  folders with four or more TypeScript projects fail.
- If discovery or the composite proposal fails, every live provider is reported
  unavailable with the failure reason in its notice.
- `LiveRegistration.Workspaces` holds up to 16 instances per provider. Tool and
  toolchain identities are namespaced per directory; semantic provider IDs stay
  unchanged. The composite proposal allows 256 tools; nested discovery reserves
  16 for other providers and skips whole instances past that budget.
- A file is routed to the deepest boundary per provider. A file in a boundary
  whose server is missing, over budget or over the instance cap is not covered
  at all — never by the root or a sibling. `Coverage.Roots` lists only healthy
  instances; `MissingTool` and the notice name projects lacking a server, so the
  setup offer and automatic download stay available.

## Verification

Synthetic tests cover a non-Git catalog directory with sibling Python
environments and a TypeScript project, child interpreter/SDK/config binding in
fingerprints, deepest routing and the Ruff+Pyright pair, scoped rootUri and
workspace folders, full-composite approval vs subset grants, missing-sibling
exclusion, npm workspace grouping, scan and instance bounds, over-budget
rejection, symlink and traversal containment, and an uncataloged parent being
refused. Real desktop, SSH/WSL and real nested language-server behavior have not
been exercised and require manual QA.
