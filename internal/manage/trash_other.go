//go:build !linux && !darwin

package manage

import "errors"

// platformTrash returns an error on unsupported platforms: Linux uses gio,
// macOS uses Finder; other platforms fall back to disabled deletes.
func platformTrash() (Trash, error) {
	return nil, errors.New("manage: trash is not supported on this platform")
}
