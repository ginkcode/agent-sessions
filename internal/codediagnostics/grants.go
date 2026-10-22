package codediagnostics

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/configfile"
)

// GrantStore is a host-user-owned dedicated store, not the shared config.toml.
// All reads and read/modify/writes take the same cross-process configfile lock.
type GrantStore struct{ path string }

func NewGrantStore(configDir string) (*GrantStore, error) {
	if !filepath.IsAbs(configDir) {
		return nil, failure("invalidArgument", "Grant config directory must be absolute")
	}
	return &GrantStore{filepath.Join(configDir, "code-diagnostics", "grants.json")}, nil
}

type grantRecord struct {
	Project     Project `json:"project"`
	Fingerprint string  `json:"fingerprint"`
}
type grantDisk struct {
	Version int           `json:"version"`
	Grants  []grantRecord `json:"grants"`
}

func validProject(p Project) bool {
	return bounded(p.HostID, 256) && bounded(p.Root, 4096) && filepath.IsAbs(p.Root) && !hasParent(p.Root) && filepath.Clean(p.Root) == p.Root
}

func (s *GrantStore) transaction(edit func(*grantDisk) error, write bool) error {
	lock, err := configfile.Lock(s.path)
	if err != nil {
		return failure("busy", "Cannot lock execution grants")
	}
	defer func() { _ = lock.Unlock() }()
	disk := grantDisk{1, []grantRecord{}}
	f, err := os.Open(s.path)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		_ = f.Close()
		if readErr != nil || len(data) > 1<<20 || json.Unmarshal(data, &disk) != nil || disk.Version != 1 || disk.Grants == nil || len(disk.Grants) > 1024 {
			return failure("approvalRequired", "Execution grant store is invalid; repair or remove it before approval")
		}
		seen := map[Project]bool{}
		for _, g := range disk.Grants {
			if !validProject(g.Project) || !validFingerprint(g.Fingerprint) || seen[g.Project] {
				return failure("approvalRequired", "Execution grant records are invalid")
			}
			seen[g.Project] = true
		}
	} else if !os.IsNotExist(err) {
		return failure("approvalRequired", "Execution grants cannot be read")
	}
	if err := edit(&disk); err != nil {
		return err
	}
	if !write {
		return nil
	}
	if len(disk.Grants) > 1024 {
		return failure("limitExceeded", "Too many persistent execution grants")
	}
	data, err := json.Marshal(disk)
	if err != nil || len(data) > 1<<20 {
		return failure("limitExceeded", "Execution grants exceed storage limit")
	}
	if err := configfile.WriteAtomic(s.path, data); err != nil {
		return failure("toolFailed", "Cannot save execution grants")
	}
	return nil
}

func (s *GrantStore) lookup(p Project, fp string) (bool, error) {
	found := false
	err := s.transaction(func(d *grantDisk) error {
		for _, g := range d.Grants {
			if g.Project == p && g.Fingerprint == fp {
				found = true
			}
		}
		return nil
	}, false)
	return found, err
}

func (s *GrantStore) set(p Project, fp string) error {
	return s.transaction(func(d *grantDisk) error {
		kept := d.Grants[:0]
		for _, g := range d.Grants {
			if g.Project != p {
				kept = append(kept, g)
			}
		}
		d.Grants = kept
		if fp != "" {
			d.Grants = append(d.Grants, grantRecord{p, fp})
		}
		return nil
	}, true)
}

type Approval struct {
	State       string `json:"state"`
	Mode        string `json:"mode,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type grantState struct {
	fingerprint, mode string
	revoked           bool
	epoch             uint64
	ctx               context.Context
	cancel            context.CancelFunc
}

// Trust owns exactly one authenticated connection/app lifetime (including Local).
// Close MUST be called on disconnect/shutdown. Never reuse it on reconnect.
// One authority per host-user owns revocation; peer processes observe durable
// changes on lookup/preflight, not via a cross-process notification service.
type Trust struct {
	mu     sync.Mutex
	store  *GrantStore
	states map[Project]*grantState
	closed bool
}

func NewTrust(store *GrantStore) *Trust {
	return &Trust{store: store, states: map[Project]*grantState{}}
}

func (t *Trust) state(p Project) *grantState {
	s := t.states[p]
	if s == nil {
		ctx, cancel := context.WithCancel(context.Background())
		s = &grantState{ctx: ctx, cancel: cancel}
		t.states[p] = s
	}
	return s
}

func invalidate(s *grantState) {
	s.epoch++
	s.cancel()
	s.ctx, s.cancel = context.WithCancel(context.Background())
}

func (t *Trust) lookup(p Project, fp string) (Approval, error) {
	if t.closed {
		return Approval{}, failure("disconnected", "Execution trust owner is closed")
	}
	s := t.state(p)
	if s.revoked {
		return Approval{State: "revoked"}, nil
	}
	if s.mode == "session" && s.fingerprint == fp {
		return Approval{"approved", "session", fp}, nil
	}
	if t.store != nil {
		found, err := t.store.lookup(p, fp)
		if err != nil {
			invalidate(s)
			return Approval{}, err
		}
		if found {
			return Approval{"approved", "persistent", fp}, nil
		}
	}
	// A changed fingerprint or externally removed grant cancels old leases.
	invalidate(s)
	return Approval{State: "required"}, nil
}

func (t *Trust) Lookup(root AuthorizedRoot, fp string) (Approval, error) {
	if !validFingerprint(fp) {
		return Approval{}, failure("invalidArgument", "Invalid execution fingerprint")
	}
	if err := root.Revalidate(); err != nil {
		return Approval{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lookup(root.project, fp)
}

// Approve re-discovers the proposed metadata under the trust lock. Approval
// never launches tools. A stale proposal cannot approve a changed executable.
func (t *Trust) Approve(root AuthorizedRoot, tools []ToolInput, proposal, mode string) (Approval, error) {
	if mode != "session" && mode != "persistent" {
		return Approval{}, failure("invalidArgument", "Invalid approval mode")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Approval{}, failure("disconnected", "Execution trust owner is closed")
	}
	fp, err := Fingerprint(root, tools)
	if err != nil {
		return Approval{}, err
	}
	if fp != proposal {
		return Approval{}, failure("approvalChanged", "Execution identity changed; review a new approval proposal")
	}
	if mode == "persistent" {
		if t.store == nil {
			return Approval{}, failure("unsupported", "Persistent execution grants are unavailable")
		}
		if err := t.store.set(root.project, fp); err != nil {
			return Approval{}, err
		}
	}
	s := t.state(root.project)
	invalidate(s)
	s.fingerprint = fp
	s.mode = mode
	s.revoked = false
	return Approval{"approved", mode, fp}, nil
}

// Deny is Not now: it creates no grant, and does not revoke an existing grant.
func (t *Trust) Deny() {}

// Revoke invalidates generation BEFORE cancellation and durable removal. Even
// if the disk write fails, this owner remains revoked and its leases are dead.
func (t *Trust) Revoke(root AuthorizedRoot) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return failure("disconnected", "Execution trust owner is closed")
	}
	s := t.state(root.project)
	s.revoked = true
	s.fingerprint = ""
	s.mode = ""
	invalidate(s)
	if t.store != nil {
		return t.store.set(root.project, "")
	}
	return nil
}

func (t *Trust) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for _, s := range t.states {
		s.epoch++
		s.cancel()
		s.fingerprint = ""
		s.mode = ""
	}
}

// Lease is a queued/running-work cancellation hook, not a process launch permit.
// Future manager/supervisor must validate immediately before spawn AND before
// publication, and observe Context for process-tree cancellation. Epoch changes
// are committed before Context is canceled. Never hold work across reconnects.
type Lease struct {
	trust       *Trust
	project     Project
	epoch       uint64
	fingerprint string
	Context     context.Context
}

func (t *Trust) Preflight(root AuthorizedRoot, tools []ToolInput) (*Lease, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fp, err := Fingerprint(root, tools)
	if err != nil {
		invalidate(t.state(root.project))
		return nil, err
	}
	approval, err := t.lookup(root.project, fp)
	if err != nil {
		return nil, err
	}
	if approval.State != "approved" {
		return nil, failure("approvalRequired", "Explicit execution approval is required")
	}
	s := t.state(root.project)
	return &Lease{t, root.project, s.epoch, fp, s.ctx}, nil
}

// Validate also rehashes identity and rechecks the durable grant, not just epoch.
func (l *Lease) Validate(root AuthorizedRoot, tools []ToolInput) error {
	t := l.trust
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.state(l.project)
	if t.closed || s.epoch != l.epoch || root.project != l.project {
		return failure("staleContext", "Execution trust generation is no longer current")
	}
	fp, err := Fingerprint(root, tools)
	if err != nil || fp != l.fingerprint {
		invalidate(s)
		return failure("approvalChanged", "Execution identity changed")
	}
	a, err := t.lookup(l.project, fp)
	if err != nil {
		return err
	}
	if a.State != "approved" || s.epoch != l.epoch {
		return failure("approvalRequired", "Execution approval is no longer current")
	}
	return nil
}
