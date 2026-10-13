//go:build webkit2_41 || desktop

package main

// mouseNavigateEvent carries "back" or "forward" to the frontend when the
// mouse's side buttons are handled natively (see hookMouseNavigation).
const mouseNavigateEvent = "nav:mouse"
