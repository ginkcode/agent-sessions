package app

import (
	"errors"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// CreateDirectory creates a directory on the host captured by the UI.
// Paths and the basename are interpreted and authorized only on that backend.
func (a *App) CreateDirectory(dir, path, name, host string) error {
	r := a.route()
	if r.host != host {
		return ErrSaveHostChanged
	}
	if r.host != "" {
		if r.transport == nil {
			return fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
		}
		if c, ok := r.backend.(*rpc.Client); ok && !c.Capabilities().CreateDirectory {
			return errors.New("remote host does not support creating directories")
		}
	}
	if a.route().host != host {
		return ErrSaveHostChanged
	}
	return r.backend.CreateDirectory(a.appCtx(), dir, path, name)
}

// CreateFile creates an empty file on the host captured by the UI, like
// CreateDirectory.
func (a *App) CreateFile(dir, path, name, host string) error {
	r := a.route()
	if r.host != host {
		return ErrSaveHostChanged
	}
	if r.host != "" {
		if r.transport == nil {
			return fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
		}
		if c, ok := r.backend.(*rpc.Client); ok && !c.Capabilities().CreateFile {
			return errors.New("remote host does not support creating files")
		}
	}
	if a.route().host != host {
		return ErrSaveHostChanged
	}
	return r.backend.CreateFile(a.appCtx(), dir, path, name)
}
