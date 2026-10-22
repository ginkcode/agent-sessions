package codediagnostics

// Svelte language-server adapter seam (CD-24).
//
// STATUS: no Svelte language server was found on this host, so NO server
// version is verified (supportedSvelteLSVersions is empty). Every discovered
// server is therefore reported "unsupported" until a spike adds a verified
// version. What is implemented and testable without a server: contained
// package detection (metadata only, config never evaluated), fixed-argv
// resolution, deterministic Svelte/TypeScript ownership, session keying, and a
// publishDiagnostics bridge. Fake-server tests are SYNTHETIC. Evidence and
// limits: docs/plan/code-diagnostics-svelte.md and testdata/svelte-lsp/evidence.json.
// This file never installs, downloads, runs npx/package scripts or executes
// anything to probe versions.

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	svelteLSProviderID = "svelte"
	svelteLSName       = "svelte-language-server"
	svelteLSPolicy     = "svelte-ls-v1:%s:svelte%s:ts%s:darwin/arm64:push-only:no-edits:opened-documents:source-coordinates-only"
	maxSveltePackages  = 48
	maxSvelteConfigs   = 192
	maxSvelteEntries   = 4096

	partialSvelteNotice = "Some Svelte diagnostics could not be verified; results are partial."
	svelteOpenedNotice  = "Svelte diagnostics cover opened .svelte documents only; unopened files are not continuously synchronized."
	// SvelteVirtualNotice is shown when a server reports on a virtual/generated
	// document. No mapping to source offsets is verified, so nothing is linked.
	SvelteVirtualNotice = "Some Svelte findings were reported on generated documents and cannot be mapped to source; coverage is limited."
)

// SvelteLSVersion is a verified server/runtime combination.
type SvelteLSVersion struct{ Server, Svelte, TypeScript string }

// supportedSvelteLSVersions is intentionally empty: none verified.
var supportedSvelteLSVersions []SvelteLSVersion

// SvelteLSEnvironment is reviewed host metadata, not the inherited environment.
type SvelteLSEnvironment struct {
	Home    string
	NodeDir string // reviewed directory containing the node runtime

	// versionOf reads package metadata without executing anything. Nil reads
	// the installed package.json files.
	versionOf func(executable string) (name, server, svelte, typescript string, err error)
	// supported overrides the (empty) verified list; tests only.
	supported []SvelteLSVersion
}

// SvelteLSPlan is the discovery result. Availability is independent of the
// svelte-check, TypeScript and Go providers.
type SvelteLSPlan struct {
	root         AuthorizedRoot
	availability string
	notice       string
	config       ServerConfig
	version      SvelteLSVersion
	packages     []string
	partial      bool
	projectLocal bool
}

// DiscoverSvelteLS inspects metadata only. Errors are reserved for an invalid
// host description or a root that is no longer authorized.
func DiscoverSvelteLS(root AuthorizedRoot, reviewedPath []string, env SvelteLSEnvironment) (*SvelteLSPlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	for _, p := range []string{env.Home, env.NodeDir} {
		if !filepath.IsAbs(p) || !bounded(p, 4096) || hasParent(p) {
			return nil, failure("invalidArgument", "Reviewed absolute home and node paths are required")
		}
	}
	p := &SvelteLSPlan{root: root, availability: "available"}
	unavailable := func(state, notice string) (*SvelteLSPlan, error) {
		p.availability, p.notice, p.partial = state, notice, true
		return p, nil
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return unavailable("unsupported", "Svelte language server support has not been verified on this platform.")
	}
	executable, local := resolveSvelteServer(root, reviewedPath)
	if executable == "" {
		return unavailable("missingTool", "No Svelte language server (svelteserver) is installed.")
	}
	if !isExecutable(filepath.Join(env.NodeDir, "node")) {
		return unavailable("missingTool", "The Node runtime required by the Svelte language server is not installed.")
	}
	versionOf := env.versionOf
	if versionOf == nil {
		versionOf = svelteServerVersions
	}
	name, server, svelte, typescript, err := versionOf(executable)
	if err != nil || name != svelteLSName {
		return unavailable("unsupported", "The Svelte language server version could not be verified.")
	}
	if svelte == "" || typescript == "" {
		return unavailable("missingTool", "The Svelte or TypeScript package required by the language server is not installed.")
	}
	supported := supportedSvelteLSVersions
	if env.supported != nil {
		supported = env.supported
	}
	v := SvelteLSVersion{server, svelte, typescript}
	if !slices.Contains(supported, v) {
		return unavailable("unsupported", "This Svelte language server version is not supported.")
	}
	p.version, p.projectLocal = v, local
	w, err := discoverSveltePackages(root)
	if err != nil {
		return nil, err
	}
	p.packages, p.partial = w.packages, w.partial
	if len(w.packages) == 0 {
		return unavailable("unsupported", "No contained Svelte package was found.")
	}
	p.config = ServerConfig{
		ProviderID:    svelteLSProviderID,
		Executable:    executable,
		Args:          []string{"--stdio"},
		Env:           []string{"HOME=" + env.Home, "PATH=" + env.NodeDir + ":/usr/bin:/bin"},
		AdapterPolicy: sprintfPolicy(server, svelte, typescript),
		ConfigPaths:   w.configs,
	}
	if _, err := Fingerprint(root, []ToolInput{p.config.Tool()}); err != nil {
		return nil, err
	}
	return p, nil
}

func sprintfPolicy(server, svelte, ts string) string {
	parts := strings.SplitN(svelteLSPolicy, "%s", 4)
	return parts[0] + server + parts[1] + svelte + parts[2] + ts + parts[3]
}

// resolveSvelteServer prefers the contained project-local binary (executable
// project content, covered by CD-02 trust), then reviewed absolute PATH
// directories. A project-local link must stay inside the root.
func resolveSvelteServer(root AuthorizedRoot, reviewedPath []string) (string, bool) {
	base := root.Project().Root
	if real, err := filepath.EvalSymlinks(filepath.Join(base, "node_modules", ".bin", "svelteserver")); err == nil && isExecutable(real) {
		if rel, err := filepath.Rel(base, real); err == nil && filepath.IsLocal(rel) {
			return real, true
		}
	}
	if exe := firstExecutable(reviewedPath, "svelteserver"); exe != "" {
		return exe, false
	}
	return "", false
}

// svelteServerVersions reads package metadata next to the resolved entry
// point (<node_modules>/svelte-language-server/bin/server.js) and sibling
// svelte/typescript packages. It never executes the server. Missing or foreign
// sibling packages report an empty version.
func svelteServerVersions(executable string) (string, string, string, string, error) {
	pkgDir := filepath.Dir(filepath.Dir(executable))
	var pkg struct{ Name, Version string }
	if err := readJSONFile(filepath.Join(pkgDir, "package.json"), &pkg); err != nil {
		return "", "", "", "", err
	}
	nodeModules := filepath.Dir(pkgDir)
	sibling := func(name string) string {
		var m struct{ Name, Version string }
		if err := readJSONFile(filepath.Join(nodeModules, name, "package.json"), &m); err != nil || m.Name != name {
			return ""
		}
		return m.Version
	}
	return pkg.Name, pkg.Version, sibling("svelte"), sibling("typescript"), nil
}

func (p *SvelteLSPlan) Availability() string         { return p.availability }
func (p *SvelteLSPlan) Version() SvelteLSVersion     { return p.version }
func (p *SvelteLSPlan) Packages() []string           { return slices.Clone(p.packages) }
func (p *SvelteLSPlan) ProjectLocal() bool           { return p.projectLocal }
func (p *SvelteLSPlan) Server() (ServerConfig, bool) { return p.config, p.availability == "available" }

// Provider describes honest opened-documents coverage. Data only; no
// production route consumes it, and an empty push is never reported as clean.
func (p *SvelteLSPlan) Provider() ProviderState {
	notice := svelteOpenedNotice
	if p.notice != "" {
		notice = p.notice + " " + svelteOpenedNotice
	}
	roots := slices.Clone(p.packages)
	if roots == nil {
		roots = []string{}
	}
	return ProviderState{
		ProviderID: svelteLSProviderID, Availability: p.availability, Phase: "idle", Freshness: "uncertain",
		Coverage: Coverage{Kind: "openedDocuments", Roots: roots, Partial: p.partial, ExternalChanges: CoverageExternalChanges},
		Notice:   shorten(notice, 2048),
	}
}

// ---- ownership ----------------------------------------------------------------

// DocumentOwner is the single deterministic routing rule shared with CD-23.
// .svelte belongs to the Svelte provider; JS/TS (including Svelte 5 rune
// modules such as x.svelte.ts, whose last extension is .ts) belongs to the
// TypeScript provider; everything else has no LSP owner. A document is sent to
// exactly one provider, so one file never gets two didOpen streams.
func DocumentOwner(file string) (providerID, languageID string) {
	if strings.ToLower(path.Ext(file)) == ".svelte" {
		return svelteLSProviderID, "svelte"
	}
	if lang, ok := TSLanguageID(file); ok {
		return tsProviderID, lang
	}
	return "", ""
}

// OwnedDocuments builds the single resolver over both plans (either may be
// nil or unavailable). Session keying is keyOf: (project, provider, policy),
// so there is at most one Svelte session and one TypeScript session per
// authorized root regardless of package count; packages are coverage, not
// session keys.
func OwnedDocuments(sessions *SessionManager, ts *TSPlan, sv *SvelteLSPlan) SessionDocuments {
	return SessionDocuments{Sessions: sessions, Resolve: func(root AuthorizedRoot, file string) (DocumentServer, bool) {
		owner, lang := DocumentOwner(file)
		switch owner {
		case svelteLSProviderID:
			if sv == nil || root.Project() != sv.root.Project() || !coversPackage(sv.packages, file) {
				return DocumentServer{}, false
			}
			cfg, ok := sv.Server()
			return DocumentServer{Config: cfg, LanguageID: lang}, ok
		case tsProviderID:
			if ts == nil || root.Project() != ts.root.Project() || !ts.covers(file) {
				return DocumentServer{}, false
			}
			cfg, ok := ts.Server()
			return DocumentServer{Config: cfg, LanguageID: lang}, ok
		}
		return DocumentServer{}, false
	}}
}

// Documents resolves only .svelte files inside a contained Svelte package.
func (p *SvelteLSPlan) Documents(sessions *SessionManager) SessionDocuments {
	return OwnedDocuments(sessions, nil, p)
}

func coversPackage(packages []string, file string) bool {
	dir := path.Dir(file)
	for {
		check := dir
		if dir == "." {
			check = ""
		}
		if slices.Contains(packages, check) {
			return true
		}
		if dir == "." || dir == "/" {
			return false
		}
		dir = path.Dir(dir)
	}
}

// ---- contained package detection ----------------------------------------------

type svelteWorkspace struct {
	packages, configs []string
	partial           bool
}

// discoverSveltePackages walks only inside the authorized root with bounded
// rooted reads. A directory is a Svelte package when it has a svelte.config.*
// file (existence only; never imported or evaluated) or a package.json that
// declares svelte or @sveltejs/kit (parsed as JSON, bounded). Companion
// package/tsconfig/jsconfig files bind into the approval fingerprint. Parent
// directories are never promoted; node_modules, hidden directories and
// symlinks are skipped (hidden/symlink ⇒ partial).
func discoverSveltePackages(root AuthorizedRoot) (svelteWorkspace, error) {
	var w svelteWorkspace
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return w, failure("unauthorized", "Svelte discovery root cannot be opened")
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
		ds, err := f.ReadDir(maxSvelteEntries + 1 - entries)
		if err != nil && !errors.Is(err, io.EOF) {
			w.partial = true
			return
		}
		slices.SortFunc(ds, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		var files []string
		svelte := false
		for _, d := range ds {
			entries++
			if entries > maxSvelteEntries {
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
				if n == "node_modules" {
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
			case "svelte.config.js", "svelte.config.mjs", "svelte.config.cjs", "svelte.config.ts":
				svelte = true
				files = append(files, portable)
			case "tsconfig.json", "jsconfig.json":
				files = append(files, portable)
			case "package.json":
				files = append(files, portable)
				if declaresSvelte(root, portable) {
					svelte = true
				}
			}
		}
		if !svelte {
			return
		}
		if len(w.packages) >= maxSveltePackages || len(w.configs)+len(files) > maxSvelteConfigs {
			w.partial = true
			return
		}
		pkg := filepath.ToSlash(dir)
		if pkg == "." {
			pkg = ""
		}
		w.packages = append(w.packages, pkg)
		w.configs = append(w.configs, files...)
	}
	walk(".", 0)
	slices.Sort(w.packages)
	slices.Sort(w.configs)
	return w, nil
}

// declaresSvelte parses package.json as data only; unreadable or oversized
// manifests simply do not declare Svelte.
func declaresSvelte(root AuthorizedRoot, rel string) bool {
	data, err := rootedRead(root, rel, 1<<20)
	if err != nil {
		return false
	}
	var m struct {
		Dependencies, DevDependencies, PeerDependencies map[string]json.RawMessage
	}
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	for _, deps := range []map[string]json.RawMessage{m.Dependencies, m.DevDependencies, m.PeerDependencies} {
		for _, name := range []string{"svelte", "@sveltejs/kit"} {
			if _, ok := deps[name]; ok {
				return true
			}
		}
	}
	return false
}

// ---- notification bridge --------------------------------------------------------

// SvelteBridge turns session notifications into document-set replacements. It
// reuses the CD-22 normalizer with the "svelte" attribution prefix and
// accepts source coordinates for real, contained .svelte files only. Virtual
// documents (non-file URIs) and files owned by TypeScript are never published;
// virtual URIs raise SvelteVirtualNotice instead of fabricated links/offsets.
type SvelteBridge struct {
	root   AuthorizedRoot
	sink   goplsSink
	Notice func(string)
}

func NewSvelteBridge(root AuthorizedRoot, m *Manager) *SvelteBridge {
	return &SvelteBridge{root: root, sink: m}
}

func (b *SvelteBridge) note(s string) {
	if b.Notice != nil {
		b.Notice(s)
	}
}

// Handle must be quick and runs on the session goroutine.
func (b *SvelteBridge) Handle(n SessionNotification) {
	if n.Project != b.root.Project() || n.ProviderID != svelteLSProviderID || n.Method != "textDocument/publishDiagnostics" {
		return
	}
	var head struct {
		URI string `json:"uri"`
	}
	if len(n.Params) <= maxGoplsParams && json.Unmarshal(n.Params, &head) == nil {
		if u, err := url.Parse(head.URI); err == nil && u.Scheme != "" && u.Scheme != "file" {
			b.note(SvelteVirtualNotice)
			return
		}
	}
	pub, err := normalizeLSPPublication(b.root, n.Params, svelteLSProviderID)
	switch {
	case errors.Is(err, errGoplsOutside):
		b.note(OutsideRootNotice)
		return
	case errors.Is(err, errGoplsUnknown):
		return
	case err != nil:
		b.note(partialSvelteNotice)
		return
	}
	if owner, _ := DocumentOwner(pub.Path); owner != svelteLSProviderID {
		return // TypeScript-owned and config files never enter the Svelte set
	}
	if err := publishLivePublication(b.sink, n, pub); err != nil {
		var e *Error
		if !errors.As(err, &e) || (e.Code != "notFound" && e.Code != "staleContext" && e.Code != "unsupported") {
			b.note(partialSvelteNotice)
		}
		return
	}
	if pub.Dropped > 0 || pub.Partial || pub.Truncated {
		b.note(partialSvelteNotice)
	}
}
