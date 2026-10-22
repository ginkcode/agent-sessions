package codediagnostics

import (
	"path/filepath"
	"strings"
)

// LiveRegistration is metadata from a concrete adapter, not an execution adapter.
// Available servers join the same exact attachment-wide proposal as saved checkers.
// Resolve assigns one synchronization owner; it never starts a process.
type LiveRegistration struct {
	State   ProviderState
	Server  ServerConfig
	Resolve func(string) (language string, owned bool)
}

// LiveEnvironment contains reviewed host-local paths only. Discovery does not
// search parents, private editor caches, install tools, or evaluate project code.
type LiveEnvironment struct {
	Path   []string
	Go     GoplsEnvironment
	TS     TSEnvironment
	Svelte SvelteLSEnvironment
}

// DiscoverLive supplies production adapter metadata without activating a route.
// Invalid host metadata/root fails closed; missing/unsupported tools remain
// independent registrations. Existing adapter walk/hash budgets remain in force.
func DiscoverLive(root AuthorizedRoot, env LiveEnvironment) ([]Registration, error) {
	if len(env.Path) > 64 {
		return nil, failure("limitExceeded", "Reviewed executable search budget exceeded")
	}
	for _, dir := range env.Path {
		if !bounded(dir, 4096) || !filepath.IsAbs(dir) || hasParent(dir) {
			return nil, failure("invalidArgument", "Invalid reviewed executable directory")
		}
	}
	goPlan, err := DiscoverGopls(root, env.Path, env.Go)
	if err != nil {
		return nil, err
	}
	ts, err := DiscoverTypeScript(root, env.Path, env.TS)
	if err != nil {
		return nil, err
	}
	sv, err := DiscoverSvelteLS(root, env.Path, env.Svelte)
	if err != nil {
		return nil, err
	}
	goConfig, _ := goPlan.Server()
	tsConfig, _ := ts.Server()
	svConfig, _ := sv.Server()
	return []Registration{
		liveRegistration(goPlan.Provider(), goConfig, func(file string) (string, bool) {
			return "go", strings.HasSuffix(file, ".go") && coversPackage(goPlan.modules, file)
		}),
		liveRegistration(ts.Provider(), tsConfig, func(file string) (string, bool) {
			lang, ok := TSLanguageID(file)
			return lang, ok && ts.covers(file)
		}),
		liveRegistration(sv.Provider(), svConfig, func(file string) (string, bool) {
			owner, lang := DocumentOwner(file)
			return lang, owner == svelteLSProviderID && coversPackage(sv.packages, file)
		}),
	}, nil
}

// DiscoverGoLive is the production local live route's discovery: gopls only.
// TypeScript and Svelte language servers have no verified host install
// (CD-23/CD-24), so they are registered as independently unsupported
// opened-document providers and never offered for execution. Metadata only.
func DiscoverGoLive(root AuthorizedRoot, reviewedPath []string, env GoplsEnvironment) ([]Registration, error) {
	if len(reviewedPath) > 64 {
		return nil, failure("limitExceeded", "Reviewed executable search budget exceeded")
	}
	goPlan, err := DiscoverGopls(root, reviewedPath, env)
	if err != nil {
		return nil, err
	}
	goConfig, _ := goPlan.Server()
	regs := []Registration{liveRegistration(goPlan.Provider(), goConfig, func(file string) (string, bool) {
		return "go", strings.HasSuffix(file, ".go") && coversPackage(goPlan.modules, file)
	})}
	for _, id := range []string{tsProviderID, svelteLSProviderID} {
		state := ProviderState{
			ProviderID: id, Availability: "unsupported", Phase: "idle", Freshness: "uncertain",
			Coverage: Coverage{Kind: "openedDocuments", Roots: []string{}, Partial: true, ExternalChanges: CoverageExternalChanges},
			Notice:   "Live diagnostics for this language are not enabled: no language server version has been verified.",
		}
		regs = append(regs, liveRegistration(state, ServerConfig{}, nil))
	}
	return regs, nil
}

func liveRegistration(state ProviderState, cfg ServerConfig, resolve func(string) (string, bool)) Registration {
	return Registration{
		ProviderID: state.ProviderID, Availability: state.Availability,
		Live: &LiveRegistration{State: state, Server: cfg, Resolve: resolve},
	}
}

// DiscoveryDocuments binds all available checker and live identities, not just
// the selected server. Trust deliberately still stores one exact fingerprint per
// project. Adding/removing/changing ANY tool requires renewed composite approval;
// this seam never turns a subset into a grant. Call with the same registrations
// supplied to Manager.Attach/CompleteDiscovery. R05 owns lifecycle wiring.
func DiscoveryDocuments(root AuthorizedRoot, registrations []Registration, sessions *SessionManager) (SessionDocuments, error) {
	if len(registrations) > 32 {
		return SessionDocuments{}, failure("limitExceeded", "Provider budget exceeded")
	}
	var tools []ToolInput
	var live []LiveRegistration
	seen := map[string]bool{}
	for _, r := range registrations {
		if seen[r.ProviderID] || !bounded(r.ProviderID, 256) || !availability(r.Availability) {
			return SessionDocuments{}, failure("invalidArgument", "Invalid provider registration")
		}
		seen[r.ProviderID] = true
		if r.Adapter != nil {
			tools = append(tools, r.Adapter.Tools()...)
		}
		if r.Live != nil {
			if err := validateLive(r); err != nil {
				return SessionDocuments{}, err
			}
			if r.Availability == "available" {
				cfg := r.Live.Server
				tools = append(tools, cfg.Tools()...)
				entry := *r.Live
				entry.Server = cloneServer(cfg)
				live = append(live, entry)
			}
		}
	}
	if len(tools) > 0 {
		if _, err := Fingerprint(root, tools); err != nil {
			return SessionDocuments{}, err
		}
	}
	tools = cloneTools(tools)
	return SessionDocuments{Sessions: sessions, Resolve: func(current AuthorizedRoot, file string) (DocumentServer, bool) {
		if current.Project() != root.Project() {
			return DocumentServer{}, false
		}
		if _, err := current.ResolvePath(current.Project().HostID, file); err != nil {
			return DocumentServer{}, false
		}
		var result DocumentServer
		found := false
		for _, entry := range live {
			if lang, ok := entry.Resolve(file); ok {
				if found { // conflicting adapters fail closed, never pick by order
					return DocumentServer{}, false
				}
				cfg := cloneServer(entry.Server)
				cfg.ApprovalTools = cloneTools(tools)
				result, found = DocumentServer{Config: cfg, LanguageID: lang}, true
			}
		}
		return result, found
	}}, nil
}

func cloneServer(cfg ServerConfig) ServerConfig {
	cfg.Args = append([]string(nil), cfg.Args...)
	cfg.Env = append([]string(nil), cfg.Env...)
	cfg.ConfigPaths = append([]string(nil), cfg.ConfigPaths...)
	cfg.Toolchain = cloneTools(cfg.Toolchain)
	cfg.ApprovalTools = cloneTools(cfg.ApprovalTools)
	return cfg
}

func validateLive(r Registration) error {
	l := r.Live
	if r.Adapter != nil || l.State.ProviderID != r.ProviderID || l.State.Availability != r.Availability || l.State.Coverage.Kind != "openedDocuments" {
		return failure("invalidArgument", "Invalid live provider metadata")
	}
	if r.Availability == "available" && (l.Server.ProviderID != r.ProviderID || l.Resolve == nil || l.Server.ApprovalTools != nil) {
		return failure("invalidArgument", "Invalid live server identity")
	}
	return nil
}
