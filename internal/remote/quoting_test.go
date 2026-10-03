package remote

import (
	"bytes"
	"os/exec"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func TestQuotePOSIX_Evaluation(t *testing.T) {
	platform.RequireCommand(t, "/bin/sh")
	testCases := []string{
		"",
		"simple",
		"with spaces",
		"with\ttabs\tand\nnewlines",
		"it's a quote",
		`"double quotes"`,
		"`backticks`",
		"$VAR and ${VAR}",
		"$(cat /etc/passwd)",
		"foo; bar && baz | qux",
		"wildcards: * ? [a-z]",
		"quotes'inside\"quotes'and`backticks`",
		"backslashes: \\ and \\\\",
		"semi;colon&ampersand",
		">redirect <input >>append 2>&1",
		"emoji: 🚀 ✨ 🐱‍👤",
		"special chars: ~!@#$%^&*()_+`-={}|[]\\:\";'<>?,./",
		"a'b'c'd'e'f'g'",
	}

	for _, tc := range testCases {
		quoted := QuotePOSIX(tc)

		// Test that /bin/sh evaluates the quoted string back to the original text exactly
		cmd := exec.Command("/bin/sh", "-c", "printf '%s' "+quoted)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Errorf("shell failed evaluating %s: %v", quoted, err)
			continue
		}

		if out.String() != tc {
			t.Errorf("quoted: %s\ngot:  %q\nwant: %q", quoted, out.String(), tc)
		}
	}
}

func TestQuoteArgs_Evaluation(t *testing.T) {
	platform.RequireCommand(t, "/bin/sh")
	args := []string{
		"echo",
		"hello world",
		"$(whoami)",
		"it's",
		"\"quoted\"",
		"; rm -rf /",
	}

	quotedLine := QuoteArgs(args)
	// Verify evaluating with sh parses arguments back to original slice
	script := `eval 'set -- '` + QuotePOSIX(quotedLine) + `; for arg in "$@"; do printf "%s\0" "$arg"; done`
	cmd := exec.Command("/bin/sh", "-c", script)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to evaluate quoted args: %v", err)
	}

	parts := bytes.Split(out.Bytes(), []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}

	if len(parts) != len(args) {
		t.Fatalf("expected %d args, got %d: %q", len(args), len(parts), out.String())
	}

	for i, expected := range args {
		if string(parts[i]) != expected {
			t.Errorf("arg %d: got %q, want %q", i, string(parts[i]), expected)
		}
	}
}
