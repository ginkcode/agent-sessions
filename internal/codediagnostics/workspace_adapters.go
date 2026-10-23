package codediagnostics

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// workspaceMetadata lets the single bounded workspace scan feed existing adapter
// tool inspection without running a new filesystem walk for every child/provider.
func workspaceMetadata(w *tsWorkspace, discover func() (tsWorkspace, error)) (tsWorkspace, error) {
	if w != nil {
		return *w, nil
	}
	return discover()
}

type workspaceDirectory struct {
	rel                                   string
	files                                 []string
	python, typescript, npm, npmWorkspace bool
}
type workspaceScan struct {
	dirs    []workspaceDirectory
	partial bool
	// truncated: the entry/depth budget or a read error left the listing
	// incomplete, beyond the deliberately skipped dot-directories and links.
	truncated bool
}

// scanLiveWorkspaces reads metadata only, never dependencies or linked trees.
func scanLiveWorkspaces(root AuthorizedRoot) (workspaceScan, error) {
	var result workspaceScan
	if err := root.Revalidate(); err != nil {
		return result, err
	}
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return result, err
	}
	defer r.Close()
	entries := 0
	var walk func(string, int)
	walk = func(dir string, depth int) {
		if entries >= maxTSEntries {
			result.partial, result.truncated = true, true
			return
		}
		f, err := r.Open(dir)
		if err != nil {
			result.partial, result.truncated = true, true
			return
		}
		ds, err := f.ReadDir(maxTSEntries + 1 - entries)
		f.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			result.partial, result.truncated = true, true
			return
		}
		slices.SortFunc(ds, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		d := workspaceDirectory{rel: filepath.ToSlash(dir)}
		if dir == "." {
			d.rel = ""
		}
		var children []string
		for _, e := range ds {
			entries++
			if entries > maxTSEntries {
				result.partial, result.truncated = true, true
				break
			}
			rel := filepath.Join(dir, e.Name())
			if ValidatePath(filepath.ToSlash(rel)) != nil || e.Type()&os.ModeSymlink != 0 {
				result.partial = true
				continue
			}
			if e.IsDir() {
				if slices.Contains([]string{"node_modules", ".venv", "venv", "__pycache__", "site-packages", ".tox", ".nox", ".git", ".next", "dist", "build"}, e.Name()) {
					if e.Name() == ".venv" || e.Name() == "venv" {
						if st, err := r.Lstat(filepath.Join(rel, "pyvenv.cfg")); err == nil && st.Mode().IsRegular() {
							d.python = true
						}
					}
					continue
				}
				if strings.HasPrefix(e.Name(), ".") || depth >= 32 {
					result.partial = true
					result.truncated = result.truncated || depth >= 32
					continue
				}
				children = append(children, rel)
				continue
			}
			if !e.Type().IsRegular() {
				continue
			}
			d.files = append(d.files, e.Name())
			switch e.Name() {
			case "pyproject.toml", "pyrightconfig.json", "setup.cfg", "requirements.txt":
				d.python = true
			case "package.json":
				d.typescript, d.npm = true, true
				// npm workspaces belong to the package root, not an instance per tsconfig.
				var pkg struct{ Workspaces any }
				if abs, err := root.ResolvePath(root.Project().HostID, filepath.ToSlash(rel)); err == nil && readJSONFile(abs, &pkg) == nil {
					d.npmWorkspace = pkg.Workspaces != nil
				}
			case "tsconfig.json", "jsconfig.json":
				d.typescript = true
			}
		}
		result.dirs = append(result.dirs, d)
		for _, child := range children {
			walk(child, depth+1)
		}
	}
	walk(".", 0)
	return result, root.Revalidate()
}

// workspaceNotices lists each distinct message once, after the projects it names.
type workspaceNotices struct {
	messages []string
	labels   map[string][]string
}

func (n *workspaceNotices) add(label, message string) {
	if n.labels == nil {
		n.labels = map[string][]string{}
	}
	if _, ok := n.labels[message]; !ok {
		n.messages = append(n.messages, message)
	}
	if label != "" {
		n.labels[message] = append(n.labels[message], label)
	}
}

func (n *workspaceNotices) String() string {
	parts := make([]string, 0, len(n.messages))
	for _, m := range n.messages {
		if labels := n.labels[m]; len(labels) > 0 {
			m = strings.Join(labels, ", ") + ": " + m
		}
		parts = append(parts, m)
	}
	return strings.Join(parts, " ")
}

func withinWorkspace(dir, file string) bool { return dir == "" || strings.HasPrefix(file, dir+"/") }
func deepestWorkspace(roots []string, file string) string {
	best := ""
	for _, dir := range roots {
		if withinWorkspace(dir, file) && len(dir) > len(best) {
			best = dir
		}
	}
	return best
}

// scopedDiscoveryRoot is not a new authorization grant. Its revalidation retains
// the catalog-authorized parent, its identity, and the child's contained identity.
// The resulting root is only used for discovery; launch retains the parent root.
func scopedDiscoveryRoot(parent AuthorizedRoot, rel string) (AuthorizedRoot, error) {
	if rel == "" {
		return parent, nil
	}
	if err := ValidatePath(rel); err != nil {
		return AuthorizedRoot{}, err
	}
	if err := parent.Revalidate(); err != nil {
		return AuthorizedRoot{}, err
	}
	requested := filepath.Join(parent.Project().Root, filepath.FromSlash(rel))
	real, info, err := canonicalDirectory(requested)
	if err != nil {
		return AuthorizedRoot{}, err
	}
	contained, err := filepath.Rel(parent.Project().Root, real)
	if err != nil || !filepath.IsLocal(contained) || contained == "." || real != requested {
		return AuthorizedRoot{}, failure("unauthorized", "Workspace directory is outside its authorized parent")
	}
	return AuthorizedRoot{project: Project{HostID: parent.Project().HostID, Root: real}, requested: requested, identity: info, known: func(candidate string) bool {
		if candidate != requested || parent.Revalidate() != nil {
			return false
		}
		now, st, err := canonicalDirectory(candidate)
		return err == nil && now == real && os.SameFile(info, st)
	}}, nil
}

func parentWorkspaceConfig(cfg ServerConfig, dir string) ServerConfig {
	cfg = cloneServer(cfg)
	cfg.Directory = dir
	translate := func(paths []string) {
		for i, p := range paths {
			if dir != "" {
				paths[i] = path.Join(dir, p)
			}
		}
	}
	translate(cfg.ConfigPaths)
	for i := range cfg.Toolchain {
		translate(cfg.Toolchain[i].ConfigPaths)
	}
	return cfg
}

// discoverWorkspaceLive returns false when legacy single-root discovery suffices.
// Missing deepest children are retained in ownership boundaries, never filled in
// by a healthy ancestor's server.
func discoverWorkspaceLive(root AuthorizedRoot, env LocalLiveEnvironment) ([]Registration, bool, error) {
	scan, err := scanLiveWorkspaces(root)
	if err != nil {
		return nil, false, err
	}
	var pyRoots, tsRoots []string
	npmWorkspaces := []string{}
	for _, d := range scan.dirs {
		if d.python {
			pyRoots = append(pyRoots, d.rel)
		}
		if d.typescript {
			suppressed := false
			for _, ancestor := range tsRoots {
				if withinWorkspace(ancestor, d.rel+"/") && (!d.npm || slices.Contains(npmWorkspaces, ancestor)) {
					suppressed = true
					break
				}
			}
			if !suppressed {
				tsRoots = append(tsRoots, d.rel)
				if d.npmWorkspace {
					npmWorkspaces = append(npmWorkspaces, d.rel)
				}
			}
		}
	}
	nested := false
	for _, dirs := range [][]string{pyRoots, tsRoots} {
		for _, dir := range dirs {
			nested = nested || dir != ""
		}
	}
	if !nested {
		return nil, false, nil
	}
	// Markerless sources outside all boundaries keep a separate root instance.
	for _, d := range scan.dirs {
		for _, f := range d.files {
			file := path.Join(d.rel, f)
			if _, ok := PythonLanguageID(file); ok && deepestWorkspace(pyRoots, file) == "" && !slices.Contains(pyRoots, "") {
				pyRoots = append(pyRoots, "")
			}
			if _, ok := TSLanguageID(file); ok && deepestWorkspace(tsRoots, file) == "" && !slices.Contains(tsRoots, "") {
				tsRoots = append(tsRoots, "")
			}
		}
	}
	// A boundary owning no scanned source (a manifest-only folder) would only
	// spend approval and hashing budget. A truncated scan keeps every boundary.
	owningSources := func(roots []string, language func(string) (string, bool)) []string {
		if scan.truncated {
			return roots
		}
		all := slices.Clone(roots)
		return slices.DeleteFunc(roots, func(dir string) bool {
			for _, d := range scan.dirs {
				for _, f := range d.files {
					file := path.Join(d.rel, f)
					if _, ok := language(file); ok && deepestWorkspace(all, file) == dir {
						return false
					}
				}
			}
			return true
		})
	}
	pyRoots = owningSources(pyRoots, PythonLanguageID)
	tsRoots = owningSources(tsRoots, TSLanguageID)
	type provider struct {
		id       string
		roots    []string
		language func(string) (string, bool)
	}
	providers := []provider{{tsProviderID, tsRoots, TSLanguageID}, {ruffProviderID, pyRoots, PythonLanguageID}, {pyrightProviderID, pyRoots, PythonLanguageID}}
	var registrations []Registration
	// Reserve sixteen contributions for the unchanged Go/Svelte registrations.
	// Every instance stays atomic: never discard part of its approved toolchain.
	toolBudget := maxProposalTools - 16
	for _, provider := range providers {
		roots := provider.roots
		slices.Sort(roots)
		state := ProviderState{ProviderID: provider.id, Availability: "unsupported", Phase: "idle", Freshness: "uncertain", Coverage: Coverage{Kind: "openedDocuments", Roots: []string{}, Partial: scan.partial, ExternalChanges: CoverageExternalChanges}}
		if provider.id == tsProviderID {
			state.DocumentExtensions = slices.Clone(tsDocumentExtensions)
		} else {
			state.DocumentExtensions = slices.Clone(ruffDocumentExtensions)
		}
		live := &LiveRegistration{}
		var notices workspaceNotices
		for i, dir := range roots {
			if i >= maxLiveWorkspaces {
				state.Coverage.Partial = true
				notices.add("", "Workspace instance limit reached; projects not covered: "+strings.Join(roots[i:], ", ")+".")
				break
			}
			scoped, err := scopedDiscoveryRoot(root, dir)
			if err != nil {
				return nil, true, err
			}
			w := tsWorkspace{sources: true, partial: scan.partial}
			for _, d := range scan.dirs {
				for _, f := range d.files {
					file := path.Join(d.rel, f)
					if !withinWorkspace(dir, file) || deepestWorkspace(roots, file) != dir {
						continue
					}
					isConfig := false
					switch provider.id {
					case tsProviderID:
						isConfig = slices.Contains(tsProjectMarkers.configs, f)
					case ruffProviderID:
						isConfig = slices.Contains(ruffProjectMarkers.configs, f) || f == "setup.cfg" || f == "requirements.txt"
					case pyrightProviderID:
						isConfig = slices.Contains(pyrightProjectMarkers.configs, f) || f == "setup.cfg" || f == "requirements.txt"
					}
					if isConfig {
						local := file
						if dir != "" {
							local = strings.TrimPrefix(file, dir+"/")
						}
						if len(w.configs) < maxTSConfigs {
							w.configs = append(w.configs, local)
						} else {
							w.partial = true
						}
					}
				}
			}
			slices.Sort(w.configs)
			var cfg ServerConfig
			var child ProviderState
			switch provider.id {
			case tsProviderID:
				p, e := discoverTypeScriptWorkspace(scoped, env.Path, env.TS, &w)
				if e != nil {
					return nil, true, e
				}
				cfg, _ = p.Server()
				child = p.Provider()
			case ruffProviderID:
				p, e := discoverRuffWorkspace(scoped, env.Path, env.Ruff, &w)
				if e != nil {
					return nil, true, e
				}
				cfg, _ = p.Server()
				child = p.Provider()
			case pyrightProviderID:
				p, e := discoverPyrightWorkspace(scoped, env.Path, env.Pyright, &w)
				if e != nil {
					return nil, true, e
				}
				cfg, _ = p.Server()
				child = p.Provider()
			}
			state.Coverage.Partial = state.Coverage.Partial || child.Coverage.Partial
			if child.Availability != "available" {
				if state.Availability != "available" && (state.Availability == "unsupported" || child.Availability == "missingTool") {
					state.Availability = child.Availability
				}
				if child.Availability == "missingTool" {
					state.MissingTool = true
				}
				label := dir
				if label == "" {
					label = "workspace root"
				}
				notices.add(label, child.Notice)
				continue
			}
			if cost := len(cfg.Tools()); cost > toolBudget {
				state.Coverage.Partial = true
				label := dir
				if label == "" {
					label = "workspace root"
				}
				notices.add(label, "Project skipped because the composite tool budget was reached.")
				continue
			} else {
				toolBudget -= cost
			}
			state.Availability = "available"
			state.Coverage.Roots = append(state.Coverage.Roots, dir)
			if len(live.Workspaces) == 0 {
				state.Notice = child.Notice
			}
			ownedDir := dir
			ownedRoots := slices.Clone(roots)
			language := provider.language
			live.Workspaces = append(live.Workspaces, LiveWorkspace{Server: parentWorkspaceConfig(cfg, dir), Resolve: func(file string) (string, bool) {
				lang, ok := language(file)
				return lang, ok && withinWorkspace(ownedDir, file) && deepestWorkspace(ownedRoots, file) == ownedDir
			}})
		}
		if len(roots) == 0 {
			state.Notice = "No matching language project was found in this workspace."
		}
		if len(notices.messages) > 0 {
			state.Coverage.Partial = true
			state.Notice = shorten(notices.String()+" "+state.Notice, 2048)
		}
		live.State = state
		registrations = append(registrations, Registration{ProviderID: provider.id, Availability: state.Availability, Live: live})
	}
	return registrations, true, nil
}
