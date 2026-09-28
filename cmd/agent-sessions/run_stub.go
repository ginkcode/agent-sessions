//go:build !webkit2_41

package main

import (
	"fmt"
	"os"
)

// run is the fallback entrypoint compiled when the webkit2_41 build tag is
// absent. It keeps plain `go build ./...` and `go test -race ./...` green on
// machines without GTK/WebKitGTK development headers.
func run() {
	fmt.Fprintln(os.Stderr, "agent-sessions: the desktop GUI requires a build with -tags webkit2_41 (see cmd/agent-sessions/main.go)")
	os.Exit(1)
}
