//go:build darwin && (webkit2_41 || desktop)

#import <Cocoa/Cocoa.h>

#include "_cgo_export.h"

// kVK_ANSI_Grave: the key left of 1 on ANSI keyboards, and the key macOS
// itself uses for "Move focus to next window" on other layouts.
#define KEY_GRAVE 50

static id cycleMonitor;

// Each window of the app is its own process, so AppKit's own Cmd+` only
// cycles this process's one window and does nothing. A local monitor sees
// the key before AppKit and the web view do: it takes Cmd+` (and
// Cmd+Shift+` for the other direction) and passes every other key on.
void hook_window_cycling(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (cycleMonitor != nil) {
			return;
		}
		cycleMonitor = [[NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskKeyDown handler:^NSEvent *(NSEvent *event) {
			if ([event keyCode] != KEY_GRAVE) {
				return event;
			}
			NSEventModifierFlags mods = [event modifierFlags] & NSEventModifierFlagDeviceIndependentFlagsMask;
			NSEventModifierFlags others = NSEventModifierFlagControl | NSEventModifierFlagOption;
			if (!(mods & NSEventModifierFlagCommand) || (mods & others)) {
				return event;
			}
			if (![event isARepeat]) {
				goCycleWindow((mods & NSEventModifierFlagShift) == 0);
			}
			return nil;
		}] retain];
	});
}
