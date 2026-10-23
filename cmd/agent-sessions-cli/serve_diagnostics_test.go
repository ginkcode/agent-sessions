package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/remote"
)

// A server started the way the desktop launches it on an SSH host advertises
// code diagnostics under an ssh: identity it computes itself, discovers tools
// in a cataloged project, and offers managed installs into its own app data.
func TestServe_RemoteCodeDiagnostics(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("no remote diagnostics route on this platform")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", "/usr/bin/true") // never run the developer's login shell
	project := filepath.Join(base, "project")
	sessions := filepath.Join(base, "claude", "projects", "p")
	for _, d := range []string{project, sessions} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "a.py"), []byte("import os\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-10-23T10:00:00Z","cwd":"` + project + `","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessions, "s1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := paths.Roots{
		Cache: filepath.Join(base, "cache"), Config: filepath.Join(base, "config"), Data: filepath.Join(base, "data"),
		Claude: filepath.Join(base, "claude"), Codex: filepath.Join(base, "codex"), OpenCodeData: filepath.Join(base, "opencode"),
	}
	ctx := t.Context()

	// Without the launcher's variable, as from an older desktop, diagnostics stay off.
	t.Setenv(remote.DiagnosticsHostEnv, "")
	plain := startServe(t, ctx, roots)
	if _, err := plain.client.CodeDiagnosticsHostID(ctx); err == nil {
		t.Fatal("diagnostics identity without a launcher host kind")
	}
	plain.stop(t)

	t.Setenv(remote.DiagnosticsHostEnv, "ssh")
	h := startServe(t, ctx, roots)
	defer h.stop(t)
	host, err := h.client.CodeDiagnosticsHostID(ctx)
	if err != nil || !strings.HasPrefix(host, "ssh:") {
		t.Fatal(host, err)
	}
	if err := h.client.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	att, err := h.client.AttachCodeDiagnostics(ctx, project, 1)
	if err != nil {
		t.Fatalf("attach: %v (stderr: %s)", err, h.stderr.String())
	}
	if att.Project.HostID != host {
		t.Fatal(att.Project.HostID)
	}
	// Discovery completes after attach returns.
	var ruff string
	for deadline := time.Now().Add(10 * time.Second); ruff == "" && time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		snapshot, err := h.client.GetCodeDiagnosticsSnapshot(ctx, att.Context)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range snapshot.Providers {
			if p.ProviderID == "ruff" {
				ruff = p.Availability
			}
		}
	}
	// Found (and awaiting approval) or missing, depending on this machine.
	if ruff != "approvalRequired" && ruff != "missingTool" {
		t.Fatalf("a Python file did not enable Ruff: %q", ruff)
	}

	status, err := h.client.ManagedTool(ctx, engine.ManagedToolRequest{Action: "status", ExpectedHostID: host})
	if err != nil || status.State != "idle" || status.Tools["ruff"] != "missingTool" {
		t.Fatalf("%+v %v", status, err)
	}
	// A proposal names the host and the server's own app data, and writes nothing.
	p, err := h.client.ManagedTool(ctx, engine.ManagedToolRequest{Action: "propose", ExpectedHostID: host, Tool: "ruff"})
	if err != nil || p.State != "proposed" || p.Proposal == nil || p.Proposal.HostID != host ||
		!strings.HasPrefix(p.Proposal.Destination, roots.Data+string(os.PathSeparator)) {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := os.Stat(roots.Data); !os.IsNotExist(err) {
		t.Fatal("proposal wrote app data", err)
	}
	if _, err := h.client.ManagedTool(ctx, engine.ManagedToolRequest{Action: "propose", ExpectedHostID: "ssh:other", Tool: "ruff"}); err == nil {
		t.Fatal("request for another host accepted")
	}
	if _, err := h.client.ManagedTool(ctx, engine.ManagedToolRequest{Action: "dismiss", ExpectedHostID: host}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.client.DetachCodeDiagnostics(ctx, att.Context); err != nil {
		t.Fatal(err)
	}
}
