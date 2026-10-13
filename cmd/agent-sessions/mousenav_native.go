//go:build (linux || darwin) && (webkit2_41 || desktop)

package main

/*
#cgo linux pkg-config: gtk+-3.0 webkit2gtk-4.1
#cgo darwin LDFLAGS: -framework Cocoa
void hook_side_buttons(void);
*/
import "C"

import (
	"context"
	"sync/atomic"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var mouseNavCtx atomic.Pointer[context.Context]

// hookMouseNavigation makes the mouse's side buttons go back and forward in
// the app. WebKitGTK and WKWebView do not report them to the page as buttons
// 3 and 4, so the page cannot tell them apart; they are taken natively
// before the web view sees them (mousenav_linux.c, mousenav_darwin.m) and
// sent to the frontend as a mouseNavigateEvent.
func hookMouseNavigation(ctx context.Context) {
	mouseNavCtx.Store(&ctx)
	C.hook_side_buttons()
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
	// Off the UI main loop: emitting runs script in the web view.
	go wruntime.EventsEmit(*ctx, mouseNavigateEvent, direction)
}
