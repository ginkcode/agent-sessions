# Go vet adapter evidence (CD-05)

Implemented backend adapter primitives only; no engine/RPC/UI capability is enabled.
The backend owner must share CD-03's supervisor and hold the returned result until
its own attachment/generation/run guards pass (CD-07). No following task is implemented.

## Policy and scope

`DiscoverGoVet` performs metadata/rooted configuration reads only. It never probes
before approval or promotes a selected child directory to a parent module. Discovery
is sorted, bounded to 4096 entries, depth 32, 24 modules and 128 configs. Hidden,
underscore, vendor, node_modules and testdata directories and all symlinks are
skipped with partial scope. Capped discovery is explicitly partial. Missing contained
modules are unsupported. Nested modules run separately and serially through the
same supervisor. Coverage roots are module directories; `./...` covers only Go's
build-selected packages, not every file. External changes remain unwatched.

Only the CD-04 evidenced Go 1.26.8 darwin/arm64 tuple is supported. Other platforms
fail before executable resolution. Reviewed host PATH entries resolve a regular
executable named `go`; there is no shell, installation, fallback, inherited environment,
or package script. An absent executable returns `notFound` for the future manager
to map to `missingTool`. Approved version probing requires exact stdout and empty
stderr. Runtime failures remain distinct from unsupported versions/projects.

The replacement environment fixes GOENV=off, GOWORK=off, GOFLAGS empty, native
GOOS/GOARCH, CGO_ENABLED=0 and explicit HOME/cache/GOROOT/PATH. Supervisor no-download
settings remain mandatory. go.work is intentionally ignored/disabled, including
parent workspace files; local `replace` directives are conservatively unsupported
(including contained ones). Only go.mod/go.sum configurations are supported;
module/cache dependencies may still be read by trusted Go. Go may write caches or
module metadata: this is not a no-write sandbox or source-fix feature.

Each probe/module command is separately fingerprinted using `Command.Policy()`:
exact argv, cwd, executable, environment, accepted exits, and adapter version. All
contained go.mod/go.sum inputs and installed VERSION/go.env/vet/compile/asm/link
identities are included. Missing identity files decline support. This is bounded
toolchain identity, not transitive standard-library/dependency attestation. Config
set creation/removal is re-enumerated before and after checks. Trust rehashes each
launch and publication; hostile swap races remain as documented by CD-02.

## Results

Fixed invocation: `go version`, then `go vet -json ./...` in each module.
Only exit 0 with empty stderr is accepted. Consecutive pretty-printed JSON objects
are decoded through EOF; empty output, truncation, unknown fields/shapes, nonzero
exits, operational stderr, cancellation, cleanup/capture limits and mixed-module
failures never authorize replacement. `CheckerResult.Replace=false` instructs the
manager to retain old results stale, even if an earlier module yielded findings.
Successful zero findings explicitly allow replacement. Successful partial discovery
or rejected finding paths always carry partial coverage. Findings are limited to
10,000 and a 7 MiB encoded per-provider budget; CD-07 must enforce combined budgets.

Paths pass portable validation, host containment and existing regular-file checks.
Numeric location suffixes parse from the right; no drive-colon splitting. Bounded
rooted source reads convert one-based byte columns into zero-based UTF-16, including
CRLF/non-BMP characters and exclusive ends. Missing end becomes zero-width; invalid
coordinates/read failures are file-level with invalidRange. Missing filenames cannot
be linked and produce partial scope. Read budget: 5 MiB/file, 16 MiB/module conversion (at most 24 modules).
Messages remain plain untrusted text; overlong UTF-8 messages are bounded with partial
scope. No raw stderr or output is exposed or logged.

Every finding uses `{kind:"saved"}` **without revision** and freshness `uncertain`.
Ranges describe a bounded saved read, not proof of the tool's analyzed snapshot;
consumers must keep these panel-only and never decorate dirty drafts. Neither
before/after configuration identity nor matching saved hashes supplies snapshot proof.

## Validation

`go_vet_test.go` feeds CD-04 recorded fixtures into the production parser and uses
synthetic module trees plus the supervisor's fake process seam. Tests never execute
the installed checker, shell, project code, install tools or edit source. Covered:
consecutive objects, clean/findings/failure/truncation, exact version rejection,
unsafe/drive paths, UTF-16/CRLF/rune boundaries, file-level/invalid/endless ranges,
limits, nested/parent-only modules, symlink exclusions, local replacements,
launch-policy/helper fingerprint changes, config creation, cancellation, stderr,
nonzero JSON exits and mixed outcomes.

2026-10-22 validation: full `go test -race ./...` passed; scoped
`go tool golangci-lint run ./internal/codediagnostics/...` passed. Full
`go tool golangci-lint run` was run and remains blocked by existing unrelated
errcheck/errorlint/gocritic/gofumpt/nilerr/staticcheck/unparam/unused findings outside
this task. No unrelated lint files were changed. Native real-tool/platform QA and
manager/transport/UI integration remain deferred, not claimed as passing here.
