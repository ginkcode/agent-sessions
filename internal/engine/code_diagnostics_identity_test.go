package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/paths"
)

// testMachineID is a fixed fixture machine identity: tests must not depend on
// the CI host's real machine identity (unavailable off darwin by design).
const testMachineID = "00112233445566778899aabbccddeeff"

func requireUnsupported(t *testing.T, err error) {
	t.Helper()
	var de *cd.Error
	if !errors.As(err, &de) || de.Code != "unsupported" {
		t.Fatalf("want unsupported, got %v", err)
	}
}

func TestDiagnosticsIdentityFixtureIsDeterministic(t *testing.T) {
	config := t.TempDir()
	fixed := func() (string, error) { return testMachineID, nil }
	first, err := localDiagnosticsIdentity(config, fixed)
	if err != nil || first == "" {
		t.Fatalf("%q %v", first, err)
	}
	again, err := localDiagnosticsIdentity(config, fixed)
	if err != nil || again != first {
		t.Fatalf("unstable identity %q != %q (%v)", again, first, err)
	}
	other, err := localDiagnosticsIdentity(config, func() (string, error) { return "ffeeddccbbaa99887766554433221100", nil })
	if err != nil || other == first {
		t.Fatalf("machine not bound into identity: %q %v", other, err)
	}
}

func TestDiagnosticsIdentityFailsClosedWithoutMachine(t *testing.T) {
	for name, source := range map[string]func() (string, error){
		"empty":  func() (string, error) { return "", nil },
		"blank":  func() (string, error) { return "  \t", nil },
		"failed": func() (string, error) { return "", errors.New("no identity") },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := localDiagnosticsIdentity(t.TempDir(), source)
			requireUnsupported(t, err)
		})
	}
	if _, err := localDiagnosticsIdentity("relative", func() (string, error) { return testMachineID, nil }); err == nil {
		t.Fatal("relative config accepted")
	}
}

// The Engine seam fails closed for an empty fixture ID, and the production
// default (no fixture) stays fail-closed on platforms without verified
// machine-identity acquisition.
func TestEngineDiagnosticsIdentityFailsClosed(t *testing.T) {
	e := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir()}), WithFixtureMachineIdentity(""))
	defer func() { _ = e.Close() }()
	_, err := e.CodeDiagnosticsHostID(context.Background())
	requireUnsupported(t, err)
	if e.codeDiagnostics != nil {
		t.Fatal("owner created without identity")
	}

	m := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir(), Data: t.TempDir()}), WithFixtureMachineIdentity(""))
	defer func() { _ = m.Close() }()
	_, err = m.ProposeManagedTool(context.Background(), "gopls")
	requireUnsupported(t, err)
	if m.managedTools != nil {
		t.Fatal("managed owner created without identity")
	}

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		d := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir()}))
		defer func() { _ = d.Close() }()
		_, err = d.CodeDiagnosticsHostID(context.Background())
		requireUnsupported(t, err)
	}
}

// Only a serve Engine launched for an SSH or WSL connection advertises a
// remote identity, computed on that host with the connection's kind.
func TestRemoteDiagnosticsIdentity(t *testing.T) {
	config := t.TempDir()
	ids := map[string]string{}
	for _, kind := range []string{"", "local", "other", "ssh", "wsl"} {
		e := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: config}), WithFixtureMachineIdentity(testMachineID), WithRemoteCodeDiagnostics(kind))
		id, err := e.RemoteCodeDiagnosticsHostID(context.Background())
		_ = e.Close()
		if kind != "ssh" && kind != "wsl" {
			requireUnsupported(t, err)
			if e.LiveCodeDiagnosticsReady() {
				t.Fatal("remote route without a remote kind", kind)
			}
			continue
		}
		if err != nil || !strings.HasPrefix(id, kind+":") {
			t.Fatal(kind, id, err)
		}
		ids[kind] = strings.TrimPrefix(id, kind+":")
	}
	local := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: config}), WithFixtureMachineIdentity(testMachineID))
	defer func() { _ = local.Close() }()
	id, err := local.CodeDiagnosticsHostID(context.Background())
	if err != nil || !strings.HasPrefix(id, "local:") || ids["ssh"] == ids["wsl"] || ids["ssh"] == strings.TrimPrefix(id, "local:") {
		t.Fatal("identity kinds not distinct", id, ids, err)
	}
}
