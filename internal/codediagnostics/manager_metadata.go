package codediagnostics

// Bound repeated provider metadata, not just findings. Discovery proposals are
// rejected on attach if too large; coverage expansion is recoverably partial.
func (m *Manager) boundMetadata(a *attachment) {
	s := a.snapshot
	s.ResultSets = []ResultSet{}
	cap := min(128<<10, m.limits.EventBytes/4, m.limits.SnapshotBytes/4)
	for i := len(a.snapshot.Providers) - 1; encodedSize(s) > cap && i >= 0; i-- {
		p := &a.snapshot.Providers[i]
		p.Coverage.Roots = []string{}
		p.Coverage.Partial = true
		p.Notice = "Coverage metadata limit reached; scope is partial"
	}
}

func eventBufferBytes(updates [][]Event) int {
	n := 0
	for _, update := range updates {
		for _, e := range update {
			n += encodedSize(e)
		}
	}
	return n
}
