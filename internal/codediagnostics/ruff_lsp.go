package codediagnostics

// Python adapter: Ruff's built-in language server.
//
// Like ts_lsp.go this file owns only contained project discovery, a fixed-argv
// server identity with independent availability, extension ownership and
// publishDiagnostics bridging (normalization is shared with CD-22). Ruff is a
// native binary that never imports or runs project code. Nothing here installs,
// downloads or executes the tool. Any native ruff binary is accepted, as
// editors do (docs/plan/code-diagnostics-zed-model.md); the version is shown
// when a wheel's METADATA and RECORD identify it. The spike evidence and its
// limits are recorded in docs/plan/code-diagnostics-python.md and
// testdata/ruff/evidence.json. Fake-server tests in ruff_lsp_test.go are
// SYNTHETIC; they prove this adapter's handling, not the behavior of any real
// server.

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	ruffProviderID = "ruff"
	// ruffPolicy binds the launch shape: a native binary runs "ruff server"
	// directly (no shell, uvx or pipx run). The binary's bytes are bound by
	// the approval fingerprint, so an upgrade asks for approval again.
	ruffPolicy        = "ruff-v2:any-version:" + runtime.GOOS + "/" + runtime.GOARCH + ":native:push-only:no-edits:opened-documents"
	partialRuffNotice = "Some Python diagnostics could not be verified; results are partial."
	ruffOpenedNotice  = "Python diagnostics cover opened documents only; unopened files are not continuously synchronized."
	ruffScopeNotice   = "Ruff reports lint and syntax problems, not type errors. Ruff settings outside the project (parent directories, extend targets, your user configuration) still apply but are not part of approval."
	ruffInstallHint   = "The app downloads Ruff into its own folder, or install it yourself (for example uv tool install ruff), then refresh."
	maxRuffDistInfo   = 64
)

// ruffProjectMarkers: Ruff's configuration files are bound into approval. Any
// Python source or configuration enables Ruff for the whole project, as in
// editors; a project needs no pyproject.toml. Virtual environments and other
// dependency trees are skipped silently.
var ruffProjectMarkers = projectMarkers{
	configs: []string{"pyproject.toml", "ruff.toml", ".ruff.toml"},
	skipDir: []string{".venv", "venv", "__pycache__", "site-packages", "node_modules", ".tox", ".nox"},
	sources: ruffDocumentExtensions,
}

// RuffEnvironment is reviewed host metadata, not the inherited environment.
type RuffEnvironment struct {
	Home string
}

// RuffPlan is the discovery result. Availability is independent of every other
// provider.
type RuffPlan struct {
	root         AuthorizedRoot
	availability string
	notice       string
	config       ServerConfig
	version      string
	projects     []string
	partial      bool
	projectLocal bool
}

// DiscoverRuff inspects metadata only. Errors are reserved for an invalid host
// description or a root that is no longer authorized.
func DiscoverRuff(root AuthorizedRoot, reviewedPath []string, env RuffEnvironment) (*RuffPlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(env.Home) || !bounded(env.Home, 4096) || hasParent(env.Home) {
		return nil, failure("invalidArgument", "A reviewed absolute home path is required")
	}
	p := &RuffPlan{root: root, availability: "available", notice: ruffScopeNotice}
	unavailable := func(state, notice string) (*RuffPlan, error) {
		p.availability, p.notice, p.partial = state, notice, true
		return p, nil
	}
	// Python first: a root without Python stays a muted "unsupported" rather
	// than asking for an install it does not need.
	w, err := discoverProjectDirs(root, ruffProjectMarkers)
	if err != nil {
		return nil, err
	}
	p.partial = w.partial
	if !w.sources && len(w.configs) == 0 {
		return unavailable("unsupported", "No Python file was found in this project.")
	}
	p.projects = []string{""}
	bin, version, local, state := resolveRuff(root, reviewedPath)
	switch state {
	case "missing":
		return unavailable("missingTool", "Ruff is not installed. "+ruffInstallHint)
	case "foreign":
		return unavailable("unsupported", "The ruff command found is a script or version-manager shim, not the Ruff program, so it is not used. "+ruffInstallHint)
	}
	p.version, p.projectLocal = version, local
	p.config = ServerConfig{
		ProviderID:    ruffProviderID,
		Executable:    bin,
		Args:          []string{"server"},
		Env:           []string{"HOME=" + env.Home, "PATH=/usr/bin:/bin"},
		AdapterPolicy: ruffPolicy,
		ConfigPaths:   w.configs,
	}
	if _, err := Fingerprint(root, p.config.Tools()); err != nil {
		if err := root.Revalidate(); err != nil {
			return nil, err
		}
		p.config = ServerConfig{}
		return unavailable("unsupported", "The Ruff binary could not be identified for approval.")
	}
	return p, nil
}

// resolveRuff finds a native ruff binary. The contained project virtual
// environments (.venv, venv: project content covered by CD-02 trust) are
// tried first, then reviewed absolute PATH directories, which include the
// app-managed copy last. A script or shim (mise links to its own binary) is
// skipped and reported as foreign; nothing is ever executed. The version is
// read from wheel metadata when the binary came from a wheel, else empty.
// state is "", "missing" or "foreign".
func resolveRuff(root AuthorizedRoot, reviewedPath []string) (bin, version string, local bool, state string) {
	state = "missing"
	try := func(link string) string {
		real, err := filepath.EvalSymlinks(link)
		if err != nil {
			return ""
		}
		if filepath.Base(real) != "ruff" || !isExecutable(real) || isScript(real) {
			state = "foreign"
			return ""
		}
		return real
	}
	base := root.Project().Root
	for _, venv := range []string{".venv", "venv"} {
		if real := try(filepath.Join(base, venv, "bin", "ruff")); real != "" {
			if rel, err := filepath.Rel(base, real); err == nil && filepath.IsLocal(rel) {
				v, _ := inspectRuffWheel(real)
				return real, v, true, ""
			}
		}
	}
	for _, dir := range reviewedPath {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		if real := try(filepath.Join(dir, "ruff")); real != "" {
			v, _ := inspectRuffWheel(real)
			return real, v, false, ""
		}
	}
	return "", "", false, state
}

// inspectRuffWheel identifies real as the bin/ruff of an installed ruff
// wheel: <prefix>/lib/python*/site-packages (or the macOS user-site
// lib/python/site-packages) holds ruff-*.dist-info whose RECORD lists this
// exact file with a matching sha256. uv tool, pipx, pip and virtual
// environments share that layout. The version is METADATA's. Metadata only.
func inspectRuffWheel(real string) (version, state string) {
	prefix := filepath.Dir(filepath.Dir(real))
	lib := filepath.Join(prefix, "lib")
	pys, err := os.ReadDir(lib)
	if err != nil {
		return "", "foreign"
	}
	state = "foreign"
	seen := 0
	for _, py := range pys {
		if !py.IsDir() || !strings.HasPrefix(py.Name(), "python") {
			continue
		}
		site := filepath.Join(lib, py.Name(), "site-packages")
		dists, err := os.ReadDir(site)
		if err != nil {
			continue
		}
		for _, d := range dists {
			if !d.IsDir() || !strings.HasPrefix(d.Name(), "ruff-") || !strings.HasSuffix(d.Name(), ".dist-info") {
				continue
			}
			if seen++; seen > maxRuffDistInfo {
				return "", state
			}
			dist := filepath.Join(site, d.Name())
			hash, listed := ruffRecordHash(site, dist, real)
			if !listed {
				continue
			}
			if hash == "" || hash != fileSHA256(real) {
				state = "modified"
				continue
			}
			name, v := wheelMetadata(filepath.Join(dist, "METADATA"))
			if !strings.EqualFold(name, "ruff") || !plainVersion(v) {
				continue
			}
			return v, ""
		}
	}
	return "", state
}

// ruffRecordHash returns the urlsafe base64 sha256 RECORD lists for real, and
// whether RECORD lists it at all. RECORD paths are relative to site-packages.
func ruffRecordHash(site, dist, real string) (string, bool) {
	f, err := os.Open(filepath.Join(dist, "RECORD"))
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(io.LimitReader(f, 4<<20))
	r.FieldsPerRecord = -1
	for {
		rec, err := r.Read()
		if err != nil {
			return "", false
		}
		if len(rec) < 2 || path.Base(rec[0]) != "ruff" {
			continue
		}
		listed, err := filepath.EvalSymlinks(filepath.Join(site, filepath.FromSlash(rec[0])))
		if err != nil || listed != real {
			continue
		}
		hash, ok := strings.CutPrefix(rec[1], "sha256=")
		if !ok {
			return "", true
		}
		return hash, true
	}
}

func fileSHA256(file string) string {
	f, err := os.Open(file)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if n, err := io.Copy(h, io.LimitReader(f, maxIdentityFile+1)); err != nil || n > maxIdentityFile {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// plainVersion admits a displayable version string only.
func plainVersion(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '.' || c == '+' || c == '-') {
			return false
		}
	}
	return true
}

// wheelMetadata reads Name and Version from the METADATA header block.
func wheelMetadata(file string) (name, version string) {
	f, err := os.Open(file)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for s.Scan() {
		line := s.Text()
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Name: "); ok {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "Version: "); ok {
			version = strings.TrimSpace(v)
		}
	}
	return name, version
}

// isScript reports a "#!" interpreter script (a shim or console-script
// wrapper rather than the native binary).
func isScript(file string) bool {
	f, err := os.Open(file)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 2)
	n, _ := io.ReadFull(f, head)
	return n == 2 && string(head) == "#!"
}

func (p *RuffPlan) Availability() string { return p.availability }
func (p *RuffPlan) Version() string      { return p.version }
func (p *RuffPlan) Projects() []string   { return slices.Clone(p.projects) }

// ProjectLocal reports that the binary is executable project content.
func (p *RuffPlan) ProjectLocal() bool { return p.projectLocal }

// Server is the fixed server identity, only when the plan is available.
func (p *RuffPlan) Server() (ServerConfig, bool) { return p.config, p.availability == "available" }

// Provider describes honest opened-documents coverage for DiscoverLocalLive. An
// empty push means Ruff found no lint or syntax problem, never "type-clean".
func (p *RuffPlan) Provider() ProviderState {
	notice := ruffOpenedNotice
	if p.notice != "" {
		notice = p.notice + " " + ruffOpenedNotice
	}
	roots := slices.Clone(p.projects)
	if roots == nil {
		roots = []string{}
	}
	return ProviderState{
		ProviderID: ruffProviderID, Availability: p.availability, Phase: "idle", Freshness: "uncertain",
		Coverage:           Coverage{Kind: "openedDocuments", Roots: roots, Partial: p.partial, ExternalChanges: CoverageExternalChanges},
		Notice:             shorten(notice, 2048),
		DocumentExtensions: slices.Clone(ruffDocumentExtensions),
	}
}

// covers reports whether a contained project directory is an ancestor of file.
func (p *RuffPlan) covers(file string) bool { return coversDir(p.projects, file) }

// PythonLanguageID is the extension ownership rule. Notebooks are not owned.
func PythonLanguageID(file string) (string, bool) {
	switch strings.ToLower(path.Ext(file)) {
	case ".py", ".pyi":
		return "python", true
	}
	return "", false
}

var ruffDocumentExtensions = []string{".py", ".pyi"}

// RuffBridge turns session notifications into document-set replacements with
// the "ruff" attribution prefix. Ruff pushes carry the document version, so
// matching results are current; absence of a push is not "clean".
type RuffBridge struct {
	root   AuthorizedRoot
	sink   goplsSink
	Notice func(string)
}

func NewRuffBridge(root AuthorizedRoot, m *Manager) *RuffBridge {
	return &RuffBridge{root: root, sink: m}
}

// Handle must be quick and runs on the session goroutine.
func (b *RuffBridge) Handle(n SessionNotification) {
	handleOwnedPush(b.root, b.sink, n, ruffProviderID, partialRuffNotice, func(file string) bool {
		_, owned := PythonLanguageID(file)
		return owned
	}, func(s string) {
		if b.Notice != nil {
			b.Notice(s)
		}
	})
}
