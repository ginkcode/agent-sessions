//go:build darwin

package claude_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/provider/claude"
)

func TestLiveDetectionDarwin(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, pid int, id, procStart string) {
		t.Helper()
		data := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"procStart":%q,"status":"idle","updatedAt":1790514010000}`, pid, id, procStart)
		if err := os.WriteFile(filepath.Join(sessions, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// macOS has no start time to compare, so a recorded procStart fails
	// closed (a reused PID is not live). Without one a running PID is live;
	// a PID that does not exist is not.
	write("a.json", os.Getpid(), "sess-recorded-start", "123456")
	write("b.json", os.Getpid(), "sess-no-start", "")
	write("c.json", 0x7FFFFFFC, "sess-dead", "")

	live, err := claude.New(root, nil).Live(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 1 || live["sess-no-start"].PID != os.Getpid() {
		t.Fatalf("unexpected live sessions: %v", live)
	}
}
