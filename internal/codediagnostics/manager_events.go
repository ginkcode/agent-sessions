package codediagnostics

// SnapshotEvents builds bounded recovery events after a drain overflow. It does
// not increment sequence or retain another copy in the delivery queue.
func (m *Manager) SnapshotEvents(c Context) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return nil, err
	}
	return m.eventParts(a.snapshot)
}

// eventParts uses encoded sizes, including Go's HTML/control escaping. A 1024
// byte reserve covers the RPC notification wrapper (CD-09 must verify its own
// exact envelope). No finding is split; clients assemble whole updates only.
func (m *Manager) eventParts(snapshot Snapshot) ([]Event, error) {
	s := copySnapshot(snapshot)
	sets := s.ResultSets
	s.ResultSets = []ResultSet{}
	base := Event{ContractVersion: 1, Context: s.Context, Sequence: s.Sequence, UpdateID: opaqueID(), PartCount: 32, PartIndex: 31, Snapshot: s}
	overhead := encodedSize(base) + 1024
	capacity := m.limits.EventBytes - overhead
	if capacity <= 0 {
		return nil, failure("limitExceeded", "Event metadata exceeds frame budget")
	}
	parts := []Event{}
	current := base
	used := 0
	flush := func() {
		current.PartIndex = len(parts)
		parts = append(parts, current)
		current = base
		current.Snapshot.ResultSets = []ResultSet{}
		used = 0
	}
	for _, set := range sets {
		empty := set
		empty.Diagnostics = []Diagnostic{}
		setBytes := encodedSize(empty) + 1
		if used+setBytes > capacity {
			flush()
		}
		if setBytes > capacity {
			return nil, failure("limitExceeded", "Result identity exceeds frame budget")
		}
		fragment := empty
		fragmentBytes := setBytes
		for _, d := range set.Diagnostics {
			n := encodedSize(d) + 1
			if used+fragmentBytes+n > capacity {
				if len(fragment.Diagnostics) > 0 {
					current.Snapshot.ResultSets = append(current.Snapshot.ResultSets, fragment)
					used += fragmentBytes
					fragment = empty
					fragmentBytes = setBytes
				}
				flush()
			}
			if fragmentBytes+n > capacity {
				return nil, failure("limitExceeded", "Finding exceeds event frame budget")
			}
			fragment.Diagnostics = append(fragment.Diagnostics, d)
			fragmentBytes += n
		}
		current.Snapshot.ResultSets = append(current.Snapshot.ResultSets, fragment)
		used += fragmentBytes
	}
	if len(current.Snapshot.ResultSets) > 0 || len(parts) == 0 {
		flush()
	}
	if len(parts) > 32 {
		return nil, failure("limitExceeded", "Event part budget exceeded")
	}
	for i := range parts {
		parts[i].PartCount = len(parts)
	}
	return parts, nil
}
