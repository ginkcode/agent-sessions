package managedtools

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Discover is read-only: no directory creation, probes, network or repair. A
// receipt alone is never authority: validate the install AND its main file's
// bytes. Missing (os.IsNotExist) and corrupt installs are deliberately
// distinct. tool is a catalog name, including "node".
func (i *Installer) Discover(tool string) (string, error) {
	switch tool {
	case "gopls":
		// Any catalog release counts: the host Go decides which one a
		// proposal builds, and a later Go upgrade must not hide it.
		var first error
		for _, r := range goplsReleases {
			path, err := i.discover(goplsSpec(i, TrustedGo{}, r))
			if err == nil {
				return path, nil
			}
			if first == nil || (os.IsNotExist(first) && !os.IsNotExist(err)) {
				first = err
			}
		}
		return "", first
	case "ruff":
		return i.discover(ruffSpec())
	case "vtsls":
		return i.discover(vtslsSpec())
	case "pyright":
		return i.discover(pyrightSpec())
	case "node":
		return i.discover(nodeSpec())
	}
	return "", errors.New("managed tools: unknown tool")
}

func (i *Installer) discover(s spec) (string, error) {
	dir := filepath.Join(i.root, installDir(s.name, s.version))
	// Not installed is "missing" however the app data directory looks; only an
	// existing install is judged by its directories.
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return "", err
	}
	if err := readAppData(filepath.Dir(i.root)); err != nil {
		return "", err
	}
	for _, d := range []string{i.root, dir} {
		if err := readPrivateTree(d); err != nil {
			return "", err
		}
	}
	path, err := i.existing(s, dir)
	if err != nil {
		return "", errors.New("managed tools: invalid installation")
	}
	return path, nil
}

// Dirs returns the directories of valid installs that tool discovery searches
// after the user's own: gopls's and Ruff's directories, npm servers'
// node_modules/.bin and Node.js's bin.
func (i *Installer) Dirs() []string {
	var dirs []string
	for _, tool := range []string{"gopls", "ruff", "vtsls", "pyright", "node"} {
		path, err := i.Discover(tool)
		if err != nil {
			continue
		}
		switch tool {
		case "vtsls", "pyright":
			// Both main files live below the package root; command shims are
			// in node_modules/.bin rather than next to those main files.
			pkg := vtslsPackage()
			if tool == "pyright" {
				pkg = pyrightPackage()
			}
			dirs = append(dirs, filepath.Join(i.root, installDir(pkg.name, pkg.version), "node_modules", ".bin"))
		default:
			dirs = append(dirs, filepath.Dir(path))
		}
	}
	return dirs
}

func readPrivateTree(path string) error {
	if err := noLinks(filepath.Dir(path)); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0o077 != 0 {
		return errors.New("managed tools: unsafe installation directory")
	}
	return nil
}

// readAppData checks the app data directory holding managed-tools. It is
// shared with other app storage (the Linux desktop's web view creates it with
// the umask), so it may be readable by others but must be this account's and
// not writable by anyone else, who could otherwise replace managed-tools.
func readAppData(path string) error {
	if err := noLinks(filepath.Dir(path)); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0o022 != 0 || !ownedByUser(st) {
		return errors.New("managed tools: unsafe app data directory")
	}
	return nil
}

// readBounded reads a regular file of at most limit bytes.
func readBounded(path string, limit int64) ([]byte, error) {
	if err := regularOrAbsent(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("invalid file")
	}
	return data, nil
}
