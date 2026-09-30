package version

import "testing"

func TestCurrent(t *testing.T) {
	if v := Current(); v == "" {
		t.Fatal("Current() returned empty string")
	}
}
