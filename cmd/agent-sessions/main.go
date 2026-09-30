// Command agent-sessions is the Wails v2 desktop application entrypoint.
//
// The GUI build is gated behind a build tag so that plain `go build ./...`
// and `go test -race ./...` never require the GTK or WebKitGTK development
// headers: `webkit2_41` on Linux (it also selects WebKitGTK 4.1), `desktop`
// on macOS:
//
//	wails build -tags webkit2_41 -clean   # or: make app
//	wails dev -tags webkit2_41            # or: make dev
//	wails build -tags desktop -platform darwin/universal   # macOS
//
// System build dependencies on Linux: libgtk-3-dev, libwebkit2gtk-4.1-dev
// (verify with: pkg-config --exists webkit2gtk-4.1 gtk+-3.0).
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ginkcode/agent-sessions/internal/remote"
)

// init applies the WebKitGTK DMA-BUF renderer workaround before any GTK
// initialization. On some Linux drivers (NVIDIA and certain Mesa versions)
// WebKitGTK 4.1 fails to allocate DMA-BUF textures and renders a blank or
// black window. Disabling the DMABUF renderer falls back to shared-memory
// rendering at a small performance cost. The user may opt out by exporting
// the variable explicitly.
func init() {
	if os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
}

func main() {
	if sock := os.Getenv("AGENT_SESSIONS_ASKPASS_SOCK"); sock != "" {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[1]
		}
		token := os.Getenv("AGENT_SESSIONS_ASKPASS_TOKEN")
		if err := remote.RunAskpassHelper(context.Background(), sock, token, prompt, os.Stdout, os.Stderr); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "agent-sessions askpass: %v\n", err)
			os.Exit(1)
		}
		return
	}
	run()
}
