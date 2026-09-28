package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/jsonl"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// FuzzRecord feeds arbitrary bytes as one transcript record into the scan
// aggregator and the Load record handler. Neither may panic.
func FuzzRecord(f *testing.F) {
	seeds := fixtureSeeds(f)
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Add([]byte(`{"type":"assistant","message":{"id":"x","content":[{"type":"tool_use","id":"t","name":"Bash","input":{}}]}}`))
	f.Add([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]}}`))
	f.Add([]byte(`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","preTokens":1,"postTokens":2}}`))
	f.Add([]byte(`{"type":"attachment","attachment":{"type":"file","rendered":"x"}}`))
	f.Add([]byte(`not json at all`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, line []byte) {
		var d provider.Diagnostics
		var cp checkpoint
		cp.path = "fuzz"
		cp.consume(line, &d)

		l := &loader{path: "fuzz", tools: make(map[string]*model.ToolCall), diag: &provider.Diagnostics{}}
		l.record(line, 0, 0)
	})
}

// FuzzReader runs arbitrary byte streams through the jsonl reader.
func FuzzReader(f *testing.F) {
	seeds := fixtureSeeds(f)
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Add([]byte("a\n\nb\r\nc"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, data []byte) {
		r := jsonl.NewReader(bytes.NewReader(data), 0)
		for {
			line, _, err := r.Next()
			if line != nil {
				// Lines are non-empty and contain no newline. A single CR
				// from a CRLF terminator is stripped; earlier CRs remain.
				if len(line) == 0 {
					t.Fatalf("empty line returned")
				}
				if strings.ContainsRune(string(line), '\n') {
					t.Fatalf("line contains newline: %q", line)
				}
			}
			if err != nil {
				break
			}
		}
	})
}

// fixtureSeeds returns lines from fixture files as fuzz seeds.
func fixtureSeeds(f *testing.F) [][]byte {
	f.Helper()
	var seeds [][]byte
	paths, err := filepath.Glob(filepath.Join("testdata", "*.jsonl"))
	if err != nil {
		f.Fatal(err)
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				seeds = append(seeds, line)
			}
		}
	}
	return seeds
}
