//go:build linux && (webkit2_41 || desktop)

package main

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static gboolean disable_font_hinting(gpointer data) {
	GtkSettings *settings = gtk_settings_get_default();
	if (settings != NULL) {
		g_object_set(settings, "gtk-xft-hintstyle", "hintnone", NULL);
	}
	return G_SOURCE_REMOVE;
}

static void schedule_disable_font_hinting(void) {
	g_idle_add(disable_font_hinting, NULL);
}
*/
import "C"

// disableFontHinting turns off glyph hinting for this process's web view.
// WebKitGTK's Skia backend snaps hinted glyph advances to whole pixels, so
// with the desktop's usual "slight" hinting the letter spacing goes uneven,
// worse still when GTK3 renders at 2x for a fractional display scale and the
// compositor shrinks the result. Unhinted text keeps the font's own spacing,
// as GTK4 and browsers do. Safe from any goroutine: the change runs on the
// GTK main loop, and WebKit re-renders when the setting changes.
func disableFontHinting() {
	C.schedule_disable_font_hinting()
}
