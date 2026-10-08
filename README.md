# agent-sessions

A desktop app to find, read and manage the sessions that agentic coding tools
leave on disk: **Claude Code**, **Codex CLI** and **OpenCode** (CLI and
Desktop). It reads each tool's own files, needs no account or server, and
changes nothing unless you turn session management on.

It runs on **Linux**, **macOS** and **Windows**, and can browse sessions on
other machines over SSH or in WSL distributions.

- [SPEC.md](SPEC.md): product specification
- [TASKS.md](TASKS.md): task breakdown per milestone
- [docs/formats.md](docs/formats.md): verified on-disk session formats
- [docs/plan/](docs/plan/README.md): per-task implementation plans

## Features

**Browse**

- One list for every agent's sessions, grouped by directory then agent, by
  agent then directory, or flat. Filter by agent, directory and time.
- A cache index makes startup fast; watchers pick up new and changed sessions
  as the agents write them. Several running copies of the app and remote clients share the
  index.
- Full-text search across all transcripts (press `/`), jumping to the
  matching message.

**Read**

- Transcripts with Markdown, tool calls, patches, reasoning, compactions and
  links to subagent transcripts.
- Three display levels: **Chat** (just the conversation), **Activity** and
  **All**. Settings chooses the default.
- The session header shows the directory, Git branch, created and updated
  times, token counts and cost.
- Right-click a message to copy it as Markdown, or to translate it through an
  OpenAI-compatible API of your choice (Vietnamese by default). The
  translation replaces the text in place, with a toggle back to the original.

**Continue**

- Resume a session in its own agent: copy the resume command, or open it in a
  terminal. Linux supports GNOME Terminal, Ptyxis, GNOME Console, Konsole,
  Xfce Terminal, Ghostty, kitty, WezTerm, Alacritty, foot and XTerm; macOS
  supports Terminal, iTerm2 and Ghostty; Windows uses PowerShell (in Windows
  Terminal when that is the default).
- **Continue in another agent**: a handoff turns a session into a prompt for
  a different agent, with a size budget, optional reasoning and secret
  redaction. It is delivered as a file the new agent reads, then waits for
  you.

**Share**

- Export a session as a `.agent-session.zip` bundle. *Complete* bundles keep
  the native files; *share-safe* bundles drop them and redact secrets (private
  keys, known token prefixes, JWTs, `KEY=` assignments, home paths). Redaction
  is best-effort, and the dialog shows what it found.
- Open a bundle to read it and continue it in any agent.

**Manage** (off by default)

- Delete sessions after a preview of every file involved. Claude Code sessions
  go to the system Trash (Linux, macOS) or Recycle Bin (Windows). Codex and
  OpenCode sessions are deleted permanently through their own CLIs, which
  needs a second opt-in. Live sessions cannot be deleted.

**Remote hosts**

- Pick an SSH alias from `~/.ssh/config`, or type one, and the app browses
  that machine as if it were local. It uploads a small headless server
  (`agent-sessions-cli serve`) to the host, checks it against `SHA256SUMS`,
  and talks to it over the ssh session. Remote hosts can run Linux or macOS on
  amd64 or arm64.
- On Windows, WSL distributions are listed separately and reached through
  `wsl.exe`, with no ssh needed.
- Each host can have its own environment overrides (⚙ in the host menu), for
  example data roots that differ from the defaults.
- On Linux and macOS, ssh password and passphrase prompts appear in the app.
  On Windows, ssh must authenticate without prompts (keys or an agent).

**Look and feel**

- Light, dark and system themes; times in UTC or the local time zone.
- `Ctrl+,` (`⌘,` on macOS) opens Settings; `/` opens search; `Esc` closes
  dialogs.

## Where data lives

The app reads each agent's default location, and honours `CLAUDE_CONFIG_DIR`,
`CODEX_HOME` and `XDG_DATA_HOME`:

| Agent       | Default location         |
|-------------|--------------------------|
| Claude Code | `~/.claude`              |
| Codex CLI   | `~/.codex`               |
| OpenCode    | `~/.local/share/opencode` |

Its own files:

| Platform | Settings (`config.toml`)                       | Cache                                 |
|----------|------------------------------------------------|---------------------------------------|
| Linux    | `~/.config/agent-sessions`                     | `~/.cache/agent-sessions`             |
| macOS    | `~/Library/Application Support/agent-sessions` | `~/Library/Caches/agent-sessions`     |
| Windows  | `%APPDATA%\agent-sessions`                     | `%LOCALAPPDATA%\agent-sessions\cache` |

`config.toml` holds the `[manage]`, `[terminal]` and `[translate]` settings.
It is written `0600`, as it may hold the translation API key, which the app
never shows again once saved. The cache holds transcript text and is written
`0600` too.

## Privacy

Credential files (`.credentials.json`, `auth.json`, OpenCode account tables)
are never read. There is no telemetry and no listening port. The network is
used only for:

- ssh to hosts you pick, through your own `ssh`;
- translation, which sends the message you translate to the endpoint you set
  up. Nothing is sent until you configure it and choose Translate.

## Install

Releases on GitHub provide:

- **macOS**: a universal `.dmg`. The app is not notarized, so the first
  launch needs right-click → Open.
- **Linux**: `.deb` and `.rpm` for amd64 and arm64. They need WebKitGTK 4.1.
- **Windows**: an amd64 `.msi`. It is not code-signed, so SmartScreen shows
  "Windows protected your PC" on first run: choose More info → Run anyway.
  It installs for the current user in `%LOCALAPPDATA%\Programs\Agent
  Sessions` without an admin prompt, and needs the WebView2 Runtime, which
  Windows 11 includes.

Every package includes the remote servers. The CLI is not packaged; build it
with `make cli`.

## CLI

`agent-sessions-cli` inspects sessions without the GUI:

```sh
agent-sessions-cli scan [--agent claude-code] [--json] [--all]   # table of sessions
agent-sessions-cli show <agent> <id> [--json] [--meta]           # one transcript
agent-sessions-cli handoff <agent> <id> --target <agent> [--budget compact|detailed|full|unlimited|N] [--redact]
agent-sessions-cli export <agent> <id> [--profile complete|share-safe] [--redact] -o <file>
agent-sessions-cli inspect <bundle> [--json]
agent-sessions-cli detect                                        # which agents are installed
agent-sessions-cli version
```

Agents are `claude-code`, `codex` and `opencode`. `serve` and `transfer` are
used by the desktop app on remote hosts.

## Build

Requires Go 1.26+ and Node.js for the frontend. The desktop app also needs
the [Wails v2 CLI](https://wails.io) (`go install
github.com/wailsapp/wails/v2/cmd/wails@v2.14.0`), and on Linux `gcc`,
`libgtk-3-dev` and `libwebkit2gtk-4.1-dev`.

```sh
make test            # go test -race ./...
make lint            # golangci-lint (via go tool)
make fmt             # gofumpt + golangci-lint fmt
make cli             # build bin/agent-sessions-cli
make dev             # run the desktop app with live reload
make app             # build the desktop app
make remote-servers  # build the remote servers into build/remote
make help            # every target
```

Frontend checks run from `frontend/`: `npm test`, `npm run check` and
`npm run build`.

## Release

Pushing a `v<version>` tag runs `.github/workflows/release.yml`: it tests,
then builds a universal macOS `.dmg`, Linux `.deb`/`.rpm` packages (amd64
and arm64) and a Windows amd64 `.msi`, and publishes them as the GitHub
Release for that tag. The tag must match `info.productVersion` in
`wails.json`. Tags with a suffix (`v0.2.0-beta.1`) publish as pre-releases.

```sh
make version               # current version and whether its tag exists
make tags                  # release tags, newest first
make set-version V=0.2.0   # bump wails.json and commit it
make release               # tag HEAD as v<version> and push the tag
make untag                 # delete a local tag created by mistake
```

Local equivalents write to `dist/`: `make package-linux` (needs `gcc`,
`libgtk-3-dev` and `libwebkit2gtk-4.1-dev`), `make package-macos` (macOS
with the Wails CLI) and `make package-windows` (Linux with the Wails CLI,
`jq` and `wixl`).
