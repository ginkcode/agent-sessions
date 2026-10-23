package managedtools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPyrightInstallAndDiscovery(t *testing.T) {
	for _, hostVersion := range []string{"18.0.0", "17.9.0", ""} {
		t.Run("host-"+hostVersion, func(t *testing.T) {
			i, _ := fixture(t)
			host := filepath.Join(filepath.Dir(i.root), "host-node")
			writeTree(t, host, map[string]string{"bin/node": "node", "lib/node_modules/npm/bin/npm-cli.js": "npm"})
			if err := os.Chmod(filepath.Join(host, "bin/node"), 0755); err != nil {
				t.Fatal(err)
			}
			i.UseHostNode(func() (string, string) { return host, hostVersion })
			node := tarGz(t, entry{name: "node-x/bin/node", body: "node", mode: 0755}, entry{name: "node-x/lib/node_modules/npm/bin/npm-cli.js", body: "npm"})
			replace(t, nodeArchives, pin(node))
			serve(i, node)
			p, err := i.Propose("pyright")
			if err != nil {
				t.Fatal(err)
			}
			if p.Version != pyrightVersion || !strings.Contains(p.Source, "pyright "+pyrightVersion) || !strings.Contains(p.Integrity, "install scripts off") {
				t.Fatal(p)
			}
			nodeDir := filepath.Join(i.root, installDir("node", nodeVersion))
			if hostVersion == "18.0.0" {
				nodeDir = host
				if !strings.Contains(p.Note, "not downloaded") {
					t.Fatal(p)
				}
			} else if !strings.Contains(p.Note, "Also downloads Node.js") {
				t.Fatal(p)
			}
			ran := false
			i.command = func(_ context.Context, exe, cwd string, env []string, args ...string) ([]byte, error) {
				ran = true
				if exe != filepath.Join(nodeDir, "bin/node") || !slices.Equal(args, []string{filepath.Join(nodeDir, "lib/node_modules/npm/bin/npm-cli.js"), "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error"}) {
					t.Fatal(exe, args)
				}
				if !slices.Contains(env, "npm_config_ignore_scripts=true") || !slices.Contains(env, "npm_config_registry="+npmRegistry) {
					t.Fatal(env)
				}
				for name, want := range map[string][]byte{"package.json": pyrightPackageJSON, "package-lock.json": pyrightLockfile} {
					if got, err := os.ReadFile(filepath.Join(cwd, name)); err != nil || !bytes.Equal(got, want) {
						t.Fatal(name, err)
					}
				}
				writeTree(t, cwd, map[string]string{pyrightMain: "pyright", "node_modules/pyright/package.json": `{"name":"pyright","version":"` + pyrightVersion + `"}`})
				if err := os.Mkdir(filepath.Join(cwd, "node_modules/.bin"), 0755); err != nil {
					t.Fatal(err)
				}
				return nil, os.Symlink("../pyright/langserver.index.js", filepath.Join(cwd, "node_modules/.bin/pyright-langserver"))
			}
			path, err := i.Install(context.Background(), i.binding, p.Token)
			if err != nil || !ran || path != filepath.Join(p.Destination, pyrightMain) {
				t.Fatal(path, err, ran)
			}
			if got, err := i.Discover("pyright"); err != nil || got != path {
				t.Fatal(got, err)
			}
			if !slices.Contains(i.Dirs(), filepath.Join(p.Destination, "node_modules/.bin")) {
				t.Fatal(i.Dirs())
			}
			if _, err := os.Stat(filepath.Join(p.Destination, ".work")); !os.IsNotExist(err) {
				t.Fatal("scratch retained", err)
			}
			// Metadata, the command shim, and receipt-bound main bytes all matter.
			metadata := filepath.Join(p.Destination, "node_modules/pyright/package.json")
			if err := os.WriteFile(metadata, []byte(`{"name":"pyright","version":"0.0.0"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := i.Discover("pyright"); err == nil {
				t.Fatal("wrong version accepted")
			}
			if err := os.WriteFile(metadata, []byte(`{"name":"pyright","version":"`+pyrightVersion+`"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(p.Destination, "node_modules/.bin/pyright-langserver")); err != nil {
				t.Fatal(err)
			}
			if _, err := i.Discover("pyright"); err == nil {
				t.Fatal("missing command accepted")
			}
			if err := os.Symlink("../pyright/langserver.index.js", filepath.Join(p.Destination, "node_modules/.bin/pyright-langserver")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := i.Discover("pyright"); err == nil {
				t.Fatal("changed main accepted")
			}
		})
	}
}

func TestPyrightLockfilePinsOfficialPackages(t *testing.T) {
	var lock struct {
		LockfileVersion int
		Packages        map[string]struct{ Version, Resolved, Integrity string }
	}
	if err := json.Unmarshal(pyrightLockfile, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.LockfileVersion != 3 || len(lock.Packages) != pyrightLockPackages+1 {
		t.Fatal("unexpected lockfile")
	}
	for name, pkg := range lock.Packages {
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "node_modules/") || !strings.HasPrefix(pkg.Resolved, npmRegistry) || !strings.HasPrefix(pkg.Integrity, "sha512-") {
			t.Fatal(name, pkg)
		}
	}
	pyright := lock.Packages["node_modules/pyright"]
	if pyright.Version != pyrightVersion || pyright.Integrity != "sha512-FPZZb51jepDX4eP7TEYDeNFtmE3WgwkkEcJpvH3/QmUSsj0EAy3LXu+xB4T/FWDejtsXlCfFr/rbWFjwuwuXww==" {
		t.Fatal(pyright)
	}
}
