package app

import (
	"context"
	"errors"
	"testing"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

type diagnosticsBackend struct {
	engine.Backend
	host   string
	calls  int
	attach func() (engine.CodeDiagnosticsAttachment, error)
}

func (b *diagnosticsBackend) CodeDiagnosticsHostID(context.Context) (string, error) {
	return b.host, nil
}

func (b *diagnosticsBackend) AttachCodeDiagnostics(context.Context, string, int) (engine.CodeDiagnosticsAttachment, error) {
	b.calls++
	if b.attach != nil {
		return b.attach()
	}
	return engine.CodeDiagnosticsAttachment{}, nil
}

func (b *diagnosticsBackend) GetCodeDiagnosticsSnapshot(context.Context, cd.Context) (cd.Snapshot, error) {
	b.calls++
	return cd.Snapshot{}, nil
}

func (b *diagnosticsBackend) ApproveCodeDiagnostics(context.Context, cd.Context, string, string) (cd.Snapshot, error) {
	b.calls++
	return cd.Snapshot{}, nil
}

func (b *diagnosticsBackend) RevokeCodeDiagnostics(context.Context, cd.Context) (cd.Snapshot, error) {
	b.calls++
	return cd.Snapshot{}, nil
}

func (b *diagnosticsBackend) RunCodeDiagnostics(context.Context, cd.Context, []string) (engine.CodeDiagnosticsRuns, error) {
	b.calls++
	return engine.CodeDiagnosticsRuns{}, nil
}

func (b *diagnosticsBackend) CancelCodeDiagnostics(context.Context, cd.Context, []cd.RunIdentity) (engine.CodeDiagnosticsAck, error) {
	b.calls++
	return engine.CodeDiagnosticsAck{}, nil
}

func (b *diagnosticsBackend) DetachCodeDiagnostics(context.Context, cd.Context) (engine.CodeDiagnosticsAck, error) {
	b.calls++
	return engine.CodeDiagnosticsAck{}, nil
}

func checkDiagnosticsError(t *testing.T, err error, code string) {
	t.Helper()
	var failure *cd.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("want %s: %v", code, err)
	}
}

func TestCodeDiagnosticsRouting(t *testing.T) {
	local := &diagnosticsBackend{host: "local-id"}
	a := &App{backend: local}
	_, err := a.AttachCodeDiagnostics("/project", 1, "other-id")
	checkDiagnosticsError(t, err, "staleContext")
	if local.calls != 0 {
		t.Fatal("wrong host dispatched")
	}
	_, err = a.AttachCodeDiagnostics("/project", 1, "local-id")
	if err != nil || local.calls != 1 {
		t.Fatalf("local routing: %v", err)
	}
	a.conn = &connection{wantHost: "remote", localActive: false, state: ConnectionState{Phase: ConnDisconnected}}
	calls := []func() error{
		func() error { _, err := a.AttachCodeDiagnostics("/project", 1, "local-id"); return err },
		func() error { _, err := a.GetCodeDiagnosticsSnapshot(cd.Context{}, "local-id"); return err },
		func() error {
			_, err := a.ApproveCodeDiagnostics(cd.Context{}, "session", "fp", "local-id")
			return err
		},
		func() error { _, err := a.RevokeCodeDiagnostics(cd.Context{}, "local-id"); return err },
		func() error { _, err := a.RunCodeDiagnostics(cd.Context{}, nil, "local-id"); return err },
		func() error { _, err := a.CancelCodeDiagnostics(cd.Context{}, nil, "local-id"); return err },
		func() error { _, err := a.DetachCodeDiagnostics(cd.Context{}, "local-id"); return err },
	}
	for _, call := range calls {
		checkDiagnosticsError(t, call(), "disconnected")
	}
	if local.calls != 1 {
		t.Fatal("offline fell back to local")
	}
	remote := &diagnosticsBackend{host: "remote-id"}
	a.conn.client = remote
	a.conn.state.Phase = ConnConnected
	_, err = a.AttachCodeDiagnostics("/project", 1, "remote-id")
	if err != nil || remote.calls != 1 {
		t.Fatalf("remote route: %v", err)
	}
	remote.attach = func() (engine.CodeDiagnosticsAttachment, error) {
		a.conn.mu.Lock()
		a.conn.gen++
		a.conn.mu.Unlock()
		return engine.CodeDiagnosticsAttachment{}, nil
	}
	_, err = a.AttachCodeDiagnostics("/project", 1, "remote-id")
	checkDiagnosticsError(t, err, "staleContext")
	if remote.calls != 3 {
		t.Fatal("stale attach was not detached")
	}
}

type liveDiagnosticsBackend struct {
	diagnosticsBackend
	live int
}

func (b *liveDiagnosticsBackend) OpenCodeDiagnosticDocument(context.Context, cd.Context, string, string, int64, string, string) (engine.CodeDiagnosticsDocument, error) {
	b.live++
	return engine.CodeDiagnosticsDocument{DocumentID: "d", Version: 1}, nil
}
func (b *liveDiagnosticsBackend) ChangeCodeDiagnosticDocument(context.Context, cd.Context, string, int64, string) (engine.CodeDiagnosticsDocument, error) {
	b.live++
	return engine.CodeDiagnosticsDocument{}, nil
}
func (b *liveDiagnosticsBackend) SaveCodeDiagnosticDocument(context.Context, cd.Context, string, int64, string) (engine.CodeDiagnosticsDocument, error) {
	b.live++
	return engine.CodeDiagnosticsDocument{}, nil
}
func (b *liveDiagnosticsBackend) CloseCodeDiagnosticDocument(context.Context, cd.Context, string) (engine.CodeDiagnosticsDocument, error) {
	b.live++
	return engine.CodeDiagnosticsDocument{}, nil
}

func TestLiveCodeDiagnosticsRouting(t *testing.T) {
	c := cd.Context{}
	live := &liveDiagnosticsBackend{diagnosticsBackend: diagnosticsBackend{host: "local-id"}}
	a := &App{backend: live}
	if _, err := a.OpenCodeDiagnosticDocument(c, "a.go", "d", 1, "x", "", "other-id"); err == nil {
		t.Fatal("wrong host accepted")
	} else {
		checkDiagnosticsError(t, err, "staleContext")
	}
	if live.live != 0 {
		t.Fatal("wrong host dispatched")
	}
	if out, err := a.OpenCodeDiagnosticDocument(c, "a.go", "d", 1, "x", "", "local-id"); err != nil || out.DocumentID != "d" || live.live != 1 {
		t.Fatal(out, err)
	}
	// A backend with only the Stage 1 surface (old remote) is unsupported.
	old := &diagnosticsBackend{host: "local-id"}
	a = &App{backend: old}
	for _, call := range []func() error{
		func() error { _, e := a.OpenCodeDiagnosticDocument(c, "a.go", "d", 1, "x", "", "local-id"); return e },
		func() error { _, e := a.ChangeCodeDiagnosticDocument(c, "d", 2, "x", "local-id"); return e },
		func() error { _, e := a.SaveCodeDiagnosticDocument(c, "d", 1, "r", "local-id"); return e },
		func() error { _, e := a.CloseCodeDiagnosticDocument(c, "d", "local-id"); return e },
		func() error {
			_, e := a.NavigateCode(c, "d", 1, cd.NavigateHover, cd.Position{}, "local-id")
			return e
		},
	} {
		checkDiagnosticsError(t, call(), "unsupported")
	}
	if old.calls != 0 {
		t.Fatal("unsupported call dispatched")
	}
	// Offline never falls back to the local backend.
	a = &App{backend: live}
	a.conn = &connection{wantHost: "remote", localActive: false, state: ConnectionState{Phase: ConnDisconnected}}
	before := live.live
	for _, call := range []func() error{
		func() error { _, e := a.OpenCodeDiagnosticDocument(c, "a.go", "d", 1, "x", "", "local-id"); return e },
		func() error { _, e := a.ChangeCodeDiagnosticDocument(c, "d", 2, "x", "local-id"); return e },
		func() error { _, e := a.SaveCodeDiagnosticDocument(c, "d", 1, "r", "local-id"); return e },
		func() error { _, e := a.CloseCodeDiagnosticDocument(c, "d", "local-id"); return e },
		func() error {
			_, e := a.NavigateCode(c, "d", 1, cd.NavigateHover, cd.Position{}, "local-id")
			return e
		},
	} {
		checkDiagnosticsError(t, call(), "disconnected")
	}
	if live.live != before {
		t.Fatal("offline fell back to local")
	}
}
