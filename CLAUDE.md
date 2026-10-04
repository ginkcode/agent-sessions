# Notes for Claude

## Windows test builds

Development runs in WSL2 on Windows. When the user asks for a Windows test
version of the desktop client, put it in the next unused
`C:\Users\Gink\agent-sessions-launchN` (WSL: `/mnt/c/Users/Gink/agent-sessions-launchN`).
Check which numbers exist first and never overwrite an existing folder: an
older build in it may still be running.

Each folder holds:

- `agent-sessions.exe`: the desktop app.
- `agent-sessions-cli.exe`: the CLI.
- `remote/`: freshly built servers, with their `SHA256SUMS`. The app deploys
  these to WSL and SSH hosts from `remote\` next to the exe. Stale bundles miss
  server-side fixes, so always rebuild them.

Build in a fresh `/tmp` folder so `build/bin` and `build/windows` stay
untouched. The temporary `wails.json` edit mirrors `make package-windows`, and
the trap restores the original.

```sh
N=6   # next unused number
B=/tmp/as-win-build-$N; W=/mnt/c/Users/Gink/agent-sessions-launch$N
test ! -e "$B" && test ! -e "$W"
V=$(sed -n 's/^ *"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
LDFLAGS="-s -w -X github.com/ginkcode/agent-sessions/internal/version.Version=$V"

mkdir -p "$B/windows"
cp frontend/assets/Icon-universal.png "$B/appicon.png"
cp build/windows/icon.ico build/windows/info.json build/windows/wails.exe.manifest "$B/windows/"
(cd frontend && npm run build)
make remote-servers REMOTE_DIR="$B/remote"

backup=$(mktemp ./wails.json.release.XXXXXX); cp -p wails.json "$backup"
trap 'mv "$backup" wails.json' EXIT
jq --arg root "$PWD" --arg out "$B" \
  '.projectdir = ($root + "/cmd/agent-sessions") | .["build:dir"] = $out' "$backup" > wails.json
wails build -platform windows/amd64 -tags desktop -s -m -nosyncgomod -skipbindings -trimpath -ldflags "$LDFLAGS"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" \
  -o "$B/bin/agent-sessions-cli.exe" ./cmd/agent-sessions-cli

mkdir -p "$W/remote"
cp "$B/bin/agent-sessions.exe" "$B/bin/agent-sessions-cli.exe" "$W/"
cp "$B/remote/"*.gz "$B/remote/SHA256SUMS" "$W/remote/"
(cd "$W/remote" && sha256sum -c SHA256SUMS)
"$W/agent-sessions-cli.exe" version
```

Tell the user the Windows path of `agent-sessions.exe`, the version it
reports, and whether the build includes uncommitted changes.
