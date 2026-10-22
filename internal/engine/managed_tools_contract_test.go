package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestManagedContractScopeProgressCancel(t *testing.T) {
	e, f, o := fakeManagedEngine(t)
	e.roots = paths.Roots{Config: t.TempDir()}
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request := func(action, token, job string) (ManagedToolState, error) {
		return e.ManagedTool(context.Background(), ManagedToolRequest{Action: action, ExpectedHostID: host, Tool: "gopls", Token: token, JobID: job})
	}
	if _, err = e.ManagedTool(context.Background(), ManagedToolRequest{Action: "propose", ExpectedHostID: "wrong", Tool: "gopls"}); err == nil {
		t.Fatal("wrong host")
	}
	if _, err = e.ManagedTool(context.Background(), ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "https://evil"}); err == nil {
		t.Fatal("open catalog")
	}
	if _, err = request("install", "consent", ""); err == nil {
		t.Fatal("no proposal")
	}
	p, err := request("propose", "", "")
	if err != nil || p.State != "proposed" || f.calls.Load() != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	// Fake backend proposal expiry; production core supplies its own two-minute TTL.
	o.pending.Expires = time.Now().Add(time.Minute)
	if _, err = request("install", "wrong", ""); err == nil {
		t.Fatal("wrong token")
	}
	job, err := request("install", "consent", "")
	if err != nil || job.State != "installing" {
		t.Fatalf("%+v %v", job, err)
	}
	<-f.started
	if _, err = request("install", "consent", ""); err == nil {
		t.Fatal("replay")
	}
	if _, err = request("cancel", "", "wrong"); err == nil {
		t.Fatal("wrong job")
	}
	if _, err = request("cancel", "", job.JobID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		state, err := request("status", "", job.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if state.State == "canceled" {
			if len(state.Message) > 256 || strings.Contains(state.Message, "/") {
				t.Fatal("unsanitized")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel did not complete")
		}
		time.Sleep(time.Millisecond)
	}
	if f.calls.Load() != 1 {
		t.Fatal("replayed")
	}
	if _, err = e.ManagedTool(context.Background(), ManagedToolRequest{Action: "status", ExpectedHostID: host, Token: strings.Repeat("x", 257)}); err == nil {
		t.Fatal("unbounded")
	}
	e.RevokeManagedTools()
	if o.ctx.Err() == nil {
		t.Fatal("owner survived")
	}
}

func TestManagedContractUnavailable(t *testing.T) {
	e := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir()}))
	defer func() { _ = e.Close() }()
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.ManagedTool(context.Background(), ManagedToolRequest{Action: "status", ExpectedHostID: host})
	if err != nil || out.State != "unavailable" || !strings.Contains(out.Message, "explicitly review") || e.managedTools != nil || e.LiveCodeDiagnosticsReady() {
		t.Fatalf("%+v %v", out, err)
	}
}
