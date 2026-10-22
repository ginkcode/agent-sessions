package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	mt "github.com/ginkcode/agent-sessions/internal/managedtools"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

type fakeManagedInstaller struct {
	calls   atomic.Int32
	started chan struct{}
	succeed bool
}

func (f *fakeManagedInstaller) Propose(tool string) (mt.Proposal, error) {
	return mt.Proposal{Tool: tool, Token: "consent"}, nil
}
func (f *fakeManagedInstaller) Dismiss() {}
func (f *fakeManagedInstaller) Discover() (string, error) {
	return "", errors.New("invalid receipt/binary")
}

func (f *fakeManagedInstaller) Install(ctx context.Context, _ mt.Binding, token string) (string, error) {
	if token != "consent" {
		return "", errors.New("stale consent")
	}
	f.calls.Add(1)
	if f.succeed {
		return "/backend/private/gopls", nil
	}
	close(f.started)
	<-ctx.Done()
	return "", ctx.Err()
}

func fakeManagedEngine(t *testing.T) (*Engine, *fakeManagedInstaller, *managedToolsOwner) {
	t.Helper()
	e := NewEngine(WithCacheEnabled(false))
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeManagedInstaller{started: make(chan struct{})}
	o := &managedToolsOwner{installer: f, ctx: ctx, cancel: cancel, binding: mt.Binding{HostID: "local:test", ConnectionID: "one"}}
	e.managedTools = o
	t.Cleanup(func() { _ = e.Close() })
	return e, f, o
}

func TestManagedNoImplicitProvisioningOrLive(t *testing.T) {
	e := NewEngine(WithCacheEnabled(false))
	defer func() { _ = e.Close() }()
	if _, err := e.ProposeManagedTool(context.Background(), "gopls"); !errors.Is(err, mt.ErrPrerequisite) {
		t.Fatalf("%v", err)
	}
	if e.LiveCodeDiagnosticsReady() || e.codeDiagnostics != nil || e.managedTools != nil {
		t.Fatal("implicit activation")
	}
}

func TestManagedConsentAndTeardown(t *testing.T) {
	for _, action := range []string{"disconnect", "revoke", "close"} {
		t.Run(action, func(t *testing.T) {
			e, f, o := fakeManagedEngine(t)
			if _, err := e.ProposeManagedTool(context.Background(), "gopls"); err != nil {
				t.Fatal(err)
			}
			if f.calls.Load() != 0 || e.codeDiagnostics != nil {
				t.Fatal("proposal executed or granted diagnostics")
			}
			if _, err := e.InstallManagedTool(context.Background(), "wrong"); err == nil {
				t.Fatal("no consent")
			}
			done := make(chan error, 1)
			go func() { _, err := e.InstallManagedTool(context.Background(), "consent"); done <- err }()
			<-f.started
			switch action {
			case "disconnect":
				e.ResetCodeDiagnostics()
			case "revoke":
				e.RevokeManagedTools()
			case "close":
				_ = e.Close()
			}
			if err := <-done; err == nil {
				t.Fatal("not canceled")
			}
			if o.ctx.Err() == nil || e.managedTools != nil || e.codeDiagnostics != nil {
				t.Fatal("owner survived")
			}
		})
	}
}

func TestManagedAvailabilityDoesNotTrustReceipt(t *testing.T) {
	e, f, _ := fakeManagedEngine(t)
	state, err := e.ManagedToolAvailability(context.Background())
	if err == nil || state != "unsupported" || f.calls.Load() != 0 {
		t.Fatalf("%s %v", state, err)
	}
}

func TestManagedSuccessInvalidatesExecutionOwnerWithoutStarting(t *testing.T) {
	e, f, _ := fakeManagedEngine(t)
	e.roots = paths.Roots{Config: t.TempDir()}
	before, err := e.diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.succeed = true
	if _, err := e.InstallManagedTool(context.Background(), "consent"); err != nil {
		t.Fatal(err)
	}
	if e.codeDiagnostics != nil || e.LiveCodeDiagnosticsReady() {
		t.Fatal("installation retained approvals or enabled live")
	}
	select {
	case <-before.done:
	default:
		t.Fatal("old execution owner not joined")
	}
}
