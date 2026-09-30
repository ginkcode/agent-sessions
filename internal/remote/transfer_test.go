package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/rpc"
)

func TestRandomArtifactToken(t *testing.T) {
	tok1, err := RandomArtifactToken("export")
	if err != nil {
		t.Fatalf("RandomArtifactToken failed: %v", err)
	}
	if !strings.HasPrefix(tok1, "export-") {
		t.Errorf("token %q does not have prefix 'export-'", tok1)
	}
	if !validTokenRe.MatchString(tok1) {
		t.Errorf("token %q does not match regex", tok1)
	}

	tok2, err := RandomArtifactToken("")
	if err != nil {
		t.Fatalf("RandomArtifactToken failed: %v", err)
	}
	if !strings.HasPrefix(tok2, "artifact-") {
		t.Errorf("token %q does not have default prefix 'artifact-'", tok2)
	}
	if tok1 == tok2 {
		t.Errorf("expected unique tokens, got %q == %q", tok1, tok2)
	}
}

func TestSession_StagingPath(t *testing.T) {
	s := NewTestSession("box", nil, &rpc.InitializeResult{
		Roots: paths.Roots{Cache: "/remote/home/.cache/agent-sessions"},
	})
	got := s.StagingPath("my-token")
	want := "/remote/home/.cache/agent-sessions/staging/my-token"
	if got != want {
		t.Errorf("StagingPath = %q, want %q", got, want)
	}

	sNil := NewTestSession("box", nil, nil)
	if p := sNil.StagingPath("token"); p != "" {
		t.Errorf("expected empty path for nil init, got %q", p)
	}
}

func TestSession_PutAndGetArtifact(t *testing.T) {
	stagingDir := t.TempDir()
	s := NewTestSession("box", nil, &rpc.InitializeResult{
		Roots: paths.Roots{Cache: stagingDir},
	})

	// Mock ssh command builder that executes local file write/read simulating transfer
	s.SetSSHCmdFunc(func(ctx context.Context, alias string, remoteCmd []string, opts SSHOptions) (*exec.Cmd, error) {
		// remoteCmd: [bin, "transfer", "put|get", token, ...flags]
		if len(remoteCmd) < 4 || remoteCmd[1] != "transfer" {
			t.Fatalf("unexpected remoteCmd: %v", remoteCmd)
		}
		action := remoteCmd[2]
		token := remoteCmd[3]
		if !strings.Contains(strings.Join(remoteCmd, " "), " --cache "+stagingDir) {
			t.Errorf("remoteCmd %v does not pass the session cache dir", remoteCmd)
		}

		switch action {
		case "put":
			// Helper script in sh: read stdin to target file, calculate sha256
			target := filepath.Join(stagingDir, "staging", token)
			_ = os.MkdirAll(filepath.Dir(target), 0700)
			script := `
				target="$1"
				cat > "$target"
				sum=$(sha256sum "$target" | cut -d' ' -f1)
				printf "sha256:%s\n" "$sum"
			`
			cmd := exec.CommandContext(ctx, "sh", "-c", script, "sh", target)
			return cmd, nil
		case "get":
			remove := slices.Contains(remoteCmd[4:], "--remove")
			target := filepath.Join(stagingDir, "staging", token)
			script := `
				target="$1"
				remove="$2"
				cat "$target"
				if [ "$remove" = "true" ]; then rm -f "$target"; fi
			`
			cmd := exec.CommandContext(ctx, "sh", "-c", script, "sh", target, map[bool]string{true: "true", false: "false"}[remove])
			return cmd, nil
		default:
			t.Fatalf("unexpected action: %s", action)
			return nil, nil
		}
	})

	ctx := context.Background()
	payload := []byte("hello, artifact bundle content here!")
	expectedHash := sha256.Sum256(payload)
	expectedHashHex := "sha256:" + hex.EncodeToString(expectedHash[:])

	// 1. PutArtifact
	token := "test-artifact-1"
	outHash, err := s.PutArtifact(ctx, token, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("PutArtifact failed: %v", err)
	}
	if outHash != expectedHashHex {
		t.Errorf("PutArtifact hash = %q, want %q", outHash, expectedHashHex)
	}

	// Verify file exists in staging
	stagedFile := filepath.Join(stagingDir, "staging", token)
	if _, err := os.Stat(stagedFile); err != nil {
		t.Fatalf("staged file does not exist: %v", err)
	}

	// 2. GetArtifact with remove=true
	var downloaded bytes.Buffer
	if err := s.GetArtifact(ctx, token, true, &downloaded); err != nil {
		t.Fatalf("GetArtifact failed: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), payload) {
		t.Errorf("GetArtifact content = %q, want %q", downloaded.String(), string(payload))
	}

	// Verify file removed
	if _, err := os.Stat(stagedFile); !os.IsNotExist(err) {
		t.Errorf("expected staged file to be removed, stat err: %v", err)
	}
}

func TestSession_TransferValidation(t *testing.T) {
	s := NewTestSession("box", nil, nil)
	ctx := context.Background()

	if _, err := s.PutArtifact(ctx, "../bad-token", strings.NewReader("")); err == nil {
		t.Error("expected error for path traversal token in PutArtifact")
	}

	var buf bytes.Buffer
	if err := s.GetArtifact(ctx, "../bad-token", false, &buf); err == nil {
		t.Error("expected error for path traversal token in GetArtifact")
	}
}
