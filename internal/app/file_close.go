package app

import (
	"context"
	"sync"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Once editing has been enabled, native close requests always go through the
// frontend. Keeping this armed for the window lifetime avoids a first-keystroke
// race between asynchronous dirty notifications and the native UI thread.
// The frontend must freeze editor input while approving a close.
type fileCloseGuard struct {
	mu         sync.Mutex
	armed      bool
	sequence   uint64
	pending    uint64
	authorized bool
}

// EnableFileEditing must complete before the first editable document mounts.
// False means a native close is already being coordinated.
func (a *App) EnableFileEditing() bool {
	a.fileClose.mu.Lock()
	defer a.fileClose.mu.Unlock()
	if a.fileClose.pending != 0 || a.fileClose.authorized {
		return false
	}
	a.fileClose.armed = true
	return true
}

// OnBeforeClose never waits for the webview: on Windows this runs on the UI
// thread. Instead it prevents the close and emits a request. Approval triggers
// another Quit, whose hook consumes the one-shot authorization.
func (a *App) OnBeforeClose(ctx context.Context) bool {
	prevent, id := a.fileClose.request()
	if id != 0 {
		NewWailsEmitter(ctx).Emit("files:close-request", id)
	}
	return prevent
}

func (g *fileCloseGuard) request() (bool, uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.authorized {
		g.authorized = false
		return false, 0
	}
	if !g.armed {
		return false, 0
	}
	if g.pending != 0 {
		return true, 0
	}
	g.sequence++
	g.pending = g.sequence
	return true, g.pending
}

func (g *fileCloseGuard) complete(id uint64, allow bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id == 0 || id != g.pending {
		return false
	}
	g.pending = 0
	g.authorized = allow
	return allow
}

// CompleteFileClose answers the pending native close request. Replayed or stale
// approvals cannot close the window. The frontend must keep editing frozen after
// approval until this window has exited.
func (a *App) CompleteFileClose(id uint64, allow bool) {
	if a.fileClose.complete(id, allow) {
		wruntime.Quit(a.appCtx())
	}
}
