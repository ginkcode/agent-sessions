//go:build darwin || linux

package managedtools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyDiscoveryValidatesBinaryAndReceipt(t *testing.T) {
	i, data := fixture(t)
	if _, err := i.Discover("gopls"); !os.IsNotExist(err) {
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
	if got, err := i.Discover("gopls"); err != nil || got != binary {
		t.Fatalf("%s %v", got, err)
	}
	if dirs := i.Dirs(); len(dirs) != 1 || dirs[0] != filepath.Dir(binary) {
		t.Fatal(dirs)
	}
	for _, tool := range []string{"ruff", "vtsls", "pyright", "node"} {
		if _, err := i.Discover(tool); !os.IsNotExist(err) {
			t.Fatal(tool, err)
		}
	}
	if _, err := i.Discover("go"); err == nil {
		t.Fatal("unknown tool")
	}
	// Valid-looking metadata never substitutes for checking binary bytes.
	if err := os.WriteFile(binary, []byte("modified bytes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("gopls"); err == nil {
		t.Fatal("tampered binary accepted")
	}
	if err := os.WriteFile(binary, []byte("fake verified binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(filepath.Dir(binary), "receipt.json")
	if err := os.WriteFile(receipt, []byte(`{"Version":"`+p.Version+`","Policy":"go-module-sumdb-v1","SHA256":"forged"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("gopls"); err == nil {
		t.Fatal("forged receipt accepted")
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(binary, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("gopls"); err == nil {
		t.Fatal("receipt symlink accepted")
	}
	if len(i.Dirs()) != 0 {
		t.Fatal("invalid install searched")
	}
}

func TestPrerequisiteRemainsActionableAfterConsent(t *testing.T) {
	i, _ := fixture(t)
	i.build = func(context.Context, string, TrustedGo, goplsRelease) error { return ErrPrerequisite }
	p := propose(t, i)
	if _, err := i.Install(context.Background(), i.binding, p.Token); !errors.Is(err, ErrPrerequisite) {
		t.Fatalf("%v", err)
	}
	if _, err := i.Install(context.Background(), i.binding, p.Token); err == nil {
		t.Fatal("reused consent")
	}
}

func TestHostGoReadsMetadataOnly(t *testing.T) {
	// Reads the installed compiler entrypoint only. Never runs Go.
	root, err := hostGoRoot()
	if err != nil {
		t.Fatal(err)
	}
	g, v, err := hostGo(root)
	if err != nil || g.Path != filepath.Join(root, "bin", "go") || !strings.HasPrefix(v, "go1.") {
		t.Fatal(g, v, err)
	}
	if sum, _ := fileDigest(g.Path); sum != g.SHA256 {
		t.Fatal("unpinned executable")
	}
	if _, _, err := hostGo(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, ErrNoGo) {
		t.Fatal(err)
	}
}
