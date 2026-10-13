//go:build !linux && !darwin && (webkit2_41 || desktop)

package main

import "context"

// hookMouseNavigation is native on Linux and macOS only; WebView2 reports
// the side buttons to the page as buttons 3 and 4.
func hookMouseNavigation(context.Context) {}
