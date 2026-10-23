package codediagnostics

// Python adapter: Ruff's built-in language server.
//
// Like ts_lsp.go this file owns only contained project discovery, a fixed-argv
// server identity with independent availability, extension ownership and
// publishDiagnostics bridging (normalization is shared with CD-22). Ruff is a
// native binary that never imports or runs project code. Nothing here installs,
// downloads or executes the tool to learn its version: the version comes from
// the wheel's installed METADATA, and the binary is accepted only when the
// wheel's RECORD lists it with a matching hash. The spike evidence and its
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
	// ruffPolicy binds the verified version and launch shape: the wheel's own
	// native binary runs "ruff server" directly (no shell, uvx or pipx run).
	ruffPolicy        = "ruff-v1:0.15.20:darwin/arm64:native-wheel:push-only:no-edits:opened-documents"
	ruffVersion       = "0.15.20"
	partialRuffNotice = "Some Python diagnostics could not be verified; results are partial."
	ruffOpenedNotice  = "Python diagnostics cover opened documents only; unopened files are not continuously synchronized."
	ruffScopeNotice   = "Ruff reports lint and syntax problems, not type errors. Ruff settings outside the project (parent directories, extend targets, your user configuration) still apply but are not part of approval."
	ruffInstallHint   = "Install it yourself with: uv tool install ruff==0.15.20 (or pipx install ruff==0.15.20), then refresh. Nothing is installed automatically."
	maxRuffDistInfo   = 64
)

// ruffProjectMarkers: Ruff's configuration files are bound into approval;
// common Python project files only mark a project. Virtual environments and
// other dependency trees are skipped silently.
var ruffProjectMarkers = projectMarkers{
	configs: []string{"pyproject.toml", "ruff.toml", ".ruff.toml"},
	plain:   []string{"setup.py", "setup.cfg", "requirements.txt"},
	skipDir: []string{".venv", "venv", "__pycache__", "site-packages", "node_modules", ".tox", ".nox"},
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
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return unavailable("unsupported", "Python language server support has not been verified on this platform.")
	}
	// Projects first: a root without Python stays a muted "unsupported" rather
	// than asking for an install it does not need.
	w, err := discoverProjectDirs(root, ruffProjectMarkers)
	if err != nil {
		return nil, err
	}
	p.projects, p.partial = w.packages, w.partial
	if len(w.packages) == 0 {
		return unavailable("unsupported", "No contained Python project (pyproject.toml, ruff.toml, setup.py, setup.cfg or requirements.txt) was found.")
	}
	bin, version, local, state := resolveRuff(root, reviewedPath)
	switch state {
	case "missing":
		return unavailable("missingTool", "Ruff is not installed. "+ruffInstallHint)
	case "foreign":
		return unavailable("unsupported", "The ruff command found is not a verifiable Python wheel install (for example a standalone, Homebrew or version-manager copy), so it is not used. "+ruffInstallHint)
	case "modified":
		return unavailable("unsupported", "The ruff binary does not match its installed package record. "+ruffInstallHint)
	}
	if version != ruffVersion {
		return unavailable("unsupported", "Found Ruff "+versionText(version)+"; only Ruff "+ruffVersion+" is verified. "+ruffInstallHint)
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

// resolveRuff finds a ruff binary installed from a wheel. The contained
// project virtual environment (.venv, project content covered by CD-02 trust)
// is tried first, then reviewed absolute PATH directories. A command that is
// not a verifiable wheel install is skipped and reported as foreign; nothing
// is ever executed. state is "", "missing", "foreign" or "modified".
func resolveRuff(root AuthorizedRoot, reviewedPath []string) (bin, version string, local bool, state string) {
	state = "missing"
	try := func(link string) (string, string, bool) {
		real, err := filepath.EvalSymlinks(link)
		if err != nil || filepath.Base(real) != "ruff" || !isExecutable(real) || isScript(real) {
			if err == nil && state == "missing" {
				state = "foreign"
			}
			return "", "", false
		}
		version, s := inspectRuffWheel(real)
		switch s {
		case "":
			return real, version, true
		case "modified":
			state = s
		default:
			if state == "missing" {
				state = s
			}
		}
		return "", "", false
	}
	base := root.Project().Root
	if real, v, ok := try(filepath.Join(base, ".venv", "bin", "ruff")); ok {
		if rel, err := filepath.Rel(base, real); err == nil && filepath.IsLocal(rel) {
			return real, v, true, ""
		}
	}
	for _, dir := range reviewedPath {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		if real, v, ok := try(filepath.Join(dir, "ruff")); ok {
			return real, v, false, ""
		}
	}
	return "", "", false, state
}

// inspectRuffWheel accepts real only when it is the bin/ruff of an installed
// ruff wheel: <prefix>/lib/python*/site-packages (or the macOS user-site
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
			if !strings.EqualFold(name, "ruff") || v == "" {
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
