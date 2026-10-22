package codediagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// SveltePlan supports explicit local tsconfig packages and the CD-04 runtime tuple.
// Discovery never evaluates configuration or runs even a version probe.
type SveltePlan struct {
	root              AuthorizedRoot
	node, home        string
	packages, configs []string
	partial           bool
	tools             []ToolInput
	commands          []Command
}

func (p *SveltePlan) Tools() []ToolInput {
	out := slices.Clone(p.tools)
	for i := range out {
		out[i].ConfigPaths = slices.Clone(out[i].ConfigPaths)
	}
	return out
}

func DiscoverSvelte(root AuthorizedRoot, reviewedPath []string, home string) (*SveltePlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, failure("unsupported", "Svelte runtime platform has not been verified")
	}
	if !filepath.IsAbs(home) || hasParent(home) || !bounded(home, 4096) {
		return nil, failure("invalidArgument", "Reviewed absolute home is required")
	}
	p := &SveltePlan{root: root, home: home}
	for _, dir := range reviewedPath {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		path, err := filepath.EvalSymlinks(filepath.Join(dir, "node"))
		if err != nil {
			continue
		}
		st, err := os.Stat(path)
		if err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			p.node = path
			break
		}
	}
	if p.node == "" {
		return nil, failure("notFound", "Installed Node runtime is unavailable")
	}
	if err := p.discover(); err != nil {
		return nil, err
	}
	if len(p.packages) == 0 {
		return nil, failure("unsupported", "No supported contained Svelte package with local tsconfig was found")
	}
	env := []string{"HOME=" + home, "PATH=" + filepath.Dir(p.node) + ":/usr/bin:/bin", "NO_COLOR=1"}
	p.commands = append(p.commands, Command{Args: []string{"--version"}})
	for _, dir := range p.packages {
		entry := filepath.Join(root.Project().Root, filepath.FromSlash(dir), "node_modules/svelte-check/bin/svelte-check")
		p.commands = append(p.commands, Command{Directory: dir, Args: []string{entry, "--version"}}, Command{Directory: dir, Args: []string{entry, "--tsconfig", "./tsconfig.json", "--output", "machine-verbose"}, AcceptedExits: []int{0, 1}})
	}
	for i := range p.commands {
		c := &p.commands[i]
		c.ProviderID = fmt.Sprintf("svelte-check-%d", i)
		c.Executable = p.node
		c.Env = env
		c.AdapterPolicy = "svelte-check-v1:4.7.6:node22.23.2:svelte5.57.1:ts5.9.3:darwin/arm64:local-json-tsconfig:saved-uncertain"
		if len(c.AcceptedExits) == 0 {
			c.AcceptedExits = []int{0}
		}
		p.tools = append(p.tools, ToolInput{ProviderID: c.ProviderID, Executable: p.node, ConfigPaths: slices.Clone(p.configs), Policy: c.Policy()})
	}
	if _, err := Fingerprint(root, p.tools); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *SveltePlan) discover() error {
	return walkContained(p.root, func(rel string) error {
		base := filepath.Base(rel)
		if base == "package.json" {
			data, err := rootedRead(p.root, rel, 1<<20)
			if err != nil {
				return err
			}
			var manifest struct{ Dependencies, DevDependencies map[string]string }
			if json.Unmarshal(data, &manifest) != nil {
				return failure("unsupported", "Package metadata cannot be verified")
			}
			if manifest.Dependencies["svelte"] == "" && manifest.DevDependencies["svelte"] == "" {
				p.configs = append(p.configs, rel)
				return nil
			}
			dir := filepath.ToSlash(filepath.Dir(rel))
			if dir == "." {
				dir = ""
			}
			config := filepath.ToSlash(filepath.Join(dir, "tsconfig.json"))
			text, err := rootedRead(p.root, config, 1<<20)
			if err != nil {
				p.partial = true
				return nil //nolint:nilerr // Missing local config is explicitly omitted partial scope.
			}
			// Narrow JSON-only policy: no inherited config, project references, plugins,
			// or outside include/root paths. JSONC/broader monorepos need separate evidence.
			var ts map[string]any
			if json.Unmarshal(text, &ts) != nil {
				return failure("unsupported", "Only explicit local JSON tsconfig is supported")
			}
			if !localTSConfig(ts) {
				return failure("unsupported", "Inherited or external TypeScript configuration is unsupported")
			}
			if len(p.packages) >= 12 {
				return failure("limitExceeded", "Svelte package limit exceeded")
			}
			// A local Svelte config prevents implicit parent configuration lookup.
			if _, err := rootedRead(p.root, filepath.ToSlash(filepath.Join(dir, "svelte.config.js")), 1<<20); err != nil {
				return failure("unsupported", "An explicit local svelte.config.js is required")
			}
			p.packages = append(p.packages, dir)
			for _, name := range []string{"svelte-check", "svelte", "typescript"} {
				path := filepath.ToSlash(filepath.Join(dir, "node_modules", name, "package.json"))
				b, err := rootedRead(p.root, path, 1<<20)
				if err != nil {
					return failure("notFound", "Contained Svelte checker or runtime dependencies are unavailable")
				}
				var installed struct{ Version string }
				if json.Unmarshal(b, &installed) != nil {
					return failure("unsupported", "Installed dependency metadata is invalid")
				}
				want := map[string]string{"svelte-check": "4.7.6", "svelte": "5.57.1", "typescript": "5.9.3"}[name]
				if installed.Version != want {
					return failure("unsupported", "Installed Svelte runtime tuple has not been verified")
				}
				p.configs = append(p.configs, path)
			}
			for _, name := range []string{"svelte-check/bin/svelte-check", "svelte-check/bin/ts-version-check.js", "svelte-check/dist/src/index.js", "svelte/compiler/index.js", "typescript/lib/typescript.js", "@sveltejs/load-config/package.json", "@sveltejs/load-config/dist/src/index.js", "@jridgewell/trace-mapping/package.json", "@jridgewell/trace-mapping/dist/trace-mapping.umd.js", "chokidar/package.json", "chokidar/index.js", "picocolors/package.json", "picocolors/picocolors.js", "sade/package.json", "sade/lib/index.js"} {
				path := filepath.ToSlash(filepath.Join(dir, "node_modules", name))
				if _, err := p.root.ResolvePath(p.root.Project().HostID, path); err != nil {
					return failure("notFound", "Contained checker implementation is unavailable")
				}
				p.configs = append(p.configs, path)
			}
		}
		// Bind all discovered project manifests, locks and executable configs, including
		// nested configs. Creation/removal is detected by re-enumeration around runs.
		if base == "package.json" || strings.HasSuffix(base, "lock.json") || base == "pnpm-lock.yaml" || base == "yarn.lock" || strings.Contains(base, "config.") {
			p.configs = append(p.configs, rel)
			if len(p.configs) > 128 {
				return failure("limitExceeded", "Svelte configuration identity limit exceeded")
			}
		}
		return nil
	}, &p.partial)
}

func localTSConfig(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			if k == "extends" || k == "references" || k == "plugins" {
				return false
			}
			if !localTSConfig(v) {
				return false
			}
		}
	case []any:
		for _, v := range x {
			if !localTSConfig(v) {
				return false
			}
		}
	case string:
		if strings.Contains(x, "..") || filepath.IsAbs(x) || strings.ContainsAny(x, `\:`) {
			return false
		}
	}
	return true
}

// Shared bounded metadata traversal. Symlinks are never followed during discovery;
// ignored/capped scopes are explicitly partial, not a whole-repository claim.
func walkContained(root AuthorizedRoot, visit func(string) error, partial *bool) error {
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	entries := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		f, err := r.Open(dir)
		if err != nil {
			*partial = true
			return nil
		}
		defer func() { _ = f.Close() }()
		ds, err := f.ReadDir(4097 - entries)
		if err != nil && !errors.Is(err, io.EOF) {
			*partial = true
			return nil
		}
		slices.SortFunc(ds, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, d := range ds {
			entries++
			if entries > 4096 {
				*partial = true
				return nil
			}
			rel := filepath.ToSlash(filepath.Join(dir, d.Name()))
			if ValidatePath(rel) != nil || d.Type()&os.ModeSymlink != 0 {
				*partial = true
				continue
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "vendor" || d.Name() == "build" || d.Name() == "dist" || strings.HasPrefix(d.Name(), ".") {
					*partial = true
					continue
				}
				if depth >= 32 {
					*partial = true
					continue
				}
				if err := walk(rel, depth+1); err != nil {
					return err
				}
			} else if err := visit(rel); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(".", 0)
}

func (p *SveltePlan) Run(ctx context.Context, s *Supervisor, runID string) CheckerResult {
	return p.RunApproved(ctx, s, runID, p.Tools())
}

// RunApproved uses the attachment-wide tool proposal for lease identity.
func (p *SveltePlan) RunApproved(ctx context.Context, s *Supervisor, runID string, approved []ToolInput) CheckerResult {
	out := CheckerResult{Diagnostics: []Diagnostic{}, Roots: slices.Clone(p.packages), Partial: p.partial, Availability: "available", Phase: "idle", Freshness: "uncertain", Notice: "Svelte check covers tsconfig-selected saved files only; dependencies and excluded scopes are not fully attested. External changes are unwatched."}
	fail := func(code string) CheckerResult {
		out.Replace = false
		out.Partial = true
		out.Phase = "failed"
		out.Freshness = "stale"
		out.Failure = &Error{code, "Svelte check did not complete; previous results must be retained stale"}
		if code == "canceled" {
			out.Phase = "canceled"
		}
		return out
	}
	if ctx == nil || s == nil || !bounded(runID, 200) {
		return fail("invalidArgument")
	}
	fingerprint, err := Fingerprint(p.root, p.tools)
	if err != nil {
		return fail("approvalChanged")
	}
	revalidate := func() bool {
		fresh, err := DiscoverSvelte(p.root, []string{filepath.Dir(p.node)}, p.home)
		if err != nil {
			return false
		}
		fp, err := Fingerprint(p.root, fresh.tools)
		return err == nil && fp == fingerprint && slices.Equal(p.packages, fresh.packages) && p.partial == fresh.partial
	}
	if !revalidate() {
		return fail("approvalChanged")
	}
	var runs []*Run
	for i, c := range p.commands {
		r, err := s.Submit(ctx, p.root, approved, c, fmt.Sprintf("%s-%d", runID, i), false)
		if err != nil {
			var e *Error
			if errors.As(err, &e) {
				return fail(e.Code)
			}
			return fail("toolFailed")
		}
		runs = append(runs, r)
		o := r.Result()
		if o.Kind != OutcomeAccepted || o.Partial || o.Truncated || len(o.Stderr) > 0 {
			out.Truncated = o.Truncated
			if o.Kind == OutcomeCanceled {
				return fail("canceled")
			}
			if o.Kind == OutcomeLimit {
				return fail("limitExceeded")
			}
			return fail("toolFailed")
		}
		if i == 0 || i%2 == 1 {
			want := "svelte-check, 4.7.6\n"
			if i == 0 {
				want = "v22.23.2\n"
			}
			if o.ExitCode != 0 || string(o.Stdout) != want {
				out.Availability = "unsupported"
				return fail("unsupported")
			}
			continue
		}
		parsed, err := parseSvelte(p.root, c.Directory, o.Stdout, o.ExitCode, 10000-len(out.Diagnostics))
		if err != nil {
			var e *Error
			if errors.As(err, &e) {
				out.Truncated = e.Code == "limitExceeded"
				return fail(e.Code)
			}
			return fail("toolFailed")
		}
		out.Diagnostics = append(out.Diagnostics, parsed.diagnostics...)
		if parsed.partial {
			out.Partial = true
			out.Notice = OutsideRootNotice
		}
	}
	if !revalidate() {
		return fail("approvalChanged")
	}
	encoded, err := json.Marshal(out.Diagnostics)
	if err != nil || len(encoded) > 7<<20 {
		out.Truncated = true
		return fail("limitExceeded")
	}
	for _, r := range runs {
		if r.ValidatePublication() != nil {
			return fail("approvalChanged")
		}
	}
	out.Replace = true
	return out
}
