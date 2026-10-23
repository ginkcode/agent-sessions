package codediagnostics

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFingerprintBoundsAndOrdering(t *testing.T) {
	r, tools, fp := fixture(t)
	second := tools[0]
	second.ProviderID = "another"
	a, err := Fingerprint(r, []ToolInput{tools[0], second})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Fingerprint(r, []ToolInput{second, tools[0]})
	if err != nil || a != b {
		t.Fatal("unstable tool ordering")
	}
	if _, err := Fingerprint(r, nil); err == nil {
		t.Fatal("empty tools accepted")
	}
	if _, err := Fingerprint(r, []ToolInput{tools[0], tools[0]}); err == nil {
		t.Fatal("duplicate providers accepted")
	}
	tools[0].Executable = filepath.Join(r.project.Root, "missing")
	if _, err := Fingerprint(r, tools); err == nil {
		t.Fatal("missing executable accepted")
	}
	tools[0].Executable = r.project.Root
	if _, err := Fingerprint(r, tools); err == nil {
		t.Fatal("directory executable accepted")
	}
	if validFingerprint("sha256-v1:bad") || !validFingerprint(fp) {
		t.Fatal("fingerprint syntax")
	}
}

func TestFailedPersistentRevokeStillCancels(t *testing.T) {
	r, tools, fp := fixture(t)
	s := store(t)
	tr := NewTrust(s)
	defer tr.Close()
	approve(t, tr, r, tools, fp, "persistent")
	l, err := tr.Preflight(r, tools)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	code(t, tr.Revoke(r), "approvalRequired")
	select {
	case <-l.Context.Done():
	default:
		t.Fatal("failed durable revoke left work alive")
	}
	code(t, l.Validate(r, tools), "staleContext")
	a, err := tr.Lookup(r, fp)
	if err != nil || a.State != "revoked" {
		t.Fatalf("revoke lost: %+v %v", a, err)
	}
}

func TestWindowsDeviceNames(t *testing.T) {
	for _, name := range []string{"CON", "con.txt", "Nul .log", "COM1", "lpt9.tar.gz", "COM¹", "conin$"} {
		if !windowsDevice(name) {
			t.Errorf("%q not reserved", name)
		}
	}
	for _, name := range []string{"CONFIG", "console.go", "COM10", "LPT", "aux_test.go", "nul-file"} {
		if windowsDevice(name) {
			t.Errorf("%q reserved", name)
		}
	}
}

func TestWindowsNativePaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows path semantics")
	}
	for _, bad := range []string{"NUL", "CON.txt", "folder/aux", "file.", "folder /file", "C:/file", "//server/share/file", "\\\\?\\C:\\file"} {
		if ValidatePath(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	r, _, _ := fixture(t)
	native := filepath.Join(r.project.Root, "source.go")
	uri := "file:///" + filepath.ToSlash(native)
	rel, err := r.ResolveURI(r.project.HostID, uri)
	if err != nil || rel != "source.go" {
		t.Fatalf("drive URI: %q %v", rel, err)
	}
	_, err = r.ResolveURI(r.project.HostID, "file:///Z:/other/file")
	if err == nil {
		t.Fatal("cross drive accepted")
	}
}
