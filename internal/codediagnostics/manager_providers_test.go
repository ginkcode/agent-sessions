package codediagnostics

import (
	"testing"
)

func TestManagerIndependentProvidersAndAttachmentBudget(t *testing.T) {
	root, tools, _ := fixture(t)
	second := cloneTools(tools)
	second[0].ProviderID = "second-tool"
	f := &fakeChecker{tools, make(chan adapterCall, 4)}
	g := &fakeChecker{second, make(chan adapterCall, 4)}
	trust := NewTrust(nil)
	s := NewSupervisor(trust, SupervisorOptions{})
	m, err := NewManager("connection", trust, s, ManagerLimits{Attachments: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snap, err := m.Attach(root, 1, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}, {ProviderID: "second", Availability: "available", Adapter: g}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Attach(root, 1, nil)
	code(t, err, "busy")
	snap, err = m.Approve(snap.Context, snap.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	runs, err := m.Run(snap.Context, nil, false)
	if err != nil || len(runs) != 2 {
		t.Fatal(runs, err)
	}
	a, b := <-f.calls, <-g.calls
	a.result <- goodResult("shared")
	b.result <- goodResult("shared")
	m.workers.Wait()
	snap, _ = m.GetSnapshot(snap.Context)
	if len(snap.ResultSets) != 2 || CountFindings(snap).Errors != 1 {
		t.Fatal(snap)
	}
	a = startManager(t, m, snap, f, false)
	a.result <- goodResult()
	m.workers.Wait()
	snap, _ = m.GetSnapshot(snap.Context)
	if CountFindings(snap).Errors != 1 || len(snap.ResultSets[1].Diagnostics) != 1 {
		t.Fatal(snap)
	}
}

func TestManagerRejectsUnauthorizedCompletion(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{})
	a := startManager(t, m, s, f, false)
	m.mu.Lock()
	root := m.attachments[s.Context.AttachmentID].root
	m.mu.Unlock()
	// External trust invalidation is still caught at publication, even if it did
	// not originate in the manager's revoke hook.
	if err := m.trust.Revoke(root); err != nil {
		t.Fatal(err)
	}
	a.result <- goodResult("unauthorized")
	m.workers.Wait()
	s, _ = m.GetSnapshot(s.Context)
	if len(s.ResultSets) != 0 || s.Providers[0].Phase != "failed" {
		t.Fatal(s)
	}
}

func TestManagerEncodedSnapshotBudget(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{SnapshotBytes: 100000, FindingBytes: 90000})
	a := startManager(t, m, s, f, false)
	r := goodResult()
	for range 100 {
		r.Diagnostics = append(r.Diagnostics, goodResult(string(makeEscapedMessage())).Diagnostics[0])
	}
	a.result <- r
	m.workers.Wait()
	s, _ = m.GetSnapshot(s.Context)
	if encodedSize(s) > 100000 || !s.Providers[0].Truncated || len(s.ResultSets[0].Diagnostics) == 0 {
		t.Fatal("encoded snapshot limit")
	}
}

func makeEscapedMessage() []byte {
	b := make([]byte, 8000)
	for i := range b {
		b[i] = '<'
	}
	return b
}
