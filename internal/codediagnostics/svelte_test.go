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

func TestSvelteEvidenceParser(t *testing.T) {
	for _, f := range goFixtures(t) {
		if !strings.HasPrefix(f.ID, "svelte-") || f.Expected == "version" || f.Expected == "help" || f.ID == "svelte-machine-findings" {
			continue
		}
		t.Run(f.ID, func(t *testing.T) {
			root := vetRoot(t, f.Files)
			text := strings.ReplaceAll(f.Stdout, f.Cwd, root.Project().Root)
			got, err := parseSvelte(root, "", []byte(text), f.ExitCode, 10000)
			bad := f.Expected == "failure" || f.Expected == "reject"
			if (err != nil) != bad {
				t.Fatalf("%+v %v", got, err)
			}
			if bad {
				return
			}
			if f.ID == "svelte-clean" && (len(got.diagnostics) != 0 || got.partial) {
				t.Fatal(got)
			}
			if f.ID == "svelte-findings" {
				d := got.diagnostics[2]
				if d.Range == nil || d.Range.Start != (Position{3, 11}) || d.Range.End != (Position{3, 18}) || d.Freshness != "uncertain" || d.Basis.Kind != "saved" {
					t.Fatal(d)
				}
				if got.diagnostics[3].Code != "2322" || got.diagnostics[3].Source != "svelte-check" {
					t.Fatal(got)
				}
			}
			if f.ID == "svelte-file-level" && got.diagnostics[0].Range != nil {
				t.Fatal(got)
			}
		})
	}
}

func svelteFiles(dir string) map[string]string {
	files := map[string]string{}
	for name, text := range map[string]string{
		"package.json": `{"devDependencies":{"svelte":"^5","svelte-check":"^4"}}`, "tsconfig.json": `{"include":["*.svelte"]}`, "svelte.config.js": "export default {};", "a.svelte": "é😀\r\nhello\n",
		"node_modules/svelte-check/package.json": `{"version":"4.7.6"}`, "node_modules/svelte/package.json": `{"version":"5.57.1"}`, "node_modules/typescript/package.json": `{"version":"5.9.3"}`,
		"node_modules/svelte-check/bin/svelte-check": "synthetic", "node_modules/svelte-check/bin/ts-version-check.js": "synthetic", "node_modules/svelte-check/dist/src/index.js": "synthetic", "node_modules/svelte/compiler/index.js": "synthetic", "node_modules/typescript/lib/typescript.js": "synthetic",
	} {
		files[filepath.ToSlash(filepath.Join(dir, name))] = text
	}
	for _, name := range []string{"@sveltejs/load-config/package.json", "@sveltejs/load-config/dist/src/index.js", "@jridgewell/trace-mapping/package.json", "@jridgewell/trace-mapping/dist/trace-mapping.umd.js", "chokidar/package.json", "chokidar/index.js", "picocolors/package.json", "picocolors/picocolors.js", "sade/package.json", "sade/lib/index.js"} {
		files[filepath.ToSlash(filepath.Join(dir, "node_modules", name))] = "synthetic identity"
	}
	return files
}

func sveltePlan(t *testing.T, files map[string]string) *SveltePlan {
	t.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("production platform gate")
	}
	root := vetRoot(t, files)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("never executed"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := DiscoverSvelte(root, []string{bin}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSvelteDiscovery(t *testing.T) {
	files := svelteFiles("frontend")
	for k, v := range svelteFiles("nested") {
		files[k] = v
	}
	p := sveltePlan(t, files)
	if !reflect.DeepEqual(p.packages, []string{"frontend", "nested"}) || !p.partial {
		t.Fatal(p)
	}
	c := p.commands[2]
	if !reflect.DeepEqual(c.Args, []string{filepath.Join(p.root.Project().Root, "frontend/node_modules/svelte-check/bin/svelte-check"), "--tsconfig", "./tsconfig.json", "--output", "machine-verbose"}) {
		t.Fatal(c)
	}
	if err := validateCommand(p.root, p.Tools(), c); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, text, code string }{
		{"frontend/node_modules/svelte/package.json", `{"version":"5.0.0"}`, "unsupported"},
		{"frontend/tsconfig.json", `{"extends":"../outside.json"}`, "unsupported"},
	} {
		root := vetRoot(t, files)
		if err := os.WriteFile(filepath.Join(root.Project().Root, tc.name), []byte(tc.text), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := DiscoverSvelte(root, []string{filepath.Dir(p.node)}, p.home)
		code(t, err, tc.code)
	}
	root := vetRoot(t, map[string]string{"package.json": `{"dependencies":{"svelte":"5"}}`})
	q := &SveltePlan{root: root}
	if err := q.discover(); err != nil || !q.partial || len(q.packages) != 0 {
		t.Fatal(q, err)
	}
	root = vetRoot(t, svelteFiles(""))
	if err := os.Remove(filepath.Join(root.Project().Root, "node_modules/typescript/package.json")); err != nil {
		t.Fatal(err)
	}
	_, err := DiscoverSvelte(root, []string{filepath.Dir(p.node)}, p.home)
	code(t, err, "notFound")
	_, err = DiscoverSvelte(root, []string{t.TempDir()}, p.home)
	code(t, err, "notFound")
}

func TestSvelteTraversalBounds(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 4100; i++ {
		files[fmt.Sprintf("file-%04d", i)] = ""
	}
	p := &SveltePlan{root: vetRoot(t, files)}
	if err := p.discover(); err != nil || !p.partial {
		t.Fatal(err)
	}
	p = &SveltePlan{root: vetRoot(t, map[string]string{"child/a": ""})}
	if err := os.Symlink(t.TempDir(), filepath.Join(p.root.Project().Root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := p.discover(); err != nil || !p.partial || len(p.packages) != 0 {
		t.Fatal(p, err)
	}
}

func TestSvelteSupervisor(t *testing.T) {
	for _, mode := range []string{"clean", "finding", "mixed", "config", "version", "stderr", "cancel", "limit", "created"} {
		t.Run(mode, func(t *testing.T) {
			files := svelteFiles("")
			if mode == "mixed" {
				for k, v := range svelteFiles("nested") {
					files[k] = v
				}
			}
			p := sveltePlan(t, files)
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
				text := "v22.23.2\n"
				switch {
				case calls == 1:
					if mode == "version" {
						text = "v99.0.0\n"
					}
				case calls%2 == 0:
					text = "svelte-check, 4.7.6\n"
				default:
					dir := p.commands[calls-1].Directory
					text = fmt.Sprintf("1 START %q\n1 COMPLETED 1 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS\n", filepath.Join(p.root.Project().Root, dir))
					switch mode {
					case "finding":
						text = svelteOutput(p.root, "a.svelte", `{"line":0,"character":1}`, `{"line":0,"character":3}`, "message")
					case "config":
						text = svelteOutput(p.root, "tsconfig.json", "null", "null", "broken")
					case "mixed":
						if calls == 5 {
							proc.exit = 1
						}
					case "stderr":
						_, _ = in.Stderr.Write([]byte("private stack"))
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
			if mode == "created" {
				if err := os.WriteFile(filepath.Join(p.root.Project().Root, "svelte.config.js"), []byte("never evaluated"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := p.Run(ctx, s, "synthetic")
			success := mode == "clean" || mode == "finding"
			if got.Replace != success || (!success && (got.Failure == nil || !got.Partial)) {
				t.Fatalf("%s: %+v", mode, got)
			}
			if mode == "created" && calls != 0 {
				t.Fatal("stale config launched")
			}
		})
	}
}

func svelteOutput(root AuthorizedRoot, path, start, end, message string) string {
	msg, _ := json.Marshal(message)
	return fmt.Sprintf("1 START %q\n1 {\"type\":\"WARNING\",\"filename\":%q,\"start\":%s,\"end\":%s,\"message\":%s}\n1 COMPLETED 1 FILES 0 ERRORS 1 WARNINGS 1 FILES_WITH_PROBLEMS\n", root.Project().Root, path, start, end, msg)
}

func TestSvelteHostileRanges(t *testing.T) {
	root := vetRoot(t, map[string]string{"a.svelte": "é😀\r\nhello\n"})
	for _, tc := range []struct {
		path, start, end string
		ranged, partial  bool
	}{
		{"a.svelte", `{"line":0,"character":1}`, `{"line":0,"character":3}`, true, false},
		{"a.svelte", `{"line":0,"character":2}`, `{"line":0,"character":3}`, false, false},
		{"a.svelte", `{"line":-1,"character":0}`, `{"line":0,"character":0}`, false, false},
		{"a.svelte", `{"line":1,"character":4}`, `{"line":1,"character":2}`, false, false},
		{"../escape", "null", "null", false, true},
	} {
		got, err := parseSvelte(root, "", []byte(svelteOutput(root, tc.path, tc.start, tc.end, "m")), 0, 10)
		if err != nil || got.partial != tc.partial {
			t.Fatal(got, err)
		}
		if !tc.partial && (got.diagnostics[0].Range != nil) != tc.ranged {
			t.Fatal(got)
		}
	}
	if _, err := parseSvelte(root, "", []byte(svelteOutput(root, "a.svelte", "null", "null", "m")), 0, 0); err == nil {
		t.Fatal("budget ignored")
	}
}
