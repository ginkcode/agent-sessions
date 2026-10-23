//go:build linux

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMachineIDFallsBackToHostname(t *testing.T) {
	dir := t.TempDir()
	id, short := filepath.Join(dir, "machine-id"), filepath.Join(dir, "short")
	if err := os.WriteFile(id, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(short, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host := func() (string, error) { return "box", nil }
	if got, err := machineIDFrom([]string{filepath.Join(dir, "missing"), short, id}, host); err != nil || got != "0123456789abcdef0123456789abcdef" {
		t.Fatal(got, err)
	}
	if got, err := machineIDFrom([]string{short}, host); err != nil || got != "hostname:box" {
		t.Fatal(got, err)
	}
	for _, h := range []func() (string, error){
		func() (string, error) { return "", nil },
		func() (string, error) { return "", errors.New("no name") },
	} {
		if _, err := machineIDFrom(nil, h); err == nil {
			t.Fatal("identity without a machine ID or hostname")
		}
	}
}
