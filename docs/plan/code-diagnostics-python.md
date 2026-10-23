# Code diagnostics: Python adapters (Ruff and Pyright)

> **Pyright implementation — 2026-10-23:** Python uses complementary servers:
> Ruff for lint/syntax and Pyright for type checking. One frontend document
> lifecycle is synchronized to both, with provider-qualified generations,
> publication isolation and independently replaced result sets. Missing tools
> are independent: installing Pyright does not replace Ruff. Pyright follows
> project → login-shell PATH → app-managed npm discovery and the same separate
> execution approval as other servers. `.py` and `.pyi` only; no notebooks,
> completion, formatting or code actions. Real Pyright/editor and SSH/WSL QA
> remain pending. This supersedes the older no-type-checker decision below.

> **Update 2026-10-23:** Any native `ruff` (project `.venv`, login-shell PATH or the app's managed copy) is accepted at any version, any `.py` file enables it, and remote hosts are supported. See the [editor (Zed) model](code-diagnostics-zed-model.md).

Status: **partial**. The production local live route discovers Ruff 0.15.20 from
installed wheel metadata. The real server passed a smoke test through the Go
adapter (2026-10-23), and the user's own `uv tool install ruff==0.15.20` is
detected on this host. Not done: manual editor QA, other platforms. Remote stays
fail-closed. Parent plan: [code-diagnostics.md](code-diagnostics.md). Pattern:
[code-diagnostics-typescript.md](code-diagnostics-typescript.md). Fixtures:
`internal/codediagnostics/testdata/ruff/evidence.json`. Code:
`internal/codediagnostics/ruff_lsp.go` (+ the shared walker and push bridge in
`ts_lsp.go`, the shared normalizer in `go_lsp.go`).

Ruff reports **lint and syntax problems only, not type errors**. No type checker
(pyright, basedpyright, mypy, ty) is wired. The UI and the provider notice say so.

## Why Ruff

Ruff is a native binary with a built-in language server (`ruff server`). It never
imports or runs project code, needs no Python runtime at run time, and is a single
file to bind into approval. Python type checkers either run under Node (pyright) or
import the project's environment, which is a larger trust surface. They remain
unverified.

## Supported-version decision

| Install | Decision |
|---|---|
| Ruff **0.15.20** from a wheel (uv tool, pipx, pip, project `.venv`) whose `RECORD` lists the binary with a matching sha256 | **Verified** through the Go adapter on darwin/arm64 (policy `ruff-v1:0.15.20:darwin/arm64:native-wheel:…`) |
| any other wheel version | **Unsupported**, with the found version in the notice |
| binary differs from its `RECORD` hash | **Unsupported** ("does not match its installed package record") |
| standalone/Homebrew/version-manager copy, `#!` shim, anything without wheel metadata | **Unsupported**; never executed |
| none found | **missingTool** ("Not installed"), with a manual install command |
| no contained Python project | muted **unsupported** (no install request) |
| not darwin/arm64 | **Unsupported** |

The version comes from the wheel's `METADATA`. The binary is accepted only when the
resolved real path is the `bin/ruff` listed in a `ruff-*.dist-info/RECORD` under
`<prefix>/lib/python*/site-packages` (or the macOS user-site
`lib/python/site-packages`) and its sha256 matches. Nothing is executed to probe a
version. A project `.venv` with the wrong version is reported, not silently replaced
by another copy.

## Observed behavior (Ruff 0.15.20, Python stdio driver)

Disposable synthetic project, temp `HOME`, `PATH=/usr/bin:/bin`, cwd = project, no
workspace folders. The client declared only UTF-16 position encoding (the same
shape as gopls' initialize).

- **Initialize**: `serverInfo {ruff, 0.15.20}`, `positionEncoding: utf-16`,
  `textDocumentSync {openClose, change: 2}` (incremental). It advertises
  `diagnosticProvider` (pull) **but pushes anyway**, so no client change was needed:
  no settings, no declared diagnostics capability.
- **Pushes carry the document version**: matching results are `current`, positioned
  and drawn inline.
- A draft that does not parse reports only `invalid-syntax` (severity 1). Lint
  findings are severity 2 with string codes (`F401`, `E402`, `E702`, `E501`),
  `codeDescription` and fix/noqa `data` (ignored). Messages can be multi-line
  (`…\n\nhelp: …`).
- The fix edit and `didClose` each produced an empty push (clear).
- No server-to-client requests. Nothing written to the project or `HOME`; no
  `.ruff_cache`. `shutdown` ⇒ `null`, exit 0.
- **Configuration outside the root applies**: an `extend` target outside the project
  was read (it set `line-length = 20`). Ruff also reads parent-directory and
  user-level configuration. Only the project's own `pyproject.toml`, `ruff.toml` and
  `.ruff.toml` are bound into approval; the notice says outside settings still apply.
- Without dynamic registration Ruff does not reload configuration on its own. A
  changed bound config changes the approval fingerprint instead.
- Ruff silently ignored the project config when launched in a non-canonical path
  (`/var` vs `/private/var`). The session launches in the canonical authorized root.
- An invalid config is only logged to stderr; Ruff falls back to default rules.

## Implemented (automated)

- **Ownership** `PythonLanguageID`: `.py`, `.pyi` → `python`. Notebooks (`.ipynb`)
  are not owned. Files must sit under a discovered project directory; there is no
  parent promotion.
- **Project discovery** reuses the bounded contained walker shared with TypeScript
  (`discoverProjectDirs`). A directory with `pyproject.toml`, `ruff.toml`,
  `.ruff.toml` (bound into approval) or `setup.py`, `setup.cfg`,
  `requirements.txt` (markers only, not bound) is a project. `.venv`, `venv`,
  `__pycache__`, `site-packages`, `node_modules`, `.tox` and `.nox` are skipped
  silently; other hidden directories and symlinks mark coverage partial.
- **Binary resolution** (`resolveRuff`): contained `.venv/bin/ruff` first (project
  content, `ProjectLocal`), then the reviewed executable directories (which include
  `~/.local/bin`, where uv tool and pipx link). The launch is the wheel's real
  binary, `ruff server`, env exactly `HOME` and `PATH=/usr/bin:/bin`. No shell,
  `uvx` or `pipx run`.
- **Bridge**: `RuffBridge` shares `handleOwnedPush` with `TSBridge`. Unowned paths
  (e.g. `pyproject.toml`), unopened files and other providers never create state.
- **Coverage** `openedDocuments`, `externalChanges: "unwatched"`; no push is not
  "clean".

## Go adapter smoke (2026-10-23)

`TestLocalLiveRealRuffSmoke` (`internal/engine/code_diagnostics_ruff_smoke_test.go`)
is opt-in: it runs only when `AGENT_SESSIONS_RUFF_DIR` names a directory with a
wheel-installed `ruff`. Run with `AGENT_SESSIONS_RUFF_DIR=~/.local/bin` against the
user's uv tool install, on a disposable synthetic project with an empty temp `HOME`,
through `DiscoverLocalLive`, composite approval, `HostSessionLaunch`, Engine
open/change/close and the shared normalizer:

- Opening before approval returns `approvalRequired`, and no process starts.
- v1 `x = (` ⇒ `invalid-syntax` error; v2 `import os` ⇒ `F401` warning; v3 fixed ⇒
  empty clear. Each took about 20 ms. All results were `current` with a range and
  the matching document version.
- Drafts were never written; `HOME` stayed empty; no `.ruff_cache`.

## Test labeling

- `TestRuffEvidenceReplay` replays **observed** Ruff payloads from `evidence.json`.
- `TestLocalLiveRealRuffSmoke` is the **real-server** run. It is opt-in and skipped by default.
- Everything using `ruffHost`/`writeRuffWheel`, the fake session server or fake
  project trees is **synthetic**. It proves adapter handling only.

## Not done

1. Manual editor QA: open a `.py` file in the app, approve, introduce and fix an
   unused import and a syntax error, check that inline markers, panel and tree
   agree, and that editor undo still works.
2. Only darwin/arm64 is verified; other platforms report unsupported.
3. No Python type checker. Ruff settings outside the project are not part of approval.
