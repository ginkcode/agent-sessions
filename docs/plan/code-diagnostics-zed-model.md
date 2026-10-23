# Code diagnostics: the editor model (2026-10-23)

Feedback after real use: the strict model did not work in practice. Tools that
were installed were rejected, and a project with plain `.py` files had no
Python checks. Remote connections always showed "Code checks unavailable". This
plan switches to the model that editors such as Zed use, and adds SSH/WSL.
It replaces the version pins of CD-22/CD-23, the Python and the managed-gopls
plans, and the fail-closed remote rule of CD-09. Approval of project execution
(CD-02) does not change.

## What editors do

Zed finds a language server in this order:

1. **The project's own copy.** `node_modules/.bin`, `node_modules/typescript`,
   a `.venv`.
2. **The user's environment.** The `PATH` of the user's login shell, including
   what version managers add there. A GUI app started from the Dock or Finder
   does not inherit that `PATH`, so Zed asks the shell for it.
3. **Its own download.** If neither has the tool, Zed downloads a known version
   into its own data directory. It uses its own Node.js for npm-based servers.

It accepts any version it finds, and runs it on the remote host when the
project is remote.

## What we do

### Discovery (any version)

| Tool | Project | User environment | Managed download |
|---|---|---|---|
| gopls | — | `PATH`, `~/go/bin`, `GOPATH/bin` | `go install golang.org/x/tools/gopls@<release>` with the Go found on the host (see below) |
| vtsls | `node_modules/.bin/vtsls` | `PATH` (global npm) | `npm ci` of a pinned lockfile (vtsls 0.3.0, TypeScript 5.9.3) |
| Pyright | `node_modules/.bin/pyright-langserver` | `PATH` (npm package) | `npm ci` of a pinned lockfile (Pyright 1.1.414) |
| Node.js (for vtsls/Pyright) | — | `PATH`, version 18 or newer (reused, not copied) | Node.js v24.22.1 LTS archive |
| Ruff | `.venv/bin`, `venv/bin` | `PATH` (uv, pipx, pip, Homebrew, standalone) | Ruff 0.15.20 release archive |
| TypeScript library | `node_modules/typescript` | — | the copy vtsls ships with |

- **Versions are not gated.** gopls is accepted when its build info names the
  gopls module. vtsls is accepted when its entry is the real
  `@vtsls/language-server` package. Ruff is accepted when it is a native binary
  named `ruff` (not a script or a version-manager shim). The version is shown
  when it can be read without running the tool. go vet accepts any Go that
  reports this host's platform.
- **Approval still binds the bytes.** The approval fingerprint hashes every
  executable and loaded entry file. A tool upgrade asks for approval again, as
  before.
- **Login shell PATH.** The desktop app runs `$SHELL -l -i -c` once (5 s limit)
  to read `PATH`, and searches those directories after its own `PATH`. This runs
  the user's own shell startup files, not project files. Remote servers already
  start inside a login shell.
- **Workspace TypeScript.** When `<root>/node_modules/typescript/lib/tsserver.js`
  exists, vtsls is told to use it (`typescript.tsdk` and
  `vtsls.autoUseWorkspaceTsdk`), and its files are bound into approval.
  Otherwise vtsls uses its own TypeScript. Automatic type acquisition stays off.
- **Python servers complement each other.** Ruff supplies lint/syntax findings;
  Pyright supplies type checks. Both receive the same open `.py`/`.pyi` lifecycle,
  with independent server generations, failures, publications and clears. Pyright
  selects a contained `.venv`/`venv` interpreter or a reviewed host Python; it may
  execute that interpreter to discover import paths, so the interpreter is bound
  into project approval too. No interpreter means limited stub-only checks and no
  unresolved-import claims. Native Pyright and remote QA are still pending.
- **Which files a tool covers.** Any Python source (`.py`, `.pyi`) or Python
  configuration in the project enables the Python providers for the whole project. Any
  TypeScript/JavaScript source or `package.json`/`tsconfig.json`/`jsconfig.json`
  enables vtsls for the whole project. Configuration files found are still bound
  into approval. Dependency trees (`node_modules`, virtual environments) do not
  count.

### Managed downloads (automatic, like Zed)

The first version asked for one click per download. In real use (2026-10-23)
that read as "install it yourself", so downloads are now automatic, as in Zed:

- When discovery reports a catalog server **Not installed** for this project
  (the project has files of its language) and the app has no copy of it, the
  app downloads it on that host. The panel shows the download with **Cancel
  download**. Running it still needs project approval; downloading approves
  and starts nothing.
- Each host and tool is tried once per app session. A failed or canceled
  download is retried only with the row's **Install <tool>…** button, which
  opens the review card (source, pinned hash or lockfile, what else it uses,
  destination) and installs on **Install**.
- **Tools setup** has "Download missing language servers automatically"
  (on by default, kept in local storage). Off, every download goes through the
  review card. Turning it off while a proposal is in flight stops before the
  install.
- The automatic path uses the same single-use, two-minute proposal token as
  the manual one; the backend contract is unchanged.

- Destination: `<app data>/managed-tools/<tool>-<version>-<os>-<arch>/`, private
  (0700), written to a staging directory and renamed into place, with a receipt
  holding the main file's SHA-256. A changed file is rejected. The app data
  directory above it is shared with other app storage (the Linux desktop's web
  view creates `~/.local/share/agent-sessions` group-writable), so it only has
  to be this account's and not writable by others; installing removes such
  write access. A tool that is not installed is reported missing whatever that
  directory looks like, so setup stays offered (it used to show every tool as a
  failed app copy on such hosts, hiding the Install offers).
- "Rediscover checkers" (approval card when nothing is approvable, and Tools
  setup) detaches and attaches again, so a server the user installed is found
  without reopening Go to File. An unchanged proposal keeps its approval.
- Language servers see only the text the app sends. A fix saved by another
  editor or an agent used to stay reported until the tab was reopened. Like
  editors, the app now re-reads open tabs when its window regains focus while
  live checks run (one read at a time, nothing saved), and the approved panel has
  **Refresh check status**, which re-reads them and reconciles the results.
- Archives (Ruff, Node.js) are verified against the SHA-256 pinned in the source
  before extraction. Extraction refuses absolute paths, `..` and links that
  leave the staging directory.
- vtsls is installed with `npm ci --ignore-scripts` from a lockfile embedded in
  the app, so every package is checked against its pinned integrity hash and no
  install scripts run. Like Zed, it uses the host's Node.js when the first real
  `node` on the reviewed `PATH` is version 18 or newer and has its bundled npm;
  otherwise the managed Node.js, downloaded first if needed. The proposal says
  which. vtsls later runs on the same rule. npm gets empty, separate user and global config files (one file for
  both makes npm exit with "double-loading config"; this broke the first real
  install on 2026-10-23).
- A failed install names its step ("downloading Node.js", "installing the vtsls
  packages with npm", "building gopls", "checking the installed …"). Process
  output and paths stay in the backend.
- gopls is built with the Go toolchain found on the host (`GOTOOLCHAIN=local`,
  checksum database on, module sums compared with the catalog). The release
  follows that Go, because each gopls release needs a recent Go: v0.24.0 for
  Go 1.27 and newer, v0.23.0 for Go 1.26, v0.21.1 for Go 1.25. An older Go, or
  none, is reported as the reason; Go is never downloaded or upgraded.
- The app data directory (`~/.local/share/agent-sessions` on Linux) may not
  exist yet on a fresh SSH host. Install creates its parent with 0755 and the
  app's own directories as private; a symlinked or group-writable ancestor makes
  managed installs unavailable.
- Installing never approves a project. The backend closes the diagnostics
  session when an install completes; the app attaches again once per finished
  job, which rediscovers the tools. The approval fingerprint then includes the
  new tool, so the user approves again before it runs.
- Status reports each managed install as available, missing or failing
  verification (a changed file). A failing copy is not used and is not
  replaced; Tools setup says so.

### Platforms

macOS and Linux, on amd64 and arm64, for both the local route and remote
servers. Windows projects stay unsupported (no process-group ownership for
server trees); WSL projects run on the Linux server inside WSL. Svelte live
diagnostics stay unsupported.

### Remote (SSH, WSL)

Like Zed, the language servers run on the remote host, next to the files.

- **Host identity is created on the host.** The headless server computes the
  same kind of identity as the desktop: the host's machine ID, the account's
  UID and a random installation ID kept in the server's own config directory.
  A Linux host without `/etc/machine-id` or `/var/lib/dbus/machine-id`
  (containers, minimal systems) uses its hostname instead, so it still works;
  renaming such a host asks for approval again.
  The kind (`ssh` or `wsl`) comes from the launcher, which sets
  `AGENT_SESSIONS_DIAGNOSTICS_HOST` in the remote command. A picker alias is
  never part of the identity.
- **Capabilities.** A server started with that variable advertises
  `codeDiagnostics` and `liveCodeDiagnostics` (on Linux and macOS). A server
  started without it, including older servers, stays unsupported.
- **Approval and trust** are stored on the remote host, in its config
  directory, as before.
- **Managed downloads** run on the remote host into its app data directory,
  through a new `managedTool` RPC method. The proposal names the remote host.

This replaces the CD-09 requirement of an SSH host key or WSL distro tuple
verified by the client. The trade-off: two hosts that clone the same machine ID
and installation ID would share approvals, which is the same as for the local
desktop identity.

## Work packages

1. Adapters: drop the version and platform gates (gopls, go vet, vtsls, Ruff),
   project detection by source files, workspace TypeScript.
2. Identity: Linux machine ID, host kind option, remote identity, serve
   composition, launcher variable.
3. Managed tools: catalog (gopls, Ruff, Node.js, vtsls), downloads and
   extraction, engine wiring without operator review, RPC route.
4. Discovery environment: login shell `PATH`, managed directories.
5. Frontend: automatic download with a setting, per-tool review and Install
   on the provider row, remote setup allowed, rediscovery after install.
6. Tests and docs.

## Verification limits

Pinned hashes were taken from the official release pages on 2026-10-23. The
lockfile was produced by `npm install --package-lock-only` and installed with
`npm ci --ignore-scripts` once. Unit tests use fake downloads and archives. One
real vtsls install was reproduced in a scratch directory with the app's npm
environment after the config fix; the automatic path itself has only run in
unit tests. No real SSH or WSL session and no Linux run
of the language servers has been performed for this change. The remote route is
covered by RPC tests with a fake backend, by the launcher command test, and by
`TestServe_RemoteCodeDiagnostics`, which drives the real `serve` command with
`AGENT_SESSIONS_DIAGNOSTICS_HOST=ssh` over the RPC client: identity, attach,
discovery, managed status and a proposal that writes nothing. That test and the
Go suite also ran on Linux (arm64, non-root, offline, in the local `golang:1.26`
image with the `go` line lowered to 1.26.6 in a scratch copy). There,
`TestDropFilesWSL` (needs `wslpath`) and `TestDesktopDoesNotLinkTestHarness`
(needs a module download) fail for environment reasons only.
