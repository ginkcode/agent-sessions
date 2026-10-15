//go:build darwin && (webkit2_41 || desktop)

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// Points per millimetre; the margin matches @page in style.css.
#define PRINT_MARGIN (14.0 * 72.0 / 25.4)

static WKWebView *find_web_view(NSView *view) {
	if ([view isKindOfClass:[WKWebView class]]) {
		return (WKWebView *)view;
	}
	for (NSView *sub in [view subviews]) {
		WKWebView *found = find_web_view(sub);
		if (found != nil) {
			return found;
		}
	}
	return nil;
}

// Wails' WindowPrint always prints landscape with no margins and hides the
// orientation choice. This prints the app's web view the same way, but in
// portrait with margins, and the print panel offers orientation, paper size
// and scale. Returns 0 once the print is started on the main thread, or -1
// before macOS 11, where WKWebView cannot print.
int print_web_view(void) {
	if (@available(macOS 11.0, *)) {
		dispatch_async(dispatch_get_main_queue(), ^{
			NSWindow *window = nil;
			WKWebView *webView = nil;
			NSMutableArray<NSWindow *> *windows = [NSMutableArray array];
			if ([NSApp mainWindow] != nil) {
				[windows addObject:[NSApp mainWindow]];
			}
			[windows addObjectsFromArray:[NSApp windows]];
			for (NSWindow *candidate in windows) {
				webView = find_web_view([candidate contentView]);
				if (webView != nil) {
					window = candidate;
					break;
				}
			}
			if (webView == nil) {
				NSLog(@"print: no web view to print");
				return;
			}

			NSPrintInfo *info = [[[NSPrintInfo sharedPrintInfo] copy] autorelease];
			info.orientation = NSPaperOrientationPortrait;
			info.horizontalPagination = NSPrintingPaginationModeAutomatic;
			info.verticalPagination = NSPrintingPaginationModeAutomatic;
			info.horizontallyCentered = NO;
			info.verticallyCentered = NO;
			info.leftMargin = PRINT_MARGIN;
			info.rightMargin = PRINT_MARGIN;
			info.topMargin = PRINT_MARGIN;
			info.bottomMargin = PRINT_MARGIN;

			NSPrintOperation *op = [webView printOperationWithPrintInfo:info];
			op.showsPrintPanel = YES;
			op.showsProgressPanel = YES;
			op.printPanel.options |= NSPrintPanelShowsOrientation | NSPrintPanelShowsPaperSize | NSPrintPanelShowsScaling;
			// Without a frame the operation's view prints blank pages.
			op.view.frame = webView.bounds;
			[op runOperationModalForWindow:window delegate:nil didRunSelector:nil contextInfo:nil];
		});
		return 0;
	}
	return -1;
}
