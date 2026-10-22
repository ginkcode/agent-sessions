package codediagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func liveBoundsFixture(t *testing.T) (*Manager, Snapshot) {
	t.Helper()
	m, snap, _ := docFixture(t, incrementalCaps)
	a := m.attachments[snap.Context.AttachmentID]
	a.snapshot.Providers = append(a.snapshot.Providers, ProviderState{ProviderID: goplsProviderID, Availability: "available", Phase: "idle", Freshness: "uncertain", Coverage: Coverage{Kind: "openedDocuments", Roots: []string{}, Partial: true, ExternalChanges: "unwatched"}})
	return m, snap
}

func assertLiveBudgets(t *testing.T, m *Manager, c Context) Snapshot {
	t.Helper()
	s, err := m.GetSnapshot(c)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > 8<<20 {
		t.Fatalf("snapshot bytes %d: %v", len(data), err)
	}
	count, size := 0, 0
	for _, set := range s.ResultSets {
		for _, d := range set.Diagnostics {
			count++
			size += encodedSize(d) + 1
		}
	}
	if count > 10000 || size > 7<<20 {
		t.Fatalf("retained %d / %d", count, size)
	}
	events, err := m.SnapshotEvents(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) > 32 {
		t.Fatal("parts")
	}
	for _, event := range events {
		envelope, err := json.Marshal(struct {
			JSONRPC string `json:"jsonrpc"`
			Method  string `json:"method"`
			Params  Event  `json:"params"`
		}{"2.0", "codeDiagnostics:updated", event})
		if err != nil || len(envelope) > 1<<20 {
			t.Fatalf("event bytes %d: %v", len(envelope), err)
		}
	}
	return s
}

func TestLiveRetentionManyDocumentsEscapedBudgets(t *testing.T) {
	for _, message := range []string{"small", strings.Repeat("<\u2028\x01", 1500)} {
		t.Run(fmt.Sprint(len(message)), func(t *testing.T) {
			m, s := liveBoundsFixture(t)
			a := m.attachments[s.Context.AttachmentID]
			v := int64(1)
			for i := 0; i < 16; i++ {
				path, id := fmt.Sprintf("doc%02d.go", i), fmt.Sprintf("d%02d", i)
				if err := os.WriteFile(filepath.Join(a.root.Project().Root, path), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := m.OpenDocument(context.Background(), s.Context, path, id, 1, "x", ""); err != nil {
					t.Fatal(err)
				}
				batch := make([]Diagnostic, 1200)
				for j := range batch {
					batch[j] = Diagnostic{Path: path, Source: "fake", Severity: "error", Message: fmt.Sprintf("%04d%s", j, message)}
				}
				if err := m.PublishDocumentResults(s.Context, id, "g1", &v, batch); err != nil {
					t.Fatal(err)
				}
				assertLiveBudgets(t, m, s.Context)
			}
			got := assertLiveBudgets(t, m, s.Context)
			if p := provider(a, goplsProviderID); !p.Truncated || !p.Coverage.Partial || p.Notice == "" {
				t.Fatal("silent truncation")
			}
			// A failure must not replace bounded findings with an empty success.
			before := CountFindings(got)
			m.LiveFailure(a.root.Project(), goplsProviderID)
			got = assertLiveBudgets(t, m, s.Context)
			if CountFindings(got) != before {
				t.Fatal("failure cleared findings")
			}
			for _, set := range got.ResultSets {
				for _, d := range set.Diagnostics {
					if d.Freshness != "stale" {
						t.Fatal("failure not stale")
					}
				}
			}
			if err := m.PublishDocumentResults(s.Context, "d00", "g1", &v, nil); err != nil {
				t.Fatal(err)
			}
			got = assertLiveBudgets(t, m, s.Context)
			if len(got.ResultSets[0].Diagnostics) != 0 {
				t.Fatal("empty did not replace exact scope")
			}
		})
	}
}

func TestLiveNormalizationLossAndPointerIsolation(t *testing.T) {
	m, s := liveBoundsFixture(t)
	if _, err := m.OpenDocument(context.Background(), s.Context, "source.go", "d", 1, "x", ""); err != nil {
		t.Fatal(err)
	}
	v := int64(1)
	batch := []Diagnostic{
		{Path: "source.go", Source: "fake", Severity: "error", Message: strings.Repeat("😀", 3000), Range: &Range{End: Position{Character: 999999}}},
		{Path: "source.go", Source: strings.Repeat("s", 257), Severity: "error", Message: "bad source"},
		{Path: "source.go", Source: "fake", Code: strings.Repeat("c", 257), Severity: "error", Message: "bad code"},
	}
	if err := m.PublishDocumentResults(s.Context, "d", "g1", &v, batch); err != nil {
		t.Fatal(err)
	}
	v = 100
	got := assertLiveBudgets(t, m, s.Context)
	d := got.ResultSets[0].Diagnostics[0]
	if len(got.ResultSets[0].Diagnostics) != 1 || len(d.Message) > 8192 || d.Range != nil || d.LocationIssue != "invalidRange" || *d.Basis.Version != 1 {
		t.Fatalf("normalization: %+v", d)
	}
	a := m.attachments[s.Context.AttachmentID]
	if p := provider(a, goplsProviderID); !p.Truncated || !p.Coverage.Partial || p.Notice == "" {
		t.Fatal("silent normalization")
	}
}

func TestAdapterShorteningAndDroppedEmptyObservable(t *testing.T) {
	m, s := liveBoundsFixture(t)
	a := m.attachments[s.Context.AttachmentID]
	if _, err := m.OpenDocument(context.Background(), s.Context, "source.go", "d", 1, "x", ""); err != nil {
		t.Fatal(err)
	}
	uri := fileURI(filepath.Join(a.root.Project().Root, "source.go"))
	batch := make([]map[string]any, 15000)
	for i := range batch {
		batch[i] = map[string]any{"message": fmt.Sprintf("%05d", 14999-i)}
	}
	rawBatch, err := json.Marshal(map[string]any{"uri": uri, "version": 1, "diagnostics": batch})
	if err != nil {
		t.Fatal(err)
	}
	pubBatch, err := normalizeGoplsPublication(a.root, rawBatch)
	if err != nil || !pubBatch.Truncated || len(pubBatch.Diagnostics) != 10000 || pubBatch.Diagnostics[0].Message != "00000" {
		t.Fatalf("batch cap %d: %v", len(pubBatch.Diagnostics), err)
	}
	for _, diagnostics := range []any{
		[]any{map[string]any{"message": strings.Repeat("<", 9000), "source": strings.Repeat("s", 1000), "code": strings.Repeat("c", 1000)}},
		[]any{map[string]any{"message": "", "severity": 1}},
	} {
		raw, err := json.Marshal(map[string]any{"uri": uri, "version": 1, "diagnostics": diagnostics})
		if err != nil {
			t.Fatal(err)
		}
		pub, err := normalizeGoplsPublication(a.root, raw)
		if err != nil {
			t.Fatal(err)
		}
		if pub.Dropped == 0 && !pub.Partial {
			t.Fatal("adapter hid loss")
		}
		if err := publishLivePublication(m, SessionNotification{Project: a.root.Project(), ProviderID: goplsProviderID, Generation: "g1"}, pub); errCode(err) != "resyncRequired" {
			t.Fatal("path route must still require URI isolation", err)
		}
		// Exercise the same atomic status-bearing replacement after the independent
		// isolation guard; framed ownership/isolation tests cover that guard itself.
		if err := m.publishLocked(a, a.docs["d"], "d", goplsProviderID, "g1", pub.Version, pub.Diagnostics, pub.Dropped > 0 || pub.Partial, pub.Truncated); err != nil {
			t.Fatal(err)
		}
		got := assertLiveBudgets(t, m, s.Context)
		if pub.Truncated && !provider(a, goplsProviderID).Truncated {
			t.Fatal("adapter shortening not observable")
		}
		if p := provider(a, goplsProviderID); !p.Coverage.Partial || p.Notice == "" {
			t.Fatal("false clean empty", got)
		}
	}
}
