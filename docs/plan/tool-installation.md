# Managed language-server installation — TI-01 design and evidence

Date: 2026-10-22. Status: **backend core only, offline tested**. Task split:
[TI-01–TI-05](../tasks/tool-installation.md). Diagnostics progress stays **19/28**;
CD-09, Stage 1/2 release HOLDs and production live capability are unchanged.

## Authority and boundaries

The user approved implementing explicit consent-based app-managed installation.
This narrowly supersedes older blanket no-download statements: only an actual
Install approval for a current host-bound proposal permits managed installation.
Project diagnostics execution approval and installation approval are independent.
No execution grant, project process start, automatic update, PATH change or
project/global write follows installation. No real installation/download/build
was performed on this development host. Existing R11 is a separate unit.

TI-01 adds `internal/managedtools`, not Engine/App/RPC/frontend integration.
There is no advertised capability, UI prompt, missing-tool detector, remote route
or progress event yet. Installation success returns a private executable path;
errors are fixed sanitized text, never raw compiler/network/filesystem output.
TI-02–TI-04 must supply trusted ownership, context cancellation on disconnect,
actual host display, status/progress, Install/Not now and independent execution
approval before this can be offered to users. No request accepts URLs, versions,
executable argv or destinations. Backend construction is a trusted Go API, **not**
a wire schema; callers must not deserialize requests into its parameters.

## Closed catalog and source decision

The only catalog entry is `golang.org/x/tools/gopls@v0.24.0`, native darwin/linux
amd64/arm64. Windows is deliberately rejected pending ACL/publish/native evidence.
This is an installer candidate, **not** an adapter compatibility/security approval.
The official [gopls installation page](https://go.dev/gopls/) documents `go install`;
no trusted signed/checksummed upstream prebuilt artifact was established in this
unit. No invented artifact URL/checksum or third-party binary mirror is used.

Source metadata read during implementation (no module zip fetched):

- [Pinned go.mod](https://proxy.golang.org/golang.org/x/tools/gopls/@v/v0.24.0.mod)
  requires **Go 1.27.0**. This host has Go 1.26.8; a real install would reject it,
  not auto-upgrade or quietly choose an older release.
- [Checksum database lookup](https://sum.golang.org/lookup/golang.org/x/tools/gopls@v0.24.0),
  entry 69138145: module `h1:nPO/LffYa4sP62Ueuema5PPnZPGaZjGl+vZGvbTek4g=`,
  go.mod `h1:abHXJMs6FnwH1z/R1GrYVo/TBfxyDbSFMCKakBQURg0=`.
  These exact sums are pinned in code. Research observed metadata, not a locally
  executed signature verifier. At installation the trusted Go command performs
  checksum-database authentication and the core compares both pinned sums.

After consent only, exact argv is:

1. `<trusted-go> version` (reject development/old/wrong-platform toolchains).
2. `<trusted-go> mod download -json golang.org/x/tools/gopls@v0.24.0`.
3. `<trusted-go> install -trimpath golang.org/x/tools/gopls@v0.24.0`.

Go source compilation is **not** a prebuilt artifact download: it downloads the
transitive module graph and compiles with an already trusted installed Go tree.
`GOSUMDB=sum.golang.org` authenticates dependency sums; `GOPROXY` is the single
HTTPS Go proxy, no direct fallback; bypass lists are empty and `GOVCS=*:off`.
There is no app-pinned independent manifest of every transitive module hash;
integrity for those modules relies on the trusted Go client/checksum database.
Consent UI must disclose proxy/sumdb network traffic (module names and client IP),
CPU/disk use, graph downloads and private ephemeral caches. This is not offline,
sandboxed or reproducible-binary certification. No secrets, source bodies,
project cwd or user Go configuration enter the build.

## Trusted toolchain and isolation

Backend policy must provision the absolute installed Go path and an independently
trusted SHA-256 before constructing the installer. Install checks the executable
hash before any Go probe. A user/project-controlled PATH executable or a hash
computed from untrusted discovery is not acceptable provisioning. **Trust extends
to the complete Go installation tree**, not just the entrypoint hash; same-account
malicious modification of that tree is outside this core's threat model. TI-02
must define production acquisition and fingerprint invalidation policy. Nothing
currently wires a production caller or treats PATH as trusted.

The command environment is allowlisted: no inherited proxy overrides, GOENV,
GOFLAGS, GOROOT, workspace, package-driver, cgo or compiler variables. `GOTOOLCHAIN=local`
forbids auto toolchain fetching; `CGO_ENABLED=0` avoids external C compiler
execution, and Go install does not run go:generate/package scripts. HOME, tmp,
GOPATH, module cache and build cache are private staging children, removed on
return. Go still executes its own compiler/linker tools. Go telemetry is disabled.
Output storage caps at 64 KiB (further bytes drained/discarded; operation fails),
whole installation caps at ten minutes; command pipe wait caps at two seconds.
Unix process-group kill cancels owned build descendants, not global processes.

## Consent, storage and publication

- Constructor and proposal perform no filesystem IO, executable probe or network.
  Closed tool ID is the sole proposal input. Stable `local:<64 lowercase hex>`
  backend identity and current connection owner are required; SSH/WSL rejected
  regardless of display labels pending authenticated CD-09 identity work.
- One cryptographically random 256-bit token is retained per owner, expires after
  two minutes, binds host/connection/tool/version/source/integrity/destination,
  replaces previous proposals, and is consumed before installation even on failure.
  Dismiss discards it; retry needs a new approval. The UI cannot modify bindings.
- Destination is `<backend app-data>/managed-tools/gopls-v0.24.0-<os>-<arch>`.
  Backend app-data must be canonical, private, and outside project control; this
  is a caller invariant, not inferred from an arbitrary requested directory.
  Symlink ancestors/leaves, public private-directories and writable binary files
  are rejected. No world/project/global location is selected by the client.
- In-owner busy guard plus nonblocking OS advisory lock serialize cooperating
  processes. Private same-filesystem random staging keeps incomplete binaries
  invisible; atomic directory rename publishes only after checksum/build metadata
  validation, receipt creation and final cancellation check. No automatic repair
  or overwrite of invalid existing installs. Caches are removed before publish.
- Existing output is validated without executing it: bounded binary read, Go
  build path/module/version/checksum/platform/cgo metadata and receipt SHA-256.
  Receipts detect accidental corruption, **not** malicious same-account forgery.
  No protection against a malicious same-user process racing path checks is
  claimed. App-data ownership and cooperating-process locks are trust boundaries.
  Publish is atomic but not crash-durable (no fsync guarantee); crash may leave
  private staging requiring future bounded cleanup. Disk/network byte quotas and
  progress/cancel RPC are future work; deadline is not a disk quota.

## TI-01 evidence

Synthetic tests only; fake seams never execute Go/download modules/install gopls.
`install_test.go`: proposal closed catalog/metadata, no pre-consent IO/build,
wrong owner/connection/token, dismiss/expiry/replacement/replay, atomic publish,
reuse/tampered receipt, failure/cancel redaction/cleanup, concurrent owner and OS
lock, symlink/private permission refusals, unsupported remote identity, bounded
output/non-Go binary rejection. `go_test.go`: exact argv/environment/private caches,
Go compatibility and checksum mismatch/download failure, untrusted executable
rejected before any command. `process_test.go`: cancellation of owned test-binary
child; no real server is started. Native descendant/ACL evidence remains TI-05.

Validation (2026-10-22): `go test ./...` PASS; `go test -race ./...`
PASS; `go test -race ./internal/managedtools -timeout=30s` PASS;
`go tool golangci-lint run ./internal/managedtools/...` PASS (0 issues).
An initial full race run hit the 120s harness timeout: a new private app-data
permission check exposed the test temp directory's public mode and a waiting
concurrency test. Fixtures now explicitly use 0700; the error sanitizer no longer
mislabels failures after its own timeout-context defer cancels. Focused race and
full reruns passed. No real Go install or gopls invocation occurred.
