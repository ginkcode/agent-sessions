# Engine/App integration (CD-08)

Stage 1 uses the optional `engine.CodeDiagnosticsBackend` surface; the unrelated
`Backend.Diagnostics` and catalog browsing/editing APIs are unchanged. The Engine
implements attach, snapshot, approve/revoke, run/cancel and detach. Retry is run
with one provider ID, coalescing active work and creating a fresh completed-run
identity. RPC embeds explicit unsupported methods until CD-09 negotiates and
implements this surface; no Stage 1 or live remote capability is advertised.
Offline methods return bounded `disconnected` failures and App never falls back
to Local for a selected offline remote.

The Service authorizes catalog CWD/RepoRoot before canonicalization. Engine attach
rejects unknown directories even before identity initialization. Attach creates a
pending empty attachment and returns before bounded metadata discovery/hashing;
manager CompleteDiscovery installs the proposal only on the original live,
unrevoked epoch. No version probe runs until explicit approval and run. The
registry includes only CD-05 Go vet and CD-06 Svelte-check policies, with their
existing darwin/arm64/version/config restrictions. Missing/unsupported tools are
explicitly unavailable; combined proposal budget failures disable both rather
than silently enabling an unapproved subset. Approval never starts a checker.

Local identity uses Darwin `gethostuuid`, the effective account UID and an atomic
0600 installation UUID under `<Config>/code-diagnostics/installation-id`. The
bounded canonical JSON machine/account tuple is hashed by HostIdentity.ID.
Unavailable native identity fails closed (non-Darwin identity acquisition is not
implemented; those checker platforms were already unsupported). No subprocess is
used for identity discovery. `ConnectionState.hostId` exposes Local's stable ID;
remote authenticated identity and handshake remain CD-09. App wrappers require
expectedHostId, check the actual backend identity, and reject connection-generation
changes before dispatch and after replies; stale successful attach is detached.
No mutation is retried after a routing change.

Each Engine owner holds a fresh connection ID, Trust, Supervisor and Manager.
Shutdown/reset removes the owner before canceling/joining workers outside the
Engine lock. App Connect/Disconnect resets Local ownership/session grants; remote
replacement already closes its transport. Pending discovery cannot revive an old
owner. A bounded periodic pull drains manager events; gap recovery uses the same
bounded event splitter (never an oversized one-part snapshot). Delivery checks
current manager context and Engine owner, then passes through the existing
Local-only/remote-generation emitter guards. No diagnostic bodies are logged or
persisted. No document sync or automatic execution is enabled.

## Validation (macOS arm64)

- `go test ./...` passes.
- `go test -race ./...` passes; after final discovery/error changes, focused
  `go test -race ./internal/codediagnostics ./internal/engine ./internal/app ./internal/rpc` passes.
- Scoped lint with v2.11.3 `--new-from-rev=HEAD` passes (0 new issues). Unfiltered
  scoped lint reports existing unrelated errcheck/errorlint/unused/style findings.
- Generated ignored `frontend/wailsjs` bindings with Wails' generator, not manual
  edits. This repository's split main requires a temporary `cmd/agent-sessions/wails.json`
  with projectdir `.` and frontend/wailsjs dirs `../../frontend`, then
  `wails generate module -tags webkit2_41` there. Wails strips the `desktop` tag
  during generation; `webkit2_41` selects the same GUI entrypoint on macOS. The
  temporary config was removed. Generated bindings remain ignored by repository
  policy. `npm run check` passes with the existing ignoreDeprecations warning.

Tests cover catalog-first unknown/missing roots, nested root identity, canceled
request, unapproved/stale approval, foreign contexts, revoke/stale generation,
late discovery, detach/close idempotency, local/remote/offline routing, expected
host rejection, backend switch mid-attach and stale attachment cleanup. Existing
manager/supervisor suites cover approved fake execution, retries, cancellation,
fingerprint changes and stale publication. No native GUI/manual QA, Windows/Linux
runtime checker QA or real-tool/remote end-to-end execution is claimed.
