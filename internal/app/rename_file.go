package app

import (
	"errors"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// RenameFile renames a file or directory on the host captured by the UI.
// Paths and the basename are interpreted and authorized only on that backend.
func (a *App) RenameFile(dir, path, name, host string) error {
	r := a.route()
	if r.host != host {
		return ErrSaveHostChanged
	}
	if r.host != "" {
		if r.transport == nil {
			return fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
		}
		if c, ok := r.backend.(*rpc.Client); ok && !c.Capabilities().RenameFile {
			return errors.New("remote host does not support renaming files")
		}
	}
	if a.route().host != host {
		return ErrSaveHostChanged
	}
	return r.backend.RenameFile(a.appCtx(), dir, path, name)
}
