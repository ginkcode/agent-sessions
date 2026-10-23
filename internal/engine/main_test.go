package engine

import (
	"os"
	"testing"
)

// Tests never run the developer's login shell; the ones that need a login
// PATH set loginShellPath themselves.
func TestMain(m *testing.M) {
	loginShellPath = func(string) []string { return nil }
	os.Exit(m.Run())
}
