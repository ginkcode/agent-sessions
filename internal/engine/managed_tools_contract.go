package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	mt "github.com/ginkcode/agent-sessions/internal/managedtools"
)

// ManagedToolsBackend is optional. Inputs contain no executable, destination,
// source, version or integrity override. Status polling never retains history.
type ManagedToolsBackend interface {
	ManagedTool(context.Context, ManagedToolRequest) (ManagedToolState, error)
}

type ManagedToolRequest struct {
	Action         string `json:"action"`
	ExpectedHostID string `json:"expectedHostId"`
	Tool           string `json:"tool,omitempty"`
	Token          string `json:"token,omitempty"`
	JobID          string `json:"jobId,omitempty"`
}

type ManagedToolState struct {
	HostID   string       `json:"hostId"`
	State    string       `json:"state"`
	Message  string       `json:"message,omitempty"`
	JobID    string       `json:"jobId,omitempty"`
	Proposal *mt.Proposal `json:"proposal,omitempty"`
}

type managedJob struct {
	state  ManagedToolState
	cancel context.CancelFunc
}

func managedFailure() error {
	return diagnosticError("invalidArgument", "Invalid managed tool request or stale consent")
}

// ManagedTool offers coarse progress (installing then terminal), not fabricated
// compiler percentages. Only one job and one pending consent exist per owner.
func (e *Engine) ManagedTool(ctx context.Context, p ManagedToolRequest) (ManagedToolState, error) {
	if len(p.ExpectedHostID) > 256 || p.ExpectedHostID == "" || len(p.Token) > 256 || len(p.JobID) > 64 || len(p.Tool) > 32 {
		return ManagedToolState{}, managedFailure()
	}
	switch p.Action {
	case "status", "propose", "install", "cancel", "dismiss":
	default:
		return ManagedToolState{}, managedFailure()
	}
	host, err := e.CodeDiagnosticsHostID(ctx)
	if err != nil || host != p.ExpectedHostID {
		return ManagedToolState{}, diagnosticError("staleContext", "Actual local host identity required")
	}
	out := ManagedToolState{HostID: host, State: "unavailable", Message: "Backend operator must explicitly review and provision an installed Go >=1.27 tree; automatic provisioning is disabled"}
	o, err := e.managedOwner(ctx)
	if err != nil {
		return out, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.managedTools != o || o.ctx.Err() != nil {
		return ManagedToolState{}, diagnosticError("disconnected", "Connection changed")
	}
	out.State, out.Message = "idle", ""
	switch p.Action {
	case "status":
		if o.job != nil {
			if p.JobID != "" && p.JobID != o.job.state.JobID {
				return ManagedToolState{}, managedFailure()
			}
			return o.job.state, nil
		}
		_, discoverErr := o.installer.Discover()
		switch {
		case discoverErr == nil:
			out.State = "available"
		case os.IsNotExist(discoverErr):
			out.State = "missingTool"
		default:
			out.State, out.Message = "unavailable", "Managed installation is invalid; backend operator review is required"
		}
	case "propose":
		if p.Tool != "gopls" || (o.job != nil && o.job.state.State == "installing") {
			return ManagedToolState{}, managedFailure()
		}
		proposal, err := o.installer.Propose(p.Tool)
		if err != nil {
			return ManagedToolState{}, managedFailure()
		}
		o.pending = &proposal
		out.State, out.Proposal = "proposed", &proposal
	case "dismiss":
		o.pending = nil
		o.installer.Dismiss()
	case "cancel":
		if o.job == nil || p.JobID != o.job.state.JobID {
			return ManagedToolState{}, managedFailure()
		}
		o.job.cancel()
		return o.job.state, nil
	case "install":
		if o.pending == nil || p.Token != o.pending.Token || !time.Now().Before(o.pending.Expires) || (o.job != nil && o.job.state.State == "installing") {
			return ManagedToolState{}, managedFailure()
		}
		o.pending = nil // reserve/consume synchronously before acknowledging the job
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			o.installer.Dismiss()
			return ManagedToolState{}, managedFailure()
		}
		jobCtx, cancel := context.WithTimeout(o.ctx, 10*time.Minute)
		out.State, out.JobID = "installing", hex.EncodeToString(id[:])
		job := &managedJob{state: out, cancel: cancel}
		o.job = job
		o.workers.Add(1) // admission is serialized with owner removal
		go func() {
			defer o.workers.Done()
			defer cancel()
			_, installErr := e.installManagedOwned(jobCtx, o, p.Token)
			e.mu.Lock()
			defer e.mu.Unlock()
			job.state.State = "installed"
			if installErr != nil {
				job.state.State = "failed"
				job.state.Message = "Installation failed; request fresh approval to retry"
				if jobCtx.Err() != nil {
					job.state.State = "canceled"
					job.state.Message = "Installation canceled"
				} else if errors.Is(installErr, mt.ErrPrerequisite) {
					job.state.Message = "Reviewed Go >=1.27 is required; automatic upgrades are disabled"
				}
			}
		}()
	}
	return out, nil
}
