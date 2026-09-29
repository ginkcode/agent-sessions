package bundle_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/model"
)

func truncatedTranscript(output, ref string) model.Transcript {
	return model.Transcript{
		Meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "sess-1"}},
		Messages: []model.Message{{
			Role: model.RoleAssistant,
			Parts: []model.Part{{
				Kind: model.PartTool,
				Tool: &model.ToolCall{
					Name:            "Bash",
					Output:          output[:10],
					OutputTruncated: true,
					OutputRef:       ref,
				},
			}},
		}},
	}
}

func TestResolveFidelityRestoresTruncatedOutput(t *testing.T) {
	full := strings.Repeat("output line\n", 100)
	tr := truncatedTranscript(full, "rec:0:tool-1")
	original := tr.Messages[0].Parts[0].Tool.Output

	blob := func(_ context.Context, ref model.SessionRef, key string) ([]byte, error) {
		if ref.ID != "sess-1" || key != "rec:0:tool-1" {
			t.Errorf("unexpected blob request %s %s", ref.ID, key)
		}
		return []byte(full), nil
	}
	got, report, err := bundle.ResolveFidelity(context.Background(), []model.Transcript{tr}, blob)
	if err != nil {
		t.Fatal(err)
	}
	tool := got[0].Messages[0].Parts[0].Tool
	if tool.Output != full || tool.OutputTruncated || tool.OutputRef != "" {
		t.Fatalf("output not fully restored: truncated=%v ref=%q len=%d", tool.OutputTruncated, tool.OutputRef, len(tool.Output))
	}
	if report.Resolved != 1 || report.ResolvedBytes != int64(len(full)) {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Overflow) != 0 || len(report.Unavailable) != 0 {
		t.Fatalf("unexpected report entries: %+v", report)
	}
	// The caller's transcript is not mutated.
	if tr.Messages[0].Parts[0].Tool.Output != original || !tr.Messages[0].Parts[0].Tool.OutputTruncated {
		t.Fatal("input transcript was mutated")
	}
}

func TestResolveFidelityReportsOverflow(t *testing.T) {
	// Each blob is just over half the cap, so the first fits and the second
	// must be reported instead of being cut down to fill the remainder.
	size := bundle.MaxFidelityBytes/2 + 1
	first := strings.Repeat("a", int(size))
	second := strings.Repeat("b", int(size))
	tr := model.Transcript{
		Meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentCodex, ID: "sess-2"}},
		Messages: []model.Message{{
			Parts: []model.Part{
				{Kind: model.PartTool, Tool: &model.ToolCall{Output: "head-1", OutputTruncated: true, OutputRef: "rec:1"}},
				{Kind: model.PartTool, Tool: &model.ToolCall{Output: "head-2", OutputTruncated: true, OutputRef: "rec:2"}},
			},
		}},
	}
	blob := func(_ context.Context, _ model.SessionRef, key string) ([]byte, error) {
		if key == "rec:1" {
			return []byte(first), nil
		}
		return []byte(second), nil
	}
	got, report, err := bundle.ResolveFidelity(context.Background(), []model.Transcript{tr}, blob)
	if err != nil {
		t.Fatal(err)
	}
	tools := got[0].Messages[0].Parts
	if tools[0].Tool.Output != first || tools[0].Tool.OutputTruncated {
		t.Fatal("first output should be restored whole")
	}
	if tools[1].Tool.Output != "head-2" || !tools[1].Tool.OutputTruncated || tools[1].Tool.OutputRef != "rec:2" {
		t.Fatalf("second output was altered: %+v", tools[1].Tool)
	}
	if len(report.Overflow) != 1 || !strings.Contains(report.Overflow[0], "rec:2") {
		t.Fatalf("overflow = %v", report.Overflow)
	}
	if report.Resolved != 1 {
		t.Fatalf("resolved = %d", report.Resolved)
	}
}

func TestResolveFidelityReportsUnavailable(t *testing.T) {
	tr := truncatedTranscript(strings.Repeat("x", 20), "rec:9:missing")
	blob := func(context.Context, model.SessionRef, string) ([]byte, error) {
		return nil, errors.New("record gone")
	}
	got, report, err := bundle.ResolveFidelity(context.Background(), []model.Transcript{tr}, blob)
	if err != nil {
		t.Fatal(err)
	}
	tool := got[0].Messages[0].Parts[0].Tool
	if !tool.OutputTruncated || tool.OutputRef != "rec:9:missing" {
		t.Fatalf("failed blob should leave the display output: %+v", tool)
	}
	if len(report.Unavailable) != 1 || !strings.Contains(report.Unavailable[0], "record gone") {
		t.Fatalf("unavailable = %v", report.Unavailable)
	}
	if report.Resolved != 0 {
		t.Fatalf("resolved = %d", report.Resolved)
	}
}

func TestResolveFidelityInlinesTextAttachment(t *testing.T) {
	tr := model.Transcript{
		Meta: model.SessionMeta{Ref: model.SessionRef{Agent: model.AgentOpenCode, ID: "ses_1"}},
		Messages: []model.Message{{
			Parts: []model.Part{{
				Kind: model.PartFile,
				File: &model.FileRef{Name: "notes.txt", Mime: "text/plain", Ref: "v2:m1:file:0"},
			}},
		}},
	}
	blob := func(context.Context, model.SessionRef, string) ([]byte, error) {
		return []byte("full note"), nil
	}
	got, report, err := bundle.ResolveFidelity(context.Background(), []model.Transcript{tr}, blob)
	if err != nil {
		t.Fatal(err)
	}
	part := got[0].Messages[0].Parts[0]
	if part.Kind != model.PartText || part.File != nil || !strings.Contains(part.Text, "full note") || !strings.Contains(part.Text, "notes.txt") {
		t.Fatalf("attachment not inlined: %+v", part)
	}
	if report.Resolved != 1 {
		t.Fatalf("report = %+v", report)
	}
}
