package providertest

import (
	"context"
	"errors"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
)

func TestBarrierOrderingAndCancellation(t *testing.T) {
	b := NewBarrier(NewFake(model.AgentClaude, "synthetic"), 2)
	t.Cleanup(b.Close)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "synthetic"}
	first := make(chan *model.Transcript, 1)
	go func() { tr, _ := b.Load(t.Context(), ref); first <- tr }()
	a := <-b.Started
	second := make(chan *model.Transcript, 1)
	go func() { tr, _ := b.Load(t.Context(), ref); second <- tr }()
	c := <-b.Started
	one, two := &model.Transcript{}, &model.Transcript{}
	c.Reply(two, nil)
	if <-second != two {
		t.Fatal("second reply")
	}
	a.Reply(one, nil)
	if <-first != one || b.Calls.Load() != 2 {
		t.Fatal("first reply/count")
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := b.Load(ctx, ref); done <- err }()
	attempt := <-b.Started
	cancel()
	if !errors.Is(<-done, context.Canceled) || attempt.Context.Err() != context.Canceled {
		t.Fatal("cancellation not observed")
	}
}

func TestBarrierIgnoredCancellationAndClose(t *testing.T) {
	b := NewBarrier(NewFake(model.AgentClaude, "synthetic"), 1)
	t.Cleanup(b.Close)
	b.IgnoreCancellation = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := b.Load(ctx, model.SessionRef{}); done <- err }()
	attempt := <-b.Started
	sentinel := errors.New("reply")
	attempt.Reply(nil, sentinel)
	if !errors.Is(<-done, sentinel) {
		t.Fatal("ignored cancellation should permit reply")
	}
	go func() { _, err := b.Load(ctx, model.SessionRef{}); done <- err }()
	<-b.Started
	b.Close()
	b.Close()
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("close did not unblock load")
	}
}
