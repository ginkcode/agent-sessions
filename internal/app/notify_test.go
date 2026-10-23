package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

type fakeNotifier struct {
	available  bool
	initErr    error
	authorized bool
	grant      bool
	requests   int
	sent       []string
	removed    []string
	click      func(string)
	cleaned    bool
}

func (f *fakeNotifier) Available() bool           { return f.available }
func (f *fakeNotifier) Initialize() error         { return f.initErr }
func (f *fakeNotifier) Authorized() (bool, error) { return f.authorized, nil }
func (f *fakeNotifier) RequestAuthorization() (bool, error) {
	f.requests++
	f.authorized = f.grant
	return f.grant, nil
}
func (f *fakeNotifier) Send(id, title, body string) error {
	f.sent = append(f.sent, id+"|"+title+"|"+body)
	return nil
}
func (f *fakeNotifier) Remove(id string) error  { f.removed = append(f.removed, id); return nil }
func (f *fakeNotifier) OnClick(fn func(string)) { f.click = fn }
func (f *fakeNotifier) Cleanup()                { f.cleaned = true }

func TestNotificationsAskOnceAndSend(t *testing.T) {
	fake := &fakeNotifier{available: true, grant: true}
	a := &App{notifierOverride: fake}

	if s := a.NotificationStatus(); s.Available || !strings.Contains(s.Reason, "asks for permission") {
		t.Fatalf("status before asking = %+v", s)
	}
	if err := a.Notify(AgentNotification{Tab: "t1", Title: "Codex finished", Body: "fix"}); err == nil {
		t.Fatal("Notify before permission succeeded")
	}
	if s := a.PrepareNotifications(); !s.Available {
		t.Fatalf("status after granting = %+v", s)
	}
	a.PrepareNotifications()
	if fake.requests != 1 {
		t.Fatalf("asked %d times, want 1", fake.requests)
	}
	if err := a.Notify(AgentNotification{Tab: "t1", Title: "Codex finished", Body: "fix"}); err != nil {
		t.Fatal(err)
	}
	id := notificationID("t1")
	if len(fake.sent) != 1 || fake.sent[0] != id+"|Codex finished|fix" {
		t.Fatalf("sent %q", fake.sent)
	}
	if len(fake.removed) != 1 || fake.removed[0] != id {
		t.Fatalf("the tab's earlier alert was not replaced: removed %q", fake.removed)
	}
	if err := a.ClearNotification("t1"); err != nil || len(fake.removed) != 2 {
		t.Fatalf("clear: %v, removed %q", err, fake.removed)
	}
	a.closeNotifications()
	if !fake.cleaned {
		t.Fatal("not cleaned up at shutdown")
	}
}

func TestNotificationsDeniedAndUnavailable(t *testing.T) {
	denied := &fakeNotifier{available: true}
	a := &App{notifierOverride: denied}
	if s := a.PrepareNotifications(); s.Available || !strings.Contains(s.Reason, "turned off") {
		t.Fatalf("denied status = %+v", s)
	}

	b := &App{notifierOverride: &fakeNotifier{}}
	if s := b.PrepareNotifications(); s.Available || s.Reason == "" {
		t.Fatalf("unavailable status = %+v", s)
	}

	c := &App{notifierOverride: &fakeNotifier{available: true, initErr: errors.New("no bundle identifier")}}
	if s := c.PrepareNotifications(); s.Available || !strings.Contains(s.Reason, "no bundle identifier") {
		t.Fatalf("init failure status = %+v", s)
	}

	// Outside the desktop app there is nothing to notify with.
	if s := (&App{}).NotificationStatus(); s.Available || s.Reason == "" {
		t.Fatalf("no desktop status = %+v", s)
	}
	if err := (&App{}).ClearNotification("t1"); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationClickShowsItsTab(t *testing.T) {
	fake := &fakeNotifier{available: true, authorized: true}
	raised := 0
	var events []any
	a := &App{
		notifierOverride: fake,
		notifyRaise:      func() { raised++ },
		wailsEvents: engine.EmitterFunc(func(name string, payload any) {
			if name == notifyOpenEvent {
				events = append(events, payload)
			}
		}),
	}
	a.PrepareNotifications()
	fake.click(notificationID("t7"))
	// Another window's notification, and an unrelated one, are not this window's.
	fake.click("agent-sessions-1-t7")
	fake.click("other")
	if raised != 1 || len(events) != 1 || events[0].(map[string]string)["id"] != "t7" {
		t.Fatalf("raised %d, events %v", raised, events)
	}
}
