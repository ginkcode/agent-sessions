package rpc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/files"
)

type rpcChecker struct {
	tools   []cd.ToolInput
	started chan context.Context
}

func (f *rpcChecker) Tools() []cd.ToolInput { return f.tools }
func (f *rpcChecker) RunApproved(ctx context.Context, _ *cd.Supervisor, _ string, _ []cd.ToolInput) cd.CheckerResult {
	f.started <- ctx
	<-ctx.Done()
	return cd.CheckerResult{Phase: "canceled"}
}

type diagnosticsBackend struct {
	*mockBackend
	m             *cd.Manager
	root          cd.AuthorizedRoot
	fake          *rpcChecker
	host          string
	registrations []cd.Registration // optional Stage 1 integration adapters
	live          bool              // test-only: advertise liveCodeDiagnostics
	liveCalls     int
	navCalls      int
	tamper        func(*cd.NavigationResult) // test-only: change an answer on its way out
	tamperFile    func(*files.Content)
	fileReads     int
}

func (b *diagnosticsBackend) RemoteCodeDiagnosticsHostID(context.Context) (string, error) {
	return b.host, nil
}

func (b *diagnosticsBackend) AttachCodeDiagnostics(_ context.Context, dir string, v int) (engine.CodeDiagnosticsAttachment, error) {
	if dir != b.root.Project().Root {
		return engine.CodeDiagnosticsAttachment{}, cdError("unauthorized", "Unknown directory")
	}
	registrations := b.registrations
	if registrations == nil {
		registrations = []cd.Registration{{ProviderID: "test", Availability: "available", Adapter: b.fake}}
	}
	s, e := b.m.Attach(b.root, v, registrations)
	return engine.CodeDiagnosticsAttachment{Project: b.root.Project(), Context: s.Context, Snapshot: s}, e
}

func (b *diagnosticsBackend) DetachCodeDiagnostics(_ context.Context, c cd.Context) (engine.CodeDiagnosticsAck, error) {
	return engine.CodeDiagnosticsAck{Context: c}, b.m.Detach(c)
}

func (b *diagnosticsBackend) GetCodeDiagnosticsSnapshot(_ context.Context, c cd.Context) (cd.Snapshot, error) {
	return b.m.GetSnapshot(c)
}

func (b *diagnosticsBackend) ApproveCodeDiagnostics(_ context.Context, c cd.Context, mode, fp string) (cd.Snapshot, error) {
	return b.m.Approve(c, fp, mode)
}

func (b *diagnosticsBackend) RevokeCodeDiagnostics(_ context.Context, c cd.Context) (cd.Snapshot, error) {
	return b.m.Revoke(c)
}

func (b *diagnosticsBackend) RunCodeDiagnostics(_ context.Context, c cd.Context, ids []string) (engine.CodeDiagnosticsRuns, error) {
	r, e := b.m.Run(c, ids, false)
	return engine.CodeDiagnosticsRuns{Context: c, Runs: r}, e
}

func (b *diagnosticsBackend) CancelCodeDiagnostics(_ context.Context, c cd.Context, r []cd.RunIdentity) (engine.CodeDiagnosticsAck, error) {
	return engine.CodeDiagnosticsAck{Context: c}, b.m.Cancel(c, r)
}
func (b *diagnosticsBackend) ResetCodeDiagnostics() { b.m.Close() }
func rpcDiagnosticsFixture(t *testing.T) (*diagnosticsBackend, *Server, *Client, func()) {
	t.Helper()
	return rpcDiagnosticsTransportFixture(t, nil)
}

func rpcDiagnosticsTransportFixture(t *testing.T, wrap func(io.WriteCloser) io.WriteCloser) (*diagnosticsBackend, *Server, *Client, func()) {
	t.Helper()
	dir := t.TempDir()
	tool := filepath.Join(dir, "tool")
	if err := os.WriteFile(tool, []byte("non executable fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, _ := (cd.HostIdentity{Kind: "ssh", Authority: "verified synthetic key/account", Endpoint: "verified synthetic endpoint/install"}).ID()
	root, e := cd.AuthorizeRoot(host, dir, func(s string) bool { return s == dir })
	if e != nil {
		t.Fatal(e)
	}
	trust := cd.NewTrust(nil)
	m, e := cd.NewManager("owner", trust, cd.NewSupervisor(trust, cd.SupervisorOptions{}), cd.ManagerLimits{})
	if e != nil {
		t.Fatal(e)
	}
	b := &diagnosticsBackend{mockBackend: &mockBackend{}, m: m, root: root, host: host, fake: &rpcChecker{tools: []cd.ToolInput{{ProviderID: "test", Executable: tool, Policy: "fixed test"}}, started: make(chan context.Context, 8)}}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	var output io.WriteCloser = outW
	if wrap != nil {
		output = wrap(output)
	}
	s, c := NewServer(b, inR, output), NewClient(outR, inW)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(context.Background())
	}()
	return b, s, c, func() {
		_ = c.Close()
		_ = s.Close()
		<-done
		m.Close()
	}
}

func TestDiagnosticsRPCOwnershipApprovalAndExpiry(t *testing.T) {
	b, s, c, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	ctx := context.Background()
	init, e := c.Initialize(ctx, InitializeRequest{})
	if e != nil || !init.Capabilities.CodeDiagnostics || init.Capabilities.LiveCodeDiagnostics {
		t.Fatal(init, e)
	}
	if _, e = c.AttachCodeDiagnostics(ctx, "/unknown", 1); e == nil {
		t.Fatal("arbitrary root accepted")
	}
	a, e := c.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.RunCodeDiagnostics(ctx, a.Context, nil); e == nil || !strings.Contains(e.Error(), "approvalRequired") {
		t.Fatal(e)
	}
	foreign := a.Context
	foreign.ConnectionID = "other"
	// Bypass client guard to exercise actual dispatcher ownership.
	var out cd.Snapshot
	if e = c.call(ctx, "getCodeDiagnosticsSnapshot", codeDiagnosticsParams{Context: foreign}, &out); e == nil {
		t.Fatal("foreign token accepted")
	}
	approved, e := c.ApproveCodeDiagnostics(ctx, a.Context, "session", a.Snapshot.Proposal.Fingerprint)
	if e != nil || approved.Approval.State != "approved" {
		t.Fatal(approved, e)
	}
	if _, e = c.RunCodeDiagnostics(ctx, a.Context, []string{}); e == nil {
		t.Fatal("explicit empty provider list accepted")
	}
	runs, e := c.RunCodeDiagnostics(ctx, a.Context, nil)
	if e != nil {
		t.Fatal(e)
	}
	active := <-b.fake.started
	if _, e = c.CancelCodeDiagnostics(ctx, a.Context, runs.Runs); e != nil {
		t.Fatal(e)
	}
	select {
	case <-active.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop work")
	}
	if _, e = c.RunCodeDiagnostics(ctx, a.Context, nil); e != nil {
		t.Fatal(e)
	}
	active = <-b.fake.started
	revoked, e := c.RevokeCodeDiagnostics(ctx, a.Context)
	if e != nil || revoked.Context == a.Context {
		t.Fatal(revoked, e)
	}
	select {
	case <-active.Done():
	case <-time.After(time.Second):
		t.Fatal("revoke did not stop work")
	}
	if _, e = c.GetCodeDiagnosticsSnapshot(ctx, a.Context); e == nil {
		t.Fatal("old generation accepted")
	}
	if _, e = c.ApproveCodeDiagnostics(ctx, revoked.Context, "session", revoked.Proposal.Fingerprint); e != nil {
		t.Fatal(e)
	}
	if _, e = c.RunCodeDiagnostics(ctx, revoked.Context, nil); e != nil {
		t.Fatal(e)
	}
	active = <-b.fake.started
	_ = s.Close()
	select {
	case <-active.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not expire work")
	}
}

func TestDiagnosticsOldFlagsNoWrites(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	rec := &recordingWriter{}
	c := NewClient(r, rec)
	defer func() { _ = c.Close() }()
	ctx := context.Background()
	token := cd.Context{}
	calls := []func() error{
		func() error { _, e := c.AttachCodeDiagnostics(ctx, "/repo", 1); return e }, func() error { _, e := c.GetCodeDiagnosticsSnapshot(ctx, token); return e }, func() error { _, e := c.DetachCodeDiagnostics(ctx, token); return e }, func() error { _, e := c.ApproveCodeDiagnostics(ctx, token, "session", "fp"); return e }, func() error { _, e := c.RevokeCodeDiagnostics(ctx, token); return e }, func() error { _, e := c.RunCodeDiagnostics(ctx, token, nil); return e }, func() error { _, e := c.CancelCodeDiagnostics(ctx, token, nil); return e },
	}
	for _, call := range calls {
		if e := call(); e == nil {
			t.Fatal("unsupported succeeded")
		}
	}
	if rec.Len() != 0 {
		t.Fatal("unsupported wrote transport")
	}
	var caps Capabilities
	if e := json.Unmarshal([]byte(`{"search":true}`), &caps); e != nil || caps.CodeDiagnostics || caps.LiveCodeDiagnostics {
		t.Fatal(caps, e)
	}
}

func TestDiagnosticsFailClosedIdentity(t *testing.T) {
	b, _, c, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	b.host = "local:not-a-remote-identity"
	init, e := c.Initialize(context.Background(), InitializeRequest{})
	if e != nil || init.Capabilities.CodeDiagnostics || init.CodeDiagnosticsHostID != "" {
		t.Fatal(init, e)
	}
}

func (b *diagnosticsBackend) InvalidateCodeDiagnosticsPath(_ context.Context, c cd.Context, path string) (cd.Snapshot, error) {
	if err := b.m.DeletePath(c, path); err != nil {
		return cd.Snapshot{}, err
	}
	return b.m.GetSnapshot(c)
}

func TestInvalidateDiagnosticsWireOwnership(t *testing.T) {
	b, _, c, cleanup := rpcDiagnosticsFixture(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := c.Initialize(ctx, InitializeRequest{}); err != nil {
		t.Fatal(err)
	}
	a, err := c.AttachCodeDiagnostics(ctx, b.root.Project().Root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.InvalidateCodeDiagnosticsPath(ctx, a.Context, "source.go"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.InvalidateCodeDiagnosticsPath(ctx, a.Context, "../escape"); err == nil {
		t.Fatal("invalid path accepted")
	}
	foreign := a.Context
	foreign.ConnectionID = "other"
	var out cd.Snapshot
	if err = c.call(ctx, "invalidateCodeDiagnosticsPath", codeDiagnosticsParams{Context: foreign, Path: "source.go"}, &out); err == nil {
		t.Fatal("foreign owner accepted")
	}
}
