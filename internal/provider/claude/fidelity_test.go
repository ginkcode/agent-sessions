package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

// TestBundleFidelityCompletesTruncatedOutput is the M6-06 acceptance check:
// a tool output the loader truncates for display is complete in the bundle
// transcript, resolved through the provider's own Blob.
func TestBundleFidelityCompletesTruncatedOutput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	project := filepath.Join(root, "projects", "p")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("a", maxOutputBytes-1) + "é" + "FIN"
	contents := fmt.Sprintf(""+
		"{\"type\":\"assistant\",\"message\":{\"id\":\"m\",\"content\":[{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Bash\",\"input\":{}}]}}\n"+
		"{\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"tool-1\",\"content\":%q}]}}\n", payload)
	if err := os.WriteFile(filepath.Join(project, "s.jsonl"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	p := New(root, nil)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s"}
	tx, err := p.Load(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !tx.Messages[0].Parts[0].Tool.OutputTruncated {
		t.Fatal("fixture did not truncate; the test proves nothing")
	}

	resolved, report, err := bundle.ResolveFidelity(context.Background(), []model.Transcript{*tx}, p.Blob)
	if err != nil {
		t.Fatal(err)
	}
	tool := resolved[0].Messages[0].Parts[0].Tool
	if tool.Output != payload || tool.OutputTruncated || tool.OutputRef != "" {
		t.Fatalf("bundle transcript output incomplete: truncated=%v ref=%q len=%d want %d",
			tool.OutputTruncated, tool.OutputRef, len(tool.Output), len(payload))
	}
	if report.Resolved != 1 || len(report.Overflow) != 0 || len(report.Unavailable) != 0 {
		t.Fatalf("report = %+v", report)
	}
	// The display transcript the UI loaded is unchanged.
	if !tx.Messages[0].Parts[0].Tool.OutputTruncated {
		t.Fatal("display transcript lost its truncation marker")
	}
}
