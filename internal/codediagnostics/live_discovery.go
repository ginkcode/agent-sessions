package codediagnostics

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// LiveRegistration is metadata from a concrete adapter, not an execution adapter.
// Available servers join the same exact attachment-wide proposal as saved checkers.
// Resolve assigns one synchronization owner; it never starts a process.
type LiveRegistration struct {
	State      ProviderState
	Server     ServerConfig // legacy single-workspace registration
	Resolve    func(string) (language string, owned bool)
	Workspaces []LiveWorkspace
}

// LiveWorkspace keeps a server rooted in one contained project directory while
// approval and attachment authority remain with the parent AuthorizedRoot.
type LiveWorkspace struct {
	Server  ServerConfig
	Resolve func(string) (language string, owned bool)
}

const maxLiveWorkspaces = 16

func liveWorkspaces(l *LiveRegistration) []LiveWorkspace {
	if len(l.Workspaces) != 0 {
		return l.Workspaces
	}
	return []LiveWorkspace{{Server: l.Server, Resolve: l.Resolve}}
}

func liveServers(l *LiveRegistration) []ServerConfig {
	var configs []ServerConfig
	for _, w := range liveWorkspaces(l) {
		configs = append(configs, cloneServer(w.Server))
	}
	return configs
}

func liveTools(l *LiveRegistration) []ToolInput {
	var tools []ToolInput
	for _, cfg := range liveServers(l) {
		tools = append(tools, cfg.Tools()...)
	}
	return tools
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

// LocalLiveEnvironment is the production local route's reviewed host metadata.
type LocalLiveEnvironment struct {
	GoPath  []string // gopls search, the selected toolchain's bin first
	Path    []string // reviewed executable directories for vtsls, node and ruff
	Go      GoplsEnvironment
	TS      TSEnvironment
	Ruff    RuffEnvironment
	Pyright PyrightEnvironment
}

// DiscoverLocalLive is the production local live route's discovery: gopls,
// vtsls (CD-23) and Ruff for Python, each with independent availability. Svelte has no verified
// language server (CD-24), so it is registered as an independently unsupported
// opened-documents provider and never offered for execution. Metadata only.
func DiscoverLocalLive(root AuthorizedRoot, env LocalLiveEnvironment) ([]Registration, error) {
	if len(env.GoPath) > 64 || len(env.Path) > 64 {
		return nil, failure("limitExceeded", "Reviewed executable search budget exceeded")
	}
	goPlan, err := DiscoverGopls(root, env.GoPath, env.Go)
	if err != nil {
		return nil, err
	}
	if nested, used, err := discoverWorkspaceLive(root, env); err != nil {
		return nil, err
	} else if used {
		goConfig, _ := goPlan.Server()
		svelte := ProviderState{
			ProviderID: svelteLSProviderID, Availability: "unsupported", Phase: "idle", Freshness: "uncertain",
			Coverage: Coverage{Kind: "openedDocuments", Roots: []string{}, Partial: true, ExternalChanges: CoverageExternalChanges},
			Notice:   "Live diagnostics for Svelte are not enabled: no Svelte language server version has been verified.",
		}
		registrations := []Registration{liveRegistration(goPlan.Provider(), goConfig, func(file string) (string, bool) {
			return "go", strings.HasSuffix(file, ".go") && coversPackage(goPlan.modules, file)
		})}
		for _, r := range nested {
			if r.ProviderID == tsProviderID {
				registrations = append(registrations, r)
			}
		}
		registrations = append(registrations, liveRegistration(svelte, ServerConfig{}, nil))
		for _, r := range nested {
			if r.ProviderID != tsProviderID {
				registrations = append(registrations, r)
			}
		}
		return registrations, nil
	}
	ts, err := DiscoverTypeScript(root, env.Path, env.TS)
	if err != nil {
		return nil, err
	}
	ruff, err := DiscoverRuff(root, env.Path, env.Ruff)
	if err != nil {
		return nil, err
	}
	pyright, err := DiscoverPyright(root, env.Path, env.Pyright)
	if err != nil {
		return nil, err
	}
	pyrightConfig, _ := pyright.Server()
	goConfig, _ := goPlan.Server()
	tsConfig, _ := ts.Server()
	ruffConfig, _ := ruff.Server()
	svelte := ProviderState{
		ProviderID: svelteLSProviderID, Availability: "unsupported", Phase: "idle", Freshness: "uncertain",
		Coverage: Coverage{Kind: "openedDocuments", Roots: []string{}, Partial: true, ExternalChanges: CoverageExternalChanges},
		Notice:   "Live diagnostics for Svelte are not enabled: no Svelte language server version has been verified.",
	}
	return []Registration{
		liveRegistration(goPlan.Provider(), goConfig, func(file string) (string, bool) {
			return "go", strings.HasSuffix(file, ".go") && coversPackage(goPlan.modules, file)
		}),
		liveRegistration(ts.Provider(), tsConfig, func(file string) (string, bool) {
			lang, ok := TSLanguageID(file)
			return lang, ok && ts.covers(file)
		}),
		liveRegistration(svelte, ServerConfig{}, nil),
		liveRegistration(ruff.Provider(), ruffConfig, func(file string) (string, bool) {
			lang, ok := PythonLanguageID(file)
			return lang, ok && ruff.covers(file)
		}),
		liveRegistration(pyright.Provider(), pyrightConfig, func(file string) (string, bool) {
			lang, ok := PythonLanguageID(file)
			return lang, ok && pyright.covers(file)
		}),
	}, nil
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
				for _, cfg := range liveServers(r.Live) {
					if _, err := commandDirectory(root, cfg.Directory); err != nil {
						return SessionDocuments{}, err
					}
				}
				tools = append(tools, liveTools(r.Live)...)
				entry := *r.Live
				entry.Workspaces = nil
				for _, w := range liveWorkspaces(r.Live) {
					entry.Workspaces = append(entry.Workspaces, LiveWorkspace{Server: cloneServer(w.Server), Resolve: w.Resolve})
				}
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
	resolveAll := func(current AuthorizedRoot, file string) []DocumentServer {
		if current.Project() != root.Project() {
			return nil
		}
		if _, err := current.ResolvePath(current.Project().HostID, file); err != nil {
			return nil
		}
		var result []DocumentServer
		for _, entry := range live {
			var selected *DocumentServer
			depth := -1
			conflict := false
			for _, w := range entry.Workspaces {
				dir := w.Server.Directory
				if dir != "" && !strings.HasPrefix(file, dir+"/") {
					continue
				}
				lang, ok := w.Resolve(file)
				if !ok {
					continue
				}
				d := 0
				if dir != "" {
					d = strings.Count(dir, "/") + 1
				}
				if d < depth {
					continue
				}
				if d == depth {
					conflict = true
					continue
				}
				cfg := cloneServer(w.Server)
				cfg.ApprovalTools = cloneTools(tools)
				selected = &DocumentServer{Config: cfg, LanguageID: lang}
				depth, conflict = d, false
			}
			if conflict {
				return nil
			}
			if selected != nil {
				result = append(result, *selected)
			}
		}
		// Only the reviewed Python lint/type pair may share a document. Arbitrary
		// overlapping registrations remain conflicts, never an order-based choice.
		if len(result) > 1 {
			if len(result) != 2 || result[0].LanguageID != "python" || result[1].LanguageID != "python" {
				return nil
			}
			a, b := result[0].Config.ProviderID, result[1].Config.ProviderID
			if !((a == "ruff" && b == "pyright") || (a == "pyright" && b == "ruff")) {
				return nil
			}
		}
		return result
	}
	return SessionDocuments{Sessions: sessions, ResolveAll: resolveAll, Resolve: func(current AuthorizedRoot, file string) (DocumentServer, bool) {
		all := resolveAll(current, file)
		if len(all) != 1 {
			return DocumentServer{}, false
		}
		return all[0], true
	}}, nil
}

func cloneServer(cfg ServerConfig) ServerConfig {
	cfg.Args = append([]string(nil), cfg.Args...)
	cfg.Env = append([]string(nil), cfg.Env...)
	cfg.ConfigPaths = append([]string(nil), cfg.ConfigPaths...)
	cfg.Toolchain = cloneTools(cfg.Toolchain)
	cfg.ApprovalTools = cloneTools(cfg.ApprovalTools)
	cfg.Settings = append(json.RawMessage(nil), cfg.Settings...)
	return cfg
}

func validateLive(r Registration) error {
	l := r.Live
	if r.Adapter != nil || l.State.ProviderID != r.ProviderID || l.State.Availability != r.Availability || l.State.Coverage.Kind != "openedDocuments" {
		return failure("invalidArgument", "Invalid live provider metadata")
	}
	if len(l.Workspaces) > maxLiveWorkspaces {
		return failure("limitExceeded", "Live workspace budget exceeded")
	}
	if len(l.Workspaces) != 0 && (l.Server.ProviderID != "" || l.Resolve != nil) {
		return failure("invalidArgument", "Mixed legacy and workspace live registration")
	}
	if r.Availability == "available" {
		seen := map[string]bool{}
		for _, w := range liveWorkspaces(l) {
			cfg := w.Server
			if cfg.ProviderID != r.ProviderID || w.Resolve == nil || cfg.ApprovalTools != nil || seen[cfg.Directory] {
				return failure("invalidArgument", "Invalid live server identity")
			}
			if cfg.Directory != "" {
				if err := ValidatePath(cfg.Directory); err != nil {
					return err
				}
			}
			seen[cfg.Directory] = true
		}
	}
	return nil
}
