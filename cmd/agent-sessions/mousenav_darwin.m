//go:build darwin && (webkit2_41 || desktop)

#import <Cocoa/Cocoa.h>

#include "_cgo_export.h"

// NSEvent numbers the mouse's side buttons 3 (back) and 4 (forward) when
// no mouse utility remaps them.
#define BUTTON_BACK 3
#define BUTTON_FORWARD 4

static id monitor;

// WKWebView does not report the side buttons to the page as buttons 3 and
// 4. A local monitor sees the app's events before any view does: it takes
// the side buttons, moves back or forward on the release, and passes every
// other event on.
//
// Logi Options+ (and three-finger trackpad swipes) deliver back and forward
// as a horizontal swipe instead of a button, so those are taken too. As in
// Safari, a positive deltaX means back.
void hook_side_buttons(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (monitor != nil) {
			return;
		}
		NSEventMask mask = NSEventMaskOtherMouseDown | NSEventMaskOtherMouseUp | NSEventMaskSwipe;
		monitor = [[NSEvent addLocalMonitorForEventsMatchingMask:mask handler:^NSEvent *(NSEvent *event) {
			if ([event type] == NSEventTypeSwipe) {
				CGFloat dx = [event deltaX];
				if (dx == 0) {
					return event;
				}
				goMouseNavigate(dx < 0);
				return nil;
			}
			NSInteger button = [event buttonNumber];
			if (button != BUTTON_BACK && button != BUTTON_FORWARD) {
				return event;
			}
			if ([event type] == NSEventTypeOtherMouseUp) {
				goMouseNavigate(button == BUTTON_FORWARD);
			}
			return nil;
		}] retain];
	});
}
