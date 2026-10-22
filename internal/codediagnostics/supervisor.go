package codediagnostics

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Command is adapter-owned, never decoded from an RPC request. Env is the entire
// environment, not overrides of the backend's (possibly secret-bearing) environment.
// Directory is relative to the approved root; empty means the root itself.
type Command struct {
	ProviderID    string
	Executable    string
	Args          []string
	Env           []string
	Directory     string
	AdapterPolicy string
	AcceptedExits []int
}

// Policy binds the exact invocation to CD-02 approval. Adapters must put this
// value in the selected ToolInput.Policy, and enumerate runtime/config identities.
func (c Command) Policy() string {
	data, _ := json.Marshal(struct {
		Version     int
		Command     Command
		Environment []string
	}{1, c, c.environment()})
	return string(data)
}

func (c Command) environment() []string {
	env := slices.Clone(c.Env)
	// Defense in depth, not a sandbox: tools/plugins can still access the network.
	return append(env, "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "npm_config_offline=true", "npm_config_ignore_scripts=true", "COREPACK_ENABLE_NETWORK=0")
}

// Process owns its stream pumps. Wait must finish after Terminate and Close;
// Terminate must target the owned tree, not merely its original parent.
type Process interface {
	Wait() (int, error)
	Terminate() error
	Close() error
}
type (
	Invocation struct {
		Executable     string
		Args, Env      []string
		Directory      string
		Stdout, Stderr io.Writer
		CleanupTimeout time.Duration
	}
	ProcessFactory func(Invocation) (Process, error)
	Timer          interface {
		Done() <-chan time.Time
		Stop()
	}
	realTimer struct{ *time.Timer }
)

func (t realTimer) Done() <-chan time.Time { return t.C }
func (t realTimer) Stop()                  { t.Timer.Stop() }

type SupervisorOptions struct {
	Timeout        time.Duration
	CaptureBytes   int
	MessageBytes   int
	CleanupTimeout time.Duration
	Pending        int
	Start          ProcessFactory
	NewTimer       func(time.Duration) Timer
}

func (o SupervisorOptions) defaults() SupervisorOptions {
	if o.Timeout <= 0 || o.Timeout > 120*time.Second {
		o.Timeout = 120 * time.Second
	}
	if o.CaptureBytes <= 0 || o.CaptureBytes > 10<<20 {
		o.CaptureBytes = 10 << 20
	}
	if o.MessageBytes <= 0 || o.MessageBytes > 8192 {
		o.MessageBytes = 8192
	}
	if o.CleanupTimeout <= 0 || o.CleanupTimeout > 5*time.Second {
		o.CleanupTimeout = 5 * time.Second
	}
	if o.Pending <= 0 || o.Pending > 32 {
		o.Pending = 32
	}
	if o.Start == nil {
		o.Start = startProcess
	}
	if o.NewTimer == nil {
		o.NewTimer = func(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }
	}
	return o
}

type OutcomeKind string

const (
	OutcomeAccepted     OutcomeKind = "accepted"
	OutcomeToolFailed   OutcomeKind = "toolFailed"
	OutcomeLaunchFailed OutcomeKind = "launchFailed"
	OutcomeCanceled     OutcomeKind = "canceled"
	OutcomeTimeout      OutcomeKind = "timeout"
	OutcomeLimit        OutcomeKind = "limitExceeded"
	OutcomeTrust        OutcomeKind = "approvalRequired"
)

// Output is private parser input; never log, persist, or expose it as an error.
// Stderr is deliberately NOT a user-facing notice. Notices below contain no raw output.
type Outcome struct {
	RunID              string
	Kind               OutcomeKind
	ExitCode           int
	Stdout, Stderr     []byte
	Partial, Truncated bool
	Notice             string
	FindingBudget      int
	CleanupFailed      bool
}

type (
	runKey struct {
		project  Project
		provider string
	}
	Run struct {
		id         string
		key        runKey
		command    Command
		root       AuthorizedRoot
		tools      []ToolInput
		lease      *Lease
		ctx        context.Context
		cancel     context.CancelFunc
		done       chan struct{}
		outcome    Outcome
		supervisor *Supervisor
		current    bool // protected by supervisor.mu
		finished   bool
	}
)

func (r *Run) ID() string            { return r.id }
func (r *Run) Done() <-chan struct{} { return r.done }
func (r *Run) Cancel()               { r.cancel() }

// Result waits for cleanup. It returns a copy so callers cannot mutate retained output.
func (r *Run) Result() Outcome {
	<-r.done
	o := r.outcome
	o.Stdout, o.Stderr = slices.Clone(o.Stdout), slices.Clone(o.Stderr)
	return o
}

// ValidatePublication is a final guard, not a result-store commit. CD-07 must
// atomically guard its own generation/run identity when replacing results.
func (r *Run) ValidatePublication() error {
	s := r.supervisor
	s.mu.Lock()
	current := r.current && !s.closed && r.ctx.Err() == nil
	s.mu.Unlock()
	if !current {
		return failure("staleContext", "Checker run is no longer current")
	}
	return r.lease.Validate(r.root, r.tools)
}

type Supervisor struct {
	mu       sync.Mutex
	cond     *sync.Cond
	options  SupervisorOptions
	trust    *Trust
	queue    []*Run
	latest   map[runKey]*Run
	active   map[Project]bool
	closed   bool
	workers  sync.WaitGroup
	watchers sync.WaitGroup
}

// NewSupervisor owns two workers for one project-host backend. The backend must
// share this owner across projects and Close it on disconnect/shutdown.
func NewSupervisor(trust *Trust, options SupervisorOptions) *Supervisor {
	s := &Supervisor{trust: trust, options: options.defaults(), latest: map[runKey]*Run{}, active: map[Project]bool{}}
	s.cond = sync.NewCond(&s.mu)
	s.workers.Add(2)
	for range 2 {
		go s.worker()
	}
	return s
}

// Submit coalesces an identical live request. supersede cancels the old provider
// run before queueing its replacement; another provider in the same project waits.
// At most 32 queued and 64 remembered run identities exist; eviction invalidates
// publication of completed runs. Callers own opaque unique run IDs.
func (s *Supervisor) Submit(ctx context.Context, root AuthorizedRoot, tools []ToolInput, c Command, id string, supersede bool) (*Run, error) {
	if s.trust == nil || ctx == nil || !bounded(id, 256) {
		return nil, failure("invalidArgument", "Checker owner, context and run identity are required")
	}
	c.Args, c.Env, c.AcceptedExits = slices.Clone(c.Args), slices.Clone(c.Env), slices.Clone(c.AcceptedExits)
	inputs := slices.Clone(tools)
	for i := range inputs {
		inputs[i].ConfigPaths = slices.Clone(inputs[i].ConfigPaths)
	}
	if err := validateCommand(root, inputs, c); err != nil {
		return nil, err
	}
	lease, err := s.trust.Preflight(root, inputs)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, failure("disconnected", "Checker supervisor is closed")
	}
	if ctx.Err() != nil || lease.Context.Err() != nil {
		return nil, failure("canceled", "Checker request was canceled")
	}
	key := runKey{root.Project(), c.ProviderID}
	old := s.latest[key]
	if old != nil && !old.finished && !supersede {
		if old.command.Policy() == c.Policy() && old.lease.fingerprint == lease.fingerprint && old.ctx.Err() == nil {
			return old, nil
		}
		return nil, failure("busy", "A different checker run is already pending")
	}
	if len(s.queue) >= s.options.Pending {
		return nil, failure("busy", "Checker queue is full")
	}
	if old != nil && old.id == id {
		return nil, failure("invalidArgument", "Replacement needs a fresh run identity")
	}
	if len(s.latest) >= 64 && old == nil {
		for k, r := range s.latest {
			if r.finished {
				r.current = false
				delete(s.latest, k)
				break
			}
		}
		if len(s.latest) >= 64 {
			return nil, failure("busy", "Checker identity budget is full")
		}
	}
	if old != nil {
		old.current = false
		old.cancel()
	}
	runCtx, cancel := context.WithCancel(ctx)
	r := &Run{id: id, key: key, command: c, root: root, tools: inputs, lease: lease, ctx: runCtx, cancel: cancel, done: make(chan struct{}), supervisor: s, current: true}
	s.latest[key] = r
	s.queue = append(s.queue, r)
	s.watchers.Add(1)
	go func() {
		defer s.watchers.Done()
		select {
		case <-lease.Context.Done():
			cancel()
		case <-runCtx.Done():
		case <-r.done:
			return
		}
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	s.cond.Broadcast()
	return r, nil
}

func (s *Supervisor) worker() {
	defer s.workers.Done()
	for {
		s.mu.Lock()
		var r *Run
		for r == nil {
			for i, candidate := range s.queue {
				if candidate.ctx.Err() != nil || !s.active[candidate.key.project] {
					r = candidate
					s.queue = slices.Delete(s.queue, i, i+1)
					break
				}
			}
			if r != nil {
				break
			}
			if s.closed && len(s.queue) == 0 {
				s.mu.Unlock()
				return
			}
			s.cond.Wait()
		}
		// Canceled queued work does not acquire or release another run's project slot.
		owns := r.ctx.Err() == nil
		if owns {
			s.active[r.key.project] = true
		}
		s.mu.Unlock()
		o := s.execute(r)
		s.mu.Lock()
		r.outcome, r.finished = o, true
		if owns {
			delete(s.active, r.key.project)
		}
		close(r.done)
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// Close is idempotent, cancels queued/running work, and waits outside owner locks.
// Trust.Close/Revoke cancel the same leases; Close does not mutate persistent grants.
func (s *Supervisor) Close() {
	s.mu.Lock()
	s.closed = true
	for _, r := range s.latest {
		r.current = false
		r.cancel()
	}
	// Superseded queued runs are no longer in latest.
	for _, r := range s.queue {
		r.cancel()
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	s.workers.Wait()
	s.watchers.Wait()
}

func validateCommand(root AuthorizedRoot, tools []ToolInput, c Command) error {
	if !bounded(c.ProviderID, 256) || !bounded(c.Executable, 4096) || !filepath.IsAbs(c.Executable) || len(c.Args) > 256 || len(c.Env) > 128 || len(c.AcceptedExits) == 0 || len(c.AcceptedExits) > 32 || !bounded(c.AdapterPolicy, 2048) {
		return failure("invalidArgument", "Invalid fixed checker invocation")
	}
	total := 0
	for _, arg := range c.Args {
		total += len(arg)
		if strings.ContainsRune(arg, 0) {
			return failure("invalidArgument", "Invalid checker argument")
		}
	}
	seen := map[string]bool{}
	for _, env := range c.environment() {
		key, _, ok := strings.Cut(env, "=")
		key = strings.ToUpper(key) // conservative duplicate handling on every host
		total += len(env)
		if !ok || !bounded(env, 8192) || key == "" || seen[key] {
			return failure("invalidArgument", "Invalid or reserved checker environment")
		}
		seen[key] = true
	}
	if total > 64<<10 {
		return failure("limitExceeded", "Checker invocation exceeds size limit")
	}
	matched := false
	for _, tool := range tools {
		if tool.ProviderID == c.ProviderID && tool.Executable == c.Executable && tool.Policy == c.Policy() {
			matched = true
		}
	}
	if !matched {
		return failure("approvalChanged", "Invocation does not match approved adapter policy")
	}
	_, err := commandDirectory(root, c.Directory)
	return err
}

func commandDirectory(root AuthorizedRoot, rel string) (string, error) {
	if err := root.Revalidate(); err != nil {
		return "", err
	}
	if rel == "" {
		return root.Project().Root, nil
	}
	if err := ValidatePath(rel); err != nil {
		return "", err
	}
	real, _, err := canonicalDirectory(filepath.Join(root.Project().Root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(root.Project().Root, real)
	if err != nil || !filepath.IsLocal(within) {
		return "", failure("unauthorized", "Checker directory escapes approved root")
	}
	return real, nil
}

func (s *Supervisor) execute(r *Run) Outcome {
	o := Outcome{RunID: r.id, ExitCode: -1, FindingBudget: 10000}
	if r.ctx.Err() != nil || r.lease.Context.Err() != nil {
		o.Kind = OutcomeCanceled
		o.Partial = true
		return o
	}
	dir, err := commandDirectory(r.root, r.command.Directory)
	if err == nil {
		err = r.lease.Validate(r.root, r.tools)
	}
	if err != nil {
		o.Kind = OutcomeTrust
		o.Partial = true
		o.Notice = "Checker approval or directory changed"
		return o
	}
	if r.ctx.Err() != nil || r.lease.Context.Err() != nil {
		o.Kind = OutcomeCanceled
		o.Partial = true
		return o
	}
	capture := newCapture(s.options.CaptureBytes, s.options.MessageBytes)
	timer := s.options.NewTimer(s.options.Timeout)
	defer timer.Stop()
	p, err := s.options.Start(Invocation{r.command.Executable, r.command.Args, r.command.environment(), dir, capture.writer(false), capture.writer(true), s.options.CleanupTimeout})
	if err != nil {
		o.Kind = OutcomeLaunchFailed
		o.Partial = true
		o.Notice = "Checker could not be started"
		return o
	}
	type exit struct {
		code int
		err  error
	}
	waited := make(chan exit, 1)
	go func() { n, err := p.Wait(); waited <- exit{n, err} }()
	var result exit
	select {
	case result = <-waited:
		if result.err == nil && slices.Contains(r.command.AcceptedExits, result.code) {
			o.Kind = OutcomeAccepted
		} else {
			o.Kind = OutcomeToolFailed
		}
	case <-r.ctx.Done():
		o.Kind = OutcomeCanceled
	case <-r.lease.Context.Done():
		o.Kind = OutcomeCanceled
	case <-timer.Done():
		o.Kind = OutcomeTimeout
	case <-capture.limit:
		o.Kind = OutcomeLimit
	}
	// Cleanup also runs on ordinary parent exit: descendants may still own pipes.
	if err := p.Terminate(); err != nil {
		o.CleanupFailed = true
	}
	if err := p.Close(); err != nil {
		o.CleanupFailed = true
	}
	if o.Kind != OutcomeAccepted && o.Kind != OutcomeToolFailed {
		result = <-waited
	}
	o.ExitCode = result.code
	o.Stdout, o.Stderr, o.Truncated = capture.result()
	if o.Truncated {
		o.Kind = OutcomeLimit
	}
	if r.ctx.Err() != nil || r.lease.Context.Err() != nil {
		o.Kind = OutcomeCanceled
	}
	o.Partial = o.Kind != OutcomeAccepted || o.Truncated || o.CleanupFailed
	if o.CleanupFailed {
		o.Kind = OutcomeToolFailed
		o.Notice = "Checker process cleanup could not be verified"
	}
	return o
}
