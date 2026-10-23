# CD-09 identity activation — scoped follow-up split

> **Update 2026-10-23:** This split is replaced by a host-side identity; the client no longer verifies an SSH host key or WSL distro tuple. Real SSH/WSL QA remains. See the [editor (Zed) model](../plan/code-diagnostics-zed-model.md).

- **Status:** Not started; blocks CD-09 production remote activation and completion
- **Parent:** [CD-09](CD-09.md); not counted as completed tasks in the 28-task tracker
- **Reason for split:** authenticated launch identity is not currently available to
  the diagnostics engine. Cross-platform acquisition, channel binding and native
  validation exceed CD-09's four-hour transport budget. Do not enable capabilities
  using an SSH alias, WSL name, client initialize assertion or environment variable.

## Evidence from current launchers

`internal/remote/ssh.go` shells out to OpenSSH, including config/proxy and
ControlMaster handling. Its process/control socket identifies a connection, not
an exported verified host key + resolved endpoint + security domain/account tuple.
An `ssh -G` result or a separate key scan is not proof of the key authenticated by
that connection. The headless backend has no trusted authority injection channel.

`internal/remote/wsl_windows.go` enumerates HKCU Lxss installation GUID subkeys,
but `readDistro` discards the GUID and returns only `WSLDistro{Name: name}`. Windows
installation/account authority and binding the selected GUID to the launched
instance are absent. Keeping the GUID alone would not satisfy the CD-01 tuple.
Thus neither transport has complete known authenticated launch metadata suitable
for simply wiring into `RemoteCodeDiagnosticsHostID` today.

## Separately estimated work packages (each at most 4 developer hours)

1. **SSH authority acquisition / design spike — 4 h.** Establish how the existing
   OpenSSH connection exports its actually authenticated host key, resolved
   endpoint and security domain/account, including aliases, proxies, host-key
   rotation and multiplexing. Produce tests/design or an explicit unsupported
   result; no keyscan/label fallback. Re-estimate/split before exceeding the cap.
2. **SSH trusted launch binding / activation — 4 h, depends on 1.** Bind that tuple
   to this headless backend and its engine root authority, not arbitrary RPC
   assertions. Advertise only after successful binding. Test tuple mismatch,
   reconnect, key/account change, old servers and absent metadata fail-closed.
3. **WSL authority acquisition — 4 h.** Acquire installation/account identity and
   immutable distro installation GUID through trusted Windows APIs; bind selection
   to launch and detect deletion/reinstallation/name reuse. Add Windows tests;
   failure/unsupported environments remain disabled.
4. **WSL trusted launch binding / activation — 4 h, depends on 3.** Wire verified
   authority into backend/engine identity, preserve browse/edit compatibility,
   test mismatches and fresh grants after reinstall/reconnect. Run native Windows
   integration validation or explicitly keep activation blocked pending it.

Each activation package must validate both handshake metadata and the engine's
`Project.HostID` agree, preserve canonical-root authorization and grant isolation,
and run full Go/race plus scoped lint. CD-17 must include actual SSH/WSL checks;
synthetic identities and cross-compilation are not real-host QA. These packages
are proposed scoped follow-ups, not shipped support or completed acceptance.
