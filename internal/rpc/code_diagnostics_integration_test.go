package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
)

type stage1Reply struct {
	result cd.CheckerResult
}
type stage1Checker struct {
	tools   []cd.ToolInput
	replies chan stage1Reply
	started chan struct{}
}

func (f *stage1Checker) Tools() []cd.ToolInput { return f.tools }
func (f *stage1Checker) RunApproved(ctx context.Context, _ *cd.Supervisor, _ string, _ []cd.ToolInput) cd.CheckerResult {
	close(f.started)
	select {
	case reply := <-f.replies:
		return reply.result
	case <-ctx.Done():
		return cd.CheckerResult{Phase: "canceled"}
	}
}

func TestStage1CheckerManagerPipeReplacement(t *testing.T) {
	b, _, client, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	path := filepath.Join(b.root.Project().Root, "unopened.go")
	if err := os.WriteFile(path, []byte("package fixture\r\n// 😀\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checker := &stage1Checker{tools: b.fake.tools, replies: make(chan stage1Reply, 1), started: make(chan struct{})}
	b.registrations = []cd.Registration{{ProviderID: "test", Availability: "available", Adapter: checker}, {ProviderID: "svelte", Availability: "missingTool"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	init, err := client.Initialize(ctx, InitializeRequest{})
	if err != nil || !init.Capabilities.CodeDiagnostics {
		t.Fatal(init, err)
	}
	a, err := client.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ApproveCodeDiagnostics(ctx, a.Context, "session", a.Snapshot.Proposal.Fingerprint); err != nil {
		t.Fatal(err)
	}
	finding := cd.Diagnostic{Path: "unopened.go", Severity: "error", Message: "<script> & 😀", Source: "fixture", Freshness: "uncertain", Basis: cd.SavedBasis{Kind: "saved"}}
	// Each snapshot round trip is a scheduling barrier; the deadline is only a
	// deadlock guard. No timing assumption determines which result is accepted.
	terminal := func(run string) cd.Snapshot {
		t.Helper()
		for {
			snap, e := client.GetCodeDiagnosticsSnapshot(ctx, a.Context)
			if e != nil {
				t.Fatal(e)
			}
			for _, p := range snap.Providers {
				if p.ProviderID == "test" && p.RunID == run && p.Phase != "running" && p.Phase != "queued" {
					return snap
				}
			}
		}
	}
	for _, phase := range []string{"findings", "failed", "empty"} {
		checker.started = make(chan struct{})
		result := cd.CheckerResult{Phase: "idle", Replace: true, Roots: []string{""}}
		if phase == "findings" {
			result.Diagnostics = []cd.Diagnostic{finding, {Path: "../escape", Message: "outside"}}
		}
		if phase == "failed" {
			result = cd.CheckerResult{Phase: "failed"}
		}
		checker.replies <- stage1Reply{result: result}
		runs, e := client.RunCodeDiagnostics(ctx, a.Context, []string{"test"})
		if e != nil {
			t.Fatal(e)
		}
		<-checker.started
		snap := terminal(runs.Runs[0].RunID)
		if len(snap.ResultSets) != 1 {
			t.Fatal(snap)
		}
		set := snap.ResultSets[0]
		if phase == "empty" {
			if len(set.Diagnostics) != 0 || set.RunID != runs.Runs[0].RunID {
				t.Fatal(snap)
			}
		} else if len(set.Diagnostics) != 1 || set.Diagnostics[0].Message != finding.Message {
			t.Fatal(snap)
		}
		if phase == "failed" && set.Diagnostics[0].Freshness != "stale" {
			t.Fatal(snap)
		}
		for _, p := range snap.Providers {
			if p.ProviderID == "svelte" && p.Availability != "missingTool" {
				t.Fatal(p)
			}
			if p.ProviderID == "test" && phase == "findings" && !p.Coverage.Partial {
				t.Fatal("escape not signaled", p)
			}
		}
	}
}

func TestStage1UnverifiedRemoteCannotAttachOrExecute(t *testing.T) {
	b, _, client, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	b.host = "" // production CD-09 identity acquisition is still blocked
	ctx := context.Background()
	init, err := client.Initialize(ctx, InitializeRequest{})
	if err != nil || init.Capabilities.CodeDiagnostics {
		t.Fatal(init, err)
	}
	if _, err = client.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1); err == nil {
		t.Fatal("remote identity bypass")
	}
	select {
	case <-b.fake.started:
		t.Fatal("unsupported remote executed")
	default:
	}
}
