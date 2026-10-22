package codediagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real adapter, trust, supervisor and manager share one approval. Only the
// process boundary is replaced: neither the fixture tool nor config can execute.
func TestStage1GoAdapterManagerReplacement(t *testing.T) {
	p := goPlan(t, map[string]string{"go.mod": "module synthetic\n", "a.go": "package synthetic\r\n// 😀é\r\n", "unopened.go": "package synthetic\n", "ui/App.svelte": "<script>throw Error('never execute')</script>"})
	trust := NewTrust(nil)
	mode := "findings"
	supervisor := NewSupervisor(trust, SupervisorOptions{CaptureBytes: 16384, Start: func(in Invocation) (Process, error) {
		proc := newFakeProcess()
		text := "go version go1.26.8 darwin/arm64\n"
		if in.Args[0] != "version" {
			text = `{}`
			switch mode {
			case "findings":
				text = `{"p":{"printf":[{"posn":"unopened.go:1:1","message":"<script> & 😀"},{"posn":"../escape.go:1:1","message":"outside"}]}}`
			case "failure":
				text = `{`
				_, _ = in.Stderr.Write([]byte("private-secret-not-a-notice"))
			case "cap":
				text = strings.Repeat("x", 17000)
			}
		}
		_, _ = in.Stdout.Write([]byte(text))
		_ = proc.Terminate()
		return proc, nil
	}})
	m, err := NewManager("stage1", trust, supervisor, ManagerLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	snap, err := m.Attach(p.root, 1, []Registration{{ProviderID: "go", Availability: "available", Adapter: p}, {ProviderID: "svelte", Availability: "missingTool", Adapter: nil}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Run(snap.Context, nil, false); err == nil {
		t.Fatal("unapproved execution")
	}
	snap, err = m.Approve(snap.Context, snap.Proposal.Fingerprint, "session")
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []string{"findings", "failure", "cap", "empty"} {
		mode = next
		runs, e := m.Run(snap.Context, []string{"go"}, false)
		if e != nil {
			t.Fatal(e)
		}
		m.workers.Wait() // completion barrier, not a sleep or polling race
		snap, e = m.GetSnapshot(snap.Context)
		if e != nil {
			t.Fatal(e)
		}
		state := snap.Providers[0]
		if state.ProviderID != "go" || snap.Providers[1].Availability != "missingTool" {
			t.Fatal(snap)
		}
		if strings.Contains(state.Notice, "private-secret") {
			t.Fatal("stderr exposed")
		}
		if len(snap.ResultSets) != 1 {
			t.Fatal(snap)
		}
		set := snap.ResultSets[0]
		if next == "empty" {
			if len(set.Diagnostics) != 0 || state.Phase != "idle" || set.RunID != runs[0].RunID {
				t.Fatal(snap)
			}
		} else {
			if len(set.Diagnostics) != 1 || set.Diagnostics[0].Path != "unopened.go" {
				t.Fatal(snap)
			}
			if next == "findings" {
				if set.RunID != runs[0].RunID || !state.Coverage.Partial || set.Diagnostics[0].Freshness != "uncertain" {
					t.Fatal(snap)
				}
			} else if state.Phase != "failed" || set.Diagnostics[0].Freshness != "stale" {
				t.Fatal(snap)
			}
		}
		parts, e := m.SnapshotEvents(snap.Context)
		if e != nil {
			t.Fatal(e)
		}
		for _, part := range parts {
			if part.Sequence != snap.Sequence || encodedSize(part) > m.limits.EventBytes {
				t.Fatal(part)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(p.root.Project().Root, "a.go"))
	if err != nil || string(data) != "package synthetic\r\n// 😀é\r\n" {
		t.Fatal("source changed", err)
	}
}
