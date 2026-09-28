//go:build !linux

package manage

import "errors"

// platformTrash returns an error on unsupported platforms: only Linux gio
// trashing ships in M4; macOS and Windows fall back to disabled deletes.
func platformTrash() (Trash, error) {
	return nil, errors.New("manage: trash is not supported on this platform")
}
