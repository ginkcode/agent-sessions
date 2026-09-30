package manage

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two processes (two stores) changing different keys at once must both keep
// their change, and keys another version wrote must survive.
func TestConfigUpdateConcurrentStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "# mine\n[manage]\nenabled = false\nfuture_key = 7\n\n[other]\nx = 1\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	a, b := NewConfigStore(path), NewConfigStore(path)

	// Each round both stores read the file and hold their change for a
	// moment before writing it, so without the file lock one change is lost
	// in nearly every round.
	for round := range 20 {
		if _, err := a.Update(func(c *Config) { *c = Config{} }); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, u := range []struct {
			cs  *ConfigStore
			set func(*Config)
		}{
			{a, func(c *Config) { c.Enabled = true }},
			{b, func(c *Config) { c.AllowRestore = true }},
		} {
			wg.Go(func() {
				if _, err := u.cs.Update(func(c *Config) {
					time.Sleep(5 * time.Millisecond)
					u.set(c)
				}); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		cfg, err := b.Load()
		if err != nil {
			t.Fatal(err)
		}
		if want := (Config{Enabled: true, AllowRestore: true}); cfg != want {
			t.Fatalf("round %d: config = %+v, want %+v", round, cfg, want)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# mine", "future_key = 7", "[other]", "x = 1"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config lost %q:\n%s", want, data)
		}
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("config mode: %v %v", fi, err)
	}
}
