package codediagnostics

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSupervisorDefaultsAndInvocationGuards(t *testing.T) {
	o := (SupervisorOptions{}).defaults()
	if o.Timeout != 120*time.Second || o.CaptureBytes != 10<<20 || o.MessageBytes != 8192 || o.Pending != 32 || o.CleanupTimeout != 5*time.Second {
		t.Fatal("defaults changed")
	}
	tr := NewTrust(nil)
	root, tools, c := approvedCommand(t, tr)
	for _, mutate := range []func(*Command){
		func(c *Command) { c.Env = []string{"GOTOOLCHAIN=auto"} },
		func(c *Command) { c.Env = []string{"secret\x00=value"} },
		func(c *Command) { c.Args = []string{"nul\x00"} },
		func(c *Command) { c.Executable = "tool" },
		func(c *Command) { c.Directory = "../escape" },
	} {
		bad := c
		mutate(&bad)
		if validateCommand(root, tools, bad) == nil {
			t.Fatal("invalid command accepted")
		}
	}
	if _, err := commandDirectory(root, filepath.Join("..", "escape")); err == nil {
		t.Fatal("escaping cwd accepted")
	}
}

func TestSupervisorQueuedRevocation(t *testing.T) {
	tr := NewTrust(nil)
	starts := make(chan started, 4)
	s := NewSupervisor(tr, SupervisorOptions{Start: fakeFactory(starts)})
	defer s.Close()
	ra, ta, ca := approvedCommand(t, tr)
	a := submit(t, s, ra, ta, ca, "a")
	pa := nextStart(t, starts)
	rb, tb, cb := approvedCommand(t, tr)
	b := submit(t, s, rb, tb, cb, "b")
	pb := nextStart(t, starts)
	rc, tc, cc := approvedCommand(t, tr)
	pending := submit(t, s, rc, tc, cc, "pending")
	if err := tr.Revoke(rc); err != nil {
		t.Fatal(err)
	}
	_ = pa.process.Terminate()
	result(t, a)
	if result(t, pending).Kind != OutcomeCanceled {
		t.Fatal("revoked queued work ran")
	}
	select {
	case <-starts:
		t.Fatal("queued revocation launched process")
	default:
	}
	_ = pb.process.Terminate()
	result(t, b)
	_, err := s.Submit(context.Background(), rc, tc, cc, "revoked", false)
	code(t, err, "approvalRequired")
}
