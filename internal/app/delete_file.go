package app

import (
	"errors"
	"fmt"

	"github.com/ginkcode/agent-sessions/internal/rpc"
)

// DeleteFile permanently deletes a file or directory on the host captured by
// the UI. The frontend must confirm this irreversible action before calling.
func (a *App) DeleteFile(dir, path, host string) error {
	r := a.route()
	if r.host != host {
		return ErrSaveHostChanged
	}
	if r.host != "" {
		if r.transport == nil {
			return fmt.Errorf("%w %s", rpc.ErrDisconnected, r.host)
		}
		if c, ok := r.backend.(*rpc.Client); ok && !c.Capabilities().DeleteFile {
			return errors.New("remote host does not support deleting files")
		}
	}
	if a.route().host != host {
		return ErrSaveHostChanged
	}
	return r.backend.DeleteFile(a.appCtx(), dir, path)
}
