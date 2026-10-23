//go:build !darwin && !linux

package managedtools

import (
	"path/filepath"
	"strings"
	"testing"
)

// Managed installs need Unix private-directory semantics, so the other tests
// run on darwin and linux only. Elsewhere nothing can be installed.
func TestUnsupportedPlatform(t *testing.T) {
	data, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(data, Binding{"local:" + strings.Repeat("a", 64), "c"}, func() string { return "" }); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("managed tools on an unsupported platform: %v", err)
	}
}
