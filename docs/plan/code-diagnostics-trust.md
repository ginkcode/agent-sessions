# Code diagnostics trust policy v1 (CD-02)

Status: implemented backend primitives in `internal/codediagnostics`; authoritative
addendum to [contract v1](code-diagnostics-contract.md). No checker execution,
transport capability, or approval UI is enabled. CD-03/CD-07/CD-08/CD-11 must wire
these primitives rather than treating catalog membership as approval.

## Host identity and ownership

Project identity is exactly `(hostId, canonical root)`. `HostIdentity.ID` hashes a
length-bounded JSON tuple `(Kind, Authority, Endpoint)` with SHA-256 and prefixes
`local:`, `ssh:`, or `wsl:`. Authority and Endpoint are each nonempty, <=256 UTF-8
bytes without control characters. IDs compare exactly. They are not secrets.

**Transport integration is deferred, not guessed from current labels.** The
existing `ConnectionState.Host`, SSH alias, and WSL picker names are insufficient.
The authenticated backend/transport owner must supply these explicit inputs:

| Kind | Authority | Endpoint |
|---|---|---|
| Local | installation machine ID plus effective account ID | application host-identity installation UUID |
| SSH | authenticated server host-key identity plus effective remote account ID | resolved endpoint (host/address, port, routing/security domain) plus host-side installation UUID |
| WSL | Windows installation/account identity | immutable distribution installation GUID plus Linux effective account and host-side installation UUID |

The transport must encode composite fields unambiguously (e.g. canonical JSON),
not concatenate unchecked strings. Reconnect reuses verified tuple values but
creates a **new Trust owner and connectionId**. Machine reinstall, account,
server key, resolved endpoint/security domain, WSL unregister/reinstall, or
installation-ID reset changes hostId and requires approval. Labels alone never
suffice; discovery failure must disable diagnostics, not fall back to `local`.
SSH aliases share a grant only if their entire verified tuple is identical;
ambiguous routing, host-key rotation, and different endpoint aliases deliberately
require reapproval. Transport verification/authentication, immutable identifier
creation/storage, handshake and expectedHostId routing remain CD-08/CD-09 work.
The helper validates bounds, not the authenticity of transport assertions.

One `Trust` belongs to one host-side authenticated connection/app lifetime,
including Local. Close it on disconnect/shutdown and never reuse on reconnect.
The frontend only selects an explicit mode and echoes the displayed fingerprint;
it owns no grant database. The project-host backend for the effective user is the
execution authority. A future manager owns attachment/generation tokens and
maps trust lease invalidation to new opaque wire generations and empty snapshots.
Trust's internal monotonically changing epoch is not a wire generation.

## Directory authorization and paths

`AuthorizeRoot` takes the existing catalog-known-directory callback, calls it
**before filesystem discovery**, then resolves symlinks on the project host and
requires an existing directory. Absolute host-native input is required; `..`
components are rejected before cleaning. It records both the authorized requested
spelling and canonical root plus filesystem identity. It never expands a nested
catalog directory to a repository parent. Symlink aliases share a project only
when separately catalog-authorized and resolved to the same canonical path.
Revalidation repeats catalog permission, resolution and `os.SameFile`; removing
catalog permission, retargeting an alias, or replacing a directory fails closed.
A zero-value/manually reconstructed AuthorizedRoot cannot authorize discovery.

Paths use the host's `filepath` semantics, never the desktop's. Case is not
manually folded: do not merge distinct case-sensitive directories. Host OS
resolution supplies canonical symlink spelling; alternate case spellings that
remain distinct can conservatively require separate approval. Windows drive and
UNC roots are accepted only as absolute native existing directories, without
parent components; cross-volume paths fail containment. Device/drive/UNC paths
are never valid portable diagnostic-relative paths. Windows trailing-dot/space
components and reserved device names are rejected. Native Windows runtime QA
remains necessary; tests exercise the platform-neutral hostile spellings on all
hosts, with native checks conditional on GOOS.

`ValidatePath` enforces CD-01's portable relative grammar. `ResolvePath` checks
hostId, root revalidation, symlink resolution, component containment and an
existing regular file using metadata only; no diagnostic body is opened.
`ResolveURI` decodes once, accepts only `file:` with no authority (even localhost),
userinfo, query, fragment or opaque payload, and returns a contained validated
relative path, **not** an actionable raw URI. Remote identity is supplied outside
the URI. Missing targets, wrong hosts, unsupported schemes, traversal and
escaping links fail closed. Adapters catch such failures and report the bounded
`OutsideRootNotice`, not raw paths or clickable links. Later file reads must use
rooted opens and reauthorize; a previously resolved pathname is not a read token.
Configuration hashes use `os.OpenRoot` to prevent escaping-link body reads.

## Bounded fingerprint (sha256-v1)

`Fingerprint` never invokes an executable, `--version`, package manager, shell,
plugin loader, or configuration evaluator. It accepts only adapter-owned fixed
metadata (not arbitrary client commands), revalidates the root, sorts tools by
providerId and config paths, and SHA-256 hashes a versioned JSON identity:

- project hostId and canonical root;
- providerId and adapter `Policy` string (<=8192 bytes): adapter/policy version,
  fixed argv, permitted environment values, toolchain selectors, runtime/wrapper
  and dependency identity policy;
- resolved executable path, regular-file size/mode/mtime and SHA-256 content hash;
- the same identities for every enumerated project-relative configuration file.

Local project binaries and external installed executables receive identical
hashing; no PATH-only/name-only identity is accepted. Executable paths are
absolute. Scripts/wrappers require their interpreter/runtime identity and relevant
plugin/lock/config identities too: future adapters must enumerate these inputs
or decline support, never claim an unverified launcher is safe. Missing relevant
files, unreadable files, irregular files, duplicate providers/configs, mutation
during hashing, and unverifiable identity fail closed. Config creation/removal
must alter the adapter's discovered input set. A deliberately non-executable test
file confirms discovery/approval requires no successful tool invocation.

Limits: 1–32 tools, 128 configs/tool, 4096 bytes/path, 256 MiB/file and 512 MiB total
hashed content per discovery. Fingerprint output is `sha256-v1:` plus 64 hex
characters. Discovery ordering is deterministic. Version/policy, executable,
permissions/mtime, relevant config, host or canonical root changes require a
matching new approval. Source edits alone do not change approval unless the
adapter identifies the source as executable/config input. Hashes, not contents,
enter the fingerprint; no bodies are stored in the grant database.

## Grant store and transitions

`NewGrantStore(paths.Roots.Config)` selects the project host's user config root:
`<Config>/code-diagnostics/grants.json`. It is dedicated JSON, not an edit of
shared `config.toml`. Every read and read/modify/write uses `configfile.Lock`
(the same `<path>.lock`, bounded 10-second cross-process lock); replacement uses
`configfile.WriteAtomic` with a synced 0600 temp file in a 0700 directory and
rename. It inherits that helper's durability semantics (no additional directory
fsync guarantee). Schema:

```json
{"version":1,"grants":[{"project":{"hostId":"opaque","root":"/canonical/project"},"fingerprint":"sha256-v1:<64 hex digits>"}]}
```

Only explicit **Always allow** records persist: no session grants, denials,
drafts, diagnostic results, tool output, connection/attachment tokens or raw
metadata bodies. At most 1024 unique project records / 1 MiB. Missing store means
no grants; malformed/version-unknown/duplicate/oversized/invalid records reject
the entire store without overwriting it. Repair/removal is an explicit user/admin
action, not silent approval recovery. Lock/write failures surface bounded errors.

- Initial lookup is required regardless of browsing permission.
- Approve session/persistent rehashes and compares the exact proposed fingerprint;
  stale proposals return `approvalChanged`. Approval itself launches nothing.
- Session records exist only in that Trust owner. Persistent lookup reads the
  locked disk state anew, so reload/reconnect can reuse only the exact identity.
- Not now (`Deny`, normally no API call) creates no record and does not secretly
  revoke an existing approval.
- Revoke marks this owner revoked and invalidates its epoch **before** canceling
  all queued/running lease contexts, then removes the durable project record.
  Failed removal still leaves this owner revoked; report that persistence failed.
  Reapproval starts a fresh epoch; old leases can never become valid again.
- Close invalidates/cancels all leases and drops effective session permission.

`Preflight` reauthorizes root, recomputes fingerprint and looks up grants.
`Lease.Validate` does this again immediately before spawn and before publication,
checking the original epoch and fingerprint. Future supervisors must listen to
`Lease.Context` for queued/running cancellation and process-tree cleanup. No
late result or queued launch may rely solely on an earlier successful preflight.
Revocation cancellation is synchronous within this Trust owner. Separate host
processes see persistent changes at their next lookup/validation, not through a
cross-process push bus; a future shared manager must centralize ownership if it
promises immediate cancellation across clients. Session grants in another
connection are independent and are not represented by a persistent record.

## Approval copy for CD-11

Title: **Allow code checks for this project?**

Body: **Code checks run the listed tools on this host with your account's
permissions. Project configuration and plugins can execute code. Tools may read
files and dependencies, access the network, and write caches or other files.
This is not a sandbox or a read-only guarantee. Only approve projects and tools
you trust.**

Show verified host identity/label, canonical root, executable/config display paths
and proposal fingerprint as plain text, never command buttons or raw links.

- **Enable for this session** — “Allow these tools until this connection or app
  session ends.” Explicit session approval; does not start a check.
- **Always allow this project** — “Remember this host, project and tool/config
  identity. Relevant changes require approval again.” Explicit persistent approval.
- **Not now** — “Do not enable code checks.” No mutation, no automatic launch.
- Revoke: **Revoke code check permission**. Feedback: “Permission revoked. Pending
  and running checks are being canceled; previous findings have been cleared.”
- Changed identity: “Tools or project configuration changed. Review and approve
  the new identity before running code checks.” Return to required, no auto-retry.
- Failed durable revoke: “Checks stopped for this session, but remembered
  permission could not be removed. Retry revoking before reconnecting.”

## Limits, not isolation

A hash is a bounded identity proposal, **not a transitive sandbox**. It cannot
attest all Go toolchain/JS dependency internals, imported config, dynamic plugin
loads, environment services, or files a tool will later read. Adapter dependency
coverage must be honest; unavailable identity must not be advertised as verified.
Directory identity is rechecked within an authorization lifetime; persistence is
scoped to host/canonical pathname and enumerated fingerprint, not an unforgeable
filesystem object capability across reinstalls/restores. A new attachment to the
same restored exact identity may reuse a persistent grant.

There is an unavoidable gap between pathname/hash validation and future process
start; hostile writers can swap binaries/config/root after preflight or while
hashing (including same-size/mtime restoration). Rooted config reads prevent
out-of-root content reads through symlink swaps, but do not freeze a project.
The supervisor must revalidate immediately and observe cancellation; stronger
executable handles, immutable snapshots, or OS sandboxing are separate work.
Fixed argv, canonical containment and metadata checks are no no-write guarantee.
