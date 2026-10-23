package engine

import (
	"context"
	"errors"
	"runtime"
	"testing"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	mt "github.com/ginkcode/agent-sessions/internal/managedtools"
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

	m := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir(), Data: t.TempDir()}), WithReviewedManagedGo(mt.ReviewedGo{}), WithFixtureMachineIdentity(""))
	defer func() { _ = m.Close() }()
	_, err = m.ProposeManagedTool(context.Background(), "gopls")
	requireUnsupported(t, err)
	if m.managedTools != nil {
		t.Fatal("managed owner created without identity")
	}

	if runtime.GOOS != "darwin" {
		d := NewEngine(WithCacheEnabled(false), WithRoots(paths.Roots{Config: t.TempDir()}))
		defer func() { _ = d.Close() }()
		_, err = d.CodeDiagnosticsHostID(context.Background())
		requireUnsupported(t, err)
	}
}
