package codediagnostics

// Python type-checking adapter: Pyright's language server.
//
// Like ts_lsp.go this file owns only contained project discovery, a fixed-argv
// server identity with independent availability, extension ownership and
// publishDiagnostics bridging (normalization is shared with CD-22). Process
// launch, trust and the document protocol stay in CD-19/CD-20. Nothing here
// installs, downloads, runs npx/package scripts, executes Python or probes a
// version by executing a tool: versions come from package.json and
// node_version.h. Any pyright version is accepted, as editors do
// (docs/plan/code-diagnostics-zed-model.md).
//
// At run time Pyright executes a Python interpreter to read import search
// paths and the Python version. The adapter therefore always decides which
// one and binds it into approval: the project's virtual environment
// interpreter, else the first native Python on the reviewed path, else none
// (the fixed PATH then holds only the node directory, and anything executable
// named python there is bound too).
// Fake-server tests in pyright_lsp_test.go are SYNTHETIC; they prove this
// adapter's handling, not the behavior of any real server.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	pyrightProviderID = "pyright"
	pyrightName       = "pyright"
	// pyrightPolicy binds the launch shape: node runs the package's own
	// langserver.index.js directly (no shell, npx or version-manager shim),
	// only opened files are analyzed and language services are off
	// (pyrightSettings). The suffix names the interpreter Pyright may run.
	pyrightPolicy          = "pyright-v1:any-version:" + runtime.GOOS + "/" + runtime.GOARCH + ":node-direct:push-only:no-edits:opened-documents:language-services-off:"
	pyrightToolchainPolicy = "pyright-toolchain-v1"
	minPyrightNodeMajor    = 14 // pyright engines: node >=14.0.0
	partialPyrightNotice   = "Some Python type diagnostics could not be verified; results are partial."
	pyrightOpenedNotice    = "Pyright diagnostics cover opened documents only; unopened files are not continuously synchronized."
	pyrightScopeNotice     = "Pyright checks types in basic mode unless the project's Pyright configuration says otherwise. Pyright settings outside the project (parent directories, extends targets) and its bundled type stubs still apply but are not part of approval."
	pyrightNoVenvNotice    = "No project virtual environment (.venv or venv) was found, so Pyright uses only its bundled stubs and does not report unresolved imports."
	pyrightInstallHint     = "The app downloads Pyright into its own folder (using your Node.js, or its own), or install it yourself with npm install -g pyright, then refresh."
)

// pyrightProjectMarkers reuse Ruff's Python detection: the same sources and
// skipped dependency trees. Only root-level configurations are bound, since
// Pyright reads its configuration from the project root (pyrightconfig.json
// first, else pyproject.toml).
var pyrightProjectMarkers = projectMarkers{
	configs: []string{"pyrightconfig.json", "pyproject.toml"},
	skipDir: ruffProjectMarkers.skipDir,
	sources: ruffDocumentExtensions,
}

// pyrightVenvs are the contained virtual environments, in preference order.
var pyrightVenvs = []string{".venv", "venv"}

// pyrightPathPythons are the names Pyright falls back to on PATH, in its order.
var pyrightPathPythons = []string{"python3", "python"}

// pyrightToolchainNames label the loaded entry points bound into approval, in
// the order inspectPyright lists them; vendor.js is bound when the package
// ships it.
var pyrightToolchainNames = []string{"pyright-entry", "pyright-langserver", "pyright-internal", "pyright-vendor"}

// pyrightSettings is the fixed workspace/configuration answer. Pyright asks
// for the "python", "python.analysis" and "pyright" sections. Only opened files
// are analyzed and the language services this client never uses are off.
// pythonPath, when set, is the chosen interpreter; without one missing-import
// rules are silenced because no environment is known.
func pyrightSettings(pythonPath string) json.RawMessage {
	analysis := map[string]any{
		"diagnosticMode":         "openFilesOnly",
		"typeCheckingMode":       "basic",
		"autoSearchPaths":        true,
		"useLibraryCodeForTypes": true,
		"autoImportCompletions":  false,
		"extraPaths":             []string{},
		"logLevel":               "Error",
	}
	python := map[string]any{"analysis": analysis}
	if pythonPath != "" {
		python["pythonPath"] = pythonPath
	} else {
		analysis["diagnosticSeverityOverrides"] = map[string]any{"reportMissingImports": "none", "reportMissingModuleSource": "none"}
	}
	data, _ := json.Marshal(map[string]any{
		"python":  python,
		"pyright": map[string]any{"disableLanguageServices": true, "disableOrganizeImports": true, "disableTaggedHints": true},
	})
	return data
}

// PyrightEnvironment is reviewed host metadata, not the inherited environment.
type PyrightEnvironment struct {
	Home string
	// NodeDir optionally pins the reviewed directory containing the node
	// runtime. Empty uses the node beside a global npm install, then the first
	// real node in the reviewed path.
	NodeDir string
}

// PyrightPlan is the discovery result. Availability is independent of every
// other provider, Ruff included.
type PyrightPlan struct {
	root         AuthorizedRoot
	availability string
	notice       string
	config       ServerConfig
	version      string
	node         string
	venv, python string
	projects     []string
	partial      bool
	projectLocal bool
}

// DiscoverPyright inspects metadata only. Errors are reserved for an invalid
// host description or a root that is no longer authorized.
func DiscoverPyright(root AuthorizedRoot, reviewedPath []string, env PyrightEnvironment) (*PyrightPlan, error) {
	return discoverPyrightWorkspace(root, reviewedPath, env, nil)
}

func discoverPyrightWorkspace(root AuthorizedRoot, reviewedPath []string, env PyrightEnvironment, workspace *tsWorkspace) (*PyrightPlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	reviewed := func(p string) bool { return filepath.IsAbs(p) && bounded(p, 4096) && !hasParent(p) }
	if !reviewed(env.Home) || (env.NodeDir != "" && !reviewed(env.NodeDir)) {
		return nil, failure("invalidArgument", "Reviewed absolute home and node paths are required")
	}
	p := &PyrightPlan{root: root, availability: "available", notice: pyrightScopeNotice}
	unavailable := func(state, notice string) (*PyrightPlan, error) {
		p.availability, p.notice, p.partial = state, notice, true
		return p, nil
	}
	// Python first: a root without Python stays a muted "unsupported" rather
	// than asking for an install it does not need.
	w, err := workspaceMetadata(workspace, func() (tsWorkspace, error) { return discoverProjectDirs(root, pyrightProjectMarkers) })
	if err != nil {
		return nil, err
	}
	p.partial = w.partial
	if !w.sources && len(w.configs) == 0 {
		return unavailable("unsupported", "No Python file was found in this project.")
	}
	p.projects = []string{""}
	entry, local, foreign := resolvePyright(root, reviewedPath)
	if entry == "" {
		if foreign {
			return unavailable("unsupported", "The pyright-langserver command found is not a directly installed pyright package (for example a version-manager shim), so it is not used. "+pyrightInstallHint)
		}
		return unavailable("missingTool", "Pyright is not installed. "+pyrightInstallHint)
	}
	version, files, state, notice := inspectPyright(entry)
	if state != "" {
		return unavailable(state, notice)
	}
	p.version, p.projectLocal = version, local
	node := pyrightNode(env, filepath.Dir(entry), reviewedPath)
	if node == "" {
		return unavailable("missingTool", "Node.js, required by Pyright, was not found. Install Node.js, then refresh.")
	}
	major, nodeVer := nodeVersion(node)
	switch {
	case nodeVer == "":
		return unavailable("unsupported", "The Node.js version could not be verified from its installed metadata.")
	case major < minPyrightNodeMajor:
		return unavailable("unsupported", "Node.js "+nodeVer+" is too old for Pyright; version 14 or newer is required.")
	}
	p.node = nodeVer
	nodeBin := filepath.Dir(node)
	if strings.Contains(nodeBin, ":") {
		return unavailable("unsupported", "The Node.js directory cannot be placed on a fixed search path.")
	}
	tools := make([]ToolInput, len(files))
	for i, f := range files {
		tools[i] = ToolInput{ProviderID: "pyright-toolchain-" + pyrightToolchainNames[i], Executable: f, Policy: pyrightToolchainPolicy}
	}
	var configs []string
	for _, c := range w.configs {
		if !strings.Contains(c, "/") {
			configs = append(configs, c)
		}
	}
	// Pyright runs its interpreter (with -I) to read search paths and the
	// Python version; the interpreter is always chosen here and bound.
	interpreter := "no-interpreter"
	venv, python, venvNotice := pyrightVenv(root)
	if venvNotice != "" {
		p.notice += " " + venvNotice
	}
	switch {
	case python != "":
		// The environment's interpreter, usually a link to a host Python
		// outside the root; the fingerprint binds the file it resolves to.
		interpreter, p.venv = "venv-python", venv
		configs = append(configs, venv+"/pyvenv.cfg")
		slices.Sort(configs)
		tools = append(tools, ToolInput{ProviderID: "pyright-toolchain-venv-python", Executable: python, Policy: pyrightToolchainPolicy})
		p.notice += " Pyright runs the project environment's Python (" + venv + ") to read its import paths."
	default:
		if python = hostPython(append(slices.Clone(reviewedPath), nodeBin)); python != "" {
			interpreter = "host-python"
			tools = append(tools, ToolInput{ProviderID: "pyright-toolchain-host-python", Executable: python, Policy: pyrightToolchainPolicy})
			p.notice += " No project virtual environment (.venv or venv) was found, so Pyright runs " + python + " to read its import paths."
			break
		}
		// Without a pythonPath Pyright tries python3, then python, on PATH.
		// PATH holds only the node directory, which has no native Python;
		// anything executable there (a shim) is still bound.
		for _, name := range pyrightPathPythons {
			if f := filepath.Join(nodeBin, name); isExecutable(f) {
				interpreter = "path-python"
				tools = append(tools, ToolInput{ProviderID: "pyright-toolchain-path-" + name, Executable: f, Policy: pyrightToolchainPolicy})
			}
		}
		p.notice += " " + pyrightNoVenvNotice
	}
	p.python = python
	p.config = ServerConfig{
		ProviderID:    pyrightProviderID,
		Executable:    node,
		Args:          []string{entry, "--stdio"},
		Env:           []string{"HOME=" + env.Home, "PATH=" + nodeBin, "NODE_DISABLE_COMPILE_CACHE=1"},
		AdapterPolicy: pyrightPolicy + interpreter,
		ConfigPaths:   configs,
		Toolchain:     tools,
		Settings:      pyrightSettings(python),
		// Declared for symmetry with vtsls; Pyright's push does not depend on it.
		DeclareDiagnostics: true,
	}
	if _, err := Fingerprint(root, p.config.Tools()); err != nil {
		if err := root.Revalidate(); err != nil {
			return nil, err
		}
		p.config = ServerConfig{}
		return unavailable("unsupported", "The Pyright files could not be identified for approval.")
	}
	return p, nil
}

// resolvePyright finds the package's own langserver.index.js. The contained
// project-local link (project content covered by CD-02 trust) is tried first,
// then reviewed absolute PATH directories (a global npm prefix's bin, the
// app-managed node_modules/.bin last). A command that does not resolve to a
// real pyright entry (a mise/asdf/volta shim, the pip "pyright" wrapper or any
// other launcher) is skipped and reported as foreign; nothing is executed.
func resolvePyright(root AuthorizedRoot, reviewedPath []string) (entry string, local, foreign bool) {
	try := func(link string) string {
		real, err := filepath.EvalSymlinks(link)
		if err != nil {
			return ""
		}
		if st, err := os.Stat(real); err != nil || !st.Mode().IsRegular() {
			return ""
		}
		if !strings.HasSuffix(filepath.ToSlash(real), "/node_modules/pyright/langserver.index.js") {
			foreign = true
			return ""
		}
		return real
	}
	base := root.Project().Root
	if real := try(filepath.Join(base, "node_modules", ".bin", "pyright-langserver")); real != "" {
		if rel, err := filepath.Rel(base, real); err == nil && filepath.IsLocal(rel) {
			return real, true, false
		}
	}
	for _, dir := range reviewedPath {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		if real := try(filepath.Join(dir, "pyright-langserver")); real != "" {
			return real, false, false
		}
	}
	return "", false, foreign
}

// inspectPyright reads package metadata along the path Node itself takes:
// langserver.index.js requires dist/pyright-langserver.js, whose bundle loads
// the dist/pyright-internal.js chunk (and dist/vendor.js when shipped). The
// bundled typeshed-fallback stubs must be present but are data, not bound.
// Nothing is executed.
func inspectPyright(entry string) (version string, files []string, state, notice string) {
	dir := filepath.Dir(entry)
	var pkg struct{ Name, Version string }
	if readJSONFile(filepath.Join(dir, "package.json"), &pkg) != nil || pkg.Name != pyrightName {
		return "", nil, "unsupported", "The Pyright version could not be verified."
	}
	if !plainVersion(pkg.Version) {
		return "", nil, "unsupported", "Found Pyright (unknown); its version could not be verified."
	}
	dist := filepath.Join(dir, "dist")
	files = []string{entry, filepath.Join(dist, "pyright-langserver.js"), filepath.Join(dist, "pyright-internal.js")}
	for _, f := range files {
		if st, err := os.Stat(f); err != nil || !st.Mode().IsRegular() {
			return "", nil, "unsupported", "The Pyright installation is incomplete. " + pyrightInstallHint
		}
	}
	if st, err := os.Stat(filepath.Join(dist, "typeshed-fallback")); err != nil || !st.IsDir() {
		return "", nil, "unsupported", "The Pyright installation is incomplete. " + pyrightInstallHint
	}
	vendor := filepath.Join(dist, "vendor.js")
	if st, err := os.Lstat(vendor); err == nil {
		if !st.Mode().IsRegular() {
			return "", nil, "unsupported", "The Pyright installation is incomplete. " + pyrightInstallHint
		}
		files = append(files, vendor)
	}
	return pkg.Version, files, "", ""
}

// pyrightNode picks the runtime without executing anything: the reviewed
// NodeDir when given, else the node beside a global npm install
// (<prefix>/lib/node_modules/pyright -> <prefix>/bin/node), else the first real
// node in the reviewed path.
func pyrightNode(env PyrightEnvironment, pkgDir string, reviewedPath []string) string {
	var dirs []string
	if env.NodeDir != "" {
		dirs = []string{env.NodeDir}
	} else {
		if prefix, ok := strings.CutSuffix(pkgDir, filepath.Join("lib", "node_modules", pyrightName)); ok {
			dirs = append(dirs, filepath.Join(prefix, "bin"))
		}
		dirs = append(dirs, reviewedPath...)
	}
	for _, dir := range dirs {
		if node := realNode(dir); node != "" {
			return node
		}
	}
	return ""
}

// pyrightVenv finds the project's virtual environment interpreter without
// running it: <root>/<venv> must be a real directory (not a link) holding
// pyvenv.cfg and a real bin directory whose python resolves to a native
// executable (not a script or shim). The returned path is the unresolved one
// inside the environment, so Python finds pyvenv.cfg beside it. notice
// explains an environment that exists but is not used.
func pyrightVenv(root AuthorizedRoot) (name, python, notice string) {
	base := root.Project().Root
	for _, name := range pyrightVenvs {
		dir := filepath.Join(base, name)
		st, err := os.Lstat(dir)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			if notice == "" {
				notice = "The project virtual environment " + name + " is a link, so it is not used."
			}
			continue
		}
		if st, err := os.Lstat(filepath.Join(dir, "pyvenv.cfg")); err != nil || !st.Mode().IsRegular() {
			continue
		}
		if st, err := os.Lstat(filepath.Join(dir, "bin")); err != nil || !st.IsDir() {
			if notice == "" {
				notice = "The project virtual environment " + name + " has no usable bin directory, so it is not used."
			}
			continue
		}
		py := filepath.Join(dir, "bin", "python")
		real, err := filepath.EvalSymlinks(py)
		if err != nil || !strings.HasPrefix(filepath.Base(real), "python") || !isExecutable(real) || isScript(real) {
			if notice == "" {
				notice = "The Python of the project virtual environment " + name + " is missing, a script or a version-manager shim, so it is not used."
			}
			continue
		}
		return name, py, ""
	}
	return "", "", notice
}

// hostPython returns the first native Python Pyright's own fallback would
// pick (python3 anywhere first, then python) in reviewed absolute dirs, as its
// resolved path. Scripts and version-manager shims are skipped; nothing is run.
// The macOS /usr/bin/python3 stub is skipped when no developer tools back it,
// since running it would prompt for an install.
func hostPython(dirs []string) string {
	for _, name := range pyrightPathPythons {
		for _, dir := range dirs {
			if !filepath.IsAbs(dir) || hasParent(dir) {
				continue
			}
			real, err := filepath.EvalSymlinks(filepath.Join(dir, name))
			if err != nil || !strings.HasPrefix(filepath.Base(real), "python") || !isExecutable(real) || isScript(real) {
				continue
			}
			if runtime.GOOS == "darwin" && real == "/usr/bin/python3" && !macDeveloperPython() {
				continue
			}
			return real
		}
	}
	return ""
}

func macDeveloperPython() bool {
	for _, f := range []string{"/Library/Developer/CommandLineTools/usr/bin/python3", "/Applications/Xcode.app/Contents/Developer/usr/bin/python3"} {
		if isExecutable(f) {
			return true
		}
	}
	return false
}

func (p *PyrightPlan) Availability() string { return p.availability }
func (p *PyrightPlan) Version() string      { return p.version }
func (p *PyrightPlan) Projects() []string   { return slices.Clone(p.projects) }

// NodeVersion is the runtime version read from installed metadata.
func (p *PyrightPlan) NodeVersion() string { return p.node }

// Environment names the project virtual environment Pyright uses (".venv",
// "venv"), or "" when none is.
func (p *PyrightPlan) Environment() string { return p.venv }

// Interpreter is the Python Pyright is told to run (python.pythonPath): the
// environment's bin/python, or a native host Python found on the reviewed
// path. Empty means none was found and Pyright uses only its bundled stubs.
func (p *PyrightPlan) Interpreter() string { return p.python }

// ProjectLocal reports that the package is executable project content.
func (p *PyrightPlan) ProjectLocal() bool { return p.projectLocal }

// Server is the fixed server identity, only when the plan is available.
func (p *PyrightPlan) Server() (ServerConfig, bool) {
	return p.config, p.availability == "available"
}

// Provider describes honest opened-documents coverage for DiscoverLocalLive. An
// empty push means Pyright found no problem in that file, not that the project
// is type-clean.
func (p *PyrightPlan) Provider() ProviderState {
	notice := pyrightOpenedNotice
	if p.notice != "" {
		notice = p.notice + " " + pyrightOpenedNotice
	}
	roots := slices.Clone(p.projects)
	if roots == nil {
		roots = []string{}
	}
	return ProviderState{
		ProviderID: pyrightProviderID, Availability: p.availability, Phase: "idle", Freshness: "uncertain",
		Coverage:           Coverage{Kind: "openedDocuments", Roots: roots, Partial: p.partial, ExternalChanges: CoverageExternalChanges},
		Notice:             shorten(notice, 2048),
		DocumentExtensions: slices.Clone(ruffDocumentExtensions),
	}
}

// covers reports whether a contained project directory is an ancestor of file.
func (p *PyrightPlan) covers(file string) bool { return coversDir(p.projects, file) }

// PyrightBridge turns session notifications into document-set replacements
// with the "pyright" attribution prefix. Absence of a push is not "clean".
type PyrightBridge struct {
	root   AuthorizedRoot
	sink   goplsSink
	Notice func(string)
}

func NewPyrightBridge(root AuthorizedRoot, m *Manager) *PyrightBridge {
	return &PyrightBridge{root: root, sink: m}
}

// Handle must be quick and runs on the session goroutine.
func (b *PyrightBridge) Handle(n SessionNotification) {
	handleOwnedPush(b.root, b.sink, n, pyrightProviderID, partialPyrightNotice, func(file string) bool {
		_, owned := PythonLanguageID(file)
		return owned
	}, func(s string) {
		if b.Notice != nil {
			b.Notice(s)
		}
	})
}
