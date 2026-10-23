package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/rpc"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// R14 App leg: App -> rpc.Client -> pipes -> rpc.Server -> real Engine with the
// explicitly injected live route. The launcher is fake and always fails, so the
// test proves the App's host/offline/capability guards and the trust gate across
// the real transport; framed push/clear behavior is covered in internal/rpc.
type remoteIdentity struct {
	*engine.Engine
	host string
}

func (b remoteIdentity) RemoteCodeDiagnosticsHostID(context.Context) (string, error) {
	return b.host, nil
}

func liveRouteApp(t *testing.T, live bool) (*App, *atomic.Int32, string, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"server", "a.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "app-route"}, CWD: dir, RepoRoot: dir}})
	host, _ := (cd.HostIdentity{Kind: "ssh", Authority: "verified synthetic key/account", Endpoint: "verified synthetic endpoint/install"}).ID()
	var launches atomic.Int32
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		state := cd.ProviderState{ProviderID: "gopls", Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: cd.Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}}
		cfg := cd.ServerConfig{ProviderID: "gopls", Executable: filepath.Join(root.Project().Root, "server"), AdapterPolicy: "fake-only"}
		return []cd.Registration{{ProviderID: "gopls", Availability: "available", Live: &cd.LiveRegistration{State: state, Server: cfg, Resolve: func(p string) (string, bool) { return "go", strings.HasSuffix(p, ".go") }}}}, nil
	}
	options := []engine.Option{engine.WithService(engine.NewService(catalog, nil)), engine.WithCacheEnabled(false), engine.WithRoots(paths.Roots{Config: t.TempDir()})}
	if !live {
		// Host identity only: no launcher, so LiveCodeDiagnosticsReady() is false.
		options = append(options, engine.WithInjectedLiveCodeDiagnostics(engine.LiveCodeDiagnosticsInjection{Host: host}))
	} else {
		options = append(options, engine.WithInjectedLiveCodeDiagnostics(engine.LiveCodeDiagnosticsInjection{Host: host, Discover: discover, Session: cd.SessionOptions{
			Launch: func(cd.Invocation) (cd.SessionProcess, cd.SessionStreams, error) {
				launches.Add(1)
				return nil, cd.SessionStreams{}, errors.New("synthetic launch failure")
			},
		}}))
	}
	eng := engine.NewEngine(options...)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	server, client := rpc.NewServer(remoteIdentity{eng, host}, inR, outW), rpc.NewClient(outR, inW)
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(context.Background()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(func() { cancel(); _ = client.Close(); _ = server.Close(); <-done; _ = eng.Close() })
	init, err := client.Initialize(ctx, rpc.InitializeRequest{})
	if err != nil || !init.Capabilities.CodeDiagnostics || init.Capabilities.LiveCodeDiagnostics != live {
		t.Fatalf("capabilities %+v %v", init.Capabilities, err)
	}
	a := &App{backend: &diagnosticsBackend{host: "local-id"}}
	a.conn = &connection{wantHost: "remote", state: ConnectionState{Phase: ConnConnected}, client: client}
	return a, &launches, dir, host
}

func TestLiveRouteAppThroughRealRPCAndEngine(t *testing.T) {
	a, launches, dir, host := liveRouteApp(t, true)
	if _, err := a.AttachCodeDiagnostics(dir, 1, "local-id"); err == nil {
		t.Fatal("wrong expected host dispatched")
	} else {
		checkDiagnosticsError(t, err, "staleContext")
	}
	att, err := a.AttachCodeDiagnostics(dir, 1, host)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d1", 1, "package a", "r", "local-id"); err == nil {
		t.Fatal("wrong host reached live backend")
	} else {
		checkDiagnosticsError(t, err, "staleContext")
	}
	var snap cd.Snapshot
	deadline := time.Now().Add(15 * time.Second)
	for snap.Proposal == nil && time.Now().Before(deadline) {
		if snap, err = a.GetCodeDiagnosticsSnapshot(att.Context, host); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if snap.Proposal == nil {
		t.Fatal("discovery did not finish")
	}
	_, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d1", 1, "package a", "r", host)
	checkDiagnosticsError(t, err, "approvalRequired")
	if launches.Load() != 0 {
		t.Fatal("launch before approval")
	}
	if _, err = a.ApproveCodeDiagnostics(att.Context, "session", snap.Proposal.Fingerprint, host); err != nil {
		t.Fatal(err)
	}
	if _, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d1", 1, "package a", "r", host); err == nil || launches.Load() != 1 {
		t.Fatal("approved open must reach the injected launcher", err, launches.Load())
	}
	snap, err = a.GetCodeDiagnosticsSnapshot(att.Context, host)
	if err != nil {
		t.Fatal(err)
	}
	failed := false
	for _, p := range snap.Providers {
		failed = failed || (p.ProviderID == "gopls" && p.Phase == "failed" && p.Failure != nil)
	}
	if !failed {
		t.Fatal("live failure not visible through App", snap.Providers)
	}
	// The session failed to start, so there is no document to ask about.
	_, err = a.NavigateCode(att.Context, "d1", 1, cd.NavigateDefinition, cd.Position{}, host)
	if err == nil {
		t.Fatal("navigation without a document")
	}
	a.conn.mu.Lock()
	a.conn.state.Phase = ConnDisconnected
	a.conn.mu.Unlock()
	_, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d2", 1, "x", "r", host)
	checkDiagnosticsError(t, err, "disconnected")
	if launches.Load() != 1 {
		t.Fatal("offline launched")
	}
}

func TestLiveRouteAppOldServerOmitsLive(t *testing.T) {
	a, launches, dir, host := liveRouteApp(t, false)
	att, err := a.AttachCodeDiagnostics(dir, 1, host)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.OpenCodeDiagnosticDocument(att.Context, "a.go", "d1", 1, "package a", "r", host)
	checkDiagnosticsError(t, err, "unsupported")
	_, err = a.NavigateCode(att.Context, "d1", 1, cd.NavigateDefinition, cd.Position{}, host)
	checkDiagnosticsError(t, err, "unsupported")
	_, err = a.ReadNavigationFile(att.Context, "/go/src/fmt/print.go", host)
	checkDiagnosticsError(t, err, "unsupported")
	if launches.Load() != 0 {
		t.Fatal("old capability launched")
	}
}
