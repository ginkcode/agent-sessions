//go:build darwin || linux

package managedtools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBuildPolicyExactCommands(t *testing.T) {
	r := goplsReleases[0] // needs go1.27.0
	for _, scenario := range []string{"ok", "old-go", "development-go", "wrong-sum", "wrong-mod-sum", "download-fails", "untrusted-go"} {
		t.Run(scenario, func(t *testing.T) {
			stage, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(stage, "trusted-go")
			if err = os.WriteFile(path, []byte("fake Go"), 0o700); err != nil {
				t.Fatal(err)
			}
			sum, err := fileDigest(path)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "untrusted-go" {
				sum = strings.Repeat("0", 64)
			}
			var calls [][]string
			command := func(ctx context.Context, exe, cwd string, env []string, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{}, args...))
				if exe != path || cwd != filepath.Join(stage, "work") || ctx == nil {
					t.Fatal("wrong command scope")
				}
				values := map[string]string{}
				for _, entry := range env {
					k, v, _ := strings.Cut(entry, "=")
					values[k] = v
				}
				for k, want := range map[string]string{"GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOPROXY": goProxy, "GOSUMDB": "sum.golang.org", "GOVCS": "*:off", "CGO_ENABLED": "0", "GONOSUMDB": "", "GOPRIVATE": "", "GONOPROXY": "", "GOINSECURE": "", "GOFLAGS": "", "GOTELEMETRY": "off"} {
					if got, ok := values[k]; !ok || got != want {
						t.Fatalf("%s=%q", k, got)
					}
				}
				for _, k := range []string{"HOME", "TMPDIR", "GOMODCACHE", "GOCACHE", "GOPATH"} {
					if !strings.HasPrefix(values[k], cwd+string(os.PathSeparator)) {
						t.Fatal("nonprivate cache", k)
					}
				}
				if values["GOBIN"] != stage || values["PATH"] != filepath.Dir(path) {
					t.Fatal("unsafe destination/path")
				}
				switch args[0] {
				case "version":
					v := "go1.27.0"
					if scenario == "old-go" {
						v = "go1.26.8"
					}
					if scenario == "development-go" {
						v = "devel"
					}
					return []byte("go version " + v + " " + runtime.GOOS + "/" + runtime.GOARCH), nil
				case "mod":
					if scenario == "download-fails" {
						return nil, errors.New("offline")
					}
					s, m := r.Sum, r.ModSum
					if scenario == "wrong-sum" {
						s = "bad"
					}
					if scenario == "wrong-mod-sum" {
						m = "bad"
					}
					return json.Marshal(map[string]string{"Path": goplsModule, "Version": r.Version, "Sum": s, "GoModSum": m})
				case "install":
					return nil, nil
				default:
					t.Fatal(args)
					return nil, nil
				}
			}
			err = buildGoWith(context.Background(), stage, TrustedGo{path, sum}, r, command)
			if (err == nil) != (scenario == "ok") {
				t.Fatalf("result: %v", err)
			}
			expected := [][]string{{"version"}, {"mod", "download", "-json", goplsModule + "@" + r.Version}, {"install", "-trimpath", goplsModule + "@" + r.Version}}
			n := 3
			switch scenario {
			case "untrusted-go":
				n = 0
			case "old-go", "development-go":
				n = 1
			case "wrong-sum", "wrong-mod-sum", "download-fails":
				n = 2
			}
			if len(calls) != n || (n > 0 && !reflect.DeepEqual(calls, expected[:n])) {
				t.Fatalf("commands: %v", calls)
			}
			if _, err = os.Stat(filepath.Join(stage, "work")); !os.IsNotExist(err) {
				t.Fatal("cache leaked")
			}
		})
	}
}
