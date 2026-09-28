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