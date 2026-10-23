package engine

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
func (f *fakeManagedInstaller) Discover(tool string) (string, error) {
	switch tool {
	case "gopls":
		return "", errors.New("invalid receipt/binary")
	case "ruff":
		return "/backend/private/ruff", nil
	}
	return "", os.ErrNotExist
}
func (f *fakeManagedInstaller) Dirs() []string { return []string{"/backend/private"} }

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
	e := NewEngine(WithCacheEnabled(false), WithFixtureMachineIdentity(testMachineID))
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeManagedInstaller{started: make(chan struct{})}
	o := &managedToolsOwner{installer: f, ctx: ctx, cancel: cancel, binding: mt.Binding{HostID: "local:test", ConnectionID: "one"}}
	e.managedTools = o
	t.Cleanup(func() { _ = e.Close() })
	return e, f, o
}

// A proposal is metadata only: it never downloads, installs, writes app data
// or enables the live route.
func TestManagedNoImplicitProvisioningOrLive(t *testing.T) {
	data := t.TempDir()
	e := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Claude: t.TempDir(), Config: t.TempDir(), Data: data}), WithFixtureMachineIdentity(testMachineID))
	defer func() { _ = e.Close() }()
	if !mt.Proposable("ruff") {
		t.Fatal("catalog")
	}
	p, err := e.ProposeManagedTool(context.Background(), "ruff")
	if err != nil {
		if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
			t.Skip("managed tools platform gate")
		}
		t.Fatal(err)
	}
	if p.Tool != "ruff" || !strings.HasPrefix(p.Destination, data) || !strings.HasPrefix(p.Source, "https://") {
		t.Fatalf("%+v", p)
	}
	if entries, _ := os.ReadDir(data); len(entries) != 0 {
		t.Fatal("proposal wrote app data")
	}
	if e.LiveCodeDiagnosticsReady() || e.codeDiagnostics != nil {
		t.Fatal("implicit activation")
	}
}

// Without a host Go, a gopls proposal is a safe "unavailable" state with the
// next step, not a failure.
func TestManagedGoplsWithoutGoIsUnavailable(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("managed tools platform gate")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOPATH", "")
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/go/bin", "/usr/local/bin"} {
		if _, err := os.Stat(filepath.Join(d, "go")); err == nil {
			t.Skip("host has Go in a conventional directory")
		}
	}
	e := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Claude: t.TempDir(), Config: t.TempDir(), Data: t.TempDir()}), WithFixtureMachineIdentity(testMachineID))
	defer func() { _ = e.Close() }()
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.ManagedTool(context.Background(), ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "gopls"})
	if err != nil || out.State != "unavailable" || out.Tool != "gopls" || out.Proposal != nil || !strings.Contains(out.Message, "Go is not installed") || strings.HasPrefix(out.Message, "managed tools") {
		t.Fatalf("%+v %v", out, err)
	}
	for _, tool := range []string{"node", "../gopls", ""} {
		if _, err := e.ManagedTool(context.Background(), ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: tool}); err == nil {
			t.Fatal("unlisted tool proposed", tool)
		}
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

// Status reports each tool from validated installs only: an invalid install
// is "unavailable", never "available"; nothing is installed by asking.
func TestManagedStatusReportsEachTool(t *testing.T) {
	e, f, _ := fakeManagedEngine(t)
	e.roots = paths.Roots{Config: t.TempDir()}
	host, err := e.CodeDiagnosticsHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := e.ManagedTool(context.Background(), ManagedToolRequest{Action: "status", ExpectedHostID: host})
	want := map[string]string{"gopls": "unavailable", "ruff": "available", "vtsls": "missingTool", "pyright": "missingTool"}
	if err != nil || out.State != "idle" || !maps.Equal(out.Tools, want) || f.calls.Load() != 0 {
		t.Fatalf("%+v %v", out, err)
	}
	if dirs := e.managedDirs(); !slices.Equal(dirs, []string{"/backend/private"}) {
		t.Fatal(dirs)
	}
	if _, err := e.ManagedTool(context.Background(), ManagedToolRequest{Action: "status", ExpectedHostID: "local:other"}); err == nil {
		t.Fatal("stale host accepted")
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
