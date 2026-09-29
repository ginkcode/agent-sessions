package bundle_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/bundle"
)

func sampleRequest() bundle.WriteRequest {
	return bundle.WriteRequest{
		CreatedAt:  time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
		AppVersion: "0.6.0",
		Profile:    bundle.ProfileComplete,
		Source: bundle.SourceMeta{
			Agent:     "claude-code",
			ID:        "11111111-2222-3333-4444-555555555555",
			CWD:       "/home/dev/work/project",
			GitBranch: "feature/auth",
			Title:     "Fix auth",
		},
		Sessions: []bundle.SessionInput{
			{
				Agent: "claude-code",
				ID:    "11111111-2222-3333-4444-555555555555",
				Native: []bundle.NativeInput{
					{Agent: "claude-code", Rel: "projects/-home-dev/session.jsonl", Data: []byte(`{"cwd":"/home/dev/work/project"}` + "\n")},
					{Agent: "claude-code", Rel: "projects/-home-dev/11111111-2222-3333-4444-555555555555/tool.json", Data: []byte(`{"ok":true}`)},
				},
			},
			{
				Agent:    "claude-code",
				ID:       "child-session-1",
				ParentID: "11111111-2222-3333-4444-555555555555",
			},
		},
		Redaction:  bundle.RedactionManifest{Rules: map[string]int{"token": 2}, Counts: 2},
		Handoff:    "# Continuation Context\n\nstay on task\n",
		Transcript: []byte(`[{"meta":{"ref":{"agent":"claude-code","id":"11111111-2222-3333-4444-555555555555"}}}]`),
	}
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	req := sampleRequest()
	if err := bundle.Write(&buf, req); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := bundle.ReadBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	m := got.Manifest
	if m.Format != bundle.CurrentFormat || m.Version != bundle.CurrentVersion {
		t.Fatalf("manifest header: %+v", m)
	}
	if m.Profile != bundle.ProfileComplete {
		t.Fatalf("profile: %s", m.Profile)
	}
	if m.Source.ID != req.Source.ID || m.Source.CWD != req.Source.CWD {
		t.Fatalf("source: %+v", m.Source)
	}
	if len(m.Sessions) != 2 {
		t.Fatalf("sessions: %d", len(m.Sessions))
	}
	if m.Sessions[1].ParentID != req.Source.ID {
		t.Fatalf("parent id: %s", m.Sessions[1].ParentID)
	}
	if len(m.Sessions[0].Native) != 2 {
		t.Fatalf("native files: %d", len(m.Sessions[0].Native))
	}
	for _, n := range m.Sessions[0].Native {
		data, ok := got.Native[n.Name]
		if !ok {
			t.Fatalf("missing native entry %s", n.Name)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != n.SHA256 {
			t.Fatalf("checksum drift for %s", n.Name)
		}
		if !strings.HasPrefix(n.Name, "native/claude-code/") {
			t.Fatalf("native name %s", n.Name)
		}
	}
	if got.Handoff != req.Handoff {
		t.Fatalf("handoff mismatch")
	}
	if !bytes.Equal(got.Transcript, req.Transcript) {
		t.Fatalf("transcript mismatch")
	}
	if m.Redaction.Counts != 2 || m.Redaction.Rules["token"] != 2 {
		t.Fatalf("redaction: %+v", m.Redaction)
	}
}

func TestRoundTripFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/session.agent-session.zip"
	if err := bundle.WriteToFile(path, sampleRequest()); err != nil {
		t.Fatalf("write file: %v", err)
	}
	got, err := bundle.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got.Manifest.Source.Title != "Fix auth" {
		t.Fatalf("title: %s", got.Manifest.Source.Title)
	}
}

func TestShareSafeRejectsNative(t *testing.T) {
	req := sampleRequest()
	req.Profile = bundle.ProfileShareSafe
	var buf bytes.Buffer
	if err := bundle.Write(&buf, req); err == nil {
		t.Fatal("expected share-safe with native records to be refused")
	}

	req.Sessions[0].Native = nil
	buf.Reset()
	if err := bundle.Write(&buf, req); err != nil {
		t.Fatalf("share-safe write: %v", err)
	}
	got, err := bundle.ReadBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("share-safe read: %v", err)
	}
	if len(got.Native) != 0 {
		t.Fatalf("share-safe bundle has native entries")
	}
}

func TestWriterRejectsUnsafeInput(t *testing.T) {
	cases := []func(r *bundle.WriteRequest){
		func(r *bundle.WriteRequest) { r.Source.ID = "../etc/passwd" },
		func(r *bundle.WriteRequest) { r.Source.ID = "has space" },
		func(r *bundle.WriteRequest) { r.Source.ID = "-rf" },
		func(r *bundle.WriteRequest) { r.Source.Agent = "evil" },
		func(r *bundle.WriteRequest) { r.Sessions[0].Native[0].Rel = "../../.ssh/id_rsa" },
		func(r *bundle.WriteRequest) { r.Sessions[0].Native[0].Rel = "/etc/passwd" },
		func(r *bundle.WriteRequest) { r.Profile = "leaky" },
		func(r *bundle.WriteRequest) { r.Transcript = nil },
	}
	for i, mutate := range cases {
		req := sampleRequest()
		mutate(&req)
		var buf bytes.Buffer
		if err := bundle.Write(&buf, req); err == nil {
			t.Errorf("case %d: expected rejection", i)
		}
	}
}

// tamper rebuilds a zip from a valid one with one entry replaced.
func tamper(t *testing.T, name string, replace func(body []byte) []byte) []byte {
	t.Helper()
	var src bytes.Buffer
	if err := bundle.Write(&src, sampleRequest()); err != nil {
		t.Fatalf("write: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(src.Bytes()), int64(src.Len()))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	var dst bytes.Buffer
	zw := zip.NewWriter(&dst)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		if f.Name == name {
			body = replace(body)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("create %s: %v", f.Name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %s: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return dst.Bytes()
}

func TestBadChecksumRejected(t *testing.T) {
	data := tamper(t, "native/claude-code/projects/-home-dev/session.jsonl", func(body []byte) []byte {
		out := append([]byte(nil), body...)
		out[0] ^= 0xff
		return out
	})
	if _, err := bundle.ReadBytes(data); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	data := tamper(t, "manifest.json", func(body []byte) []byte {
		return bytes.Replace(body, []byte(`"version": 1`), []byte(`"version": 99`), 1)
	})
	if _, err := bundle.ReadBytes(data); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected version error, got %v", err)
	}
}

func TestClaudeSubagentIDRoundTrips(t *testing.T) {
	req := sampleRequest()
	sub := "cc439e78-7b55-4083-910d-e1ebe675fae6/agent-ae4ed874d982947f6"
	req.Sessions = append(req.Sessions, bundle.SessionInput{
		Agent:    "claude-code",
		ID:       sub,
		ParentID: req.Source.ID,
		Native: []bundle.NativeInput{
			{Agent: "claude-code", Rel: "projects/-home-dev/" + sub + ".jsonl", Data: []byte("{}\n")},
		},
	})
	var buf bytes.Buffer
	if err := bundle.Write(&buf, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := bundle.ReadBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Manifest.Sessions[2].Ref.ID != sub {
		t.Fatalf("subagent id = %q", got.Manifest.Sessions[2].Ref.ID)
	}
}

func TestUnsafeIDRejected(t *testing.T) {
	data := tamper(t, "manifest.json", func(body []byte) []byte {
		return bytes.Replace(body,
			[]byte("11111111-2222-3333-4444-555555555555"),
			[]byte("../../etc/passwd"), 1)
	})
	if _, err := bundle.ReadBytes(data); err == nil {
		t.Fatal("expected unsafe id rejection")
	}
}

func TestZipSlipRejected(t *testing.T) {
	var dst bytes.Buffer
	zw := zip.NewWriter(&dst)
	for _, name := range []string{
		"../escape.json",
		"/etc/passwd",
		"native/../../evil",
		`native\claude-code\secret`,
		"native/claude-code/../../outside",
		"something-else.txt",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		_, _ = w.Write([]byte("{}"))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := bundle.ReadBytes(dst.Bytes()); err == nil {
		t.Fatal("expected zip-slip rejection")
	}
}

func TestNotAZip(t *testing.T) {
	if _, err := bundle.ReadBytes([]byte("hello")); err == nil {
		t.Fatal("expected error for non-zip input")
	}
	if _, err := bundle.ReadBytes(nil); err == nil {
		t.Fatal("expected error for empty input")
	}
}
