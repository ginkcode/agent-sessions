package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/launch"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
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
