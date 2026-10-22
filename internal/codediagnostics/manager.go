package codediagnostics

import (
	"context"
	"slices"
	"strings"
	"sync"
)

type managedRun struct {
	id     string
	cancel context.CancelFunc
}
type attachment struct {
	root     AuthorizedRoot
	snapshot Snapshot
	adapters map[string]CheckerAdapter
	tools    []ToolInput
	runs     map[string]*managedRun
	events   [][]Event
	gap      bool

	// Live document lifecycles are owned by this attachment generation: revoke,
	// detach and invalidation drop them. Closed identities are a bounded FIFO.
	docs        map[string]*documentState
	closedDocs  map[string]bool
	closedOrder []string
}

// Manager owns one authenticated connection and its Supervisor and Trust. Close
// (also the disconnect hook) invalidates ownership before joining process cleanup.
// Events are pulled, never delivered through callbacks under the owner lock.
type Manager struct {
	mu          sync.Mutex
	connection  string
	trust       *Trust
	supervisor  *Supervisor
	limits      ManagerLimits
	attachments map[string]*attachment
	pending     int
	projects    map[Project]bool // bounded connection-lifetime identity ledger
	closed      bool
	workers     sync.WaitGroup
	provider    DocumentProvider // nil: live document operations are unsupported
}

func NewManager(connection string, trust *Trust, supervisor *Supervisor, limits ManagerLimits) (*Manager, error) {
	if !bounded(connection, 256) || trust == nil || supervisor == nil || supervisor.trust != trust {
		return nil, failure("invalidArgument", "A connection and shared trust/supervisor owner are required")
	}
	return &Manager{connection: connection, trust: trust, supervisor: supervisor, limits: limits.defaults(), attachments: map[string]*attachment{}, projects: map[Project]bool{}}, nil
}

// Attach accepts only a previously catalog-authorized root and backend-owned
// registrations. There is no probing, process execution or client command input.
func (m *Manager) Attach(root AuthorizedRoot, version int, registrations []Registration) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if version != 1 {
		return Snapshot{}, failure("unsupported", "Unsupported diagnostics contract")
	}
	if m.closed {
		return Snapshot{}, failure("disconnected", "Diagnostics owner is closed")
	}
	if len(m.attachments) >= m.limits.Attachments {
		return Snapshot{}, failure("busy", "Attachment budget is full")
	}
	if err := root.Revalidate(); err != nil {
		return Snapshot{}, err
	}
	if !m.projects[root.Project()] && len(m.projects) >= 64 {
		return Snapshot{}, failure("limitExceeded", "Connection project identity budget is full; reconnect required")
	}
	// Count attempted discovery too: Trust can retain failed lookup identities.
	m.projects[root.Project()] = true
	for _, a := range m.attachments {
		if a.root.Project() == root.Project() {
			return Snapshot{}, failure("busy", "Project already attached")
		}
	}
	if len(registrations) > 32 {
		return Snapshot{}, failure("limitExceeded", "Provider budget exceeded")
	}
	a, err := m.prepare(root, registrations)
	if err != nil {
		return Snapshot{}, err
	}
	m.attachments[a.snapshot.Context.AttachmentID] = a
	return copySnapshot(a.snapshot), nil
}

// CompleteDiscovery installs backend-only metadata on an attachment that has not
// yet offered an approval proposal. Detached or revoked epochs cannot be revived.
func (m *Manager) CompleteDiscovery(c Context, registrations []Registration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	if a.snapshot.Proposal != nil || a.snapshot.Approval.State != "required" {
		return failure("staleContext", "Discovery epoch is no longer current")
	}
	next, err := m.prepare(a.root, registrations)
	if err != nil {
		return err
	}
	next.snapshot.Context = a.snapshot.Context
	next.snapshot.Sequence = a.snapshot.Sequence
	a.adapters, a.tools = next.adapters, next.tools
	a.snapshot = next.snapshot
	m.commit(a)
	return nil
}

func (m *Manager) prepare(root AuthorizedRoot, registrations []Registration) (*attachment, error) {
	if len(registrations) > 32 {
		return nil, failure("limitExceeded", "Provider budget exceeded")
	}
	a := &attachment{root: root, adapters: map[string]CheckerAdapter{}, runs: map[string]*managedRun{}}
	a.snapshot = Snapshot{ContractVersion: 1, Context: Context{m.connection, opaqueID(), opaqueID()}, Approval: Approval{State: "required"}, Providers: []ProviderState{}, ResultSets: []ResultSet{}}
	seen := map[string]bool{}
	for _, r := range registrations {
		if !bounded(r.ProviderID, 256) || seen[r.ProviderID] || !availability(r.Availability) {
			return nil, failure("invalidArgument", "Invalid provider registration")
		}
		seen[r.ProviderID] = true
		state := ProviderState{ProviderID: r.ProviderID, Availability: r.Availability, Phase: "idle", Freshness: "uncertain", Coverage: Coverage{Kind: "savedProject", Roots: []string{}, Partial: true, ExternalChanges: "unwatched"}}
		switch {
		case r.Live != nil:
			if err := validateLive(r); err != nil {
				return nil, err
			}
			state = r.Live.State
			state.Coverage.Roots = slices.Clone(state.Coverage.Roots)
			if r.Availability == "available" {
				a.tools = append(a.tools, r.Live.Server.Tool())
			}
		case r.Adapter != nil:
			a.adapters[r.ProviderID] = r.Adapter
			a.tools = append(a.tools, r.Adapter.Tools()...)
		case r.Availability == "available":
			return nil, failure("invalidArgument", "Available provider needs an adapter")
		}
		a.snapshot.Providers = append(a.snapshot.Providers, state)
	}
	slices.SortFunc(a.snapshot.Providers, func(a, b ProviderState) int { return strings.Compare(a.ProviderID, b.ProviderID) })
	if len(a.tools) > 0 {
		fp, err := Fingerprint(root, a.tools)
		if err != nil {
			return nil, err
		}
		approval, err := m.trust.Lookup(root, fp)
		if err != nil {
			return nil, err
		}
		a.snapshot.Approval = approval
		a.snapshot.Proposal = &Proposal{Fingerprint: fp, Tools: []ProposalTool{}}
		for _, t := range a.tools {
			paths := slices.Clone(t.ConfigPaths)
			if paths == nil {
				paths = []string{}
			}
			a.snapshot.Proposal.Tools = append(a.snapshot.Proposal.Tools, ProposalTool{t.ProviderID, t.Executable, paths})
		}
	}
	m.syncAvailability(a)
	// Leave enough per-part room for metadata plus a maximal escaped finding.
	if encodedSize(a.snapshot) > min(128<<10, m.limits.EventBytes/4) || encodedSize(a.snapshot) > m.limits.SnapshotBytes {
		return nil, failure("limitExceeded", "Provider metadata exceeds delivery budget")
	}
	return a, nil
}

func availability(s string) bool {
	return slices.Contains([]string{"available", "approvalRequired", "unsupported", "missingTool", "disabled"}, s)
}

func (m *Manager) syncAvailability(a *attachment) {
	for i := range a.snapshot.Providers {
		p := &a.snapshot.Providers[i]
		if p.Availability == "available" || p.Availability == "approvalRequired" {
			p.Availability = "approvalRequired"
			if a.snapshot.Approval.State == "approved" {
				p.Availability = "available"
			}
		}
	}
}

func (m *Manager) lookup(c Context) (*attachment, error) {
	if m.closed {
		return nil, failure("disconnected", "Diagnostics owner is closed")
	}
	a := m.attachments[c.AttachmentID]
	if a == nil || a.snapshot.Context != c {
		return nil, failure("staleContext", "Diagnostics attachment is no longer current")
	}
	return a, nil
}

func (m *Manager) GetSnapshot(c Context) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return Snapshot{}, err
	}
	return copySnapshot(a.snapshot), nil
}

func (m *Manager) Approve(c Context, fingerprint, mode string) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return Snapshot{}, err
	}
	if a.snapshot.Proposal == nil || a.snapshot.Proposal.Fingerprint != fingerprint {
		return Snapshot{}, failure("approvalChanged", "Approval proposal is no longer current")
	}
	approval, err := m.trust.Approve(a.root, a.tools, fingerprint, mode)
	if err != nil {
		return Snapshot{}, err
	}
	// Reapproval kills leases; invalidate manager runs too, before a completion can commit.
	m.cancelAll(a)
	a.snapshot.Approval = approval
	m.syncAvailability(a)
	m.commit(a)
	return copySnapshot(a.snapshot), nil
}

func (m *Manager) cancelAll(a *attachment) {
	for id, r := range a.runs {
		delete(a.runs, id)
		r.cancel()
		m.stale(a, id, "canceled")
	}
}

func (m *Manager) Revoke(c Context) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return Snapshot{}, err
	}
	a.snapshot.Context.Generation = opaqueID()
	m.releaseDocuments(a)
	a.events = nil
	a.gap = false
	m.cancelAll(a)
	a.snapshot.ResultSets = []ResultSet{}
	a.snapshot.Approval = Approval{State: "revoked"}
	for i := range a.snapshot.Providers {
		p := &a.snapshot.Providers[i]
		p.RunID = ""
		p.Phase = "idle"
		p.Freshness = "uncertain"
		p.Coverage.Partial = true
		p.Truncated = false
		p.Failure = nil
	}
	m.syncAvailability(a)
	// Invalidate manager epoch first even if persistent removal fails.
	err = m.trust.Revoke(a.root)
	m.commit(a)
	return copySnapshot(a.snapshot), err
}

func (m *Manager) Detach(c Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	delete(m.attachments, c.AttachmentID)
	m.cancelAll(a)
	m.releaseDocuments(a)
	m.idleDocuments(a)
	return nil
}

// Invalidate requires reattach; unlike Revoke it cannot switch a live generation.
func (m *Manager) Invalidate(c Context) error { return m.Detach(c) }

func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for id, a := range m.attachments {
		delete(m.attachments, id)
		m.cancelAll(a)
		m.releaseDocuments(a)
	}
	m.mu.Unlock()
	m.trust.Close()
	m.supervisor.Close()
	m.workers.Wait()
}

// Run coalesces current work. supersede is a backend policy flag, not an argv
// escape hatch. A bounded number of adapter goroutines may exist, including
// canceled work still cleaning up. A full budget leaves current work untouched.
func (m *Manager) Run(c Context, ids []string, supersede bool) ([]RunIdentity, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return nil, err
	}
	if ids != nil && len(ids) == 0 {
		return nil, failure("invalidArgument", "Provider list is empty")
	}
	if a.snapshot.Approval.State != "approved" {
		return nil, failure("approvalRequired", "Explicit execution approval is required")
	}
	if ids == nil {
		for _, p := range a.snapshot.Providers {
			if p.Availability == "available" && a.adapters[p.ProviderID] != nil {
				ids = append(ids, p.ProviderID)
			}
		}
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	needed := 0
	for i, id := range ids {
		p := provider(a, id)
		if p == nil || p.Availability != "available" || a.adapters[id] == nil || (i > 0 && ids[i-1] == id) {
			return nil, failure("invalidArgument", "Provider is unavailable or duplicated")
		}
		if a.runs[id] == nil || supersede {
			needed++
		}
	}
	if m.pending+needed > m.limits.Pending {
		return nil, failure("busy", "Checker manager queue is full")
	}
	lease, err := m.trust.Preflight(a.root, a.tools)
	if err != nil {
		return nil, err
	}
	out := []RunIdentity{}
	for _, id := range ids {
		old := a.runs[id]
		if old != nil && !supersede {
			out = append(out, RunIdentity{id, old.id})
			continue
		}
		if old != nil {
			old.cancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		r := &managedRun{opaqueID(), cancel}
		a.runs[id] = r
		p := provider(a, id)
		p.RunID = r.id
		p.Phase = "checking"
		p.Failure = nil
		m.pending++
		m.workers.Add(1)
		tools := cloneTools(a.tools)
		go m.execute(ctx, a, c, id, r, lease, tools)
		out = append(out, RunIdentity{id, r.id})
	}
	if needed > 0 {
		m.commit(a)
	}
	return out, nil
}

func cloneTools(in []ToolInput) []ToolInput {
	out := slices.Clone(in)
	for i := range out {
		out[i].ConfigPaths = slices.Clone(out[i].ConfigPaths)
	}
	return out
}

func provider(a *attachment, id string) *ProviderState {
	for i := range a.snapshot.Providers {
		if a.snapshot.Providers[i].ProviderID == id {
			return &a.snapshot.Providers[i]
		}
	}
	return nil
}

func (m *Manager) execute(ctx context.Context, a *attachment, c Context, id string, r *managedRun, lease *Lease, tools []ToolInput) {
	defer m.workers.Done()
	defer r.cancel()
	result := a.adapters[id].RunApproved(ctx, m.supervisor, r.id, tools)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending--
	current, err := m.lookup(c)
	if err != nil || current != a || a.runs[id] != r {
		return
	}
	delete(a.runs, id)
	if ctx.Err() != nil {
		m.stale(a, id, "canceled")
		m.commit(a)
		return
	}
	if lease.Validate(a.root, a.tools) != nil {
		m.stale(a, id, "failed")
		provider(a, id).Availability = "approvalRequired"
		m.commit(a)
		return
	}
	m.apply(a, id, r.id, result)
	m.commit(a)
}

func (m *Manager) Cancel(c Context, runs []RunIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	// Only latest identities are retained. Older known runs return staleContext,
	// never cancel a newer run; no unbounded run-ID tombstone ledger is necessary.
	for _, r := range runs {
		p := provider(a, r.ProviderID)
		if p == nil || p.RunID != r.RunID {
			return failure("staleContext", "Run is no longer current")
		}
	}
	changed := false
	for _, r := range runs {
		if run := a.runs[r.ProviderID]; run != nil {
			delete(a.runs, r.ProviderID)
			run.cancel()
			m.stale(a, r.ProviderID, "canceled")
			changed = true
		}
	}
	if changed {
		m.commit(a)
	}
	return nil
}

func (m *Manager) stale(a *attachment, id, phase string) {
	p := provider(a, id)
	p.Phase = phase
	p.Freshness = "stale"
	p.Coverage.Partial = true
	for i := range a.snapshot.ResultSets {
		s := &a.snapshot.ResultSets[i]
		if s.ProviderID == id && s.Origin == "checker" {
			for j := range s.Diagnostics {
				s.Diagnostics[j].Freshness = "stale"
			}
		}
	}
}

// DeletePath cancels all current work before clearing a deleted path. Thus an
// adapter already reading that file cannot resurrect it; no tombstone set grows.
func (m *Manager) DeletePath(c Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	if err = ValidatePath(path); err != nil {
		return err
	}
	m.cancelAll(a)
	for id, d := range a.docs {
		if d.path == path {
			m.dropDocument(a, id)
		}
	}
	for i := range a.snapshot.ResultSets {
		s := &a.snapshot.ResultSets[i]
		s.Diagnostics = slices.DeleteFunc(s.Diagnostics, func(d Diagnostic) bool { return d.Path == path })
	}
	m.commit(a)
	return nil
}

// DrainEvents transfers at most two complete updates. resnapshot is explicit
// after overflow: consumers must discard assembly and GetSnapshot, not apply a
// sequence gap as an empty replacement. There is no replay/history retention.
func (m *Manager) DrainEvents(c Context) (updates [][]Event, resnapshot bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return nil, false, err
	}
	updates, a.events = a.events, nil
	resnapshot, a.gap = a.gap, false
	return updates, resnapshot, nil
}

func (m *Manager) commit(a *attachment) {
	m.boundMetadata(a)
	m.retain(a)
	if a.snapshot.Sequence == maxSequence {
		delete(m.attachments, a.snapshot.Context.AttachmentID)
		m.cancelAll(a)
		m.releaseDocuments(a)
		return
	}
	a.snapshot.Sequence++
	events, err := m.eventParts(a.snapshot)
	if err != nil {
		a.events = nil
		a.gap = true
		return
	}
	if len(a.events) == 2 || eventBufferBytes(a.events)+eventBufferBytes([][]Event{events}) > 16<<20 {
		a.events = nil
		a.gap = true
	}
	a.events = append(a.events, events)
}
