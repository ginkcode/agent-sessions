package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func TestResumeLaunch(t *testing.T) {
	svc, _ := setupTestService(t)
	eng := NewEngine(WithService(svc))
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}

	cmd, err := eng.ResumeLaunch(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	want := provider.Command{Argv: []string{"fake", "--resume", "s1"}, Dir: "/home/user/project1"}
	if !reflect.DeepEqual(cmd, want) {
		t.Errorf("ResumeLaunch = %+v, want %+v", cmd, want)
	}
	// The copied command is the same command in this platform's shell.
	copied, err := svc.CopyResumeCommand(ref)
	if err != nil || copied != launch.Format(want) {
		t.Errorf("CopyResumeCommand = %q, %v; want %q", copied, err, launch.Format(want))
	}

	if _, err := eng.ResumeLaunch(context.Background(), model.SessionRef{Agent: model.AgentClaude, ID: "missing"}); !errors.Is(err, ErrUnknownSession) {
		t.Errorf("missing session: %v", err)
	}
}

func TestNewSessionAgents(t *testing.T) {
	codex := providertest.NewFake(model.AgentCodex, "Codex")
	svc := NewService(nil, provider.Set{providertest.NewFake(model.AgentClaude, "Claude Code"), codex})
	eng := NewEngine(WithService(svc))
	var looked []string
	findAgent = func(name string) (launch.Executable, error) {
		looked = append(looked, name)
		if len(looked) == 2 {
			return launch.Executable{}, fmt.Errorf("%w: Codex was not found. Install it, then try again", launch.ErrAgentNotFound)
		}
		return launch.Executable{Path: "/bin/" + name}, nil
	}
	t.Cleanup(func() { findAgent = launch.Find })

	got, err := eng.NewSessionAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentStatus{
		{Agent: model.AgentClaude, Name: "Claude Code", Installed: true},
		{Agent: model.AgentCodex, Name: "Codex", Hint: "Codex was not found. Install it, then try again"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewSessionAgents = %+v, want %+v", got, want)
	}
	// The lookup is the new session command's: the fake runs "fake".
	if !reflect.DeepEqual(looked, []string{"fake", "fake"}) {
		t.Errorf("looked up %q", looked)
	}
}

func TestNewSessionLaunch(t *testing.T) {
	dir := t.TempDir()
	svc, _ := setupTestService(t)
	svc.Catalog().Apply(provider.ScanResult{Changed: []model.SessionMeta{{Ref: model.SessionRef{Agent: model.AgentClaude, ID: "d"}, CWD: dir}}})
	eng := NewEngine(WithService(svc))
	ctx := context.Background()

	cmd, err := eng.NewSessionLaunch(ctx, model.AgentClaude, dir)
	if err != nil || !reflect.DeepEqual(cmd, provider.Command{Argv: []string{"fake"}, Dir: dir}) {
		t.Errorf("NewSessionLaunch = %+v, %v", cmd, err)
	}
	if _, err := eng.NewSessionLaunch(ctx, model.AgentCodex, dir); !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	if _, err := eng.NewSessionLaunch(ctx, model.AgentClaude, "/home/user"); !errors.Is(err, ErrUnknownDirectory) {
		t.Errorf("unknown directory: %v", err)
	}
	// project1 is known but not on this disk.
	if _, err := eng.NewSessionLaunch(ctx, model.AgentClaude, "/home/user/project1"); err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("missing directory: %v", err)
	}
}
