package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrExternalPath is an external path that is not absolute or has a '..'.
var ErrExternalPath = errors.New("external path must be absolute without '..' components")

// ReadExternal reads a file outside any project, named by its absolute path,
// for viewing only: the source a definition in a dependency points to. The
// caller decides which paths may be read; this refuses configured stores and
// protected names (credentials, agent data) as editing does, in the path as
// given and the path it resolves to. Text has the Read limits; Edit and
// Changes are never set, and images are not decoded.
func ReadExternal(policy EditPolicy, path string) (Content, error) {
	if !filepath.IsAbs(path) || editHasParentComponent(path) {
		return Content{}, ErrExternalPath
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Content{}, err
	}
	if err := policy.refuse(false, filepath.Clean(path), real); err != nil {
		return Content{}, err
	}
	f, err := os.Open(real)
	if err != nil {
		return Content{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Content{}, err
	}
	if !info.Mode().IsRegular() {
		return Content{}, fmt.Errorf("%s is not a regular file", path)
	}
	out := Content{Path: path, Size: info.Size()}
	if _, err := readText(f, &out); err != nil {
		return Content{}, err
	}
	return out, nil
}
