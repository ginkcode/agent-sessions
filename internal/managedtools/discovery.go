package managedtools

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Discover is read-only: no directory creation, probes, network or repair. A
// receipt alone is never authority: validate the binary metadata AND its bytes.
// Missing and corrupt installs are deliberately distinct and fail closed.
func (i *Installer) Discover() (string, error) {
	path := filepath.Join(i.root, "gopls-"+version+"-"+runtime.GOOS+"-"+runtime.GOARCH, "gopls")
	for _, dir := range []string{filepath.Dir(i.root), i.root, filepath.Dir(path)} {
		if err := readPrivateTree(dir); err != nil {
			return "", err
		}
	}
	if err := i.existing(path); err != nil {
		return "", errors.New("managed tools: invalid installation")
	}
	return path, nil
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

func readReceipt(path string) ([]byte, error) {
	if err := regularOrAbsent(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
		return nil, errors.New("invalid receipt")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return nil, errors.New("invalid receipt")
	}
	return data, nil
}
