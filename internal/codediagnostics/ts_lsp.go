package codediagnostics

// TypeScript/JavaScript adapter (CD-23).
//
// Like go_lsp.go this file owns only contained package discovery, a fixed-argv
// server identity with independent availability, extension ownership and
// publishDiagnostics bridging (normalization is shared with CD-22). Process
// launch, trust and the document protocol stay in CD-19/CD-20. Nothing here
// installs, downloads, runs npx/package scripts, or probes a version by
// executing the tool. The spike evidence and its limits are recorded in
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
	"runtime"
	"slices"
	"strings"
)

const (
	tsProviderID    = "typescript"
	vtslsName       = "@vtsls/language-server"
	tsPolicy        = "vtsls-v1:0.3.0:ts5.9.3:darwin/arm64:push-only:no-edits:opened-documents"
	maxTSPackages   = 48
	maxTSConfigs    = 192
	maxTSEntries    = 4096
	partialTSNotice = "Some TypeScript diagnostics could not be verified; results are partial."
	tsOpenedNotice  = "TypeScript diagnostics cover opened documents only; unopened files are not continuously synchronized."
)

// supportedVtslsVersions pairs a wrapper version with the TypeScript version it
// was observed with. Anything else is unsupported until a spike adds it.
var supportedVtslsVersions = []struct{ Wrapper, TypeScript string }{{"0.3.0", "5.9.3"}}

// TSEnvironment is reviewed host metadata, not the inherited environment.
type TSEnvironment struct {
	Home    string
	NodeDir string // reviewed directory containing the node runtime

	// versionOf reads wrapper/TypeScript versions from package metadata without
	// executing anything. Nil reads the installed package.json files.
	versionOf func(executable string) (name, wrapper, typescript string, err error)
}

// TSPlan is the discovery result. Availability is independent of every other
// provider.
type TSPlan struct {
	root         AuthorizedRoot
	availability string
	notice       string
	config       ServerConfig
	wrapper, ts  string
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
	for _, p := range []string{env.Home, env.NodeDir} {
		if !filepath.IsAbs(p) || !bounded(p, 4096) || hasParent(p) {
			return nil, failure("invalidArgument", "Reviewed absolute home and node paths are required")
		}
	}
	p := &TSPlan{root: root, availability: "available"}
	unavailable := func(state, notice string) (*TSPlan, error) {
		p.availability, p.notice, p.partial = state, notice, true
		return p, nil
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return unavailable("unsupported", "TypeScript language server support has not been verified on this platform.")
	}
	executable, local := resolveVtsls(root, reviewedPath)
	if executable == "" {
		return unavailable("missingTool", "No TypeScript language server (vtsls) is installed.")
	}
	if !isExecutable(filepath.Join(env.NodeDir, "node")) {
		return unavailable("missingTool", "The Node runtime required by the TypeScript language server is not installed.")
	}
	versionOf := env.versionOf
	if versionOf == nil {
		versionOf = vtslsVersions
	}
	name, wrapper, typescript, err := versionOf(executable)
	if err != nil || name != vtslsName {
		return unavailable("unsupported", "The TypeScript language server version could not be verified.")
	}
	if typescript == "" {
		return unavailable("missingTool", "The TypeScript package required by the language server is not installed.")
	}
	if !slices.Contains(supportedVtslsVersions, struct{ Wrapper, TypeScript string }{wrapper, typescript}) {
		return unavailable("unsupported", "This TypeScript language server and TypeScript combination is not supported.")
	}
	p.wrapper, p.ts, p.projectLocal = wrapper, typescript, local
	w, err := discoverTSPackages(root)
	if err != nil {
		return nil, err
	}
	p.packages, p.partial = w.packages, w.partial
	if len(w.packages) == 0 {
		return unavailable("unsupported", "No contained TypeScript or JavaScript package was found.")
	}
	p.config = ServerConfig{
		ProviderID:    tsProviderID,
		Executable:    executable,
		Args:          []string{"--stdio"},
		Env:           []string{"HOME=" + env.Home, "PATH=" + env.NodeDir + ":/usr/bin:/bin"},
		AdapterPolicy: tsPolicy,
		ConfigPaths:   w.configs,
	}
	if _, err := Fingerprint(root, []ToolInput{p.config.Tool()}); err != nil {
		return nil, err
	}
	return p, nil
}

// resolveVtsls prefers the contained project-local binary (executable project
// content, covered by CD-02 trust) and then reviewed absolute PATH directories.
// The project-local path must stay inside the root after symlink resolution.
func resolveVtsls(root AuthorizedRoot, reviewedPath []string) (string, bool) {
	base := root.Project().Root
	if real, err := filepath.EvalSymlinks(filepath.Join(base, "node_modules", ".bin", "vtsls")); err == nil && isExecutable(real) {
		if rel, err := filepath.Rel(base, real); err == nil && filepath.IsLocal(rel) {
			return real, true
		}
	}
	if exe := firstExecutable(reviewedPath, "vtsls"); exe != "" {
		return exe, false
	}
	return "", false
}

// vtslsVersions reads package metadata next to the resolved entry point:
// <node_modules>/@vtsls/language-server/bin/vtsls.js and
// <node_modules>/typescript/package.json. It never executes the server.
func vtslsVersions(executable string) (string, string, string, error) {
	pkgDir := filepath.Dir(filepath.Dir(executable))
	var pkg struct{ Name, Version string }
	if err := readJSONFile(filepath.Join(pkgDir, "package.json"), &pkg); err != nil {
		return "", "", "", err
	}
	var ts struct{ Name, Version string }
	nodeModules := filepath.Dir(filepath.Dir(pkgDir))
	// A missing or foreign typescript package is reported as an empty version.
	if err := readJSONFile(filepath.Join(nodeModules, "typescript", "package.json"), &ts); err != nil || ts.Name != "typescript" {
		ts.Version = ""
	}
	return pkg.Name, pkg.Version, ts.Version, nil
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

// ProjectLocal reports that the binary is executable project content.
func (p *TSPlan) ProjectLocal() bool { return p.projectLocal }

// Server is the fixed server identity, only when the plan is available.
func (p *TSPlan) Server() (ServerConfig, bool) { return p.config, p.availability == "available" }

// Provider describes honest opened-documents coverage. Data only: no production
// route consumes it, and an empty push is never reported as clean TypeScript.
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
		Coverage: Coverage{Kind: "openedDocuments", Roots: roots, Partial: p.partial, ExternalChanges: CoverageExternalChanges},
		Notice:   shorten(notice, 2048),
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
// No parent of the authorized root is ever considered.
func (p *TSPlan) covers(file string) bool {
	dir := path.Dir(file)
	for {
		check := dir
		if dir == "." {
			check = ""
		}
		if slices.Contains(p.packages, check) {
			return true
		}
		if dir == "." || dir == "/" {
			return false
		}
		dir = path.Dir(dir)
	}
}

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
}

// discoverTSPackages walks only inside the authorized root with bounded rooted
// reads. A directory containing package.json, tsconfig.json or jsconfig.json is
// a package root. The selected directory is never promoted to a parent package
// or monorepo root; nested-only roots are supported. node_modules, hidden
// directories and symlinks are skipped (partial). tsconfig "extends" and
// "references" are not followed, so sibling/out-of-root projects are never read.
func discoverTSPackages(root AuthorizedRoot) (tsWorkspace, error) {
	var w tsWorkspace
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return w, failure("unauthorized", "TypeScript discovery root cannot be opened")
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
				if n == "node_modules" { // dependencies, not project packages
					continue
				}
				if strings.HasPrefix(n, ".") || depth >= 32 {
					w.partial = true
					continue
				}
				walk(rel, depth+1)
				continue
			}
			switch d.Name() {
			case "package.json", "tsconfig.json", "jsconfig.json":
			default:
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

// ---- notification bridge -----------------------------------------------------

// TSBridge turns session notifications into document-set replacements. It
// reuses the CD-22 normalizer with the "typescript" attribution prefix. It
// publishes nothing for unowned, unopened, closed or out-of-root files. A
// server that never sends versions yields uncertain, panel-only results via the
// manager's unversioned rule; absence of a push is not "clean".
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
	if n.Project != b.root.Project() || n.ProviderID != tsProviderID || n.Method != "textDocument/publishDiagnostics" {
		return
	}
	pub, err := normalizeLSPPublication(b.root, n.Params, tsProviderID)
	switch {
	case errors.Is(err, errGoplsOutside):
		b.note(OutsideRootNotice)
		return
	case errors.Is(err, errGoplsUnknown):
		return
	case err != nil:
		b.note(partialTSNotice)
		return
	}
	// Config files (tsconfig.json) and other unowned paths receive pushes from
	// real servers; they are not JS/TS documents.
	if _, owned := TSLanguageID(pub.Path); !owned {
		return
	}
	if err := b.sink.PublishPathDiagnostics(n.Project, n.ProviderID, n.Generation, pub.Path, pub.Version, pub.Diagnostics); err != nil {
		var e *Error
		if !errors.As(err, &e) || (e.Code != "notFound" && e.Code != "staleContext" && e.Code != "unsupported") {
			b.note(partialTSNotice)
		}
		return
	}
	if pub.Dropped > 0 || pub.Truncated {
		b.note(partialTSNotice)
	}
}
