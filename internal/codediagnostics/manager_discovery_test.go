package codediagnostics

import "testing"

func TestManagerAsynchronousDiscoveryApproval(t *testing.T) {
	root, tools, _ := fixture(t)
	trust := NewTrust(nil)
	manager, err := NewManager("connection", trust, NewSupervisor(trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	s, err := manager.Attach(root, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Proposal != nil || len(s.Providers) != 0 {
		t.Fatal("initial discovery not pending")
	}
	f := &fakeChecker{tools: tools, calls: make(chan adapterCall, 1)}
	if err := manager.CompleteDiscovery(s.Context, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}}); err != nil {
		t.Fatal(err)
	}
	s, err = manager.GetSnapshot(s.Context)
	if err != nil {
		t.Fatal(err)
	}
	if s.Proposal == nil || s.Providers[0].Availability != "approvalRequired" {
		t.Fatal("missing proposal")
	}
	if _, err := manager.Run(s.Context, nil, false); err == nil {
		t.Fatal("unapproved execution")
	}
	select {
	case <-f.calls:
		t.Fatal("adapter executed without approval")
	default:
	}
	if _, err := manager.Approve(s.Context, "wrong", "session"); err == nil {
		t.Fatal("stale approval accepted")
	}
	s, err = manager.Approve(s.Context, s.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.calls:
		t.Fatal("approval executed checker")
	default:
	}
	runs, err := manager.Run(s.Context, []string{"checker"}, false)
	if err != nil {
		t.Fatal(err)
	}
	call := <-f.calls
	coalesced, err := manager.Run(s.Context, []string{"checker"}, false)
	if err != nil || coalesced[0] != runs[0] {
		t.Fatal("retry did not coalesce")
	}
	revoked, err := manager.Revoke(s.Context)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.CompleteDiscovery(s.Context, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}}); err == nil {
		t.Fatal("old discovery revived revoke")
	}
	if err := manager.CompleteDiscovery(revoked.Context, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}}); err == nil {
		t.Fatal("revoked discovery revived proposal")
	}
	call.result <- goodResult("late")
}
