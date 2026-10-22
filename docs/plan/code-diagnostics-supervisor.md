# Checker supervisor (CD-03)

Backend primitives only; no adapters, RPC capability or engine wiring are enabled.
The project-host backend must share one `Supervisor` across all projects and close
it on disconnect/shutdown. Never route remote commands through a desktop runner.

## Invocation and trust

`Command` is internal adapter-owned metadata, not a client command interface.
`Command.Policy()` binds exact executable, argv, directory, explicit environment,
accepted exits and adapter-policy version to the selected `ToolInput.Policy`.
Enumerate other runtime/config/dependency identities using CD-02 ToolInputs.
Executable resolution is adapter discovery on the project host: absolute paths
only, no PATH lookup, command-string parsing or shell. Working directories are
canonical contained existing directories, revalidated immediately before start.
Both preflight and lease validation rehash approved identities; queued/running
work observes lease revocation. Validation cannot eliminate hostile filesystem
swap races (see the trust addendum).

Environment is **explicit replacement**, not backend inheritance. Adapters must
provide only reviewed required values (including SystemRoot on Windows), bound
in the fingerprint. The supervisor appends non-overridable GOTOOLCHAIN=local,
GOPROXY=off, GOSUMDB=off, npm_config_offline=true,
npm_config_ignore_scripts=true and COREPACK_ENABLE_NETWORK=0. These prevent
controllable implicit fetching, not arbitrary plugin network access. Adapters
must never select npx, package scripts, installation, source-fix or download flags.
This is not a sandbox or a guarantee that trusted tools cannot write caches.

## Bounds and outcomes

Defaults, injectable downward in tests: 120 seconds after preexecution validation,
10 MiB combined raw stdout+stderr, 8192 bytes per newline-delimited stream message,
5 seconds exec pipe-drain cleanup, 32 pending requests. Two workers enforce two
processes/backend; project slots enforce one process/project across providers.
Time includes process start and drain, not queue time or bounded trust hashing.
Process creation/Timer are injectable; their implementations must return promptly
and Process.Wait must unblock after termination/close. OS process creation itself
has no independently interruptible Go API. Identity hashing/store locks retain
CD-02 bounds and are not interruptible in the middle of a filesystem operation.

Capture counts every retained byte, including newlines, across both streams.
An exact boundary is valid; the next byte exceeds it. A message is a byte sequence
between LF characters; CR counts as a byte. Exceeding either budget stops capture,
signals tree termination and marks partial/truncated even if an accepted exit
races it. Pumps independently drain stdout/stderr using exec's concurrent bounded
writers. No unbounded line buffers. Captures are private parser input, never
logged, persisted or used as user-visible stderr. Only constant sanitized notices
are returned; later adapters sanitize/UTF-8-bound findings. A truncated raw prefix
may end inside UTF-8 and must not be displayed directly.

Typed outcomes distinguish launch failure, tool failure, accepted exit, cancel,
timeout, output limit and trust failure. Adapter policy supplies accepted exit
codes, including diagnostic nonzero exits. Cleanup failure is partial tool failure,
never clean success. `FindingBudget=10000` carries a parser/manager budget hint;
CD-07 must enforce the attachment-wide count and encoded-byte limits independently.

## Scheduling and lifecycle

Identical pending/running provider requests coalesce. Explicit supersession needs
a fresh run ID, invalidates the old run before canceling it, and queues the new
one. A full queue rejects without canceling old work. Canceled queued work is
removed even when its project slot is occupied. FIFO selection skips busy projects.
At most 64 latest identities are remembered; completed entries can be evicted and
are then ineligible for publication. Handles expose Done, Result, Cancel and a
final lease/latest-run `ValidatePublication` guard. Results are immutable copies.
CD-07 must additionally atomically check manager attachment/generation/run tokens
when committing: this supervisor is not a result store. Old Cancel handles cannot
cancel newer runs. Close is idempotent, invalidates publication first, cancels
work and joins workers/watchers without holding owner/engine locks.

## Process-tree cleanup and limits

Linux/macOS use a dedicated process group and SIGKILL the group on **every**
completion, not only cancellation. Windows starts suspended, assigns a kill-on-close
Job Object before resuming with NtResumeProcess, and terminates/closes that job.
Assignment/resume failure kills the still-suspended process and fails closed.
No shell/taskkill process is spawned. exec.WaitDelay bounds pipe retention by
inherited descendant handles. Wait is always joined; termination errors are
explicitly surfaced, not treated as proof of cleanup.

These mechanisms own ordinary descendants, not hostile daemonization: Unix code
can deliberately escape a process group with setsid, and kernel/filesystem stalls
cannot be forcibly bounded by Go. No sandbox/cgroup guarantee or uninterruptible
kernel-wait guarantee is claimed. Windows native QA of nested jobs and endpoint
security policy is still a release-gate requirement; unsupported job assignment
fails closed rather than silently falling back to parent-only Kill. Native
runtime scope here is macOS; Linux/Windows tests cross-compile, while helper tests
exercise interleaved streams, flooding, launch failure and ordinary descendants
on the current host (Linux accepts nonexecuting adopted zombies in containers).
