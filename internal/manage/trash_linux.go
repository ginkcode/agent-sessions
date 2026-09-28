//go:build linux

package manage

// platformTrash returns the Linux gio-backed Trash transport.
func platformTrash() (Trash, error) {
	return NewGioTrash()
}
