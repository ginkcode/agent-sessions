package managedtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// npmPackage describes a pinned npm server and its runtime metadata checks.
type npmPackage struct {
	name, version, main, bin, source string
	manifest, lockfile               []byte
	versions                         map[string]string
}

func vtslsPackage() npmPackage {
	return npmPackage{"vtsls", vtslsVersion, vtslsMain, "vtsls",
		fmt.Sprintf("@vtsls/language-server %s, TypeScript %s, %d locked packages", vtslsVersion, vtslsTypeScript, vtslsLockPackages),
		vtslsPackageJSON, vtslsLockfile, map[string]string{"@vtsls/language-server": vtslsVersion, "typescript": vtslsTypeScript}}
}

func pyrightPackage() npmPackage {
	return npmPackage{"pyright", pyrightVersion, pyrightMain, "pyright-langserver",
		fmt.Sprintf("pyright %s, %d locked packages", pyrightVersion, pyrightLockPackages),
		pyrightPackageJSON, pyrightLockfile, map[string]string{"pyright": pyrightVersion}}
}

// installNPM runs `npm ci` of the embedded lockfile in stage with the
// selected Node.js. npm ci installs exactly the locked tree and checks every
// package against its sha512 integrity value; no package script runs. The
// environment is allowlisted: no user npmrc, cache, token or registry
// override, and HOME is a scratch directory.
func installNPM(ctx context.Context, pkg npmPackage, stage, nodeDir string, command func(context.Context, string, string, []string, ...string) ([]byte, error)) error {
	node := filepath.Join(nodeDir, "bin", "node")
	npm := filepath.Join(nodeDir, "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if err := executableFile(node); err != nil {
		return err
	}
	if _, err := fileDigest(npm); err != nil {
		return err
	}
	work := filepath.Join(stage, ".work")
	if err := os.Mkdir(work, 0o700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	for _, name := range []string{"home", "tmp", "cache"} {
		if err := os.Mkdir(filepath.Join(work, name), 0o700); err != nil {
			return err
		}
	}
	// npm refuses to load one file as both the user and the global config.
	userrc, globalrc := filepath.Join(work, "npmrc"), filepath.Join(work, "global-npmrc")
	for _, rc := range []string{userrc, globalrc} {
		if err := os.WriteFile(rc, nil, 0o600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "package.json"), pkg.manifest, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "package-lock.json"), pkg.lockfile, 0o600); err != nil {
		return err
	}
	env := []string{
		"HOME=" + filepath.Join(work, "home"), "TMPDIR=" + filepath.Join(work, "tmp"),
		"PATH=" + filepath.Join(nodeDir, "bin") + ":/usr/bin:/bin",
		"npm_config_userconfig=" + userrc, "npm_config_globalconfig=" + globalrc,
		"npm_config_cache=" + filepath.Join(work, "cache"), "npm_config_registry=" + npmRegistry,
		"npm_config_ignore_scripts=true", "npm_config_update_notifier=false",
		"NODE_OPTIONS=", "NO_UPDATE_NOTIFIER=1",
	}
	_, err := command(ctx, node, stage, env, npm, "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error")
	return err
}

// validate reads package metadata only; it never runs the server.
func (pkg npmPackage) validate(dir string) error {
	main := filepath.Join(dir, filepath.FromSlash(pkg.main))
	if _, err := fileDigest(main); err != nil {
		return err
	}
	for pkg, version := range pkg.versions {
		data, err := readBounded(filepath.Join(dir, "node_modules", filepath.FromSlash(pkg), "package.json"), 1<<20)
		if err != nil {
			return err
		}
		var meta struct{ Name, Version string }
		if json.Unmarshal(data, &meta) != nil || meta.Name != pkg || meta.Version != version {
			return errors.New("unexpected package metadata")
		}
	}
	link, err := filepath.EvalSymlinks(filepath.Join(dir, "node_modules", ".bin", pkg.bin))
	real, rerr := filepath.EvalSymlinks(main)
	if err != nil || rerr != nil || link != real {
		return errors.New(pkg.name + " command missing")
	}
	return nil
}

// validateNode checks the archive layout npm servers' install and launch rely on.
func validateNode(dir string) error {
	if err := executableFile(filepath.Join(dir, "bin", "node")); err != nil {
		return err
	}
	_, err := fileDigest(filepath.Join(dir, "lib", "node_modules", "npm", "bin", "npm-cli.js"))
	return err
}
