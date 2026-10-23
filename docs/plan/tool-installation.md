# Managed language-server installation — TI-01/TI-03 design and evidence

> **Update 2026-10-23:** Managed installs now cover gopls, Ruff and vtsls (with Node.js), need no operator review, pick the gopls release from the host's Go (v0.24.0/v0.23.0/v0.21.1 for Go 1.27/1.26/1.25) and run on SSH/WSL hosts through the `managedTool` RPC. The fail-closed remote rule and the Go >=1.27 operator gate below are history. See the [editor (Zed) model](code-diagnostics-zed-model.md).

Date: 2026-10-22. Status: **backend core and Engine ownership, offline tested**. Task split:
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

TI-01 added `internal/managedtools`; TI-02 adds backend-only Engine seams below;
TI-03 adds the optional local App/backend contract and fail-closed remote RPC seam.
There is no advertised remote capability, UI prompt, automatic missing-tool prompt,
remote installation or progress event yet (TI-03 uses bounded job polling). Installation success returns a private executable path;
errors are fixed sanitized text, never raw compiler/network/filesystem output.
TI-02 supplies trusted ownership/cancellation; TI-03–TI-04 must supply actual
host display, routing, status/progress, Install/Not now and independent execution
approval UX before this can be offered to users. No request accepts URLs, versions,
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
defines explicit acquisition and fingerprint invalidation below. Nothing
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

## TI-02 ownership, provisioning and evidence

`ReviewInstalledGo(root)` is a trusted backend composition operation, NOT a wire
schema: the backend operator must explicitly review/trust the **whole installed
Go tree** first. No default selection, inherited PATH/GOROOT, arbitrary frontend
executable/hash, version fallback or toolchain fetch is accepted. It derives
`<canonical reviewed root>/bin/go`, rejects symlink ancestors/unsafe files,
reads native cmd/go metadata and Go >=1.27 compatibility without execution,
and pins SHA-256 into opaque `ReviewedGo`. Hashing is NOT trust acquisition;
the explicit prior whole-tree review is the trust boundary. Same-account tree
forgery/races remain outside the threat model. This API is intentionally not wired
to production startup: a deployment must supply its reviewed tree explicitly.
An empty review cannot construct an installer. Go 1.26.8 on this host yields
`unsupported prerequisite` with the required version and no-auto-upgrade message.
The post-consent version check returns that same useful sanitized failure.

`WithReviewedManagedGo` is backend-only provisioning. Engine creates a stable
local machine/account/installation host identity and random per-owner connection
nonce; request JSON never supplies binding. Remote fixture identities fail closed;
there is no RPC remote route pending CD-09 authenticated host identity. Constructor
and proposal never probe executables or contact network; host identity persistence
is metadata initialization only. Reset/disconnect, Engine.Close, explicit managed
revoke and successful project revocation cancel/join owner work outside Engine.mu.
WaitGroup admission is serialized with removal. Request cancellation is combined
with owner cancellation; core process-group cancellation remains in force.

Read-only discovery creates nothing: private canonical ancestor/destination checks,
strict catalog build metadata and receipt version/policy plus SHA-256 binary-byte
comparison precede use of the private executable directory. Receipt reads are now
bounded even if a file grows after stat. Missing is `missingTool`; corrupt data
is unsupported, never automatic repair/reinstall. Managed adapter discovery also
requires matching local project host and rechecks the reviewed Go entrypoint pin.
No inherited PATH fallback or new production live launcher is introduced.
A Go tree change requires explicit re-review, not automatic hash repinning.

Successful installation tears down previous diagnostics ownership/attachments
before returning; reconnect/reattach must rediscover and recompute independent
composite execution approval (binary bytes, argv/env, adapter/config identities).
No attachment, project grant, server start, or live feature enable follows install.
An already-stored exact execution grant remains governed by the existing trust
fingerprint policy; new/changed executable bytes cannot inherit an old fingerprint.

Offline evidence: new managedtools discovery tests exercise missing/no writes,
valid install reuse, tampered bytes, forged receipt and receipt symlink rejection;
review test reads existing Go metadata only and verifies this host's 1.26.8 refusal.
Engine fake-installer tests exercise pre-consent no execution/grant, wrong token,
disconnect/revoke/close cancellation+join, corrupt discovery, and successful install
invalidating/joining the prior execution owner without live activation. Existing
core fake-process argv/environment tests still pass; no real managed build,
installation, download or server run occurred.

Validation 2026-10-22: `go test ./...` PASS;
`go test -race ./internal/managedtools ./internal/engine` PASS;
`go tool golangci-lint run --new-from-rev=c68716c ./internal/managedtools/... ./internal/engine/...`
PASS (0 issues). Unfiltered scoped lint found pre-existing Engine errcheck,
gofumpt, unparam/unused issues; changed-code findings were fixed, unrelated files
left untouched. TI-03 evidence follows; diagnostics **19/28** and all release HOLDs unchanged.

## TI-03 optional contract, bounded jobs and offline evidence

`engine.ManagedToolsBackend` exposes one optional `ManagedTool` operation with
status/propose/install/dismiss/cancel actions. Requests carry expectedHostId, the
closed gopls tool ID, opaque token and job ID only. They cannot provision Go or
select URL/path/hash/version/argv. App checks actual backend identity and selected
connection generation before and after routing; stale job acknowledgments cancel
on the original backend, never the newly selected host. Existing connection reset
cancels/joins the managed owner. No base Backend interface change is required.

A proposal does not discover/probe/install anything. Status may read installed
metadata; proposal only generates consent metadata. Install reserves that consent
synchronously and acknowledges a random job ID without waiting for the build.
One job record per owner replaces prior terminal state; no event queues, polling
goroutines, timer-per-poll, log retention or unbounded job history. Poll status
with the returned job ID; progress is `installing` then `installed`, `failed` or
`canceled`, not a compiler-percent claim. Cancel requests cancellation; polling
reports terminal cancellation after cleanup. Owner cancellation/close/disconnect
joins the admitted worker. Job deadline is ten minutes in addition to core limits.
Failures use fixed bounded actionable text; install never returns executable paths
or raw process/filesystem output. Fresh proposal is required after consumption.

Production startup still supplies no reviewed Go tree. Status/proposal therefore
returns `unavailable` with explicit backend-operator review/provisioning guidance,
not a fabricated review or default PATH executable. Local optional routes are
implemented for explicitly provisioned backends. Remote RPC dispatch explicitly
rejects installation pending CD-09 authenticated managed host identity review;
neither local backend identity nor diagnostics fixture identity authorizes it.
RPC Client returns unsupported with old/new peers, no legacy fallback. Real-pipe
requests reject arbitrary fields and envelopes over 2 KiB before fail-closed
unsupported. No managed remote capability or live capability is advertised.

Offline tests: Engine fake installer covers wrong host/tool/token/job, proposal
without install, replay, progress, cancel, bounded input, explicit unavailable
prerequisite, disconnect joining and old token rejection. Existing core tests
continue to cover true token expiry/owner binding/replay and sanitized failures.
App tests cover real Engine unavailable state, wrong actual-host routing, optional
old backend and disconnected selection refusing local fallback. RPC real pipes
cover old capability flags, direct install refusal, URL/path/hash rejection,
oversized input and continued browsing after optional refusal. No frontend files
were touched, so frontend check/test/build was not needed. TI-04 UI/API adapter
and native real installation evidence remain separate work.

Validation 2026-10-22: `go test ./...` PASS;
`go test -race ./internal/engine ./internal/app ./internal/rpc ./internal/managedtools`
PASS; `go tool golangci-lint run --new-from-rev=604e6a5 ./internal/engine/... ./internal/app/... ./internal/rpc/...`
PASS (0 issues; initial new-test gofumpt spacing fixed). No real download, build,
installation or server start. Diagnostics **19/28** and release HOLDs unchanged.

## TI-04 — frontend setup controls (2026-10-22)

Implemented after `2f2cb99` in `frontend/src/lib/managedTools.ts`, `api.ts`,
`mock/mockApi.ts`, `stores/codeDiagnostics.svelte.ts` and
`components/common/go-to-file/CodeChecks.svelte`. The optional Wails adapter calls
TI-03 `App.ManagedTool`; no Go/backend policy changes. A read-only status call
on attachment discovers optional contract support (TI-03 advertises no managed
capability). Old bindings and absent authenticated identity do not produce an
enabled Install button. The mock honestly reports unavailable and never installs.

Review obtains the closed gopls proposal and displays exact host, version, source,
integrity, app-private destination and expiry, plus explicit reviewed Go >=1.27
provisioning, network/source/dependency build and private cache/staging writes.
The frontend sends only action, actual expectedHostId, closed tool ID, opaque
consent or job ID; paths, hashes, versions and URLs are never request policy.
Project execution trust remains independent. Opening, execution approval and
Not now never install. The store consumes the proposal locally before Install;
uncertain acknowledgement is status-only recovery, never token replay. Failed or
canceled work can be retried only by reviewing fresh consent. Backend production
still returns actionable unavailable: operator review/provisioning is not wired,
Go 1.26.8 cannot meet the pinned Go >=1.27 requirement, and automatic upgrades are
disabled. Remote identity remains CD-09 fail-closed; live remains disabled.

Progress is coarse installing/terminal, not fabricated percent. Polling is serial
and bounded to 300 two-second polls; explicit refresh renews only the read budget.
Host epoch, attachment/context and connection guards drop late replies. Hide,
disconnect, host replacement and context replacement clear timers/proposals.
Hiding does not cancel an approved backend job; reopening status can rediscover
it. Explicit Cancel targets the current job; backend connection ownership still
cancels on disconnect. No installation cancellation is sent to a new host.

Evidence: `frontend/test/managed-tools.test.mjs` uses the real compiled Svelte
store, SSR controls and fake timers for Install once, denial, expiry, old backend,
missing identity/capability, wrong host/proposal, late host/context/status/Install,
lost acknowledgements, failure escaping, explicit cancel, bounded polling and
hide cleanup without cancellation. Existing execution-control SSR tests filter
setup refresh separately, retaining their exact execution approval assertions.
Validation: `npm test` **458/458 PASS**; `npm run check` **0 errors** (existing
invalid ignoreDeprecations warning); `npm run build` **PASS** (existing large
chunk warning). Initial tests exposed expected SSR entity normalization and new
setup refresh in exact execution button lists; assertions corrected and full
suite rerun. Go untouched. No real download/install/build/server run. TI-05 not
started; diagnostics **19/28**, CD-09, Stage 1/2 and release HOLDs unchanged.
