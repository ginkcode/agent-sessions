package rpc

import (
	"context"
	"os"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestProbeFilesRPC(t *testing.T) {
	root := t.TempDir()
	p := writeRPCFile(t, root, "a", "one")
	c := startEditServer(t, root, paths.Roots{})
	ctx := context.Background()
	out, err := c.ProbeFiles(ctx, root, []string{"a", "gone"})
	if err != nil || len(out.Observations) != 2 || out.Observations[0].Revision != rpcRevision("one") || out.Observations[1].State != "absent" {
		t.Fatalf("%+v %v", out, err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	out, err = c.ProbeFiles(ctx, root, []string{"a"})
	if err != nil || out.Observations[0].State != "absent" {
		t.Fatalf("%+v %v", out, err)
	}
	writeRPCFile(t, root, "a", "two")
	out, err = c.ProbeFiles(ctx, root, []string{"a"})
	if err != nil || out.Observations[0].Revision != rpcRevision("two") {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := c.ProbeFiles(ctx, root, []string{"../escape"}); err == nil {
		t.Fatal("escape accepted")
	}
	if _, err := c.ProbeFiles(ctx, t.TempDir(), []string{"a"}); err == nil {
		t.Fatal("unknown catalog root accepted")
	}
	// No live capability was enabled by adding this read-only method.
	if c.Capabilities().LiveCodeDiagnostics {
		t.Fatal("live enabled")
	}
}
