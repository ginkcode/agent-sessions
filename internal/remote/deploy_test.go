package remote

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runUnpack executes the real unpack script with /bin/sh, as the remote
// login shell would, feeding gz on stdin.
func runUnpack(t *testing.T, targetDir, targetBin, sum string, gz []byte) (string, error) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", unpackScript(targetDir, targetBin, sum))
	cmd.Stdin = bytes.NewReader(gz)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stderr.String(), err
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnpackScript_InstallsVerifiedArchive(t *testing.T) {
	// A space in the path catches quoting mistakes.
	dir := filepath.Join(t.TempDir(), "home dir", "0.1.0-abcd")
	bin := filepath.Join(dir, "agent-sessions-cli")
	payload := []byte("#!/bin/sh\necho ok\n")
	gz := gzipBytes(t, payload)
	h := sha256.Sum256(gz)

	if stderr, err := runUnpack(t, dir, bin, hex.EncodeToString(h[:]), gz); err != nil {
		t.Fatalf("unpack: %v: %s", err, stderr)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("installed %q, want %q", got, payload)
	}
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, want 0700", info.Mode().Perm())
	}
	for _, leftover := range []string{bin + ".tmp", bin + ".tmp.gz"} {
		if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left behind (err %v)", leftover, err)
		}
	}
}

func TestUnpackScript_RejectsChecksumMismatch(t *testing.T) {
	if _, err := exec.LookPath("sha256sum"); err != nil {
		if _, err := exec.LookPath("shasum"); err != nil {
			t.Skip("no sha256sum or shasum on this machine")
		}
	}
	dir := filepath.Join(t.TempDir(), "srv")
	bin := filepath.Join(dir, "agent-sessions-cli")
	gz := gzipBytes(t, []byte("payload"))
	wrong := hex.EncodeToString(make([]byte, sha256.Size))

	stderr, err := runUnpack(t, dir, bin, wrong, gz)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 86 {
		t.Fatalf("err = %v (stderr %q), want exit 86", err, stderr)
	}
	if _, err := os.Stat(bin); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("binary installed despite checksum mismatch")
	}
}
