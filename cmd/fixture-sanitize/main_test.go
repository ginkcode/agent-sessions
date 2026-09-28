package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeNestedContent(t *testing.T) {
	const input = `{"type":"assistant","uuid":"uuid-123","timestamp":"2026-02-01T00:00:00Z","message":{"id":"msg_123","model":"claude-opus-4-6","content":[{"type":"text","text":"Hello 世界"},{"type":"thinking","thinking":"private thought"},{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"cat /home/alice/secret","options":["秘密",42,true,null]}}]},"cwd":"/home/alice/src","toolUseResult":{"output":"secret","exitCode":0},"aiTitle":"Title","content":"top-level string","description":"desc","lastPrompt":"prompt","summary":"summary","rendered":"rendered","output":{"part":"hidden"},"extra":{"path":"/home/bob/docs","valid":true}}` + "\n"
	var out bytes.Buffer
	if err := sanitize(strings.NewReader(input), &out, 400); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Error("sanitized JSONL should end in newline")
	}
	if record["uuid"] != "uuid-123" || record["timestamp"] != "2026-02-01T00:00:00Z" || record["type"] != "assistant" {
		t.Errorf("lost structural fields: %+v", record)
	}
	if record["cwd"] != "/home/dev/src" {
		t.Errorf("home path not mapped: %v", record["cwd"])
	}
	if record["extra"].(map[string]any)["path"] != "/home/dev/docs" {
		t.Errorf("nested home path not mapped: %v", record["extra"])
	}
	message := record["message"].(map[string]any)
	if message["id"] != "msg_123" || message["model"] != "claude-opus-4-6" {
		t.Errorf("lost message metadata: %v", message)
	}
	content := message["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if text == "Hello 世界" || utf8.RuneCountInString(text) != utf8.RuneCountInString("Hello 世界") {
		t.Errorf("text redaction wrong length or unchanged: %q", text)
	}
	thinking := content[1].(map[string]any)["thinking"].(string)
	if thinking == "private thought" || utf8.RuneCountInString(thinking) != utf8.RuneCountInString("private thought") {
		t.Errorf("thinking redaction wrong length or unchanged: %q", thinking)
	}
	tool := content[2].(map[string]any)
	if tool["id"] != "tool-1" || tool["name"] != "Bash" {
		t.Errorf("tool metadata lost: %v", tool)
	}
	toolInput := tool["input"].(map[string]any)
	if cmd := toolInput["command"].(string); cmd == "cat /home/alice/secret" || strings.Contains(cmd, "/home/") || utf8.RuneCountInString(cmd) != utf8.RuneCountInString("cat /home/alice/secret") {
		t.Errorf("tool input not redacted: %q", cmd)
	}
	option := toolInput["options"].([]any)
	if utf8.RuneCountInString(option[0].(string)) != 2 || option[1] != float64(42) || option[2] != true || option[3] != nil {
		t.Errorf("tool input leaf types were changed: %v", option)
	}
	if got := record["toolUseResult"].(map[string]any)["output"].(string); got == "secret" || utf8.RuneCountInString(got) != 6 {
		t.Errorf("result string not redacted: %q", got)
	}
	if record["toolUseResult"].(map[string]any)["exitCode"] != float64(0) {
		t.Errorf("result exit code changed: %v", record["toolUseResult"])
	}
	for key, old := range map[string]string{"aiTitle": "Title", "content": "top-level string", "description": "desc", "lastPrompt": "prompt", "summary": "summary", "rendered": "rendered"} {
		got := record[key].(string)
		if got == old || utf8.RuneCountInString(got) != utf8.RuneCountInString(old) {
			t.Errorf("%s not redacted: %q", key, got)
		}
	}
	if got := record["output"].(map[string]any)["part"].(string); got == "hidden" || utf8.RuneCountInString(got) != 6 {
		t.Errorf("output object string not redacted: %q", got)
	}
}

func TestSanitizeImageAndOtherData(t *testing.T) {
	const input = `{"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"very-private-base64"}},{"type":"text","text":"secret","data":"retain this"}],"data":"keep plain data"}` + "\n"
	var out bytes.Buffer
	if err := sanitize(strings.NewReader(input), &out, 1); err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	blocks := record["content"].([]any)
	source := blocks[0].(map[string]any)["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" {
		t.Errorf("image source metadata changed: %v", source)
	}
	imageData, err := base64.StdEncoding.DecodeString(source["data"].(string))
	if err != nil {
		t.Fatalf("replacement image is not base64: %v", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(imageData))
	if err != nil || config.Width != 1 || config.Height != 1 {
		t.Errorf("replacement is not a 1x1 PNG: config=%+v error=%v", config, err)
	}
	if record["data"] != "keep plain data" || blocks[1].(map[string]any)["data"] != "retain this" {
		t.Error("non-image data changed")
	}
}

func TestSanitizeDeterminismAndCap(t *testing.T) {
	long := strings.Repeat("🥐", 250)
	got := redacted(long)
	if utf8.RuneCountInString(got) != 200 {
		t.Errorf("redacted length = %d, want 200 runes", utf8.RuneCountInString(got))
	}
	if got != redacted(long) {
		t.Error("redaction was nondeterministic")
	}
	if got == redacted(strings.Repeat("🥖", 250)) {
		t.Error("different inputs gave same filler")
	}
	if redacted("") != "" {
		t.Error("empty text should stay empty")
	}
}

func TestSanitizeLineOrderAndTruncation(t *testing.T) {
	input := `{"uuid":"first","text":"one"}` + "\n" + `{"uuid":"second","text":"two"}` + "\n" + `{"uuid":"third","text":"three"}` + "\n"
	var out bytes.Buffer
	if err := sanitize(strings.NewReader(input), &out, 2); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines; want 2", len(lines))
	}
	for i, id := range []string{"first", "second"} {
		var record map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &record); err != nil {
			t.Fatal(err)
		}
		if record["uuid"] != id {
			t.Errorf("line %d uuid = %v; want %s", i, record["uuid"], id)
		}
	}
}

func TestSanitizeMalformedLine(t *testing.T) {
	input := `{"uuid":"good"}` + "\n" + `{"text":` // incomplete final record
	var out bytes.Buffer
	if err := sanitize(strings.NewReader(input), &out, 3); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("expected line 2 parse error; got %v", err)
	}
}

func TestRunRefusesProtectedPaths(t *testing.T) {
	for _, path := range []string{"/tmp/.credentials/account.jsonl", "/tmp/auth.json/abc", "/tmp/auth.json", "a.credentials.jsonl"} {
		if err := checkInputPath(path); err == nil {
			t.Errorf("expected protected path %q to be refused", path)
		}
	}
	dir := t.TempDir()
	protectedDir := filepath.Join(dir, ".credentials")
	if err := os.Mkdir(protectedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(protectedDir, "file.jsonl")
	if err := os.WriteFile(protected, []byte(`{"uuid":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "safe.jsonl")
	if err := os.Symlink(protected, alias); err != nil {
		t.Fatal(err)
	}
	if err := checkInputPath(alias); err == nil {
		t.Error("expected symlink to protected path to be refused")
	}
}

func TestRunOutputAndSameFileGuard(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.jsonl")
	output := filepath.Join(dir, "subdir", "out.jsonl")
	if err := os.WriteFile(input, []byte(`{"uuid":"id1","text":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-in", input, "-out", output}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte("secret")) {
		t.Errorf("source text leaked to output: %s", got)
	}
	if err := run([]string{"-in", input, "-out", input}); err == nil {
		t.Error("expected same input/output to be refused")
	}
	got, err = os.ReadFile(input)
	if err != nil || string(got) != `{"uuid":"id1","text":"secret"}` {
		t.Errorf("same-file guard damaged input: %q, %v", got, err)
	}
}

func TestRunDoesNotClobberOutputOnInvalidInput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.jsonl")
	output := filepath.Join(dir, "out.jsonl")
	if err := os.WriteFile(input, []byte(`{"uuid":"first"}`+"\n"+`{"broken":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-in", input, "-out", output}); err == nil {
		t.Error("expected parse error")
	}
	got, err := os.ReadFile(output)
	if err != nil || string(got) != "existing" {
		t.Errorf("existing output changed after parse error: %q, %v", got, err)
	}
}
