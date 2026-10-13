//go:build linux && (webkit2_41 || desktop)

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

#include "_cgo_export.h"

// GTK numbers the mouse's side buttons 8 (back) and 9 (forward).
#define BUTTON_BACK 8
#define BUTTON_FORWARD 9
#define HOOK_INTERVAL_MS 100
#define HOOK_TRIES 100

// Runs before WebKit's own handler. WebKitGTK passes the side buttons to the
// page as button 0, the left button, so they are taken here: the release
// moves back or forward and no press or release reaches the page.
static gboolean on_side_button(GtkWidget *widget, GdkEventButton *event, gpointer data) {
	if (event->button != BUTTON_BACK && event->button != BUTTON_FORWARD) {
		return FALSE;
	}
	if (event->type == GDK_BUTTON_RELEASE) {
		goMouseNavigate(event->button == BUTTON_FORWARD);
	}
	return TRUE;
}

static WebKitWebView *find_web_view(GtkWidget *widget) {
	if (WEBKIT_IS_WEB_VIEW(widget)) {
		return WEBKIT_WEB_VIEW(widget);
	}
	if (!GTK_IS_CONTAINER(widget)) {
		return NULL;
	}
	WebKitWebView *found = NULL;
	GList *children = gtk_container_get_children(GTK_CONTAINER(widget));
	for (GList *l = children; l != NULL && found == NULL; l = l->next) {
		found = find_web_view(GTK_WIDGET(l->data));
	}
	g_list_free(children);
	return found;
}

static int hook_tries;

// Polls until the window's web view exists, then connects to it once.
static gboolean try_hook_side_buttons(gpointer data) {
	gboolean hooked = FALSE;
	GList *windows = gtk_window_list_toplevels();
	for (GList *l = windows; l != NULL; l = l->next) {
		WebKitWebView *view = find_web_view(GTK_WIDGET(l->data));
		if (view == NULL) {
			continue;
		}
		if (g_object_get_data(G_OBJECT(view), "agent-sessions-side-buttons") == NULL) {
			g_signal_connect(view, "button-press-event", G_CALLBACK(on_side_button), NULL);
			g_signal_connect(view, "button-release-event", G_CALLBACK(on_side_button), NULL);
			g_object_set_data(G_OBJECT(view), "agent-sessions-side-buttons", GINT_TO_POINTER(1));
		}
		hooked = TRUE;
	}
	g_list_free(windows);
	if (hooked || ++hook_tries >= HOOK_TRIES) {
		return G_SOURCE_REMOVE;
	}
	return G_SOURCE_CONTINUE;
}

void hook_side_buttons(void) {
	g_timeout_add(HOOK_INTERVAL_MS, try_hook_side_buttons, NULL);
}
