package managedtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadOnlyDiscoveryValidatesBinaryAndReceipt(t *testing.T) {
	i, data := fixture(t)
	if _, err := i.Discover(); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	entries, _ := os.ReadDir(data)
	if len(entries) != 0 {
		t.Fatal("read-only discovery wrote data")
	}
	p := propose(t, i)
	binary, err := i.Install(context.Background(), i.binding, p.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := i.Discover(); err != nil || got != binary {
		t.Fatalf("%s %v", got, err)
	}
	// Valid-looking metadata never substitutes for checking binary bytes.
	if err := os.WriteFile(binary, []byte("modified bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover(); err == nil {
		t.Fatal("tampered binary accepted")
	}
	if err := os.WriteFile(binary, []byte("fake verified binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(filepath.Dir(binary), "receipt.json")
	if err := os.WriteFile(receipt, []byte(`{"Version":"v0.24.0","Policy":"go-module-sumdb-v1","SHA256":"forged"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover(); err == nil {
		t.Fatal("forged receipt accepted")
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover(); err == nil {
		t.Fatal("receipt symlink accepted")
	}
}

func TestPrerequisiteRemainsActionableAfterConsent(t *testing.T) {
	i, _ := fixture(t)
	i.build = func(context.Context, string, TrustedGo) error { return ErrPrerequisite }
	p := propose(t, i)
	if _, err := i.Install(context.Background(), i.binding, p.Token); !errors.Is(err, ErrPrerequisite) {
		t.Fatalf("%v", err)
	}
	if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("reused consent")
	}
}

func TestReviewInstalledGoNoExecution(t *testing.T) {
	// Reads the already installed compiler entrypoint only. Never runs Go.
	// Go 1.26 on the development host is rejected before a proposal/upgrade.
	//nolint:staticcheck // Test intentionally reads this host's build tree; no discovery/probe.
	root, err := filepath.EvalSymlinks(runtime.GOROOT())
	if err != nil {
		t.Fatal(err)
	}
	reviewed, err := ReviewInstalledGo(root)
	if runtime.Version() == "go1.26.8" && !errors.Is(err, ErrPrerequisite) {
		t.Fatalf("old Go: %v", err)
	}
	if err == nil && reviewed.tool.Path != filepath.Join(root, "bin", "go") {
		t.Fatal("unbound executable")
	}
	if _, err := ReviewInstalledGo(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing tree")
	}
	if _, err := NewReviewed(t.TempDir(), Binding{"local:" + string(make([]byte, 64)), "one"}, ReviewedGo{}); err == nil {
		t.Fatal("zero review")
	}
}
