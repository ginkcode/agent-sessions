package providertest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
	"github.com/ginkcode/agent-sessions/internal/provider/codex"
)

// TranscriptTurns is the number of user/assistant turns in a generated transcript.
const TranscriptTurns = 256

// TranscriptFixture writes one valid Claude or Codex transcript below tb.TempDir
// and returns a provider rooted there. targetBytes (decimal MB × 1,000,000) sets
// only the user prompt length, so the file is slightly larger than the target;
// targetBytes 0 gives the small equivalent with identical IDs and structure.
// Each turn has a split assistant message (two records), a tool call and its
// completed result. The file ends with a valid unterminated record that adds an
// unanswered tool call to the final assistant message.
func TranscriptFixture(tb testing.TB, agent string, targetBytes int) (provider.Provider, model.SessionRef, int64) {
	tb.Helper()
	root := tb.TempDir()
	id := "00000000-0000-0000-0000-000000000002"
	ref := model.SessionRef{Agent: model.AgentClaude, ID: id}
	path := filepath.Join(root, "projects", "synthetic", id+".jsonl")
	var p provider.Provider = claude.New(root, nil)
	if agent == "codex" {
		ref.Agent = model.AgentCodex
		path = filepath.Join(root, "sessions", "2026", "10", "24", "rollout-2026-10-24T00-00-00-"+id+".jsonl")
		p = codex.New(root, nil)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		tb.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	w := bufio.NewWriter(f)
	write := func(s string) {
		if _, err := w.WriteString(s); err != nil {
			tb.Fatal(err)
		}
	}
	stamp := `"timestamp":"2026-10-24T00:00:00Z"`
	if agent == "codex" {
		write(fmt.Sprintf(`{"type":"session_meta",%s,"payload":{"id":%q}}`+"\n", stamp, id))
	}
	// 100 MB / 256 turns keeps every record well below jsonl.MaxLineLen.
	prompt := strings.Repeat("x", max(1, targetBytes/TranscriptTurns))
	for i := range TranscriptTurns {
		if agent == "claude" {
			write(fmt.Sprintf(`{"type":"user","uuid":"u%d",%s,"message":{"role":"user","content":%q}}`+"\n", i, stamp, prompt))
			write(fmt.Sprintf(`{"type":"assistant","uuid":"a%d",%s,"message":{"id":"a%d","role":"assistant","content":[{"type":"text","text":"first"}]}}`+"\n", i, stamp, i))
			write(fmt.Sprintf(`{"type":"assistant",%s,"message":{"id":"a%d","role":"assistant","content":[{"type":"text","text":"second"},{"type":"tool_use","id":"t%d","name":"Read","input":{}}]}}`+"\n", stamp, i, i))
			write(fmt.Sprintf(`{"type":"user",%s,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t%d","content":"done"}]}}`+"\n", stamp, i))
		} else {
			record := func(payload string) {
				write(fmt.Sprintf(`{"type":"response_item",%s,"payload":%s}`+"\n", stamp, payload))
			}
			record(fmt.Sprintf(`{"type":"message","id":"u%d","role":"user","content":[{"type":"input_text","text":%q}]}`, i, prompt))
			record(fmt.Sprintf(`{"type":"message","id":"a%d","role":"assistant","content":[{"type":"output_text","text":"first"}]}`, i))
			record(fmt.Sprintf(`{"type":"message","id":"a%d","role":"assistant","content":[{"type":"output_text","text":"second"}]}`, i))
			record(fmt.Sprintf(`{"type":"function_call","call_id":"t%d","name":"Read","arguments":{}}`, i))
			record(fmt.Sprintf(`{"type":"function_call_output","call_id":"t%d","output":"done"}`, i))
		}
	}
	// A valid unterminated live-write tail continues the final split assistant; its unanswered
	// call is unknown because the generated root has no live-session evidence.
	if agent == "claude" {
		write(fmt.Sprintf(`{"type":"assistant","uuid":"tail",%s,"message":{"id":"a%d","role":"assistant","content":[{"type":"tool_use","id":"tail","name":"Read","input":{}}]}}`, stamp, TranscriptTurns-1))
	} else {
		write(fmt.Sprintf(`{"type":"response_item",%s,"payload":{"type":"function_call","call_id":"tail","name":"Read","arguments":{}}}`, stamp))
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	if err := f.Close(); err != nil {
		tb.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		tb.Fatal(err)
	}
	return p, ref, st.Size()
}

// TranscriptEstimate is an intentionally approximate shallow-layout + content
// byte estimate, NOT a heap measurement. It excludes allocator rounding, metadata
// strings, sharing, parser temporaries, maps and GC/runtime overhead.
func TranscriptEstimate(tr *model.Transcript) int64 {
	n := int64(unsafe.Sizeof(*tr)) + int64(cap(tr.Messages))*int64(unsafe.Sizeof(model.Message{}))
	for _, m := range tr.Messages {
		n += int64(len(m.ID)+len(m.Model)) + int64(cap(m.Parts))*int64(unsafe.Sizeof(model.Part{}))
		for _, p := range m.Parts {
			n += int64(len(p.Text))
			if p.Tool != nil {
				n += int64(unsafe.Sizeof(*p.Tool)) + int64(len(p.Tool.ID)+len(p.Tool.Name)+len(p.Tool.Input)+len(p.Tool.Output)+len(p.Tool.OutputRef))
			}
		}
	}
	return n
}
