# Checker format and invocation evidence (CD-04)

Status: completed spike, **not production adapter support or application approval**.
Authoritative companions: [contract](code-diagnostics-contract.md),
[trust](code-diagnostics-trust.md), [supervisor](code-diagnostics-supervisor.md).
CD-05/CD-06 must implement these narrowed policies before enabling any capability.

## Provenance and reproducibility

On 2026-10-22, manually approved developer experiments ran installed tools only
against disposable synthetic workspaces on **macOS arm64**. No installation,
download, npx, package script, repository Svelte/Vite config, or real project
source was executed by the spike. This permission does not bypass application
trust: even version/help probes require approval in production.

`internal/codediagnostics/testdata/checkers/evidence.json` records each exact argv,
cwd, stdout/stderr, exit, version inventory, synthetic input file contents (including
literal CRLF), interpretation and provenance. `/fixture` and `/installed` replace
private roots; timestamps are replaced by `1700000000000`. Only these intentional
sanitizations were applied. Fixture source/messages are synthetic and safe to retain;
this is not permission to persist real checker output. `checker_fixtures_test.go`
validates metadata and scoped format assertions without running tools.

Installed inventory: Go **1.26.8**, Node **22.23.2**, svelte-check **4.7.6**,
Svelte **5.57.1**, TypeScript **5.9.3** (the installed versions, not the manifest
ranges or checker development dependencies). Svelte fixtures except the missing
config-dependency experiment used a `node_modules` symlink to the existing installed
dependency tree. That tree supplied libraries, not repository configuration.

Environment was an explicit replacement: disposable HOME, GOCACHE and GOMODCACHE;
PATH containing the installed Go bin plus `/usr/bin:/bin`; GOENV=off, GOWORK=off,
GOFLAGS empty, GOTOOLCHAIN=local, GOPROXY=off, GOSUMDB=off,
npm_config_offline=true, npm_config_ignore_scripts=true,
COREPACK_ENABLE_NETWORK=0, NO_COLOR=1. Tool writes to disposable caches are allowed;
these controls do not sandbox arbitrary configuration/plugin code or network use.

## Fixed choices and initial supported versions

Support **only Go 1.26.8**, and **svelte-check 4.7.6 with Node 22.23.2,
Svelte 5.57.1 and TypeScript 5.9.3**, initially. These are narrow evidence-based
parser targets, not an assertion that other patches are incompatible. Reject unknown
versions as `unsupported`, missing executables/dependencies as unavailable, without
fetching or retrying via another package manager. Metadata discovery must not run
probes. After approval, require exact successful stdout version formats observed:
`go version go1.26.8 darwin/arm64`, `v22.23.2`, `svelte-check, 4.7.6` (newline
terminated, empty stderr). Platform tokens need separate runtime evidence; Linux,
Windows, SSH and WSL were **not executed** here and remain release/availability gates.
Do not infer support from semver ranges in package.json.

### Go

Resolve an absolute installed `go` candidate from reviewed host PATH metadata,
then bind the executable/toolchain identity in the approval fingerprint. Invoke
`go version`, then **`go vet -json ./...`** from a contained module directory.
Require GOENV=off, GOWORK=off, GOFLAGS empty plus supervisor no-download settings;
bind reviewed GOOS/GOARCH/CGO/environment choices in policy. No `-vettool`, fix,
generate, toolchain selection, package-script or shell fallback.

Observed JSON mode writes to **stdout**, clean `{}` and findings both exit **0**.
Multiple packages emit **consecutive JSON objects**, not one enclosing JSON value
or NDJSON (objects are pretty-printed). Decode until EOF with byte/message caps;
never accept a truncated final object. Objects map package → analyzer → diagnostics.
The printf records contain `posn`, `end`, `message`; analyzer is `printf`. No
severity field: choose normalized `warning`, source `go vet`, code analyzer name.
`posn`/`end` are one-based **byte** columns, with an exclusive end in this evidence.
Parse location suffixes from the right, never split drive/path colons naively.
The CRLF source with `é😀` proves byte column 40 becomes UTF-16 character 36 on
zero-based line 2; end byte column 42 becomes character 38. Preserve missing ends
as zero-width start locations, not guessed spans; absent/invalid positions become
file-level (`invalidRange` for invalid coordinates), validated against basis text.
Actual file-level output was not observed; synthetic contract tests cover omission.

Build failure and missing dependency exit **1**, empty stdout and stderr text;
these are failures, not clean empty replacements. Never execute a suggested `go get`
from stderr. Text mode was also observed: findings exit **1** on stderr. **Do not
use text mode or its exit policy for the JSON adapter**. JSON accepted exit is 0;
valid JSON alone is not sufficient if operational stderr, incomplete decoding,
unknown record shape, cancellation or supervisor limits indicate failure. Preserve
old results stale on failure, including mixed module outcomes.

### Svelte

Resolve absolute installed Node plus a contained installed package entry
`node_modules/svelte-check/bin/svelte-check`; invoke **Node directly with entry as
argv[0]**, followed by `--tsconfig ./tsconfig.json --output machine-verbose` from
the contained package cwd. Version argv is Node, entry, `--version`. Do not execute
`.bin` shell wrappers. Fingerprint Node, the JS entry, ts-version-check.js, bundled
checker implementation and relevant package/lock/config/runtime dependency
identities; enumeration failure must decline support, not approve a name alone.
No NODE_OPTIONS/NODE_PATH inheritance or tsgo/incremental/watch/fix flags.

Stdout records have a decimal millisecond timestamp and space prefix. START has
JSON-quoted workspace text. Diagnostic records in **machine-verbose** are JSON
objects: `type` ERROR/WARNING, `filename`, zero-based **UTF-16** `start`/`end`,
exclusive end, `message`, optional numeric/string `code`, optional `source` and
optional `codeDescription`. Treat URLs as inert text and discard codeDescription.
Numeric codes become decimal strings; absent source defaults to `svelte-check`.
CRLF/non-BMP evidence places `missing` at line 3, character 11 to 18 (byte offset
would differ). Messages include escaped newlines. COMPLETED is a plain-text record:
`<timestamp> COMPLETED <n> FILES <n> ERRORS <n> WARNINGS <n> FILES_WITH_PROBLEMS`.
Require a start, structurally valid records and a final matching summary; never
interpret progress as findings. Machine (non-verbose) instead emits one-based
line/column text and omits rich fields; it is evidence only, not the chosen format.

Clean/warning-only exit **0**, errors exit **1**, all on stdout. Supervisor accepted
exits are 0 and 1, but **exit 1 is not automatically a successful diagnostic run**.
Malformed tsconfig (code 1136), missing extended config dependency (6053), and a
throwing synthetic svelte.config.js all emit error records plus COMPLETED and exit
1. The throwing config also writes a stack to stderr. Classify these as operational
configuration/dependency failures, not successful source checks; retain stale prior
sets. Missing Node JS entry exits 1 on stderr without records/summary. These cases
show why summary/exit alone cannot prove coverage. Unknown stderr/config failures
must fail conservatively with constant sanitized notices, not raw stack display.
Source errors (e.g. code 2322/2304) with a valid summary are findings, not toolFailed.

## Coverage, paths, freshness and platform gates

- Only contained modules/packages discovered by bounded metadata traversal; never
  walk to a parent module/tsconfig. Start with go.mod modules (ignore/disable external
  go.work) and packages having explicit local tsconfig. Nested modules/packages are
  separate serial runs. Report omitted/capped/unresolved scope partial. Decline
  unsupported external workspace/config references rather than silently widening.
- Go `./...` respects build constraints and excludes nested modules; vet is not a
  complete lint suite. Svelte checks tsconfig-selected saved files and dependencies,
  not all project files. Clean Svelte fixture reports 141 FILES despite only one
  authored Svelte file because library/declaration files participate. Do not use
  that number as a count of project files verified. Dependencies outside the root
  may be read by a trusted tool; out-of-root diagnostics get a bounded notice only.
- Space-containing absolute Go paths and relative Svelte filenames were observed.
  All returned paths must pass host canonical containment and portable validation.
  Windows drive/UNC samples in existing trust tests are synthetic, **not checker
  evidence**. No native Windows launcher check was performed. Policy: go.exe and
  node.exe only, Node with JS entry as separate argv; never cmd.exe, PowerShell,
  .cmd/.bat/.ps1 shims or PATHEXT shell fallback. Missing verified native runtime
  means unavailable. Direct Node execution is observed on macOS only; Windows
  Job Object/native argv/runtime validation remains a release gate.
- Both tools read mutable saved files; no immutable snapshot or tool read attestation
  was established. Even matching before/after hashes cannot exclude concurrent
  edit/restore. Default basis saved without verified revision, **uncertain** freshness,
  panel-only unless downstream obtains actual snapshot proof. Do not put ranges on
  dirty drafts or claim current based on post-run hashes. Source reads for conversion
  need rooted bounded authorization and basis validation; mismatch is file-level.
- No naturally missing-range finding or malformed checker output was observed.
  Explicit `synthetic-mutation` fixtures cover missing range, truncated Go JSON,
  malformed Svelte JSON and missing summary; existing CD-01 fixtures cover other
  severities/file-level/hostile coordinates. These are parser requirements, not
  claims about real tool output. Multiline Svelte messages were observed; multiline
  Go messages, other analyzers/versions and broader dependency failures remain gaps.

## Downstream scope decision

CD-05 remains 3 h for the single verified Go format/version and conservative
module/environment policy. CD-06 remains 4 h for the single verified runtime tuple,
explicit-tsconfig layout, verbose format and fail-closed config detection. No broad
semver, arbitrary launcher or monorepo-workspace compatibility is included. If
identity enumeration, broader config-failure discrimination or platform support
cannot fit, split/re-estimate **before** implementing that scope; do not relax trust
or manufacture successful empty output. No production adapters are added here.
