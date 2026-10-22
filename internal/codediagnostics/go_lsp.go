package codediagnostics

// gopls adapter (CD-22).
//
// This file owns three things and nothing else: contained Go workspace
// discovery, a fixed-argv server identity (independent availability), and
// publishDiagnostics normalization. Process launch, trust and the document
// protocol stay in CD-19/CD-20. Nothing here installs or downloads a tool, runs
// a version probe, or widens the authorized root. Supported versions and the
// observed behavior are recorded in docs/plan/code-diagnostics-gopls.md and
// testdata/gopls/evidence.json.

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/modfile"
)

const (
	goplsProviderID  = "gopls"
	goplsModulePath  = "golang.org/x/tools/gopls"
	goplsPolicy      = "gopls-v1:v0.24.0:darwin/arm64:push-only:gowork-off:no-edits:opened-documents"
	maxGoplsModules  = 24
	maxGoplsConfigs  = 128
	maxGoplsEntries  = 4096
	maxGoplsParams   = 4 << 20
	partialGoNotice  = "Some Go diagnostics could not be verified; results are partial."
	goplsOpenedNotic = "Go diagnostics cover opened documents only; unopened files are not continuously synchronized."
)

// supportedGoplsVersions lists exactly the versions whose push/sync behavior
// was observed. Anything else is unsupported until a spike adds it.
var supportedGoplsVersions = []string{"v0.24.0"}

// GoplsEnvironment is reviewed host metadata, not the inherited environment.
type GoplsEnvironment struct {
	Home, Cache, ModuleCache string
	GoRoot                   string // reviewed toolchain root containing bin/go

	// versionOf reads the embedded module version without executing the binary.
	// Nil uses debug/buildinfo. Tests inject it.
	versionOf func(path string) (module, version string, err error)
}

// GoplsPlan is the discovery result. Availability is independent of every other
// provider: a missing or unsupported server never disables go vet.
type GoplsPlan struct {
	root         AuthorizedRoot
	availability string
	notice       string
	config       ServerConfig
	version      string
	modules      []string
	partial      bool
}

// DiscoverGopls inspects metadata only. Errors are reserved for an invalid host
// description or a root that is no longer authorized; everything about the tool
// or project is reported as plan availability.
func DiscoverGopls(root AuthorizedRoot, reviewedPath []string, env GoplsEnvironment) (*GoplsPlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	for _, path := range []string{env.Home, env.Cache, env.ModuleCache, env.GoRoot} {
		if !filepath.IsAbs(path) || !bounded(path, 4096) || hasParent(path) {
			return nil, failure("invalidArgument", "Reviewed absolute cache, home and toolchain paths are required")
		}
	}
	p := &GoplsPlan{root: root, availability: "available"}
	unavailable := func(state, notice string) (*GoplsPlan, error) {
		p.availability, p.notice, p.partial = state, notice, true
		return p, nil
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return unavailable("unsupported", "Go language server support has not been verified on this platform.")
	}
	executable := firstExecutable(reviewedPath, "gopls")
	if executable == "" {
		return unavailable("missingTool", "The gopls language server is not installed.")
	}
	if !isExecutable(filepath.Join(env.GoRoot, "bin", "go")) {
		return unavailable("missingTool", "The Go toolchain required by gopls is not installed.")
	}
	versionOf := env.versionOf
	if versionOf == nil {
		versionOf = buildVersion
	}
	module, version, err := versionOf(executable)
	if err != nil || module != goplsModulePath {
		return unavailable("unsupported", "The gopls version could not be verified.")
	}
	if !slices.Contains(supportedGoplsVersions, version) {
		return unavailable("unsupported", "This gopls version is not supported.")
	}
	p.version = version
	w, err := discoverGoWorkspace(root)
	if err != nil {
		return nil, err
	}
	p.modules, p.partial = w.modules, w.partial
	if len(w.modules) == 0 {
		return unavailable("unsupported", "No contained Go module was found.")
	}
	if w.outside {
		p.notice = OutsideRootNotice
	}
	p.config = ServerConfig{
		ProviderID: goplsProviderID,
		Executable: executable,
		Args:       []string{"serve"},
		Env: []string{
			"HOME=" + env.Home, "GOCACHE=" + env.Cache, "GOMODCACHE=" + env.ModuleCache, "GOROOT=" + env.GoRoot,
			"PATH=" + filepath.Join(env.GoRoot, "bin") + ":/usr/bin:/bin", "GOENV=off", "GOWORK=off", "GOFLAGS=",
			"GOOS=darwin", "GOARCH=arm64",
		},
		AdapterPolicy: goplsPolicy,
		ConfigPaths:   w.configs,
	}
	if _, err := Fingerprint(root, []ToolInput{p.config.Tool()}); err != nil {
		return nil, err
	}
	return p, nil
}

func buildVersion(path string) (string, string, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return info.Main.Path, info.Main.Version, nil
}

func isExecutable(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	st, err := os.Stat(real)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

func firstExecutable(dirs []string, name string) string {
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if err == nil && isExecutable(real) {
			return real
		}
	}
	return ""
}

func (p *GoplsPlan) Availability() string { return p.availability }
func (p *GoplsPlan) Version() string      { return p.version }
func (p *GoplsPlan) Modules() []string    { return slices.Clone(p.modules) }

// Server is the fixed server identity, only when the plan is available.
func (p *GoplsPlan) Server() (ServerConfig, bool) { return p.config, p.availability == "available" }

// Provider describes honest opened-documents coverage. It is data only: the
// manager does not yet consume it (no production route exists), and neither
// successful initialization nor an empty publication is reported as clean Go.
func (p *GoplsPlan) Provider() ProviderState {
	notice := goplsOpenedNotic
	if p.notice != "" {
		notice = p.notice + " " + goplsOpenedNotic
	}
	roots := slices.Clone(p.modules)
	if roots == nil {
		roots = []string{}
	}
	return ProviderState{
		ProviderID: goplsProviderID, Availability: p.availability, Phase: "idle", Freshness: "uncertain",
		Coverage: Coverage{Kind: "openedDocuments", Roots: roots, Partial: p.partial, ExternalChanges: CoverageExternalChanges},
		Notice:   shorten(notice, 2048),
	}
}

// Documents resolves only .go files inside the root to this server. It never
// launches anything.
func (p *GoplsPlan) Documents(sessions *SessionManager) SessionDocuments {
	cfg, ok := p.Server()
	return SessionDocuments{Sessions: sessions, Resolve: func(root AuthorizedRoot, path string) (DocumentServer, bool) {
		return DocumentServer{Config: cfg, LanguageID: "go"}, ok && root.Project() == p.root.Project() && strings.HasSuffix(path, ".go")
	}}
}

// ---- contained workspace discovery ------------------------------------------

type goWorkspace struct {
	modules, configs []string
	partial, outside bool
}

// discoverGoWorkspace walks only inside the authorized root with bounded,
// rooted reads. The selected directory is never promoted to a parent module,
// repository or go.work. A go.work entry (or local replace) that leaves the
// root is reported as bounded partial coverage; GOWORK=off keeps gopls from
// loading it, so no extra access is granted.
func discoverGoWorkspace(root AuthorizedRoot) (goWorkspace, error) {
	var w goWorkspace
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return w, failure("unauthorized", "Go discovery root cannot be opened")
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
		ds, err := f.ReadDir(maxGoplsEntries + 1 - entries)
		if err != nil && !errors.Is(err, io.EOF) {
			w.partial = true
			return
		}
		slices.SortFunc(ds, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, d := range ds {
			entries++
			if entries > maxGoplsEntries {
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
				if n == "vendor" || n == "node_modules" || n == "testdata" || strings.HasPrefix(n, ".") || strings.HasPrefix(n, "_") || depth >= 32 {
					w.partial = true
					continue
				}
				walk(rel, depth+1)
				continue
			}
			switch d.Name() {
			case "go.mod", "go.sum", "go.work":
			default:
				continue
			}
			if len(w.configs) >= maxGoplsConfigs {
				w.partial = true
				continue
			}
			w.configs = append(w.configs, portable)
			if d.Name() == "go.sum" {
				continue
			}
			data, err := rootedRead(root, portable, 1<<20)
			if err != nil {
				w.partial = true
				continue
			}
			base := filepath.Dir(rel)
			if d.Name() == "go.work" {
				wf, err := modfile.ParseWork(portable, data, nil)
				if err != nil {
					w.partial = true
					continue
				}
				for _, u := range wf.Use {
					if !localInside(base, u.Path) {
						w.partial, w.outside = true, true
					}
				}
				continue
			}
			mf, err := modfile.Parse(portable, data, nil)
			if err != nil {
				w.partial = true
				continue
			}
			for _, rep := range mf.Replace {
				if rep.New.Version == "" && !localInside(base, rep.New.Path) {
					w.partial, w.outside = true, true
				}
			}
			if len(w.modules) >= maxGoplsModules {
				w.partial = true
				continue
			}
			module := filepath.ToSlash(base)
			if module == "." {
				module = ""
			}
			w.modules = append(w.modules, module)
		}
	}
	walk(".", 0)
	slices.Sort(w.modules)
	slices.Sort(w.configs)
	return w, nil
}

// localInside reports whether a go.work/replace path, relative to its directory
// inside the root, stays inside the root. Absolute paths never do.
func localInside(dir, target string) bool {
	if target == "" || filepath.IsAbs(filepath.FromSlash(target)) || strings.ContainsRune(target, 0) {
		return false
	}
	joined := filepath.Join(dir, filepath.FromSlash(target))
	return filepath.IsLocal(joined) || joined == "."
}

// ---- publishDiagnostics normalization ---------------------------------------

var (
	errGoplsOutside = errors.New("gopls: publication outside authorized root")
	errGoplsInvalid = errors.New("gopls: invalid publication")
	errGoplsUnknown = errors.New("gopls: publication for an unresolvable file")
)

type goplsPublication struct {
	Path        string
	Version     *int64
	Diagnostics []Diagnostic
	Dropped     int
	Truncated   bool
	Partial     bool
}

// normalizeGoplsPublication validates one textDocument/publishDiagnostics
// payload against the authorized root. Ranges are already zero-based UTF-16
// with exclusive ends (negotiated), so no coordinate conversion occurs; the
// manager proves them against the displayed text. Items that cannot be
// represented honestly are dropped and counted; bad ranges become file-level
// findings with an invalidRange issue. No raw server text other than the
// bounded message is kept.
func normalizeGoplsPublication(root AuthorizedRoot, params json.RawMessage) (goplsPublication, error) {
	return normalizeLSPPublication(root, params, goplsProviderID)
}

// normalizeLSPPublication is the provider-agnostic body shared with the
// TypeScript adapter (CD-23); providerID only prefixes diagnostic attribution.
func normalizeLSPPublication(root AuthorizedRoot, params json.RawMessage, providerID string) (goplsPublication, error) {
	var out goplsPublication
	if len(params) == 0 || len(params) > maxGoplsParams {
		return out, errGoplsInvalid
	}
	var in struct {
		URI         string          `json:"uri"`
		Version     json.RawMessage `json:"version"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if json.Unmarshal(params, &in) != nil || in.Diagnostics == nil {
		return out, errGoplsInvalid
	}
	if !insideRoot(root, in.URI) {
		// A well-formed local file URI that leaves the root is provider status,
		// never a link; anything else is a malformed publication.
		if u, perr := url.Parse(in.URI); perr == nil && bounded(in.URI, maxScopeURI) && u.Scheme == "file" && u.Host == "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == "" && strings.HasPrefix(u.Path, "/") {
			return out, errGoplsOutside
		}
		return out, errGoplsInvalid
	}
	path, err := root.ResolveURI(root.Project().HostID, in.URI)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == "unauthorized" {
			return out, errGoplsOutside
		}
		if errors.As(err, &e) && e.Code == "notFound" {
			return out, errGoplsUnknown // no such file: nothing to attribute or link
		}
		return out, errGoplsInvalid
	}
	out.Path = path
	if v := string(in.Version); v != "" && v != "null" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > maxSequence {
			return out, errGoplsInvalid
		}
		out.Version = &n
	}
	decoder := json.NewDecoder(bytes.NewReader(in.Diagnostics))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return out, errGoplsInvalid
	}
	selected := boundedDiagnostics{}
	for decoder.More() {
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return out, errGoplsInvalid
		}
		d, ok := normalizeLSPDiagnostic(path, raw, providerID)
		if !ok {
			out.Dropped++
			continue
		}
		if d.partial {
			out.Partial = true
		}
		out.Truncated = out.Truncated || d.truncated
		selected.add(d, ManagerLimits{}.defaults())
	}
	out.Diagnostics = selected.results()
	out.Truncated = out.Truncated || selected.truncated
	return out, nil
}

func normalizeLSPDiagnostic(path string, raw json.RawMessage, providerID string) (Diagnostic, bool) {
	var in struct {
		Range    json.RawMessage `json:"range"`
		Severity json.RawMessage `json:"severity"`
		Code     json.RawMessage `json:"code"`
		Source   string          `json:"source"`
		Message  string          `json:"message"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Message == "" || !utf8.ValidString(in.Message) {
		return Diagnostic{}, false
	}
	d := Diagnostic{partial: len(in.Message) > 8192, truncated: len(in.Message) > 8192, Path: path, Message: shorten(in.Message, 8192), Severity: "warning", Freshness: "uncertain", Basis: SavedBasis{Kind: "document"}}
	if s := string(in.Severity); s != "" && s != "null" {
		switch s {
		case "1":
			d.Severity = "error"
		case "2":
			d.Severity = "warning"
		case "3":
			d.Severity = "information"
		case "4":
			d.Severity = "hint"
		default:
			return Diagnostic{}, false
		}
	}
	// gopls attribution is kept distinct from go vet and other providers.
	d.Source = providerID
	if in.Source != "" && utf8.ValidString(in.Source) && len(in.Source) <= 200 {
		d.Source = providerID + "/" + in.Source
	}
	if in.Source != "" && (!utf8.ValidString(in.Source) || len(in.Source) > 200) {
		d.partial = true
	}
	if c := string(in.Code); c != "" && c != "null" {
		var s string
		switch {
		case json.Unmarshal(in.Code, &s) == nil:
		case c[0] != '"' && c[0] != '{' && c[0] != '[' && c != "true" && c != "false":
			s = c
		}
		if bounded(s, 256) {
			d.Code = s
		} else {
			d.partial = true
		}
	}
	if len(in.Range) > 0 && string(in.Range) != "null" {
		if r, ok := parseLSPRange(in.Range); ok {
			d.Range = &r
		} else {
			d.LocationIssue = "invalidRange"
			d.partial = true
		}
	}
	return d, true
}

func parseLSPRange(raw json.RawMessage) (Range, bool) {
	var in struct {
		Start, End struct {
			Line, Character json.RawMessage
		}
	}
	if json.Unmarshal(raw, &in) != nil {
		return Range{}, false
	}
	vals := [4]int{}
	for i, v := range []json.RawMessage{in.Start.Line, in.Start.Character, in.End.Line, in.End.Character} {
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil || n < 0 || n > maxSequence || int64(int(n)) != n {
			return Range{}, false
		}
		vals[i] = int(n)
	}
	r := Range{Start: Position{vals[0], vals[1]}, End: Position{vals[2], vals[3]}}
	return r, validRange(&r)
}

// ---- notification bridge -----------------------------------------------------

// goplsSink is the Manager seam (PublishPathDiagnostics).
type goplsSink interface {
	PublishPathDiagnostics(project Project, providerID, generation, path string, version *int64, diagnostics []Diagnostic) error
}

// GoplsBridge turns session notifications into document-set replacements. Set
// SessionOptions.Notifications to Handle. It publishes nothing for unopened,
// closed or out-of-root files and never creates document state. Notice receives
// bounded, path-free provider status.
type GoplsBridge struct {
	root   AuthorizedRoot
	sink   goplsSink
	Notice func(string)
}

func NewGoplsBridge(root AuthorizedRoot, m *Manager) *GoplsBridge {
	return &GoplsBridge{root: root, sink: m}
}

func (b *GoplsBridge) note(s string) {
	if b.Notice != nil {
		b.Notice(s)
	}
}

// Handle must be quick and runs on the session goroutine.
func (b *GoplsBridge) Handle(n SessionNotification) {
	if n.Project != b.root.Project() || n.ProviderID != goplsProviderID || n.Method != "textDocument/publishDiagnostics" {
		return
	}
	pub, err := normalizeGoplsPublication(b.root, n.Params)
	switch {
	case errors.Is(err, errGoplsOutside):
		b.note(OutsideRootNotice)
		return
	case errors.Is(err, errGoplsUnknown):
		return
	case err != nil:
		b.note(partialGoNotice)
		return
	}
	if err := publishLivePublication(b.sink, n, pub); err != nil {
		// A closed/never-opened document or a late generation is expected
		// and must stay silent; anything else is partial coverage.
		var e *Error
		if !errors.As(err, &e) || (e.Code != "notFound" && e.Code != "staleContext" && e.Code != "unsupported") {
			b.note(partialGoNotice)
		}
		return
	}
	if pub.Dropped > 0 || pub.Partial || pub.Truncated {
		b.note(partialGoNotice)
	}
}
