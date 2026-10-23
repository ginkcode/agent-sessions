package codediagnostics

// TypeScript/JavaScript adapter (CD-23).
//
// Like go_lsp.go this file owns only contained package discovery, a fixed-argv
// server identity with independent availability, extension ownership and
// publishDiagnostics bridging (normalization is shared with CD-22). Process
// launch, trust and the document protocol stay in CD-19/CD-20. Nothing here
// installs, downloads, runs npx/package scripts, or probes a version by
// executing the tool: versions come from package.json and node_version.h. Any
// vtsls version is accepted and a workspace TypeScript is used when present, as
// editors do (docs/plan/code-diagnostics-zed-model.md). The spike evidence and
// its limits are recorded in
// docs/plan/code-diagnostics-typescript.md and testdata/typescript/evidence.json.
// Fake-server tests in ts_lsp_test.go are SYNTHETIC; they prove this adapter's
// handling, not the behavior of any real server.

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const (
	tsProviderID     = "typescript"
	vtslsName        = "@vtsls/language-server"
	vtslsServiceName = "@vtsls/language-service"
	// tsPolicy binds the launch shape: node runs the package's own
	// bin/vtsls.js directly (no shell, npx or version-manager shim) and
	// automatic type acquisition is off (tsSettings). The suffix names the
	// TypeScript used: the workspace's or the one vtsls ships with.
	tsPolicy          = "vtsls-v3:any-version:" + runtime.GOOS + "/" + runtime.GOARCH + ":node-direct:push-only:no-edits:opened-documents:ata-off:"
	tsToolchainPolicy = "vtsls-toolchain-v1"
	minTSNodeMajor    = 18 // @vtsls/language-server 0.3.0 engines
	maxTSPackages     = 48
	maxTSConfigs      = 128 // the CD-02 per-tool configuration identity limit
	maxTSEntries      = 4096
	partialTSNotice   = "Some TypeScript diagnostics could not be verified; results are partial."
	tsOpenedNotice    = "TypeScript diagnostics cover opened documents only; unopened files are not continuously synchronized."
	tsUncertainNotice = "vtsls reports results without a document version, so they are listed as uncertain and not drawn inline."
	tsInstallHint     = "The app downloads vtsls into its own folder (using your Node.js 18 or newer, or its own), or install it yourself with npm install -g @vtsls/language-server, then refresh."
)

// tsSettings is the fixed workspace/configuration answer. vtsls reads settings
// only from a client that advertises the capability; otherwise its defaults
// enable automatic type acquisition, which runs npm from the node directory.
// Plugin paths are never used. With tsdk, vtsls loads the workspace's
// TypeScript (as editors do), whose files are bound into approval.
func tsSettings(tsdk string) json.RawMessage {
	settings := map[string]any{
		"typescript": map[string]any{"disableAutomaticTypeAcquisition": true, "tsserver": map[string]any{"log": "off", "pluginPaths": []string{}}},
		"vtsls":      map[string]any{"autoUseWorkspaceTsdk": tsdk != ""},
	}
	if tsdk != "" {
		settings["typescript"].(map[string]any)["tsdk"] = tsdk
	}
	data, _ := json.Marshal(settings)
	return data
}

// tsToolchainNames label the loaded entry points bound into approval, in the
// order vtslsInstall.files lists them; tsWorkspaceNames label the workspace
// TypeScript files that replace the last three.
var (
	tsToolchainNames = []string{"vtsls-entry", "vtsls-server", "vtsls-service", "tsserver", "tsserver-impl", "typescript"}
	tsWorkspaceNames = []string{"workspace-tsserver", "workspace-tsserver-impl", "workspace-typescript"}
)

var tsVersionText = regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`)

// TSEnvironment is reviewed host metadata, not the inherited environment.
type TSEnvironment struct {
	Home string
	// NodeDir optionally pins the reviewed directory containing the node
	// runtime. Empty uses the node beside a global npm install, then the first
	// real node in the reviewed path.
	NodeDir string
}

// TSPlan is the discovery result. Availability is independent of every other
// provider.
type TSPlan struct {
	root         AuthorizedRoot
	availability string
	notice       string
	config       ServerConfig
	wrapper, ts  string
	node         string
	packages     []string
	partial      bool
	projectLocal bool
}

// DiscoverTypeScript inspects metadata only. Errors are reserved for an invalid
// host description or a root that is no longer authorized.
func DiscoverTypeScript(root AuthorizedRoot, reviewedPath []string, env TSEnvironment) (*TSPlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	reviewed := func(p string) bool { return filepath.IsAbs(p) && bounded(p, 4096) && !hasParent(p) }
	if !reviewed(env.Home) || (env.NodeDir != "" && !reviewed(env.NodeDir)) {
		return nil, failure("invalidArgument", "Reviewed absolute home and node paths are required")
	}
	p := &TSPlan{root: root, availability: "available", notice: tsUncertainNotice}
	unavailable := func(state, notice string) (*TSPlan, error) {
		p.availability, p.notice, p.partial = state, notice, true
		return p, nil
	}
	// Sources first: a project without JS/TS stays a muted "unsupported"
	// rather than asking for an install it does not need.
	w, err := discoverTSPackages(root)
	if err != nil {
		return nil, err
	}
	p.partial = w.partial
	if !w.sources && len(w.configs) == 0 {
		return unavailable("unsupported", "No TypeScript or JavaScript file was found in this project.")
	}
	p.packages = []string{""}
	entry, local, foreign := resolveVtsls(root, reviewedPath)
	if entry == "" {
		if foreign {
			return unavailable("unsupported", "The vtsls command found is not a directly installed @vtsls/language-server package (for example a version-manager shim), so it is not used. "+tsInstallHint)
		}
		return unavailable("missingTool", "No TypeScript language server (vtsls) is installed. "+tsInstallHint)
	}
	inst, state, notice := inspectVtsls(entry)
	if state != "" {
		return unavailable(state, notice)
	}
	p.wrapper, p.ts, p.projectLocal = inst.wrapper, inst.typescript, local
	node := tsNode(env, inst.serverDir, reviewedPath)
	if node == "" {
		return unavailable("missingTool", "Node.js 18 or newer, required by the TypeScript language server, was not found. Install Node.js, then refresh.")
	}
	major, version := nodeVersion(node)
	switch {
	case version == "":
		return unavailable("unsupported", "The Node.js version could not be verified from its installed metadata.")
	case major < minTSNodeMajor:
		return unavailable("unsupported", "Node.js "+version+" is too old for the TypeScript language server; version 18 or newer is required.")
	}
	p.node = version
	nodeBin := filepath.Dir(node)
	if strings.Contains(nodeBin, ":") {
		return unavailable("unsupported", "The Node.js directory cannot be placed on a fixed search path.")
	}
	tools := make([]ToolInput, len(inst.files))
	for i, f := range inst.files {
		tools[i] = ToolInput{ProviderID: "typescript-toolchain-" + tsToolchainNames[i], Executable: f, Policy: tsToolchainPolicy}
	}
	typescript := "bundled-typescript"
	tsdk, wsVersion, wsFiles := workspaceTypeScript(root)
	if tsdk != "" {
		typescript, p.ts = "workspace-typescript", wsVersion
		tools = tools[:3]
		for i, f := range wsFiles {
			tools = append(tools, ToolInput{ProviderID: "typescript-toolchain-" + tsWorkspaceNames[i], Executable: f, Policy: tsToolchainPolicy})
		}
	}
	p.config = ServerConfig{
		ProviderID:    tsProviderID,
		Executable:    node,
		Args:          []string{entry, "--stdio"},
		Env:           []string{"HOME=" + env.Home, "PATH=" + nodeBin + ":/usr/bin:/bin", "NODE_DISABLE_COMPILE_CACHE=1"},
		AdapterPolicy: tsPolicy + typescript,
		ConfigPaths:   w.configs,
		Toolchain:     tools,
		Settings:      tsSettings(tsdk),
		// vtsls sends no diagnostics to a client that does not declare them.
		DeclareDiagnostics: true,
	}
	if _, err := Fingerprint(root, p.config.Tools()); err != nil {
		if err := root.Revalidate(); err != nil {
			return nil, err
		}
		p.config = ServerConfig{}
		return unavailable("unsupported", "The TypeScript language server files could not be identified for approval.")
	}
	return p, nil
}

// resolveVtsls finds the package's own bin/vtsls.js. The contained
// project-local link (project content covered by CD-02 trust) is tried first,
// then reviewed absolute PATH directories. A command that does not resolve to
// a real @vtsls/language-server entry (a mise/asdf/volta shim or any other
// wrapper) is skipped and reported as foreign; nothing is ever executed.
func resolveVtsls(root AuthorizedRoot, reviewedPath []string) (entry string, local, foreign bool) {
	try := func(link string) string {
		real, err := filepath.EvalSymlinks(link)
		if err != nil {
			return ""
		}
		if st, err := os.Stat(real); err != nil || !st.Mode().IsRegular() {
			return ""
		}
		if !strings.HasSuffix(filepath.ToSlash(real), "/node_modules/@vtsls/language-server/bin/vtsls.js") {
			foreign = true
			return ""
		}
		return real
	}
	base := root.Project().Root
	if real := try(filepath.Join(base, "node_modules", ".bin", "vtsls")); real != "" {
		if rel, err := filepath.Rel(base, real); err == nil && filepath.IsLocal(rel) {
			return real, true, false
		}
	}
	for _, dir := range reviewedPath {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		if real := try(filepath.Join(dir, "vtsls")); real != "" {
			return real, false, false
		}
	}
	return "", false, foreign
}

type vtslsInstall struct {
	serverDir, wrapper, typescript string
	files                          []string // see tsToolchainNames
}

// inspectVtsls reads package metadata along the path Node itself takes:
// bin/vtsls.js loads dist/main.js, which requires @vtsls/language-service,
// whose dist/index.js resolves typescript and forks lib/tsserver.js (a shim
// for _tsserver.js, which loads typescript.js). Works for nested global npm
// installs and flattened project installs alike. Nothing is executed.
func inspectVtsls(entry string) (vtslsInstall, string, string) {
	serverDir := filepath.Dir(filepath.Dir(entry))
	var server, service, ts struct{ Name, Version string }
	if readJSONFile(filepath.Join(serverDir, "package.json"), &server) != nil || server.Name != vtslsName {
		return vtslsInstall{}, "unsupported", "The TypeScript language server version could not be verified."
	}
	serviceDir := nodeResolvePackage(filepath.Join(serverDir, "dist"), vtslsServiceName)
	if serviceDir == "" || readJSONFile(filepath.Join(serviceDir, "package.json"), &service) != nil || service.Name != vtslsServiceName {
		return vtslsInstall{}, "missingTool", "The @vtsls/language-service package required by vtsls is not installed. " + tsInstallHint
	}
	tsDir := nodeResolvePackage(filepath.Join(serviceDir, "dist"), "typescript")
	if tsDir == "" || readJSONFile(filepath.Join(tsDir, "package.json"), &ts) != nil || ts.Name != "typescript" {
		return vtslsInstall{}, "missingTool", "The TypeScript package required by the language server is not installed. " + tsInstallHint
	}
	if service.Version != server.Version {
		return vtslsInstall{}, "unsupported", "Found vtsls " + versionText(server.Version) + " with a different language service (" + versionText(service.Version) + "); reinstall vtsls. " + tsInstallHint
	}
	files := []string{
		entry, filepath.Join(serverDir, "dist", "main.js"), filepath.Join(serviceDir, "dist", "index.js"),
		filepath.Join(tsDir, "lib", "tsserver.js"), filepath.Join(tsDir, "lib", "_tsserver.js"), filepath.Join(tsDir, "lib", "typescript.js"),
	}
	for _, f := range files {
		if st, err := os.Stat(f); err != nil || !st.Mode().IsRegular() {
			return vtslsInstall{}, "unsupported", "The TypeScript language server installation is incomplete. " + tsInstallHint
		}
	}
	return vtslsInstall{serverDir: serverDir, wrapper: server.Version, typescript: ts.Version, files: files}, "", ""
}

// workspaceTypeScript finds the project's own TypeScript at
// <root>/node_modules/typescript, contained in the root after symlink
// resolution. It returns the tsdk directory (lib), its version and the
// files tsserver loads, in tsWorkspaceNames order. Metadata only.
func workspaceTypeScript(root AuthorizedRoot) (tsdk, version string, files []string) {
	base := root.Project().Root
	dir, err := filepath.EvalSymlinks(filepath.Join(base, "node_modules", "typescript"))
	if err != nil {
		return "", "", nil
	}
	if rel, err := filepath.Rel(base, dir); err != nil || !filepath.IsLocal(rel) {
		return "", "", nil
	}
	var pkg struct{ Name, Version string }
	if readJSONFile(filepath.Join(dir, "package.json"), &pkg) != nil || pkg.Name != "typescript" {
		return "", "", nil
	}
	lib := filepath.Join(dir, "lib")
	files = []string{filepath.Join(lib, "tsserver.js"), filepath.Join(lib, "_tsserver.js"), filepath.Join(lib, "typescript.js")}
	for _, f := range files {
		if st, err := os.Lstat(f); err != nil || !st.Mode().IsRegular() {
			return "", "", nil
		}
	}
	return lib, pkg.Version, files
}

func versionText(v string) string {
	if tsVersionText.MatchString(v) {
		return v
	}
	return "(unknown)"
}

// nodeResolvePackage mirrors Node's lookup of a bare package name from a
// directory: each ancestor's node_modules, nearest first, skipping ancestors
// that are themselves node_modules. Results are real paths, as Node resolves
// by default. Metadata only; nothing is loaded.
func nodeResolvePackage(fromDir, name string) string {
	dir := fromDir
	for range 64 {
		if filepath.Base(dir) != "node_modules" {
			if real, err := filepath.EvalSymlinks(filepath.Join(dir, "node_modules", filepath.FromSlash(name))); err == nil {
				if st, err := os.Stat(filepath.Join(real, "package.json")); err == nil && st.Mode().IsRegular() {
					return real
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// tsNode picks the runtime without executing anything: the reviewed NodeDir
// when given, else the node beside a global npm install
// (<prefix>/lib/node_modules/@vtsls/language-server -> <prefix>/bin/node), else
// the first real node in the reviewed path.
func tsNode(env TSEnvironment, serverDir string, reviewedPath []string) string {
	var dirs []string
	if env.NodeDir != "" {
		dirs = []string{env.NodeDir}
	} else {
		if prefix, ok := strings.CutSuffix(serverDir, filepath.Join("lib", "node_modules", "@vtsls", "language-server")); ok {
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

// HostNode returns the install prefix and version of the Node.js vtsls would
// run on from dirs (the first real node, as tsNode picks it), when it is new
// enough. Metadata only; nothing is executed. Managed installs reuse it.
func HostNode(dirs []string) (prefix, version string) {
	for _, dir := range dirs {
		if node := realNode(dir); node != "" {
			if major, v := nodeVersion(node); major >= minTSNodeMajor {
				return filepath.Dir(filepath.Dir(node)), v
			}
			return "", ""
		}
	}
	return "", ""
}

// realNode accepts only an executable named node after symlink resolution
// that is not a script. Version-manager shims (mise links to the mise binary,
// volta to volta-shim, asdf/nodenv are shell scripts) are skipped.
func realNode(dir string) string {
	if !filepath.IsAbs(dir) || hasParent(dir) {
		return ""
	}
	real, err := filepath.EvalSymlinks(filepath.Join(dir, "node"))
	if err != nil || filepath.Base(real) != "node" || !isExecutable(real) || isScript(real) {
		return ""
	}
	return real
}

// nodeVersion reads <prefix>/include/node/node_version.h, which every official,
// Homebrew and version-manager Node install ships. It never runs node.
func nodeVersion(node string) (int, string) {
	f, err := os.Open(filepath.Join(filepath.Dir(filepath.Dir(node)), "include", "node", "node_version.h"))
	if err != nil {
		return 0, ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return 0, ""
	}
	parts := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "#define" && strings.HasPrefix(fields[1], "NODE_") && strings.HasSuffix(fields[1], "_VERSION") {
			if _, err := strconv.Atoi(fields[2]); err == nil {
				parts[fields[1]] = fields[2]
			}
		}
	}
	major, minor, patch := parts["NODE_MAJOR_VERSION"], parts["NODE_MINOR_VERSION"], parts["NODE_PATCH_VERSION"]
	if major == "" || minor == "" || patch == "" {
		return 0, ""
	}
	n, _ := strconv.Atoi(major)
	return n, major + "." + minor + "." + patch
}

func readJSONFile(file string, v any) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("metadata unreadable")
	}
	return json.Unmarshal(data, v)
}

func (p *TSPlan) Availability() string { return p.availability }
func (p *TSPlan) Version() (wrapper, typescript string) {
	return p.wrapper, p.ts
}
func (p *TSPlan) Packages() []string { return slices.Clone(p.packages) }

// NodeVersion is the runtime version read from installed metadata.
func (p *TSPlan) NodeVersion() string { return p.node }

// ProjectLocal reports that the binary is executable project content.
func (p *TSPlan) ProjectLocal() bool { return p.projectLocal }

// Server is the fixed server identity, only when the plan is available.
func (p *TSPlan) Server() (ServerConfig, bool) { return p.config, p.availability == "available" }

// Provider describes honest opened-documents coverage for DiscoverLocalLive. An
// empty push is never reported as clean TypeScript.
func (p *TSPlan) Provider() ProviderState {
	notice := tsOpenedNotice
	if p.notice != "" {
		notice = p.notice + " " + tsOpenedNotice
	}
	roots := slices.Clone(p.packages)
	if roots == nil {
		roots = []string{}
	}
	return ProviderState{
		ProviderID: tsProviderID, Availability: p.availability, Phase: "idle", Freshness: "uncertain",
		Coverage:           Coverage{Kind: "openedDocuments", Roots: roots, Partial: p.partial, ExternalChanges: CoverageExternalChanges},
		Notice:             shorten(notice, 2048),
		DocumentExtensions: slices.Clone(tsDocumentExtensions),
	}
}

// Documents resolves only owned JS/TS files that sit inside a contained package.
// It never launches anything.
func (p *TSPlan) Documents(sessions *SessionManager) SessionDocuments {
	cfg, ok := p.Server()
	return SessionDocuments{Sessions: sessions, Resolve: func(root AuthorizedRoot, file string) (DocumentServer, bool) {
		lang, owned := TSLanguageID(file)
		return DocumentServer{Config: cfg, LanguageID: lang}, ok && owned && root.Project() == p.root.Project() && p.covers(file)
	}}
}

// covers reports whether a contained package directory is an ancestor of file.
func (p *TSPlan) covers(file string) bool { return coversDir(p.packages, file) }

// TSLanguageID is the extension ownership rule. .svelte (and everything else)
// is deliberately not owned here; CD-24 refines Svelte ownership.
func TSLanguageID(file string) (string, bool) {
	switch strings.ToLower(path.Ext(file)) {
	case ".ts", ".mts", ".cts":
		return "typescript", true
	case ".tsx":
		return "typescriptreact", true
	case ".js", ".mjs", ".cjs":
		return "javascript", true
	case ".jsx":
		return "javascriptreact", true
	}
	return "", false
}

// ---- contained package discovery --------------------------------------------

type tsWorkspace struct {
	packages, configs []string
	partial           bool
	sources           bool // a file with one of projectMarkers.sources was seen
}

// projectMarkers selects the files that make a directory a project root.
// Every configs file found is also a configuration identity bound into
// approval; plain markers are not read by the server and are not bound.
// skipDir names dependency trees that are skipped silently.
type projectMarkers struct {
	configs, plain []string
	skipDir        []string
	sources        []string // lower-case extensions of the language's source files
}

var tsProjectMarkers = projectMarkers{configs: []string{"package.json", "tsconfig.json", "jsconfig.json"}, skipDir: []string{"node_modules"}, sources: tsDocumentExtensions}

// discoverTSPackages walks only inside the authorized root with bounded rooted
// reads. A directory containing package.json, tsconfig.json or jsconfig.json is
// a package root. The selected directory is never promoted to a parent package
// or monorepo root; nested-only roots are supported. node_modules, hidden
// directories and symlinks are skipped (partial). tsconfig "extends" and
// "references" are not followed, so sibling/out-of-root projects are never read.
func discoverTSPackages(root AuthorizedRoot) (tsWorkspace, error) {
	return discoverProjectDirs(root, tsProjectMarkers)
}

// discoverProjectDirs is the bounded contained walk shared by adapters. Hidden
// directories and symlinks are skipped and mark the result partial; marker
// contents are never read here.
func discoverProjectDirs(root AuthorizedRoot, markers projectMarkers) (tsWorkspace, error) {
	var w tsWorkspace
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return w, failure("unauthorized", "Project discovery root cannot be opened")
	}
	defer func() { _ = r.Close() }()
	entries := 0
	var walk func(string, int)
	walk = func(dir string, depth int) {
		f, err := r.Open(dir)
		if err != nil {
			w.partial = true
			return
		}
		defer func() { _ = f.Close() }()
		ds, err := f.ReadDir(maxTSEntries + 1 - entries)
		if err != nil && !errors.Is(err, io.EOF) {
			w.partial = true
			return
		}
		slices.SortFunc(ds, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		found := false
		for _, d := range ds {
			entries++
			if entries > maxTSEntries {
				w.partial = true
				return
			}
			rel := filepath.Join(dir, d.Name())
			portable := filepath.ToSlash(rel)
			if ValidatePath(portable) != nil || d.Type()&os.ModeSymlink != 0 {
				w.partial = true
				continue
			}
			if d.IsDir() {
				n := d.Name()
				if slices.Contains(markers.skipDir, n) { // dependencies, not project packages
					continue
				}
				if strings.HasPrefix(n, ".") || depth >= 32 {
					w.partial = true
					continue
				}
				walk(rel, depth+1)
				continue
			}
			if !w.sources && slices.Contains(markers.sources, strings.ToLower(filepath.Ext(d.Name()))) {
				w.sources = true
			}
			if slices.Contains(markers.plain, d.Name()) {
				found = true
				continue
			}
			if !slices.Contains(markers.configs, d.Name()) {
				continue
			}
			if len(w.configs) >= maxTSConfigs {
				w.partial = true
				continue
			}
			w.configs = append(w.configs, portable)
			found = true
		}
		if !found {
			return
		}
		if len(w.packages) >= maxTSPackages {
			w.partial = true
			return
		}
		pkg := filepath.ToSlash(dir)
		if pkg == "." {
			pkg = ""
		}
		w.packages = append(w.packages, pkg)
	}
	walk(".", 0)
	slices.Sort(w.packages)
	slices.Sort(w.configs)
	return w, nil
}

// coversDir reports whether one of dirs (portable, "" for the root) is an
// ancestor of file. No parent of the authorized root is ever considered.
func coversDir(dirs []string, file string) bool {
	dir := path.Dir(file)
	for {
		check := dir
		if dir == "." {
			check = ""
		}
		if slices.Contains(dirs, check) {
			return true
		}
		if dir == "." || dir == "/" {
			return false
		}
		dir = path.Dir(dir)
	}
}

// ---- notification bridge -----------------------------------------------------

// TSBridge turns session notifications into document-set replacements. It
// reuses the CD-22 normalizer with the "typescript" attribution prefix. It
// publishes nothing for unowned, unopened, closed or out-of-root files. A
// server that never sends versions yields uncertain ranges bound to the mirror
// version at receipt, not server-confirmed freshness; absence of a push is not "clean".
type TSBridge struct {
	root   AuthorizedRoot
	sink   goplsSink
	Notice func(string)
}

func NewTSBridge(root AuthorizedRoot, m *Manager) *TSBridge {
	return &TSBridge{root: root, sink: m}
}

func (b *TSBridge) note(s string) {
	if b.Notice != nil {
		b.Notice(s)
	}
}

// Handle must be quick and runs on the session goroutine.
func (b *TSBridge) Handle(n SessionNotification) {
	// Config files (tsconfig.json) and other unowned paths receive pushes from
	// real servers; they are not JS/TS documents.
	handleOwnedPush(b.root, b.sink, n, tsProviderID, partialTSNotice, func(file string) bool {
		_, owned := TSLanguageID(file)
		return owned
	}, b.note)
}

// handleOwnedPush is the shared opened-documents bridge: one provider's
// publishDiagnostics for owned files become document-set replacements through
// the CD-22 normalizer. Unowned, unopened, closed and out-of-root files never
// create state.
func handleOwnedPush(root AuthorizedRoot, sink goplsSink, n SessionNotification, providerID, partial string, owned func(string) bool, note func(string)) {
	if n.Project != root.Project() || n.ProviderID != providerID || n.Method != "textDocument/publishDiagnostics" {
		return
	}
	pub, err := normalizeLSPPublication(root, n.Params, providerID)
	switch {
	case errors.Is(err, errGoplsOutside):
		note(OutsideRootNotice)
		return
	case errors.Is(err, errGoplsUnknown):
		return
	case err != nil:
		note(partial)
		return
	}
	if !owned(pub.Path) {
		return
	}
	if err := publishLivePublication(sink, n, pub); err != nil {
		var e *Error
		if !errors.As(err, &e) || (e.Code != "notFound" && e.Code != "staleContext" && e.Code != "unsupported") {
			note(partial)
		}
		return
	}
	if pub.Dropped > 0 || pub.Partial || pub.Truncated {
		note(partial)
	}
}

// tsDocumentExtensions lists every extension TSLanguageID owns.
var tsDocumentExtensions = []string{".ts", ".mts", ".cts", ".tsx", ".js", ".mjs", ".cjs", ".jsx"}
