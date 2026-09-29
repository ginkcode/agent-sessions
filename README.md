# agent-sessions

A local desktop GUI to discover, browse, and manage sessions created by
agentic coding tools (Claude Code, Codex CLI, OpenCode). Read-only by
default; Linux first, macOS later.

- [SPEC.md](SPEC.md) — product specification
- [TASKS.md](TASKS.md) — task breakdown per milestone
- [docs/formats.md](docs/formats.md) — verified on-disk session formats
- [docs/plan/](docs/plan/README.md) — per-task implementation plans

## Build

Requires Go 1.26+.

```sh
make test   # go test -race ./...
make lint   # golangci-lint (via go tool)
make fmt    # gofumpt + golangci-lint fmt
make cli    # build bin/agent-sessions-cli
```

The CLI is the quickest way to inspect what the scanners see:

```sh
bin/agent-sessions-cli scan            # table of all detected sessions
bin/agent-sessions-cli show claude-code <session-id>
bin/agent-sessions-cli detect
```

## Release

Pushing a `v<version>` tag runs `.github/workflows/release.yml`: it tests,
then builds a universal macOS `.dmg` and Linux `.deb`/`.rpm` packages (amd64
and arm64), and publishes them as the GitHub Release for that tag. The tag
must match `info.productVersion` in `wails.json`. Tags with a suffix
(`v0.2.0-beta.1`) publish as pre-releases.

```sh
make version               # current version and whether its tag exists
make tags                  # release tags, newest first
make set-version V=0.2.0   # bump wails.json and commit it
make release               # tag HEAD as v<version> and push the tag
make untag                 # delete a local tag created by mistake
```

The macOS app is not notarized, so first launch needs right-click → Open.

Local equivalents write to `dist/`: `make package-linux` (needs
`libgtk-3-dev` and `libwebkit2gtk-4.1-dev`) and `make package-macos`
(macOS with the Wails CLI).
