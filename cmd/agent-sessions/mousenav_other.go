//go:build !linux && (webkit2_41 || desktop)

package main

import "context"

// hookMouseNavigation is Linux-only; other web views report the side
// buttons to the page as buttons 3 and 4.
func hookMouseNavigation(context.Context) {}
