//go:build darwin || linux

package managedtools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type entry struct {
	name, body, link string
	kind             byte
	mode             int64
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.kind, Mode: e.mode, Linkname: e.link, Size: int64(len(e.body))}
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pin(data []byte) artifact {
	sum := sha256.Sum256(data)
	return artifact{"https://example.invalid/a.tar.gz", hex.EncodeToString(sum[:])}
}

// serve answers every download with data and records the URLs requested.
func serve(i *Installer, data []byte) *[]string {
	var urls []string
	i.fetch = func(_ context.Context, url string, w io.Writer) error {
		urls = append(urls, url)
		_, err := w.Write(data)
		return err
	}
	return &urls
}

// replace swaps a catalog entry for the duration of a test.
func replace(t *testing.T, m map[string]artifact, a artifact) {
	t.Helper()
	old := m[platform()]
	m[platform()] = a
	t.Cleanup(func() { m[platform()] = old })
}

func TestArchiveIntegrityBeforeExtraction(t *testing.T) {
	i, _ := fixture(t)
	data := tarGz(t, entry{name: "top/ruff", body: "binary", mode: 0o4777})
	work, dest := t.TempDir(), t.TempDir()
	serve(i, data)
	bad := pin(data)
	bad.SHA256 = strings.Repeat("0", 64)
	if err := i.installArchive(context.Background(), bad, work, dest); err == nil {
		t.Fatal("mismatched archive accepted")
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatal("extracted before verification")
	}
	if err := i.installArchive(context.Background(), pin(data), t.TempDir(), dest); err != nil {
		t.Fatal(err)
	}
	// The top directory is stripped; setuid and group/other write are dropped.
	st, err := os.Stat(filepath.Join(dest, "ruff"))
	if err != nil || st.Mode().Perm() != 0o755 || st.Mode()&os.ModeSetuid != 0 {
		t.Fatal(st, err)
	}
}

func TestExtractRefusesUnsafeEntries(t *testing.T) {
	for name, entries := range map[string][]entry{
		"absolute":        {{name: "/etc/x"}},
		"parent":          {{name: "top/../../x"}},
		"backslash":       {{name: "top\\..\\x"}},
		"escaping link":   {{name: "top/l", kind: tar.TypeSymlink, link: "../../x"}},
		"absolute link":   {{name: "top/l", kind: tar.TypeSymlink, link: "/etc"}},
		"through link":    {{name: "top/d/", kind: tar.TypeDir}, {name: "top/l", kind: tar.TypeSymlink, link: "d"}, {name: "top/l/x"}},
		"overwrite link":  {{name: "top/l", kind: tar.TypeSymlink, link: "x"}, {name: "top/l"}},
		"device":          {{name: "top/dev", kind: tar.TypeChar}},
		"fifo":            {{name: "top/fifo", kind: tar.TypeFifo}},
		"hardlink absent": {{name: "top/h", kind: tar.TypeLink, link: "top/missing"}},
		"hardlink parent": {{name: "top/h", kind: tar.TypeLink, link: "../x"}},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "a.tar.gz")
			if err := os.WriteFile(file, tarGz(t, entries...), 0o600); err != nil {
				t.Fatal(err)
			}
			outer := t.TempDir()
			dest := filepath.Join(outer, "dest")
			if err := os.Mkdir(dest, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := extractTarGz(context.Background(), file, dest, 1); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if entries, _ := os.ReadDir(outer); len(entries) != 1 {
				t.Fatal("wrote outside the destination")
			}
		})
	}
	// Links that stay inside are kept, as Node.js's archive needs them.
	file := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(file, tarGz(t,
		entry{name: "top/lib/cli.js", body: "x"},
		entry{name: "top/bin/npm", kind: tar.TypeSymlink, link: "../lib/cli.js"},
		entry{name: "top/bin/copy", kind: tar.TypeLink, link: "top/lib/cli.js"},
	), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := extractTarGz(context.Background(), file, dest, 1); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bin/npm", "bin/copy"} {
		if data, err := os.ReadFile(filepath.Join(dest, f)); err != nil || string(data) != "x" {
			t.Fatal(f, err)
		}
	}
}

func TestFetchRefusesPlainHTTP(t *testing.T) {
	if err := fetchHTTPS(context.Background(), "http://example.invalid/x", io.Discard); err == nil {
		t.Fatal("plain HTTP accepted")
	}
}

func TestRuffInstall(t *testing.T) {
	i, data := fixture(t)
	archive := tarGz(t, entry{name: "ruff-x/ruff", body: "ruff binary", mode: 0o755})
	replace(t, ruffArchives, pin(archive))
	p, err := i.Propose("ruff")
	if err != nil {
		t.Fatal(err)
	}
	if p.Tool != "ruff" || p.Version != ruffVersion || p.Source != ruffArchives[platform()].URL || p.Integrity != "sha256:"+ruffArchives[platform()].SHA256 || p.Note != "" {
		t.Fatalf("%+v", p)
	}
	if entries, _ := os.ReadDir(data); len(entries) != 0 {
		t.Fatal("preconsent IO")
	}
	urls := serve(i, archive)
	path, err := i.Install(context.Background(), i.binding, p.Token)
	if err != nil || path != filepath.Join(p.Destination, "ruff") || len(*urls) != 1 {
		t.Fatal(path, err, *urls)
	}
	if got, err := i.Discover("ruff"); err != nil || got != path {
		t.Fatal(got, err)
	}
	if dirs := i.Dirs(); !slices.Equal(dirs, []string{p.Destination}) {
		t.Fatal(dirs)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("ruff"); err == nil || len(i.Dirs()) != 0 {
		t.Fatal("changed binary accepted")
	}
}

func TestVtslsInstallUsesManagedNodeAndLockfile(t *testing.T) {
	i, _ := fixture(t)
	node := tarGz(t,
		entry{name: "node-x/bin/node", body: "node binary", mode: 0o755},
		entry{name: "node-x/lib/node_modules/npm/bin/npm-cli.js", body: "npm"},
	)
	replace(t, nodeArchives, pin(node))
	p, err := i.Propose("vtsls")
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != vtslsVersion || !strings.Contains(p.Source, npmRegistry) || !strings.Contains(p.Integrity, "install scripts off") ||
		!strings.Contains(p.Note, nodeArchives[platform()].URL) || !strings.Contains(p.Note, nodeArchives[platform()].SHA256) {
		t.Fatalf("%+v", p)
	}
	nodeDir := filepath.Join(i.root, installDir("node", nodeVersion))
	serve(i, node)
	var ran bool
	i.command = func(_ context.Context, exe, cwd string, env []string, args ...string) ([]byte, error) {
		ran = true
		if exe != filepath.Join(nodeDir, "bin", "node") || !slices.Equal(args, []string{filepath.Join(nodeDir, "lib/node_modules/npm/bin/npm-cli.js"), "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error"}) {
			t.Fatal(exe, args)
		}
		values := map[string]string{}
		for _, e := range env {
			k, v, _ := strings.Cut(e, "=")
			values[k] = v
		}
		if values["npm_config_ignore_scripts"] != "true" || values["npm_config_registry"] != npmRegistry || values["NODE_OPTIONS"] != "" ||
			!strings.HasPrefix(values["HOME"], cwd+string(os.PathSeparator)) || !strings.HasPrefix(values["npm_config_userconfig"], cwd+string(os.PathSeparator)) ||
			!strings.HasPrefix(values["npm_config_globalconfig"], cwd+string(os.PathSeparator)) {
			t.Fatal(env)
		}
		// npm exits with "double-loading config" when both name one file.
		if values["npm_config_userconfig"] == values["npm_config_globalconfig"] {
			t.Fatal("user and global npm config are the same file")
		}
		for _, rc := range []string{values["npm_config_userconfig"], values["npm_config_globalconfig"]} {
			if data, err := os.ReadFile(rc); err != nil || len(data) != 0 {
				t.Fatal("npm config not an empty file", rc, err)
			}
		}
		if lock, err := os.ReadFile(filepath.Join(cwd, "package-lock.json")); err != nil || !bytes.Equal(lock, vtslsLockfile) {
			t.Fatal("lockfile not placed", err)
		}
		writeTree(t, cwd, map[string]string{
			"node_modules/@vtsls/language-server/package.json":   `{"name":"@vtsls/language-server","version":"` + vtslsVersion + `"}`,
			"node_modules/@vtsls/language-server/bin/vtsls.js":   "vtsls",
			"node_modules/typescript/package.json":               `{"name":"typescript","version":"` + vtslsTypeScript + `"}`,
			"node_modules/@vtsls/language-server/dist/main.js":   "main",
			"node_modules/typescript/lib/typescript.js":          "ts",
			"node_modules/@vtsls/language-server/node_modules/x": "",
		})
		if err := os.Mkdir(filepath.Join(cwd, "node_modules", ".bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		return nil, os.Symlink("../@vtsls/language-server/bin/vtsls.js", filepath.Join(cwd, "node_modules", ".bin", "vtsls"))
	}
	path, err := i.Install(context.Background(), i.binding, p.Token)
	if err != nil || !ran || path != filepath.Join(p.Destination, filepath.FromSlash(vtslsMain)) {
		t.Fatal(path, err, ran)
	}
	if _, err := os.Stat(filepath.Join(p.Destination, ".work")); !os.IsNotExist(err) {
		t.Fatal("npm scratch kept")
	}
	if got, err := i.Discover("node"); err != nil || got != filepath.Join(nodeDir, "bin", "node") {
		t.Fatal(got, err)
	}
	want := []string{filepath.Join(p.Destination, "node_modules", ".bin"), filepath.Join(nodeDir, "bin")}
	if dirs := i.Dirs(); !slices.Equal(dirs, want) {
		t.Fatal(dirs)
	}
	// With Node.js in place, a new proposal downloads nothing else.
	if p, err = i.Propose("vtsls"); err != nil || p.Note != "" {
		t.Fatal(p, err)
	}
	// A lockfile resolving to other versions is refused.
	if err := os.WriteFile(filepath.Join(p.Destination, "node_modules/typescript/package.json"), []byte(`{"name":"typescript","version":"6.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := i.Discover("vtsls"); err == nil {
		t.Fatal("unexpected TypeScript accepted")
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A failed step is named so the user knows what broke; the cause, which may
// hold process output or paths, stays inside.
func TestInstallFailureNamesTheStepOnly(t *testing.T) {
	i, _ := fixture(t)
	node := tarGz(t,
		entry{name: "node-x/bin/node", body: "node binary", mode: 0o755},
		entry{name: "node-x/lib/node_modules/npm/bin/npm-cli.js", body: "npm"},
	)
	replace(t, nodeArchives, pin(node))
	serve(i, node)
	p, err := i.Propose("vtsls")
	if err != nil {
		t.Fatal(err)
	}
	i.command = func(context.Context, string, string, []string, ...string) ([]byte, error) {
		return []byte("npm ERR! secret output"), errors.New("exit status 1: /private/path")
	}
	_, err = i.Install(context.Background(), i.binding, p.Token)
	var f *InstallFailure
	if !errors.As(err, &f) || f.Step != "installing the vtsls packages with npm" || errors.Unwrap(err) != nil ||
		strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/private/path") {
		t.Fatal(err)
	}
}

// Like Zed, a usable host Node.js runs npm; Node.js is then not downloaded.
func TestVtslsInstallReusesHostNode(t *testing.T) {
	i, _ := fixture(t)
	host := filepath.Join(filepath.Dir(i.root), "host-node")
	writeTree(t, host, map[string]string{"bin/node": "node", "lib/node_modules/npm/bin/npm-cli.js": "npm"})
	if err := os.Chmod(filepath.Join(host, "bin", "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	i.UseHostNode(func() (string, string) { return host, "22.23.2" })
	i.fetch = func(context.Context, string, io.Writer) error { t.Fatal("downloaded"); return nil }
	p, err := i.Propose("vtsls")
	if err != nil || !strings.Contains(p.Note, "Uses Node.js 22.23.2 from "+host) {
		t.Fatal(p, err)
	}
	var exe string
	i.command = func(_ context.Context, node, cwd string, _ []string, args ...string) ([]byte, error) {
		exe = node
		if args[0] != filepath.Join(host, "lib/node_modules/npm/bin/npm-cli.js") {
			t.Fatal(args)
		}
		writeTree(t, cwd, map[string]string{
			"node_modules/@vtsls/language-server/package.json": `{"name":"@vtsls/language-server","version":"` + vtslsVersion + `"}`,
			"node_modules/@vtsls/language-server/bin/vtsls.js": "vtsls",
			"node_modules/typescript/package.json":             `{"name":"typescript","version":"` + vtslsTypeScript + `"}`,
		})
		if err := os.Mkdir(filepath.Join(cwd, "node_modules", ".bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		return nil, os.Symlink("../@vtsls/language-server/bin/vtsls.js", filepath.Join(cwd, "node_modules", ".bin", "vtsls"))
	}
	if _, err := i.Install(context.Background(), i.binding, p.Token); err != nil || exe != filepath.Join(host, "bin", "node") {
		t.Fatal(err, exe)
	}
	if _, err := os.Stat(filepath.Join(i.root, installDir("node", nodeVersion))); !os.IsNotExist(err) {
		t.Fatal("Node.js installed although the host has one")
	}
	// A host Node.js without its npm is not used.
	if err := os.Remove(filepath.Join(host, "lib/node_modules/npm/bin/npm-cli.js")); err != nil {
		t.Fatal(err)
	}
	if prefix, _ := i.usableHostNode(); prefix != "" {
		t.Fatal(prefix)
	}
}
