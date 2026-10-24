package claude_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
)

// Live metadata is a separate refresh channel: unchanged transcript scans must
// not start reparsing just because the active-session side file changed.
func TestScanBaselineActiveMetadata(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "projects", "synthetic")
	sessions := filepath.Join(root, "sessions")
	for _, dir := range []string{project, sessions} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "synthetic.jsonl"), []byte(`{"type":"user","uuid":"u1","timestamp":"2026-10-24T00:00:00Z","message":{"role":"user","content":"Synthetic prompt"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeLive := func(status string) {
		t.Helper()
		data := fmt.Sprintf(`{"pid":%d,"sessionId":"synthetic","status":%q,"updatedAt":1792800000000}`, os.Getpid(), status)
		if err := os.WriteFile(filepath.Join(sessions, "synthetic.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLive("working")
	p := claude.New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Changed) != 1 || !first.Changed[0].Live || first.Changed[0].LiveStatus != "working" {
		t.Fatalf("initial live overlay: %+v", first.Changed)
	}
	writeLive("idle")
	work := new(provider.ScanWork)
	next, err := p.Scan(provider.WithScanWork(context.Background(), work), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Changed) != 0 || work.SourceOpens.Load() != 0 || work.Records.Load() != 0 {
		t.Fatal("live update unexpectedly parsed unchanged transcript")
	}
	live, err := p.Live(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if live["synthetic"].Status != "idle" {
		t.Fatalf("live status not refreshed: %+v", live)
	}
	if err := os.Remove(filepath.Join(sessions, "synthetic.json")); err != nil {
		t.Fatal(err)
	}
	live, err = p.Live(context.Background())
	if err != nil || len(live) != 0 {
		t.Fatalf("live removal: %+v, %v", live, err)
	}
}
