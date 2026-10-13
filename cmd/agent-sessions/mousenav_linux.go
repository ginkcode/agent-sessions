//go:build linux && (webkit2_41 || desktop)

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1
void schedule_hook_side_buttons(void);
*/
import "C"

import (
	"context"
	"sync/atomic"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var mouseNavCtx atomic.Pointer[context.Context]

// hookMouseNavigation makes the mouse's side buttons go back and forward in
// the app. WebKitGTK reports both to the page as a left button, so the page
// cannot tell them apart; they are taken from GTK before WebKit sees them
// and sent to the frontend as a mouseNavigateEvent.
func hookMouseNavigation(ctx context.Context) {
	mouseNavCtx.Store(&ctx)
	C.schedule_hook_side_buttons()
}

//export goMouseNavigate
func goMouseNavigate(forward C.int) {
	ctx := mouseNavCtx.Load()
	if ctx == nil {
		return
	}
	direction := "back"
	if forward != 0 {
		direction = "forward"
	}
	// Off the GTK main loop: emitting runs script in the web view.
	go wruntime.EventsEmit(*ctx, mouseNavigateEvent, direction)
}
