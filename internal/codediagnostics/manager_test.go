package codediagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type adapterCall struct {
	result chan CheckerResult
	ctx    context.Context
	id     string
}
type fakeChecker struct {
	tools []ToolInput
	calls chan adapterCall
}

func (f *fakeChecker) Tools() []ToolInput { return cloneTools(f.tools) }
func (f *fakeChecker) RunApproved(ctx context.Context, _ *Supervisor, id string, _ []ToolInput) CheckerResult {
	c := adapterCall{make(chan CheckerResult, 1), ctx, id}
	f.calls <- c
	// Deliberately ignore cancellation to exercise the manager's late-result guard.
	return <-c.result
}

func managerFixture(t *testing.T, l ManagerLimits) (*Manager, Snapshot, *fakeChecker) {
	t.Helper()
	root, tools, _ := fixture(t)
	trust := NewTrust(nil)
	s := NewSupervisor(trust, SupervisorOptions{})
	m, err := NewManager("connection", trust, s, l)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeChecker{tools, make(chan adapterCall, 32)}
	snap, err := m.Attach(root, 1, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}, {ProviderID: "missing", Availability: "missingTool", Adapter: nil}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = m.Approve(snap.Context, snap.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, snap, f
}

func goodResult(messages ...string) CheckerResult {
	r := CheckerResult{Replace: true, Phase: "idle", Availability: "available", Freshness: "uncertain", Roots: []string{""}}
	for _, msg := range messages {
		r.Diagnostics = append(r.Diagnostics, Diagnostic{Path: "source.go", Severity: "error", Message: msg, Source: "test", Freshness: "uncertain", Basis: SavedBasis{Kind: "saved"}})
	}
	return r
}

func startManager(t *testing.T, m *Manager, s Snapshot, f *fakeChecker, supersede bool) adapterCall {
	t.Helper()
	runs, err := m.Run(s.Context, []string{"checker"}, supersede)
	if err != nil || len(runs) != 1 {
		t.Fatalf("run: %v %v", runs, err)
	}
	select {
	case c := <-f.calls:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("adapter not started")
	}
	return adapterCall{}
}

func awaitSnapshot(t *testing.T, m *Manager, c Context, condition func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := m.GetSnapshot(c)
		if err != nil {
			t.Fatal(err)
		}
		if condition(s) {
			return s
		}
		runtime.Gosched()
	}
	t.Fatal("snapshot transition did not commit")
	return Snapshot{}
}
func finished(s Snapshot) bool { return s.Providers[0].Phase != "checking" }
func TestManagerReplacementStaleAndCopies(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{})
	c := startManager(t, m, s, f, false)
	c.result <- goodResult("finding")
	s = awaitSnapshot(t, m, s.Context, finished)
	if CountFindings(s).Errors != 1 || s.Providers[1].Availability != "missingTool" {
		t.Fatal(s)
	}
	s.ResultSets[0].Diagnostics[0].Message = "mutated"
	s.Providers[0].Coverage.Roots[0] = "mutated"
	fresh, _ := m.GetSnapshot(s.Context)
	if fresh.ResultSets[0].Diagnostics[0].Message != "finding" || fresh.Providers[0].Coverage.Roots[0] != "" {
		t.Fatal("snapshot alias")
	}
	c = startManager(t, m, s, f, false)
	c.result <- CheckerResult{Phase: "failed", Diagnostics: goodResult("must not commit").Diagnostics}
	s = awaitSnapshot(t, m, s.Context, finished)
	if s.ResultSets[0].Diagnostics[0].Message != "finding" || s.ResultSets[0].Diagnostics[0].Freshness != "stale" {
		t.Fatal(s)
	}
	c = startManager(t, m, s, f, false)
	if err := m.Cancel(s.Context, []RunIdentity{{"checker", c.id}}); err != nil {
		t.Fatal(err)
	}
	c.result <- goodResult("late")
	s, _ = m.GetSnapshot(s.Context)
	if s.Providers[0].Phase != "canceled" || len(s.ResultSets[0].Diagnostics) != 1 {
		t.Fatal(s)
	}
	c = startManager(t, m, s, f, false)
	c.result <- goodResult()
	s = awaitSnapshot(t, m, s.Context, finished)
	if len(s.ResultSets) != 1 || s.ResultSets[0].Diagnostics == nil || len(s.ResultSets[0].Diagnostics) != 0 {
		t.Fatal("empty success did not replace", s)
	}
}

func TestManagerSupersedeCancelDeleteRaces(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{})
	a := startManager(t, m, s, f, false)
	runs, err := m.Run(s.Context, []string{"checker"}, false)
	if err != nil || runs[0].RunID != a.id {
		t.Fatal("not coalesced", err)
	}
	b := startManager(t, m, s, f, true)
	code(t, m.Cancel(s.Context, []RunIdentity{{"checker", a.id}}), "staleContext")
	b.result <- goodResult("new")
	s = awaitSnapshot(t, m, s.Context, finished)
	a.result <- goodResult("old")
	// Join all adapter completions to prove the late commit actually ran.
	m.workers.Wait()
	s, _ = m.GetSnapshot(s.Context)
	if s.ResultSets[0].Diagnostics[0].Message != "new" {
		t.Fatal(s)
	}
	a = startManager(t, m, s, f, false)
	if err = m.DeletePath(s.Context, "source.go"); err != nil {
		t.Fatal(err)
	}
	a.result <- goodResult("deleted")
	m.workers.Wait()
	s, _ = m.GetSnapshot(s.Context)
	if len(s.ResultSets[0].Diagnostics) != 0 {
		t.Fatal("delete resurrected")
	}
}

func TestManagerRevokeDetachShutdown(t *testing.T) {
	for _, mode := range []string{"revoke", "detach", "invalidate", "close"} {
		t.Run(mode, func(t *testing.T) {
			m, s, f := managerFixture(t, ManagerLimits{})
			a := startManager(t, m, s, f, false)
			old := s.Context
			switch mode {
			case "revoke":
				var err error
				s, err = m.Revoke(old)
				if err != nil || s.Context.Generation == old.Generation || s.Approval.State != "revoked" || len(s.ResultSets) != 0 {
					t.Fatal(s, err)
				}
			case "detach":
				if err := m.Detach(old); err != nil {
					t.Fatal(err)
				}
			case "invalidate":
				if err := m.Invalidate(old); err != nil {
					t.Fatal(err)
				}
			case "close":
				done := make(chan struct{})
				go func() { m.Close(); close(done) }()
				<-a.ctx.Done()
				a.result <- goodResult("late")
				<-done
			}
			if mode != "close" {
				a.result <- goodResult("late")
				m.workers.Wait()
			}
			_, err := m.GetSnapshot(old)
			if err == nil {
				t.Fatal("old epoch survived")
			}
			if mode == "revoke" {
				s, _ = m.GetSnapshot(s.Context)
				if len(s.ResultSets) != 0 {
					t.Fatal("revival")
				}
			}
			if mode == "detach" || mode == "invalidate" {
				next, err := m.Attach(mustRoot(t, f.tools[0].Executable), 1, []Registration{{ProviderID: "checker", Availability: "available", Adapter: f}})
				if err != nil || next.Context.AttachmentID == old.AttachmentID || len(next.ResultSets) != 0 {
					t.Fatal(next, err)
				}
			}
		})
	}
}

func mustRoot(t *testing.T, tool string) AuthorizedRoot {
	t.Helper() // synthetic fixture tool always lives immediately below its root
	dir := filepath.Dir(tool)
	root, err := AuthorizeRoot("local:test", dir, func(s string) bool { return s == dir })
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestManagerLimitsEventsAndIsolation(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{Findings: 8, FindingBytes: 32000, EventBytes: 16000})
	c := startManager(t, m, s, f, false)
	r := goodResult()
	for i := 19; i >= 0; i-- {
		r.Diagnostics = append(r.Diagnostics, goodResult(fmt.Sprintf("%02d%s", i, strings.Repeat("<\x00", 500))).Diagnostics[0])
	}
	c.result <- r
	s = awaitSnapshot(t, m, s.Context, finished)
	p := s.Providers[0]
	if !p.Truncated || !p.Coverage.Partial || len(s.ResultSets[0].Diagnostics) >= 8 || !strings.HasPrefix(s.ResultSets[0].Diagnostics[0].Message, "00") {
		t.Fatal("prefix/limit", p)
	}
	if encodedSize(s) > m.limits.SnapshotBytes {
		t.Fatal("snapshot oversized")
	}
	events, err := m.eventParts(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || len(events) > 32 {
		t.Fatal("not chunked", len(events))
	}
	count := 0
	for i, e := range events {
		if encodedSize(e)+1024 > m.limits.EventBytes || e.PartCount != len(events) || e.PartIndex != i || e.Sequence != s.Sequence {
			t.Fatal("bad envelope")
		}
		for _, set := range e.Snapshot.ResultSets {
			count += len(set.Diagnostics)
		}
	}
	if count != len(s.ResultSets[0].Diagnostics) {
		t.Fatal("lost/duplicate findings")
	}
	// Force overflow without unbounded callback queues.
	m.mu.Lock()
	a := m.attachments[s.Context.AttachmentID]
	for range 5 {
		m.commit(a)
	}
	m.mu.Unlock()
	updates, gap, err := m.DrainEvents(s.Context)
	if err != nil || !gap || len(updates) > 2 {
		t.Fatal("gap not signaled", gap, err)
	}
	if len(updates) > 0 {
		updates[0][0].Snapshot.Providers[0].Coverage.Roots[0] = "mutated"
	}
	fresh, _ := m.GetSnapshot(s.Context)
	if fresh.Providers[0].Coverage.Roots[0] != "" {
		t.Fatal("event alias")
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "null") {
		t.Fatal("null required array")
	}
}

func TestManagerPendingAndConcurrentSnapshots(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{Pending: 2, Attachments: 1})
	a := startManager(t, m, s, f, false)
	b := startManager(t, m, s, f, true)
	_, err := m.Run(s.Context, []string{"checker"}, true)
	code(t, err, "busy")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				_, _ = m.GetSnapshot(s.Context)
				_, _, _ = m.DrainEvents(s.Context)
			}
		}()
	}
	b.result <- goodResult("new")
	a.result <- goodResult("old")
	wg.Wait()
	m.workers.Wait()
	s, _ = m.GetSnapshot(s.Context)
	if CountFindings(s).Errors != 1 {
		t.Fatal(s)
	}
	m.mu.Lock()
	m.attachments[s.Context.AttachmentID].snapshot.Sequence = maxSequence
	m.commit(m.attachments[s.Context.AttachmentID])
	m.mu.Unlock()
	_, err = m.GetSnapshot(s.Context)
	code(t, err, "staleContext")
}

func TestManagerNormalizationAndDedup(t *testing.T) {
	m, s, f := managerFixture(t, ManagerLimits{})
	c := startManager(t, m, s, f, false)
	r := goodResult("same", "same", strings.Repeat("a", 9000))
	r.Diagnostics[0].Freshness = "current"
	r.Diagnostics[0].Range = &Range{Start: Position{-1, 0}}
	r.Diagnostics = append(r.Diagnostics, Diagnostic{Path: "../escape", Message: "secret"})
	r.Partial = true
	c.result <- r
	s = awaitSnapshot(t, m, s.Context, finished)
	if len(s.ResultSets[0].Diagnostics) != 3 || !s.Providers[0].Coverage.Partial {
		t.Fatal(s)
	}
	for _, d := range s.ResultSets[0].Diagnostics {
		if len(d.Message) > 8192 || d.Freshness == "current" {
			t.Fatal(d)
		}
	}
	// Equivalent findings from independent providers count once; attribution stays.
	s.ResultSets = append(s.ResultSets, ResultSet{ProviderID: "other", Diagnostics: goodResult("same").Diagnostics})
	if CountFindings(s).Errors != 2 {
		t.Fatal(CountFindings(s))
	}
}
