package codediagnostics

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func code(t *testing.T, err error, want string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func fixture(t *testing.T) (AuthorizedRoot, []ToolInput, string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"tool", "config", "source.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("synthetic "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := AuthorizeRoot("local:test", dir, func(s string) bool { return s == dir })
	if err != nil {
		t.Fatal(err)
	}
	tools := []ToolInput{{ProviderID: "test", Executable: filepath.Join(dir, "tool"), ConfigPaths: []string{"config"}, Policy: "adapter-v1 fixed argv and env"}}
	fp, err := Fingerprint(root, tools)
	if err != nil {
		t.Fatal(err)
	}
	return root, tools, fp
}

func store(t *testing.T) *GrantStore {
	t.Helper()
	s, err := NewGrantStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func approve(t *testing.T, tr *Trust, r AuthorizedRoot, tools []ToolInput, fp, mode string) {
	t.Helper()
	a, err := tr.Approve(r, tools, fp, mode)
	if err != nil || a.State != "approved" || a.Mode != mode {
		t.Fatalf("approval: %+v %v", a, err)
	}
}

func TestHostIdentity(t *testing.T) {
	base := HostIdentity{"ssh", "verified-server-key/account", "resolved-endpoint:22"}
	id, err := base.ID()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := base.ID()
	if id != again {
		t.Fatal("reconnect changed host")
	}
	for _, h := range []HostIdentity{{"ssh", "new-key/account", base.Endpoint}, {"ssh", base.Authority, "changed-endpoint:22"}, {"local", base.Authority, base.Endpoint}, {"wsl", base.Authority, "distribution-install-id"}} {
		other, _ := h.ID()
		if other == id {
			t.Fatal("host collision")
		}
	}
	for _, h := range []HostIdentity{{}, {"display-label", "a", "b"}, {"ssh", "a\n", "b"}, {"ssh", "a", ""}} {
		_, err := h.ID()
		code(t, err, "invalidArgument")
	}
}

func TestAuthorizationAndPaths(t *testing.T) {
	r, _, _ := fixture(t)
	called := false
	_, err := AuthorizeRoot("host", filepath.Join(r.project.Root, "missing"), func(string) bool { called = true; return false })
	code(t, err, "unauthorized")
	if !called {
		t.Fatal("no catalog check")
	}
	_, err = AuthorizeRoot("host", filepath.Join(r.project.Root, "missing"), func(string) bool { return true })
	code(t, err, "notFound")
	_, err = AuthorizeRoot("host", r.project.Root+"/../else", func(string) bool { t.Fatal("traversal reached catalog"); return true })
	code(t, err, "invalidArgument")
	nested := filepath.Join(r.project.Root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	nr, err := AuthorizeRoot("host", nested, func(string) bool { return true })
	if err != nil || nr.project.Root != nested {
		t.Fatalf("nested widened: %v %v", nr, err)
	}
	for _, bad := range []string{"../source.go", "a/../source.go", "/source.go", "a//b", "a/./b", "C:/x", "C:x", "\\\\server\\x", "a\\b", "a\x00b", "a\nb", ""} {
		if err := ValidatePath(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := r.ResolvePath("other-host", "source.go"); err == nil {
		t.Fatal("foreign host accepted")
	}
	target, err := r.ResolvePath(r.project.HostID, "source.go")
	if err != nil || target != filepath.Join(r.project.Root, "source.go") {
		t.Fatalf("resolve %s %v", target, err)
	}
	raw := (&url.URL{Scheme: "file", Path: filepath.ToSlash(target)}).String()
	if runtime.GOOS != "windows" {
		rel, err := r.ResolveURI(r.project.HostID, raw)
		if err != nil || rel != "source.go" {
			t.Fatalf("uri %s %v", rel, err)
		}
	}
	for _, bad := range []string{"https://example.org/a", "file://evil/a", "file://localhost/a", "file:///../etc/passwd", "file:///a%2f..%2fb", "file:///a?x", "file:///a#x", "file:a", "file:///%00", "file:///%ZZ", "file:///C:/x", "file:////server/share"} {
		if _, err := r.ResolveURI(r.project.HostID, bad); err == nil {
			t.Errorf("accepted URI %q", bad)
		}
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("not diagnostics"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.project.Root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = r.ResolvePath(r.project.HostID, "escape/secret")
	code(t, err, "unauthorized")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(r.project.Root, alias); err != nil {
		t.Fatal(err)
	}
	ar, err := AuthorizeRoot(r.project.HostID, alias, func(s string) bool { return s == alias })
	if err != nil || ar.project != r.project {
		t.Fatalf("alias %v %v", ar, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	code(t, ar.Revalidate(), "approvalChanged")
}

func TestSessionPersistentAndDenial(t *testing.T) {
	r, tools, fp := fixture(t)
	s := store(t)
	tr := NewTrust(s)
	defer tr.Close()
	tr.Deny()
	a, err := tr.Lookup(r, fp)
	if err != nil || a.State != "required" {
		t.Fatalf("browsing granted execution: %+v %v", a, err)
	}
	approve(t, tr, r, tools, fp, "session")
	fresh := NewTrust(s)
	defer fresh.Close()
	a, err = fresh.Lookup(r, fp)
	if err != nil || a.State != "required" {
		t.Fatal("session persisted")
	}
	tr.Close()
	_, err = tr.Lookup(r, fp)
	code(t, err, "disconnected")
	approve(t, fresh, r, tools, fp, "persistent")
	reload := NewTrust(&GrantStore{path: s.path})
	defer reload.Close()
	a, err = reload.Lookup(r, fp)
	if err != nil || a.Mode != "persistent" {
		t.Fatalf("reload: %+v %v", a, err)
	}
	other, err := AuthorizeRoot("other-host", r.project.Root, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	a, err = reload.Lookup(other, fp)
	if err != nil || a.State != "required" {
		t.Fatal("grant crossed host")
	}
	if err := fresh.Revoke(r); err != nil {
		t.Fatal(err)
	}
	a, err = reload.Lookup(r, fp)
	if err != nil || a.State != "required" {
		t.Fatal("revoke not durable")
	}
	a, err = fresh.Lookup(r, fp)
	if err != nil || a.State != "revoked" {
		t.Fatal("missing revoked state")
	}
}

func TestFingerprintChangesAndNoProbe(t *testing.T) {
	r, tools, fp := fixture(t)
	tr := NewTrust(nil)
	defer tr.Close()
	// The executable is deliberately not executable. Discovery/approval never runs it.
	approve(t, tr, r, tools, fp, "session")
	lease, err := tr.Preflight(r, tools)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.project.Root, "config"), []byte("changed config"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = tr.Approve(r, tools, fp, "session")
	code(t, err, "approvalChanged")
	code(t, lease.Validate(r, tools), "approvalChanged")
	select {
	case <-lease.Context.Done():
	default:
		t.Fatal("identity change did not cancel")
	}
	_, err = tr.Preflight(r, tools)
	code(t, err, "approvalRequired")
	fp2, err := Fingerprint(r, tools)
	if err != nil || fp2 == fp {
		t.Fatalf("unchanged fingerprint: %v", err)
	}
	tools[0].Policy = "adapter-v2"
	fp3, err := Fingerprint(r, tools)
	if err != nil || fp3 == fp2 {
		t.Fatal("policy not fingerprinted")
	}
	if err := os.WriteFile(tools[0].Executable, []byte("changed executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	fp4, err := Fingerprint(r, tools)
	if err != nil || fp4 == fp3 {
		t.Fatal("executable not fingerprinted")
	}
	tools[0].ConfigPaths = []string{"../secret"}
	if _, err := Fingerprint(r, tools); err == nil {
		t.Fatal("outside config read allowed")
	}
}

func TestRevocationAndConnectionCancellation(t *testing.T) {
	r, tools, fp := fixture(t)
	tr := NewTrust(nil)
	approve(t, tr, r, tools, fp, "session")
	queued, err := tr.Preflight(r, tools)
	if err != nil {
		t.Fatal(err)
	}
	running, err := tr.Preflight(r, tools)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Revoke(r); err != nil {
		t.Fatal(err)
	}
	for _, l := range []*Lease{queued, running} {
		select {
		case <-l.Context.Done():
		default:
			t.Fatal("work not canceled")
		}
		code(t, l.Validate(r, tools), "staleContext")
	}
	approve(t, tr, r, tools, fp, "session")
	next, err := tr.Preflight(r, tools)
	if err != nil {
		t.Fatal(err)
	}
	code(t, queued.Validate(r, tools), "staleContext")
	tr.Close()
	select {
	case <-next.Context.Done():
	default:
		t.Fatal("close did not cancel")
	}
}

func TestCorruptStoreAndConcurrentWriters(t *testing.T) {
	s := store(t)
	r, tools, fp := fixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := r.project
			p.HostID = fmt.Sprintf("host-%d", i)
			errs <- s.set(p, fp)
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 16; i++ {
		p := r.project
		p.HostID = fmt.Sprintf("host-%d", i)
		found, err := s.lookup(p, fp)
		if err != nil || !found {
			t.Fatalf("lost writer %d: %v", i, err)
		}
	}
	for _, bad := range []string{"{", `{"version":2,"grants":[]}`, `{"version":1,"grants":null}`, `{"version":1,"grants":[{"project":{"hostId":"x","root":"relative"},"fingerprint":"bad"}]}`} {
		if err := os.WriteFile(s.path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		tr := NewTrust(s)
		_, err := tr.Lookup(r, fp)
		code(t, err, "approvalRequired")
		_, err = tr.Approve(r, tools, fp, "persistent")
		code(t, err, "approvalRequired")
		tr.Close()
	}
}

func TestConcurrentRevokeAndPreflight(t *testing.T) {
	r, tools, fp := fixture(t)
	tr := NewTrust(nil)
	defer tr.Close()
	approve(t, tr, r, tools, fp, "session")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				l, err := tr.Preflight(r, tools)
				if err == nil {
					_ = l.Validate(r, tools)
				}
			}
		}()
	}
	if err := tr.Revoke(r); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	_, err := tr.Preflight(r, tools)
	code(t, err, "approvalRequired")
}

func TestAuthorizationRemovedAndRootReplaced(t *testing.T) {
	r, tools, fp := fixture(t)
	authorized := true
	r.known = func(string) bool { return authorized }
	tr := NewTrust(nil)
	defer tr.Close()
	approve(t, tr, r, tools, fp, "session")
	authorized = false
	_, err := tr.Preflight(r, tools)
	code(t, err, "unauthorized")
	authorized = true
	old := r.project.Root + "-old"
	if err := os.Rename(r.project.Root, old); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(old) }()
	if err := os.Mkdir(r.project.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	code(t, r.Revalidate(), "approvalChanged")
}
