//go:build !windows

package remote

// ListWSLDistros returns no distributions: WSL is only on Windows.
func ListWSLDistros() ([]WSLDistro, error) { return nil, ErrWSLNotInstalled }
