package codediagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type goEvidence struct {
	ID, Tool, Cwd, Stdout, Stderr, Expected string
	ExitCode                                int
	Files                                   map[string]string
}

func goFixtures(t *testing.T) []goEvidence {
	t.Helper()
	b, err := os.ReadFile("testdata/checkers/evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	var fs []goEvidence
	if err = json.Unmarshal(b, &fs); err != nil {
		t.Fatal(err)
	}
	return fs
}

func vetRoot(t *testing.T, files map[string]string) AuthorizedRoot {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := AuthorizeRoot("synthetic", dir, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// jsonText is s escaped for use inside a JSON string: Windows roots carry
// backslashes into the recorded output.
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func TestGoVetEvidenceParser(t *testing.T) {
	for _, f := range goFixtures(t) {
		if f.Tool != "go" || !strings.HasPrefix(f.ID, "go-") || f.ID == "go-version" || f.ID == "go-text-findings" {
			continue
		}
		t.Run(f.ID, func(t *testing.T) {
			r := vetRoot(t, f.Files)
			// Replace only the recorded synthetic cwd, never arbitrary output paths.
			stdout := strings.ReplaceAll(f.Stdout, f.Cwd, jsonText(r.Project().Root))
			got, err := parseGoVet(r, "", []byte(stdout), 10000)
			if f.ID == "go-truncated" || f.Expected == "failure" {
				if err == nil {
					t.Fatal("failure accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if f.ID == "go-multiple-packages" && len(got.diagnostics) != 2 {
				t.Fatalf("consecutive objects: %+v", got)
			}
			if f.ID == "go-clean" && (len(got.diagnostics) != 0 || got.partial) {
				t.Fatalf("clean: %+v", got)
			}
			if f.ID == "go-findings" {
				if len(got.diagnostics) != 2 {
					t.Fatalf("%+v", got)
				}
				d := got.diagnostics[0]
				if d.Range == nil || d.Range.Start != (Position{2, 36}) || d.Range.End != (Position{2, 38}) || d.Freshness != "uncertain" {
					t.Fatalf("%+v", d)
				}
			}
		})
	}
}

func TestGoVetPositionsAndHostileOutput(t *testing.T) {
	r := vetRoot(t, map[string]string{"a.go": "é😀\r\nhello\n"})
	for _, tc := range []struct {
		pos, end               string
		ranged, issue, partial bool
	}{
		{"a.go:1:3", "a.go:1:7", true, false, false}, {"a.go:1:2", "", false, true, false}, {"a.go:1:99", "", false, true, false}, {"a.go:2:1", "", true, false, false}, {"a.go", "", false, false, false}, {"a.go:2:4", "a.go:2:2", false, true, false}, {"../escape.go:1:1", "", false, false, true}, {`C:\outside\a.go:1:1`, "", false, false, true},
	} {
		record := map[string]any{"p": map[string]any{"printf": []goVetRecord{{tc.pos, tc.end, "message\nplain"}}}}
		b, _ := json.Marshal(record)
		got, err := parseGoVet(r, "", b, 10)
		if err != nil || got.partial != tc.partial {
			t.Fatalf("%+v %v", got, err)
		}
		if tc.partial {
			continue
		}
		d := got.diagnostics[0]
		if (d.Range != nil) != tc.ranged || (d.LocationIssue != "") != tc.issue {
			t.Fatalf("%s: %+v", tc.pos, d)
		}
	}
	for _, s := range []string{"", "null", "[]", `{"p":null}`, `{"p":{"x":[{"message":"m","unknown":1}]}}`, `{} garbage`} {
		if _, err := parseGoVet(r, "", []byte(s), 10); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	// Rooted conversion refuses oversized saved text instead of guessing a range.
	large := vetRoot(t, map[string]string{"large.go": strings.Repeat("a", (5<<20)+1)})
	largeJSON := []byte(`{"p":{"printf":[{"posn":"large.go:1:1","message":"m"}]}}`)
	largeResult, err := parseGoVet(large, "", largeJSON, 10)
	if err != nil || !largeResult.partial || len(largeResult.diagnostics) != 1 || largeResult.diagnostics[0].Range != nil || largeResult.diagnostics[0].LocationIssue != "invalidRange" {
		t.Fatalf("bounded read: %+v %v", largeResult, err)
	}
	b := []byte(`{"p":{"printf":[{"posn":"a.go","message":"m"}]}}`)
	if _, err := parseGoVet(r, "", b, 0); err == nil {
		t.Fatal("budget ignored")
	}
	path, l, c, ok := vetLocation(`C:\space dir\a.go:2:3`)
	if !ok || path != `C:\space dir\a.go` || l != 2 || c != 3 {
		t.Fatal("suffix parse")
	}
}

func fakeGoInstallation(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"bin/go", "VERSION", "go.env", "pkg/tool/darwin_arm64/vet", "pkg/tool/darwin_arm64/compile", "pkg/tool/darwin_arm64/asm", "pkg/tool/darwin_arm64/link"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("synthetic never executable code"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "bin")
}

func goPlan(t *testing.T, files map[string]string) *GoVetPlan {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("production platform gate")
	}
	root := vetRoot(t, files)
	cache := t.TempDir()
	p, err := DiscoverGoVet(root, []string{fakeGoInstallation(t)}, GoVetEnvironment{cache, cache, cache})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGoVetDiscovery(t *testing.T) {
	p := goPlan(t, map[string]string{"go.mod": "module synthetic\ngo 1.26.8\n", "nested/go.mod": "module nested\n", "nested/go.sum": "", "vendor/skip/go.mod": "module skip\n", "go.work": "go 1.26.8\nuse ../outside\n"})
	if !reflect.DeepEqual(p.modules, []string{"", "nested"}) || !p.partial || len(p.configs) != 3 {
		t.Fatalf("%+v", p)
	}
	if !reflect.DeepEqual(p.commands[1].Args, []string{"vet", "-json", "./..."}) {
		t.Fatal("argv")
	}
	fp, err := Fingerprint(p.root, p.Tools())
	if err != nil {
		t.Fatal(err)
	}
	tools := p.Tools()
	tools[1].Policy += "changed launch policy"
	other, err := Fingerprint(p.root, tools)
	if err != nil || fp == other {
		t.Fatal("policy not bound")
	}
	if err := validateCommand(p.root, p.Tools(), p.commands[1]); err != nil {
		t.Fatal(err)
	}
	changed := p.commands[1]
	changed.Args = []string{"vet", "./..."}
	code(t, validateCommand(p.root, p.Tools(), changed), "approvalChanged")
	if err := os.WriteFile(filepath.Join(filepath.Dir(p.executable), "../pkg/tool/darwin_arm64/vet"), []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	changedFP, err := Fingerprint(p.root, p.Tools())
	if err != nil || changedFP == fp {
		t.Fatal("toolchain helper not bound")
	}
}

func TestGoVetDiscoveryBounds(t *testing.T) {
	r := vetRoot(t, map[string]string{"go.mod": "module parent\n", "child/file.go": "package child"})
	child, err := AuthorizeRoot("synthetic", filepath.Join(r.Project().Root, "child"), func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	p := &GoVetPlan{root: child}
	if err = p.discover(); err != nil || len(p.modules) != 0 {
		t.Fatal("promoted parent")
	}
	p = &GoVetPlan{root: r}
	if err = os.Symlink(t.TempDir(), filepath.Join(r.Project().Root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err = p.discover(); err != nil || !p.partial {
		t.Fatal("symlink not partial")
	}
	files := map[string]string{"go.mod": "module test\n"}
	for i := 0; i < 4100; i++ {
		files[fmt.Sprintf("files/file-%04d", i)] = ""
	}
	p = &GoVetPlan{root: vetRoot(t, files)}
	if err = p.discover(); err != nil || !p.partial {
		t.Fatal("unbounded discovery", err)
	}
	p = &GoVetPlan{root: vetRoot(t, map[string]string{"go.mod": "module test\nreplace example.com/x => ../outside\n"})}
	code(t, p.discover(), "unsupported")
}

func TestGoVetSupervisorOutcomes(t *testing.T) {
	for _, mode := range []string{"findings", "clean", "version", "nonzero", "stderr", "malformed", "mixed", "cancel", "limit", "config-created"} {
		t.Run(mode, func(t *testing.T) {
			files := map[string]string{"go.mod": "module synthetic\n", "a.go": "package synthetic\n"}
			if mode == "mixed" {
				files["nested/go.mod"] = "module nested\n"
			}
			p := goPlan(t, files)
			tr := NewTrust(nil)
			fp, err := Fingerprint(p.root, p.Tools())
			if err != nil {
				t.Fatal(err)
			}
			approve(t, tr, p.root, p.Tools(), fp, "session")
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := NewSupervisor(tr, SupervisorOptions{Start: func(in Invocation) (Process, error) {
				calls++
				proc := newFakeProcess()
				text := "{}\n"
				if calls == 1 {
					text = "go version go1.26.8 " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
					if mode == "version" {
						// Any Go version is accepted; another platform is not.
						text = "go version go1.99 plan9/386\n"
					}
				} else {
					switch mode {
					case "findings":
						text = `{"p":{"printf":[{"posn":"a.go:1:1","message":"synthetic"}]}}`
					case "nonzero":
						proc.exit = 1
					case "stderr":
						_, _ = in.Stderr.Write([]byte("private error"))
					case "malformed":
						text = "{"
					case "mixed":
						if calls == 3 {
							proc.exit = 1
						}
					case "cancel":
						cancel()
					case "limit":
						text = strings.Repeat("x", 9000)
					}
				}
				_, _ = in.Stdout.Write([]byte(text))
				_ = proc.Terminate()
				return proc, nil
			}})
			defer s.Close()
			if mode == "config-created" {
				if err = os.WriteFile(filepath.Join(p.root.Project().Root, "go.sum"), []byte(""), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := p.Run(ctx, s, "synthetic-run")
			success := mode == "clean" || mode == "findings"
			if got.Replace != success {
				t.Fatalf("%s: %+v", mode, got)
			}
			if mode == "findings" && (len(got.Diagnostics) != 1 || got.Diagnostics[0].Freshness != "uncertain") {
				t.Fatal(got)
			}
			if !success && (!got.Partial || got.Failure == nil) {
				t.Fatal("failure could clear old results", got)
			}
			if mode == "config-created" && calls != 0 {
				t.Fatal("launched stale proposal")
			}
		})
	}
}
