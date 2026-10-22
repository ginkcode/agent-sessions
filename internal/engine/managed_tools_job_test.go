package engine

import (
	"context"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

func TestManagedJobSuccessNoPathOrExecutionGrant(t *testing.T) {
	e, f, o := fakeManagedEngine(t)
	e.roots = paths.Roots{Config: t.TempDir()}
	f.succeed = true
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "gopls"}
	if _, err = e.ManagedTool(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	o.pending.Expires = time.Now().Add(time.Minute)
	p.Action, p.Token = "install", "consent"
	job, err := e.ManagedTool(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	p.Action, p.JobID = "status", job.JobID
	deadline := time.Now().Add(time.Second)
	for {
		out, pollErr := e.ManagedTool(context.Background(), p)
		if pollErr != nil {
			t.Fatal(pollErr)
		}
		if out.State == "installed" {
			if out.Proposal != nil || out.Message != "" || e.LiveCodeDiagnosticsReady() {
				t.Fatalf("unexpected install payload: %+v", out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestManagedJobDisconnectJoinsAndDropsConsent(t *testing.T) {
	e, f, o := fakeManagedEngine(t)
	e.roots = paths.Roots{Config: t.TempDir()}
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "gopls"}
	if _, err = e.ManagedTool(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	o.pending.Expires = time.Now().Add(time.Minute)
	p.Action, p.Token = "install", "consent"
	if _, err = e.ManagedTool(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	<-f.started
	e.ResetCodeDiagnostics() // returns only after the job joined
	if o.ctx.Err() == nil || e.managedTools != nil {
		t.Fatal("owner survived disconnect")
	}
	if o.job.state.State != "canceled" {
		t.Fatalf("%+v", o.job.state)
	}
	// Stable host remains, but an old connection's token/job cannot be reused.
	if out, err := e.ManagedTool(context.Background(), p); err == nil && out.State == "installing" {
		t.Fatal("old token replayed after disconnect")
	}
}
