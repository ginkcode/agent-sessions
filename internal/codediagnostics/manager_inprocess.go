package codediagnostics

// InProcessApproved reports whether an in-process provider may analyze for this
// exact context now: the attachment and generation are current, the provider is
// registered as in-process, and project approval is current.
func (m *Manager) InProcessApproved(c Context, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil || !a.inProcess[id] || a.snapshot.Approval.State != "approved" {
		return false
	}
	p := provider(a, id)
	return p != nil && p.Availability == "available"
}

// PublishInProcess replaces an in-process provider's complete project result.
// It is rejected for stale contexts/generations, revoked or unapproved
// attachments and non-in-process providers, so a late scan can never resurrect
// findings after revoke, detach, reapproval or close. externalChanges is the
// caller's honest watcher status ("watching" only while its watcher is live).
func (m *Manager) PublishInProcess(c Context, id string, r CheckerResult, externalChanges string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	if !a.inProcess[id] {
		return failure("invalidArgument", "Provider is not an in-process analyzer")
	}
	if a.snapshot.Approval.State != "approved" {
		return failure("approvalRequired", "Explicit project approval is required")
	}
	if externalChanges != "watching" && externalChanges != "unwatched" {
		return failure("invalidArgument", "Invalid external change status")
	}
	m.apply(a, id, opaqueID(), r)
	p := provider(a, id)
	p.Coverage.ExternalChanges = externalChanges
	if externalChanges != "watching" {
		p.Coverage.Partial = true
	}
	m.commit(a)
	return nil
}

// InProcessStatus marks an in-process provider's retained findings stale after
// its watcher stopped or failed, without dropping them.
func (m *Manager) InProcessStatus(c Context, id, notice string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	if !a.inProcess[id] {
		return failure("invalidArgument", "Provider is not an in-process analyzer")
	}
	p := provider(a, id)
	m.stale(a, id, "failed")
	p.Coverage.ExternalChanges = "unwatched"
	p.Notice = shorten(notice, 2048)
	p.Failure = &Error{"toolFailed", "Project watching stopped; retained findings are stale"}
	m.commit(a)
	return nil
}
