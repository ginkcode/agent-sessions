package codediagnostics

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The user's browse sequence through the production LiveDocuments route (fake
// language server): main.go -> mousenav_other.go -> mousenav_linux.c, then
// reopen, change, save and a Refresh-style snapshot read. Every Go transition
// succeeds; the .c file has no owner and fails with a meaningful unsupported
// error; a second open of a still-open path (what the frontend bridge used to
// send after forgetting a lifecycle during a resync) is invalidArgument.
func TestLiveSyncBrowseSequence(t *testing.T) {
	e := newEnv(t, SessionOptions{})
	root, tools, _ := fixture(t)
	const (
		mainGo  = "cmd/agent-sessions/main.go"
		otherGo = "cmd/agent-sessions/mousenav_other.go"
		linuxC  = "cmd/agent-sessions/mousenav_linux.c"
	)
	for _, p := range []string{mainGo, otherGo, linuxC} {
		full := filepath.Join(root.Project().Root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("disk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := ServerConfig{ProviderID: goplsProviderID, Executable: tools[0].Executable, Args: []string{"--fake"}, AdapterPolicy: "fake-only"}
	state := ProviderState{ProviderID: goplsProviderID, Availability: "available", Phase: "idle", Freshness: "uncertain",
		Coverage: Coverage{Kind: "openedDocuments", Roots: []string{""}, Partial: true, ExternalChanges: "unwatched"}, DocumentExtensions: []string{".go"}}
	regs := []Registration{liveRegistration(state, cfg, func(file string) (string, bool) { return "go", strings.HasSuffix(file, ".go") })}
	m, err := NewManager("owner", e.trust, NewSupervisor(e.trust, SupervisorOptions{}), ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	d := NewLiveDocuments(e.trust, m, e.mgr.options)
	t.Cleanup(func() { m.Close(); d.Close() })
	if err = d.Register(root, regs); err != nil {
		t.Fatal(err)
	}
	m.SetDocumentProvider(d)
	s, err := m.Attach(root, 1, regs)
	if err != nil {
		t.Fatal(err)
	}
	if s, err = m.Approve(s.Context, s.Proposal.Fingerprint, "session"); err != nil {
		t.Fatal(err)
	}
	if got := s.Providers[0].DocumentExtensions; !slices.Equal(got, []string{".go"}) {
		t.Fatalf("snapshot must carry provider document metadata, got %v", got)
	}
	methods := make(chan string, 64)
	e.script = func(f *fakeSrv) {
		for {
			msg, ok := f.read()
			if !ok {
				return
			}
			switch method, _ := msg["method"].(string); method {
			case "initialize":
				f.send(`{"jsonrpc":"2.0","id":1,"result":` + defaultInit + `}`)
			case "shutdown":
				f.send(`{"jsonrpc":"2.0","id":2,"result":null}`)
			case "exit":
				_ = f.Terminate()
				return
			default:
				if strings.HasPrefix(method, "textDocument/") {
					methods <- method
				}
			}
		}
	}
	ctx, c := context.Background(), s.Context
	step := func(label string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}
	expect := func(want string) {
		t.Helper()
		if got := <-methods; got != want {
			t.Fatalf("server got %s, want %s", got, want)
		}
	}

	_, err = m.OpenDocument(ctx, c, mainGo, "d1", 1, "package main\n", "r1")
	step("open main.go", err)
	expect("textDocument/didOpen")
	_, err = m.CloseDocument(ctx, c, "d1") // preview replaced by mousenav_other.go
	step("close main.go", err)
	expect("textDocument/didClose")
	_, err = m.OpenDocument(ctx, c, otherGo, "d2", 1, "//go:build !linux && !darwin\n\npackage main\n", "r1")
	step("open mousenav_other.go", err)
	expect("textDocument/didOpen")
	_, err = m.CloseDocument(ctx, c, "d2")
	step("close mousenav_other.go", err)
	expect("textDocument/didClose")

	_, err = m.OpenDocument(ctx, c, linuxC, "d3", 1, "#include <stdio.h>\n", "r1")
	code(t, err, "unsupported")
	if !strings.Contains(err.Error(), "No unique language server owns this file") {
		t.Fatalf("unsupported must say why: %v", err)
	}
	// The unsupported attempt creates no state: its identity was never used.
	if snap, _ := m.GetSnapshot(c); len(snap.ResultSets) != 0 {
		t.Fatal("unsupported open created state")
	}

	_, err = m.OpenDocument(ctx, c, mainGo, "d4", 1, "package main\n", "r1")
	step("reopen main.go with a fresh identity", err)
	expect("textDocument/didOpen")
	_, err = m.OpenDocument(ctx, c, mainGo, "d5", 1, "package main\n", "r1")
	code(t, err, "invalidArgument")
	if !strings.Contains(err.Error(), "Path already has an open document") {
		t.Fatalf("duplicate path must be explicit: %v", err)
	}
	_, err = m.OpenDocument(ctx, c, mainGo, "d1", 1, "package main\n", "r1")
	code(t, err, "invalidArgument") // closed identity is never reused

	_, err = m.ChangeDocument(ctx, c, "d4", 2, "package main\n\nfunc main() {}\n")
	step("change main.go", err)
	expect("textDocument/didChange")
	_, err = m.SaveDocument(ctx, c, "d4", 2, "r2")
	step("save main.go", err)
	expect("textDocument/didSave")
	// Refresh reads the snapshot; it must not disturb the open lifecycle.
	_, err = m.GetSnapshot(c)
	step("refresh snapshot", err)
	_, err = m.ChangeDocument(ctx, c, "d4", 3, "package main\n\nfunc main() { _ = 1 }\n")
	step("change after refresh", err)
	expect("textDocument/didChange")
	_, err = m.CloseDocument(ctx, c, "d4")
	step("close main.go", err)
	expect("textDocument/didClose")
}

// Document metadata must describe exactly what each resolver accepts, so the
// client never opens files a provider cannot own (and never skips ones it can).
func TestProviderDocumentExtensionsMatchResolvers(t *testing.T) {
	for _, ext := range goDocumentExtensions {
		if !strings.HasSuffix("x"+ext, ".go") {
			t.Fatalf("go metadata %q is not resolved by gopls", ext)
		}
	}
	for _, ext := range tsDocumentExtensions {
		if owner, _ := DocumentOwner("x" + ext); owner != tsProviderID {
			t.Fatalf("typescript metadata %q is not owned by typescript", ext)
		}
	}
	for _, ext := range svelteDocumentExtensions {
		if owner, _ := DocumentOwner("x" + ext); owner != svelteLSProviderID {
			t.Fatalf("svelte metadata %q is not owned by svelte", ext)
		}
	}
	for _, ext := range []string{".ts", ".mts", ".cts", ".tsx", ".js", ".mjs", ".cjs", ".jsx"} {
		if !slices.Contains(tsDocumentExtensions, ext) {
			t.Fatalf("typescript owns %q but metadata omits it", ext)
		}
	}
	for _, f := range []string{"a.c", "a.h", "Makefile", "a.md"} {
		if owner, _ := DocumentOwner(f); owner != "" || strings.HasSuffix(f, ".go") {
			t.Fatalf("%s must have no live owner", f)
		}
	}
	p := (&GoplsPlan{availability: "available"}).Provider()
	if !slices.Equal(p.DocumentExtensions, []string{".go"}) {
		t.Fatalf("gopls provider metadata %v", p.DocumentExtensions)
	}
	p.DocumentExtensions[0] = ".c" // callers get a copy
	if goDocumentExtensions[0] != ".go" {
		t.Fatal("provider metadata aliases package state")
	}
}
