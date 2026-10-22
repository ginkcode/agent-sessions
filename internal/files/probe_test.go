package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestProbeObservations(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a")
	if err := os.WriteFile(file, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := func(p string) DiskObservation {
		t.Helper()
		out, err := Probe(context.Background(), root, []string{p})
		if err != nil {
			t.Fatal(err)
		}
		return out.Observations[0]
	}
	if got := probe("a"); got.State != "present" || got.Revision != revisionOf([]byte("first")) {
		t.Fatal(got)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if got := probe("a"); got.State != "absent" || got.Revision != "" {
		t.Fatal(got)
	}
	if err := os.WriteFile(file, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probe("a"); got.State != "present" || got.Revision != revisionOf([]byte("second")) {
		t.Fatal(got)
	}
	// No inferred rename: old absent + new present carries no linking identity.
	if err := os.Rename(file, filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if got := probe("a"); got.State != "absent" {
		t.Fatal(got)
	}
	if got := probe("b"); got.State != "present" {
		t.Fatal(got)
	}
	if got := probe("missing/leaf"); got.State != "unknown" {
		t.Fatal(got)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if got := probe("escape/leaf"); got.State != "unknown" {
		t.Fatal(got)
	}
	if err := os.Symlink("missing", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if got := probe("dangling"); got.State != "unknown" {
		t.Fatal(got)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := probe("directory"); got.State != "unknown" {
		t.Fatal(got)
	}
	if err := os.Chmod(filepath.Join(root, "b"), 0o000); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(filepath.Join(root, "b")); err == nil {
		_ = f.Close()
		t.Log("privileged user can read mode-000 fixture")
	} else if got := probe("b"); got.State != "unknown" {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := Probe(ctx, root, []string{"a"})
	if err != nil || out.Observations[0].State != "unknown" {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestProbeBoundsAndRevisions(t *testing.T) {
	root := t.TempDir()
	for _, paths := range [][]string{nil, {"../a"}, {"/a"}, {"a/../b"}, {"a\\b"}, {"a", "a"}, make([]string, MaxProbePaths+1)} {
		if _, err := Probe(context.Background(), root, paths); !errors.Is(err, ErrInvalidProbe) {
			t.Fatalf("%q: %v", paths, err)
		}
	}
	f, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(MaxReadBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	out, err := Probe(context.Background(), root, []string{"large"})
	if err != nil || !reflect.DeepEqual(out.Observations, []DiskObservation{{Path: "large", State: "unknown", Reason: "limit"}}) {
		t.Fatalf("%+v %v", out, err)
	}
	// Identical bytes after recreation are intentionally the same content revision,
	// not proof of persistent file identity or uninterrupted existence.
	if err := os.WriteFile(filepath.Join(root, "same"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, _ := Probe(context.Background(), root, []string{"same"})
	if err := os.Remove(filepath.Join(root, "same")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "same"), []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := Probe(context.Background(), root, []string{"same"})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("%+v %+v", a, b)
	}
}
