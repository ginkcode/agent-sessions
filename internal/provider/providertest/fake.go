// Package providertest provides fake implementations of provider interfaces for testing.
package providertest

import (
	"context"
	"fmt"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

var (
	_ provider.Provider     = (*Fake)(nil)
	_ provider.LiveDetector = (*Fake)(nil)
)

// Fake is a test provider with configurable responses and artificial latency.
type Fake struct {
	AgentID       model.AgentID
	Name          string
	DetectionData provider.Detection
	Sessions      []model.SessionMeta
	RemovedRefs   []model.SessionRef
	Transcripts   map[string]*model.Transcript // key: ref.ID
	Blobs         map[string][]byte            // key: ref.Key() + ":" + blobKey
	Watch         []string
	ScanDelay     time.Duration
	ScanStarted   chan struct{} // optional buffered entry signal for tests
	LoadDelay     time.Duration
	LiveDelay     time.Duration
	LiveMap       map[string]provider.LiveInfo
	LiveErr       error
	DetectErr     error
	ScanErr       error
	LoadErr       error
	BlobErr       error

	// StateOverride, when non-nil, replaces the ScanState returned by Scan.
	StateOverride *provider.ScanState
	// LastPrev records the ScanState passed to the most recent Scan call.
	LastPrev   provider.ScanState
	ScanCalled int
}

// NewFake returns a Fake provider initialized with basic defaults.
func NewFake(id model.AgentID, name string) *Fake {
	return &Fake{
		AgentID:     id,
		Name:        name,
		Transcripts: make(map[string]*model.Transcript),
		Blobs:       make(map[string][]byte),
		LiveMap:     make(map[string]provider.LiveInfo),
	}
}

func (f *Fake) ID() model.AgentID {
	if f.AgentID == "" {
		return model.AgentID("fake")
	}
	return f.AgentID
}

func (f *Fake) DisplayName() string {
	if f.Name == "" {
		return "Fake Provider"
	}
	return f.Name
}

func (f *Fake) Detect(ctx context.Context) (provider.Detection, error) {
	if f.DetectErr != nil {
		return provider.Detection{}, f.DetectErr
	}
	return f.DetectionData, nil
}

func (f *Fake) Scan(ctx context.Context, prev provider.ScanState) (provider.ScanResult, error) {
	f.ScanCalled++
	f.LastPrev = prev
	if f.ScanStarted != nil {
		select {
		case f.ScanStarted <- struct{}{}:
		default:
		}
	}
	if f.ScanDelay > 0 {
		select {
		case <-time.After(f.ScanDelay):
		case <-ctx.Done():
			return provider.ScanResult{}, ctx.Err()
		}
	}
	if f.ScanErr != nil {
		return provider.ScanResult{}, f.ScanErr
	}
	state := provider.ScanState{
		Sources: make(map[string]provider.SourceState),
	}
	if f.StateOverride != nil {
		state = *f.StateOverride
	}
	return provider.ScanResult{
		Changed: f.Sessions,
		Removed: f.RemovedRefs,
		State:   state,
	}, nil
}

func (f *Fake) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	if f.LoadDelay > 0 {
		select {
		case <-time.After(f.LoadDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.LoadErr != nil {
		return nil, f.LoadErr
	}
	tr, ok := f.Transcripts[ref.ID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", ref.Key())
	}
	return tr, nil
}

func (f *Fake) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	if f.BlobErr != nil {
		return nil, f.BlobErr
	}
	b, ok := f.Blobs[ref.Key()+":"+key]
	if !ok {
		return nil, fmt.Errorf("blob not found: %s:%s", ref.Key(), key)
	}
	return b, nil
}

func (f *Fake) WatchPaths() []string {
	return f.Watch
}

func (f *Fake) ResumeCommand(m model.SessionMeta) provider.Command {
	return provider.Command{
		Argv: []string{"fake", "--resume", m.Ref.ID},
		Dir:  m.CWD,
	}
}

func (f *Fake) Live(ctx context.Context) (map[string]provider.LiveInfo, error) {
	if f.LiveDelay > 0 {
		select {
		case <-time.After(f.LiveDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.LiveErr != nil {
		return nil, f.LiveErr
	}
	return f.LiveMap, nil
}
