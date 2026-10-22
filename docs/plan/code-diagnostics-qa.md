# CD-17 — Stage 1 QA preparation and blocked release record

Date: 2026-10-22. Status: **Partial / blocked; Stage 1 HOLD**.
Completion remains **15/28**. This is non-GUI preflight evidence, not native QA
sign-off or a reduced-coverage release approval.

## Scope and environment

Inspected source commit: `83b1a32d96334ff6e0f1db75e643da17eee365d6`.
The working tree was clean before preparation. No browser/computer-control tools
are available in this session's tool catalog. No desktop window was launched,
no interactive checks were performed, and no personal session/project data was
scanned, edited, exported, or used as a checker fixture. No tools were installed.

| Inventory | Observed result |
|---|---|
| Shell/build host | Darwin 27.0.0, arm64 (not an interactively tested client) |
| Go | go1.26.8 darwin/arm64 |
| Node / npm | v22.23.2 / 10.9.8 |
| Wails CLI | v2.14.0 |
| Repository-local Svelte checker | svelte-check 4.7.6 (`frontend/node_modules/.bin/svelte-check --version`) |
| SSH client | OpenSSH_10.3p1, LibreSSL 3.3.6; presence is not remote connectivity evidence |
| Windows / WSL executables | `powershell.exe` and `wsl.exe` not found on PATH |
| Docker | Executable found; daemon, containers and SSH tests not exercised |
| Approved diagnostic root / native app mode | None; no approval or app run performed |
| Checker host / transport | Local build host only; no nominated disposable SSH target or Windows/WSL environment supplied |

Installed versions are inventory only. The [CD-04 version policy](code-diagnostics-checkers.md)
remains authoritative; these observations do not expand supported versions or
prove adapter output compatibility on this host.

## Non-GUI preflight actually executed

| Command | Result and boundary |
|---|---|
| `go test ./internal/codediagnostics ./internal/rpc` | Pass (cached); deterministic tests, not new installed-tool or native evidence |
| `npm run check` in `frontend` | Pass: 0 errors, 1 existing invalid `ignoreDeprecations` warning; checks repository source only |
| `npm run build` in `frontend` | Pass; chunk-size warning; separate lazy `fileEditor` chunk emitted |
| `go build -o <temporary-dir>/agent-sessions-cli ./cmd/agent-sessions-cli` | Pass; output outside repository |
| `go build -tags desktop,production -o <temporary-dir>/agent-sessions ./cmd/agent-sessions` | **Failed** at native link: undefined `_OBJC_CLASS_$_UTType` for arm64. Also emitted deprecated `setShowsBaselineSeparator:` and duplicate `-lobjc` warnings. No build workaround or source change attempted; no desktop runtime evidence |
| Temporary CLI `--help` | Exit 2, unknown command and usage printed; not a passing help check |
| Temporary CLI `version` | Exit 0, `dev` (unstamped preflight binary); only runtime smoke performed |

Temporary build outputs used a `cd17-preflight-*` directory under the approved
OpenCode temporary directory. Frontend generated build output is not committed.
The CLI version smoke does not exercise diagnostics or user data. No native
checker invocation against a disposable Go/Svelte QA project was performed.
CD-16's [automated evidence](../tasks/CD-16.md#automated-evidence-2026-10-22)
is complementary, not a substitute for the following matrix.

## Native host/client matrix — all unperformed

“Blocked / untested” below means no interactive pass or failure was observed.
The native link failure above is a separate preflight failure.

| Client / checker host | Required run | Status | Blocker / evidence needed |
|---|---|---|---|
| macOS / local macOS | Go/Svelte installed and missing tools, findings/empty, UI focus | **Blocked / untested** | No GUI-control tools; native desktop preflight link failed; need runnable build and operator with disposable fixtures |
| Linux / local Linux | Supported WebView, wrap/gutters, cancellation/cleanup | **Blocked / untested** | No native Linux client supplied; need real desktop/operator |
| Windows / local Windows | CRLF/non-BMP, Windows roots/argv, process-tree cleanup | **Blocked / untested** | No native Windows client supplied; need real desktop/operator |
| Supported client / real SSH host | Remote tools and reconnect invalidation | **Blocked / untested** | No nominated disposable target/credentials tested; CD-09 authenticated production identity activation remains blocked |
| Windows / WSL distro | Linux tool resolution, trust identity, cleanup | **Blocked / untested** | No Windows/WSL environment; CD-09 identity activation remains blocked |
| Supported client / older SSH or WSL server | Omitted capabilities, browse/edit compatibility | **Blocked / untested** | No older server fixture or interactive client supplied; production remote activation not enabled |

## Scenario matrix — all native checks unperformed

Every row applies to each host/client cell above; neither adapter independently
nor their combined UI has native sign-off.

| Scenario / observation required | Status | Blocking prerequisite |
|---|---|---|
| Fresh root, Not now, session/always grants; no execution before approval | **Blocked / untested** | Runnable native client, GUI operator, disposable approved roots |
| Revoke and executable/config fingerprint changes stop owned work | **Blocked / untested** | Native fixtures/operator; remote identity activation for remote cells |
| Findings → fix → empty rerun, failure, cancel/retry and consistent counts/staleness | **Blocked / untested** | Installed-tool disposable fixtures and native client |
| Missing tool/dependencies, nested/unsupported package, bounded/truncated output, sanitized failures and partial coverage | **Blocked / untested** | Controlled fixture/tool environments and native client |
| Unopened findings, UTF-16 Unicode/CRLF navigation, source/editor markers, Git coexistence, saved-file labels | **Blocked / untested** | Native render/navigation observation and fixture findings |
| Dirty/uncertain drafts, preview navigation, undo/tab/host guards and unchanged disk bytes | **Blocked / untested** | Native disposable draft/save scenarios; no personal files |
| Keyboard/focus, narrow width, themes, wrapped gutters, accessibility, repeated reopen/tab caches | **Blocked / untested** | Native WebView and interactive operator |
| Cold/warm opening, usable search before discovery, no automatic check on open/save | **Blocked / untested** | Timed native interaction on identical fixtures |
| Host switch/disconnect/shutdown, descendant checker cleanup and no cross-host results | **Blocked / untested** | Owned-process fixture/native runtime; real activated transport for remote cases |
| Canonical root/host trust, out-of-root denial and hostile messages | **Blocked / untested** | Native contained security fixtures; no execution approval granted here |
| Older-server capability omission preserves browsing/editing | **Blocked / untested** | Controlled old server plus native client |

## Release decision and remaining work

**Stage 1 HOLD.** Stage 1 means approved, explicit Check Project only: not
on-open/on-save execution, live LSP, or automatic workspace diagnostics. CD-09's
production SSH/WSL identity/capability activation remains blocked; this preparation
does not bypass it. No reduced-platform owner approval has been requested or
recorded. Native security, data-loss, process-cleanup and race observations remain
unknown, not passed.

Before sign-off, assign a native QA operator and obtain a runnable macOS build
(investigate the native link failure separately), native Linux and Windows/WSL
environments, and approved disposable SSH/older-server targets. Prepare contained
Go/Svelte clean/finding fixtures including nested/unsupported/missing-dependency,
Unicode/CRLF and descendant-process cases. Record exact build, client/host OS,
checker versions, approved root, observations, disk/process evidence and timing
per executed cell without persisting draft bodies or findings dumps. Resolve
CD-09 activation before claiming production remote diagnostics QA. Split and
re-estimate remaining native matrix work before exceeding CD-17's four-hour
budget; do not silently reduce coverage. The release owner must explicitly decide
any reduced coverage only after reviewing real evidence.
