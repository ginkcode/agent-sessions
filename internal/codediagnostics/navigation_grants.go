package codediagnostics

// maxNavigationGrants bounds the external files one attachment may read; the
// oldest grant is dropped first.
const maxNavigationGrants = 256

// grantExternal records the external locations of a navigation answer as
// readable for the attachment, so going to the definition of a dependency
// can show its source. Nothing else outside the root is ever readable.
func (a *attachment) grantExternal(result NavigationResult) {
	for _, l := range result.Locations {
		if !l.External {
			continue
		}
		if a.grants == nil {
			a.grants = map[string]bool{}
		}
		if a.grants[l.Path] {
			continue
		}
		a.grants[l.Path] = true
		a.grantOrder = append(a.grantOrder, l.Path)
		if len(a.grantOrder) > maxNavigationGrants {
			delete(a.grants, a.grantOrder[0])
			a.grantOrder = a.grantOrder[1:]
		}
	}
}

// dropGrants ends every grant (detach, revoke, invalidation).
func (a *attachment) dropGrants() {
	a.grants, a.grantOrder = nil, nil
}

// NavigationGrant reports whether path, an absolute host path, came from an
// external location of a navigation answer for this attachment. The caller
// reads the file; the grant only names which one may be read.
func (m *Manager) NavigationGrant(c Context, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.lookup(c)
	if err != nil {
		return err
	}
	if err = m.liveGate(a); err != nil {
		return err
	}
	if !externalPath(path) {
		return failure("invalidArgument", "Invalid external path")
	}
	if !a.grants[path] {
		return failure("notFound", "This file was not opened by code navigation")
	}
	return nil
}
