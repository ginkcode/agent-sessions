package codediagnostics

import (
	"context"
	"sync"
)

// LiveDocuments owns the internal, host-local route. Installing it is not feature
// negotiation: callers must still keep live capability advertisement disabled.
// All registrations must be the same complete proposal installed on Manager.
// No default launcher or inherited host environment is supplied here.
type LiveDocuments struct {
	Sessions *SessionManager
	manager  *Manager
	mu       sync.RWMutex
	projects map[Project]liveProject
}

type liveProject struct {
	root      AuthorizedRoot
	documents SessionDocuments
	servers   []ServerConfig
}

func NewLiveDocuments(trust *Trust, manager *Manager, options SessionOptions) *LiveDocuments {
	d := &LiveDocuments{manager: manager, projects: map[Project]liveProject{}}
	options.Notifications = d.notification
	d.Sessions = NewSessionManager(trust, options)
	return d
}

func (d *LiveDocuments) Register(root AuthorizedRoot, registrations []Registration) error {
	documents, err := DiscoveryDocuments(root, registrations, d.Sessions)
	if err != nil {
		return err
	}
	var tools []ToolInput
	var servers []ServerConfig
	for _, r := range registrations {
		if r.Adapter != nil {
			tools = append(tools, r.Adapter.Tools()...)
		}
		if r.Live != nil && r.Availability == "available" {
			servers = append(servers, cloneServer(r.Live.Server))
			tools = append(tools, r.Live.Server.Tool())
		}
	}
	for i := range servers {
		servers[i].ApprovalTools = cloneTools(tools)
	}
	d.mu.Lock()
	d.projects[root.Project()] = liveProject{root, documents, servers}
	d.mu.Unlock()
	return nil
}

func (d *LiveDocuments) project(root AuthorizedRoot) (liveProject, bool) {
	d.mu.RLock()
	p, ok := d.projects[root.Project()]
	d.mu.RUnlock()
	return p, ok
}

// EnsureSession is invoked only after Manager validates approval, containment and
// displayed text. Attach preflights the current complete grant before every reuse
// or launch. Failure is not automatically retried.
func (d *LiveDocuments) EnsureSession(ctx context.Context, root AuthorizedRoot, path string) error {
	p, ok := d.project(root)
	if !ok {
		return failure("unsupported", "Live discovery is unavailable")
	}
	server, ok := p.documents.Resolve(root, path)
	if !ok {
		return failure("unsupported", "No unique language server owns this file")
	}
	_, err := d.Sessions.Attach(ctx, root, server.Config)
	return err
}

func (d *LiveDocuments) OwnerSession(root AuthorizedRoot, path string) (DocumentOwnerIdentity, string, ServerCapabilities, error) {
	p, ok := d.project(root)
	if !ok {
		return DocumentOwnerIdentity{}, "", ServerCapabilities{}, failure("unsupported", "Live discovery is unavailable")
	}
	return p.documents.OwnerSession(root, path)
}

func (d *LiveDocuments) Session(root AuthorizedRoot, path string) (string, ServerCapabilities, error) {
	_, generation, caps, err := d.OwnerSession(root, path)
	return generation, caps, err
}

func (d *LiveDocuments) PublicationIsolated(root AuthorizedRoot, path, generation string) bool {
	p, ok := d.project(root)
	return ok && p.documents.PublicationIsolated(root, path, generation)
}

func (d *LiveDocuments) Apply(ctx context.Context, step DocumentStep) error {
	p, ok := d.project(step.Root)
	if !ok {
		return failure("unsupported", "Live discovery is unavailable")
	}
	return p.documents.Apply(ctx, step)
}

func (d *LiveDocuments) notification(n SessionNotification) {
	d.mu.RLock()
	p, ok := d.projects[n.Project]
	d.mu.RUnlock()
	if !ok {
		return
	}
	notice := func(text string) { d.manager.LiveNotice(n.Project, n.ProviderID, text) }
	switch n.ProviderID {
	case "gopls":
		b := NewGoplsBridge(p.root, d.manager)
		b.Notice = notice
		b.Handle(n)
	case "typescript":
		b := NewTSBridge(p.root, d.manager)
		b.Notice = notice
		b.Handle(n)
	case "svelte":
		b := NewSvelteBridge(p.root, d.manager)
		b.Notice = notice
		b.Handle(n)
	}
}

// Refresh propagates terminal failures without keeping raw server error text.
// Called by the Engine event pump and before snapshots, not the serve goroutine.
func (d *LiveDocuments) Refresh() {
	d.mu.RLock()
	projects := make([]liveProject, 0, len(d.projects))
	for _, p := range d.projects {
		projects = append(projects, p)
	}
	d.mu.RUnlock()
	for _, p := range projects {
		for _, cfg := range p.servers {
			info := d.Sessions.Info(p.root, cfg)
			if info.Phase == phaseFailed || info.Phase == phaseDisconnected {
				d.manager.LiveFailure(p.root.Project(), cfg.ProviderID)
			}
		}
	}
}

// Idle is attachment-level: only attachments with no displayed documents may
// enter grace. One document closing must not idle peers sharing its server.
func (d *LiveDocuments) Idle(root AuthorizedRoot) {
	if p, ok := d.project(root); ok {
		for _, cfg := range p.servers {
			d.Sessions.Hide(root, cfg)
		}
	}
}
func (d *LiveDocuments) Revoke(root AuthorizedRoot) { d.Sessions.Revoke(root) }
func (d *LiveDocuments) Close()                     { d.Sessions.Close() }
