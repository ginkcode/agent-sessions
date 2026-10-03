package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

func TestTransfer_PutAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	roots := paths.Roots{Cache: filepath.Join(tmpDir, "cache")}

	payload := "hello remote artifact transfer 123456789"
	var putStdout, putStderr bytes.Buffer

	// 1. Put artifact
	code := transferCmd(
		[]string{"put", "test-token-123"},
		roots,
		&putStdout,
		&putStderr,
		strings.NewReader(payload),
	)
	if code != 0 {
		t.Fatalf("transfer put failed with code %d, stderr: %s", code, putStderr.String())
	}
	if !strings.HasPrefix(putStdout.String(), "sha256:") {
		t.Errorf("expected sha256 output, got %q", putStdout.String())
	}

	// Verify file exists on disk with 0600 permissions
	stagedPath := filepath.Join(roots.Cache, "staging", "test-token-123")
	info, err := os.Stat(stagedPath)
	if err != nil {
		t.Fatalf("staged file does not exist: %v", err)
	}
	if platform.ModeBits && info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}

	// 2. Get artifact
	var getStdout, getStderr bytes.Buffer
	code = transferCmd(
		[]string{"get", "test-token-123"},
		roots,
		&getStdout,
		&getStderr,
		strings.NewReader(""),
	)
	if code != 0 {
		t.Fatalf("transfer get failed with code %d, stderr: %s", code, getStderr.String())
	}
	if getStdout.String() != payload {
		t.Errorf("got payload %q, want %q", getStdout.String(), payload)
	}

	// 3. Get with --remove
	var getRemStdout, getRemStderr bytes.Buffer
	code = transferCmd(
		[]string{"get", "test-token-123", "--remove"},
		roots,
		&getRemStdout,
		&getRemStderr,
		strings.NewReader(""),
	)
	if code != 0 {
		t.Fatalf("transfer get --remove failed with code %d, stderr: %s", code, getRemStderr.String())
	}
	if getRemStdout.String() != payload {
		t.Errorf("got payload %q, want %q", getRemStdout.String(), payload)
	}

	// Verify file is removed
	if _, err := os.Stat(stagedPath); !os.IsNotExist(err) {
		t.Errorf("expected file to be removed after --remove, got err: %v", err)
	}
}

func TestTransfer_InvalidToken(t *testing.T) {
	tmpDir := t.TempDir()
	roots := paths.Roots{Cache: tmpDir}

	badTokens := []string{
		"../escape",
		"bad/slash",
		"bad\\backslash",
		"",
		"has space",
	}

	for _, token := range badTokens {
		var out, errOut bytes.Buffer
		code := transferCmd(
			[]string{"put", token},
			roots,
			&out,
			&errOut,
			strings.NewReader("data"),
		)
		if code != 2 {
			t.Errorf("token %q: expected exit code 2, got %d", token, code)
		}
	}
}

func TestTransfer_CacheFlagOverridesRoots(t *testing.T) {
	roots := paths.Roots{Cache: filepath.Join(t.TempDir(), "default")}
	override := filepath.Join(t.TempDir(), "override")

	var out, errOut bytes.Buffer
	code := transferCmd([]string{"put", "tok-1", "--cache", override}, roots, &out, &errOut, strings.NewReader("payload"))
	if code != 0 {
		t.Fatalf("put exit %d: %s", code, errOut.String())
	}
	if _, err := os.Stat(filepath.Join(override, "staging", "tok-1")); err != nil {
		t.Fatalf("artifact not in overridden cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(roots.Cache, "staging")); !os.IsNotExist(err) {
		t.Fatalf("default staging dir was used: %v", err)
	}

	out.Reset()
	code = transferCmd([]string{"get", "tok-1", "--remove", "--cache", override}, roots, &out, &errOut, strings.NewReader(""))
	if code != 0 || out.String() != "payload" {
		t.Fatalf("get exit %d, out %q: %s", code, out.String(), errOut.String())
	}

	if code := transferCmd([]string{"get", "tok-1", "--cache", "relative/dir"}, roots, &out, &errOut, nil); code != 2 {
		t.Fatalf("relative --cache exit %d, want 2", code)
	}
}
