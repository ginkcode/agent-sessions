package managedtools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandCancellation(t *testing.T) {
	if os.Getenv("MANAGED_TOOL_TEST_HELPER") == "1" {
		// This is this test binary, never Go or a language server.
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = runGo(ctx, exe, filepath.Dir(exe), []string{"MANAGED_TOOL_TEST_HELPER=1"}, "-test.run=^TestCommandCancellation$")
	if err == nil || ctx.Err() == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("unbounded cancellation: %v", err)
	}
}
