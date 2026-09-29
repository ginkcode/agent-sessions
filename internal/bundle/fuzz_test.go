package bundle

import (
	"archive/zip"
	"bytes"
	"testing"
	"time"
)

func FuzzReader(f *testing.F) {
	// A valid bundle as the base seed.
	var valid bytes.Buffer
	req := WriteRequest{
		CreatedAt:  time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC),
		Profile:    ProfileComplete,
		Source:     SourceMeta{Agent: "claude-code", ID: "sess-1", CWD: "/tmp/proj"},
		Sessions:   []SessionInput{{Agent: "claude-code", ID: "sess-1", Native: []NativeInput{{Agent: "claude-code", Rel: "a.jsonl", Data: []byte("line\n")}}}},
		Handoff:    "# handoff\n",
		Transcript: []byte(`[]`),
	}
	if err := Write(&valid, req); err != nil {
		f.Fatalf("seed: %v", err)
	}
	f.Add(valid.Bytes())
	f.Add([]byte{})
	f.Add([]byte("PK"))
	f.Add([]byte(`{"format":"agent-sessions.bundle"}`))

	// A zip with a hostile entry name.
	var slip bytes.Buffer
	zw := zip.NewWriter(&slip)
	w, _ := zw.Create("../evil")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	f.Add(slip.Bytes())

	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := ReadBytes(data)
		if err != nil {
			return
		}
		// Anything accepted must satisfy the invariants the reader claims.
		if b.Manifest.Format != CurrentFormat || b.Manifest.Version != CurrentVersion {
			t.Fatalf("accepted bundle with bad header: %+v", b.Manifest)
		}
		if b.Manifest.Profile != ProfileComplete && b.Manifest.Profile != ProfileShareSafe {
			t.Fatalf("accepted unknown profile %q", b.Manifest.Profile)
		}
		for name := range b.Native {
			if err := safeZipName(name); err != nil {
				t.Fatalf("accepted unsafe native name %q: %v", name, err)
			}
		}
		if b.Manifest.Profile == ProfileShareSafe && len(b.Native) > 0 {
			t.Fatalf("share-safe bundle contains native records")
		}
		// Re-reading the accepted bytes must succeed again.
		if _, err := ReadBytes(data); err != nil {
			t.Fatalf("accepted bundle failed on second read: %v", err)
		}
	})
}
