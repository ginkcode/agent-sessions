//go:build darwin && (webkit2_41 || desktop)

package main

/*
#cgo darwin LDFLAGS: -framework Cocoa
void hook_window_cycling(void);
*/
import "C"

import "sync/atomic"

var cycleWindow atomic.Pointer[func(forward bool)]

// hookWindowCycling makes Cmd+` bring the app's next window to the front,
// and Cmd+Shift+` the previous one, as in any macOS app. The windows are
// separate processes, so the key is taken natively (windowcycle_darwin.m)
// and passed to cycle.
func hookWindowCycling(cycle func(forward bool)) {
	cycleWindow.Store(&cycle)
	C.hook_window_cycling()
}

//export goCycleWindow
func goCycleWindow(forward C.int) {
	cycle := cycleWindow.Load()
	if cycle == nil {
		return
	}
	// Off the main thread: raising a window talks to the other process,
	// which may be slow to answer.
	go (*cycle)(forward != 0)
}
