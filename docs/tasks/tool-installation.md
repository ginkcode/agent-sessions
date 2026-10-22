# Consent-based managed tools — independent backlog

Date: 2026-10-22. This is independent of diagnostics **19/28** and R01–R20.
No task here clears CD-09, Stage 1/2 HOLDs, or enables live capability.
Each task includes tests/evidence and is at most four developer hours; split again
before exceeding that cap. Design/evidence: [managed tools](../plan/tool-installation.md).

| ID | Estimate | Status | Exit / boundary |
|---|---|---|---|
| TI-01 | 4h | Complete (offline core only) | Closed gopls catalog, consent-bound local installer core, pinned checksum policy, trusted Go build, private staging/atomic publish, offline fake tests. No App/Engine/RPC/UI integration or real installation. |
| TI-02 | 4h | Complete (backend ownership, offline tested) | Engine ownership and trusted installed-Go selection/fingerprint policy; connection-lifetime cancellation; read-only missing-tool metadata; managed executable discovery invalidates execution approval. Test no pre-consent probes/network, no automatic starts, independent execution grants. |
| TI-03 | 4h | Complete (local optional contract; remote fail-closed) | Optional RPC/App contract and routing, proposal/Install/cancel/progress with bounded sanitized states and host epochs; old backend fallback; local only until CD-09 authenticated identity is implemented and reviewed. No client executable/URL/argv. |
| TI-04 | 4h | Complete (offline frontend; production unavailable) | Desktop Install / Not now scoped to actual host; source/version/destination/network/cache disclosure, progress/failure/cancel/retry, denial and reconnect tests; separate project execution approval. |
| TI-05 | 4h per platform cell | Not started | Explicit approved disposable real build/install evidence, native private-directory permissions/ACL and publish/cancel/concurrency/tamper tests; assess compatibility/security of pinned release and Go trust provisioning. Record unsupported cells, no automatic activation. |

## TI-01 progress / evidence

Completed 2026-10-22: closed catalog local-only core and offline tests in
`internal/managedtools/`; [security policy, sources, limits and evidence](../plan/tool-installation.md).
Full `go test ./...` and `go test -race ./...` pass; scoped golangci-lint reports
0 issues. Initial race timeout and subsequent fixture/sanitization fixes are
recorded in the evidence. No real installs, downloads or server runs; no UI/RPC
flow claimed. Next independent work is TI-02, not R11 or diagnostics activation.

## TI-02 progress / evidence

Completed 2026-10-22: backend-only Engine ownership, opaque explicitly reviewed
installed-Go provisioning, stable local host plus per-owner random connection
consent, disconnect/close/revoke cancellation and join, read-only managed discovery
with receipt AND binary validation. Installation tears down prior execution owners;
reattach recomputes independent execution fingerprints. No live flag, RPC/App/UI
contract, remote installation or automatic Go upgrade added. Go 1.26.8 returns an
actionable Go >=1.27 prerequisite refusal without executing it. Full `go test ./...`
and touched-package race tests pass; changed-code lint versus `c68716c` passes
(0 issues). Broader package lint also exposed existing unrelated Engine issues;
these were left unchanged. Tests use fake installer/process seams, no real install,
download, build or server run. Detailed policy/evidence: plan TI-02 section.
Diagnostics remains **19/28**. TI-03 completion is recorded below.

## TI-03 progress / evidence

Completed 2026-10-22 after `604e6a5`: optional `ManagedToolsBackend` and App
`ManagedTool` operations: status/propose/install/dismiss/cancel with expectedHostId,
opaque consent/job IDs, connection-generation validation and bounded single-job
polling. Install consumes proposal admission synchronously; cancellation and
owner disconnect/close join work. No executable path is returned by install.
Progress is honest coarse installing/terminal state, not invented percentages.
Production provisioning remains deliberately absent: actionable unavailable state
requires explicit backend operator Go-tree review. Remote RPC and old clients
fail closed (including real-pipe tests), not local identity reuse. Inputs reject
unknown RPC fields/oversized envelopes; only the closed gopls ID is proposed.
No frontend files changed, no TI-04 UX, live flag, download or real installer run.
Full Go tests, touched-package race and changed-code lint pass; detailed evidence
and limitations are in the plan TI-03 section. Diagnostics stays **19/28**.

## TI-04 progress / evidence

Completed 2026-10-22 after `2f2cb99`: optional frontend BackendAPI/Wails App
adapter, closed request/proposal/state types, non-installing mock and real
CodeDiagnosticsStore-driven host-scoped setup controls. Status on attach is
read-only; review obtains a proposal; only Install sends the one-use token.
Not now dismisses without install. Exact backend host/version/source/integrity/
app-private destination and network/build/cache/Go >=1.27 prerequisite disclosures
are separate from project execution approval. Missing reviewed provisioning,
old App bindings and unauthenticated/remote identity fail closed with guidance.
No invented capability flag, global installation, editable installer policy,
automatic installation or live activation. Polling is coarse, one request at a
time, bounded to 300 two-second polls, and stops on hide/disconnect/host/context
replacement. Hiding does not cancel approved installation; deliberate Cancel
uses the current job ID, and backend disconnect ownership still cancels work.
Lost acknowledgements never replay tokens; retry requires fresh review.

Real-store tests plus component SSR and fake timers cover disclosures, Install
once, Not now/no install, expired consent, missing/old capability, wrong/late host,
missing identity, context replacement, late Install, polling exhaustion/cleanup,
explicit cancel, failed/lost acknowledgement and escaping. Frontend test/check/
build pass; existing ignoreDeprecations and bundle-size warnings remain. No Go
files changed; no Go rerun required. Detailed evidence: plan TI-04 section.
Diagnostics remains **19/28**, CD-09 and Stage 1/2 HOLDs unchanged. TI-05 is not
started: compatible reviewed Go is still missing, and no real installation,
download or server start was attempted.

## Scope change authorized by user

The previous blanket no-download rule is superseded **only** for explicit,
host-scoped, user-approved managed installation. Feature implementation does not
approve running an installer on this development host. No real tool installation
or module artifact download is needed in TI-01 tests. Reading official source and
checksum metadata is research, not an install. Project execution approval never
implies installation approval, nor the reverse. Existing R11 preinstalled-server
smoke is a separate work unit, not evidence for installation.
