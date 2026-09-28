package app

import "testing"

func TestPing(t *testing.T) {
	a := NewApp()
	if got := a.Ping(""); got != "Hello from agent-sessions backend!" {
		t.Errorf("Ping(\"\") = %q, want default greeting", got)
	}
	if got := a.Ping("dev"); got != "Hello dev, welcome to agent-sessions!" {
		t.Errorf("Ping(%q) = %q, want personalized greeting", "dev", got)
	}
}

func TestOnStartupStoresContext(t *testing.T) {
	// OnStartup is invoked by the Wails runtime before any bound call; the
	// service must retain the context for later runtime calls (clipboard,
	// events, ...). Verified here with a plain background context.
	a := NewApp()
	ctx := t.Context()
	a.OnStartup(ctx)
	if a.ctx != ctx {
		t.Error("OnStartup did not retain the runtime context")
	}
}
