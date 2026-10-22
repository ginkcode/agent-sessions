package codediagnostics

import (
	"context"
	"testing"
)

type supervisedChecker struct {
	root    AuthorizedRoot
	tools   []ToolInput
	command Command
}

func (f *supervisedChecker) Tools() []ToolInput { return cloneTools(f.tools) }
func (f *supervisedChecker) RunApproved(ctx context.Context, s *Supervisor, id string, tools []ToolInput) CheckerResult {
	r, err := s.Submit(ctx, f.root, tools, f.command, id, false)
	if err != nil {
		return CheckerResult{Phase: "failed"}
	}
	o := r.Result()
	if o.Kind != OutcomeAccepted || r.ValidatePublication() != nil {
		return CheckerResult{Phase: "canceled"}
	}
	return goodResult("supervised")
}

func TestManagerSupervisorRevokeIntegration(t *testing.T) {
	trust := NewTrust(nil)
	root, tools, c := approvedCommand(t, trust)
	starts := make(chan started, 4)
	supervisor := NewSupervisor(trust, SupervisorOptions{Start: fakeFactory(starts)})
	m, err := NewManager("connection", trust, supervisor, ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	f := &supervisedChecker{root, tools, c}
	snap, err := m.Attach(root, 1, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Run(snap.Context, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = nextStart(t, starts)
	snap, err = m.Revoke(snap.Context)
	if err != nil {
		t.Fatal(err)
	}
	m.workers.Wait()
	snap, _ = m.GetSnapshot(snap.Context)
	if len(snap.ResultSets) != 0 || snap.Approval.State != "revoked" {
		t.Fatal(snap)
	}
	snap, err = m.Approve(snap.Context, snap.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Run(snap.Context, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	process := nextStart(t, starts)
	_ = process.process.Terminate()
	m.workers.Wait()
	snap, _ = m.GetSnapshot(snap.Context)
	if CountFindings(snap).Errors != 1 {
		t.Fatal(snap)
	}
}
