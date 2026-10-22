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

	"golang.org/x/mod/modfile"
)

// GoVetEnvironment is reviewed host metadata, not inherited process environment.
// Cache directories may be written by Go; this adapter is not a sandbox.
type GoVetEnvironment struct{ Home, Cache, ModuleCache string }

type GoVetPlan struct {
	root             AuthorizedRoot
	executable       string
	environment      GoVetEnvironment
	modules, configs []string
	partial          bool
	tools            []ToolInput
	commands         []Command
}

// Tools returns an approval proposal without executing even a version probe.
func (p *GoVetPlan) Tools() []ToolInput {
	out := slices.Clone(p.tools)
	for i := range out {
		out[i].ConfigPaths = slices.Clone(out[i].ConfigPaths)
	}
	return out
}

// DiscoverGoVet uses only bounded, rooted metadata/config reads. PATH is supplied
// by the host owner for review; relative PATH entries and shell launchers are ignored.
func DiscoverGoVet(root AuthorizedRoot, reviewedPath []string, env GoVetEnvironment) (*GoVetPlan, error) {
	if err := root.Revalidate(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, failure("unsupported", "Go vet platform has not been verified")
	}
	for _, path := range []string{env.Home, env.Cache, env.ModuleCache} {
		if !filepath.IsAbs(path) || !bounded(path, 4096) || hasParent(path) {
			return nil, failure("invalidArgument", "Reviewed absolute cache and home paths are required")
		}
	}
	var executable string
	for _, dir := range reviewedPath {
		if !filepath.IsAbs(dir) || hasParent(dir) {
			continue
		}
		candidate := filepath.Join(dir, "go")
		real, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		st, err := os.Stat(real)
		if err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			executable = real
			break
		}
	}
	if executable == "" {
		return nil, failure("notFound", "Installed Go executable is unavailable")
	}
	p := &GoVetPlan{root: root, executable: executable, environment: env}
	if err := p.discover(); err != nil {
		return nil, err
	}
	if len(p.modules) == 0 {
		return nil, failure("unsupported", "No contained Go module was found")
	}
	goroot := filepath.Dir(filepath.Dir(executable))
	baseEnv := []string{"HOME=" + env.Home, "GOCACHE=" + env.Cache, "GOMODCACHE=" + env.ModuleCache, "GOROOT=" + goroot, "PATH=" + filepath.Join(goroot, "bin") + ":/usr/bin:/bin", "GOENV=off", "GOWORK=off", "GOFLAGS=", "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=0"}
	// Every launch has its own exact Command.Policy identity, including the probe.
	dirs := append([]string{""}, p.modules...)
	for i, dir := range dirs {
		id := fmt.Sprintf("go-vet-%d", i)
		args := []string{"vet", "-json", "./..."}
		if i == 0 {
			args = []string{"version"}
		}
		c := Command{ProviderID: id, Executable: executable, Args: args, Env: baseEnv, Directory: dir, AdapterPolicy: "go-vet-v1:go1.26.8:darwin/arm64:saved-uncertain:gowork-off:cgo-off", AcceptedExits: []int{0}}
		p.commands = append(p.commands, c)
		p.tools = append(p.tools, ToolInput{ProviderID: id, Executable: executable, ConfigPaths: slices.Clone(p.configs), Policy: c.Policy()})
	}
	// Bind installed toolchain helpers and metadata, not only the go launcher.
	// This is enumerated identity, not transitive attestation of every standard library.
	for _, name := range []string{"VERSION", "go.env", "pkg/tool/darwin_arm64/vet", "pkg/tool/darwin_arm64/compile", "pkg/tool/darwin_arm64/asm", "pkg/tool/darwin_arm64/link"} {
		p.tools = append(p.tools, ToolInput{ProviderID: "go-toolchain-" + strings.ReplaceAll(name, "/", "-"), Executable: filepath.Join(goroot, filepath.FromSlash(name)), Policy: "go-vet-toolchain-v1"})
	}
	if _, err := Fingerprint(root, p.tools); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *GoVetPlan) discover() error {
	r, err := os.OpenRoot(p.root.Project().Root)
	if err != nil {
		return failure("unauthorized", "Go discovery root cannot be opened")
	}
	defer func() { _ = r.Close() }()
	entries := 0
	var walk func(string, int) error
	walk = func(dir string, depth int) error {
		f, err := r.Open(dir)
		if err != nil {
			p.partial = true
			return nil
		}
		defer func() { _ = f.Close() }()
		// Read at most the remaining budget plus one, never an unbounded directory.
		ds, err := f.ReadDir(4097 - entries)
		if err != nil && !errors.Is(err, io.EOF) {
			p.partial = true
			return nil
		}
		slices.SortFunc(ds, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		for _, d := range ds {
			entries++
			if entries > 4096 {
				p.partial = true
				return nil
			}
			rel := filepath.Join(dir, d.Name())
			portable := filepath.ToSlash(rel)
			if ValidatePath(portable) != nil {
				p.partial = true
				continue
			}
			if d.Type()&os.ModeSymlink != 0 {
				p.partial = true
				continue
			}
			if d.IsDir() {
				if d.Name() == "vendor" || d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_") || d.Name() == "testdata" {
					p.partial = true
					continue
				}
				if depth >= 32 {
					p.partial = true
					continue
				}
				if err := walk(rel, depth+1); err != nil {
					return err
				}
			} else if d.Name() == "go.mod" || d.Name() == "go.sum" {
				if len(p.configs) >= 128 {
					return failure("limitExceeded", "Go configuration identity limit exceeded")
				}
				p.configs = append(p.configs, portable)
				if d.Name() == "go.mod" {
					if len(p.modules) >= 24 {
						return failure("limitExceeded", "Go module limit exceeded")
					}
					data, err := rootedRead(p.root, portable, 1<<20)
					if err != nil {
						return failure("unsupported", "Go module configuration cannot be verified")
					}
					m, err := modfile.Parse(portable, data, nil)
					if err != nil {
						return failure("unsupported", "Go module configuration is invalid")
					}
					for _, rep := range m.Replace {
						if rep.New.Version == "" {
							return failure("unsupported", "Local Go replacements are not supported by this policy")
						}
					}
					module := filepath.ToSlash(dir)
					if module == "." {
						module = ""
					}
					p.modules = append(p.modules, module)
				}
			}
		}
		return nil
	}
	if err := walk(".", 0); err != nil {
		return err
	}
	slices.Sort(p.modules)
	slices.Sort(p.configs)
	return nil
}

// CheckerResult is a complete adapter outcome for the future manager. Replace
// must be false on ANY module/probe failure: retain the prior provider set stale.
// Successful but partial discovery may replace only with explicitly partial scope.
type CheckerResult struct {
	Diagnostics                            []Diagnostic
	Roots                                  []string
	Replace, Partial, Truncated            bool
	Availability, Phase, Freshness, Notice string
	Failure                                *Error
}

func (p *GoVetPlan) Run(ctx context.Context, s *Supervisor, runID string) CheckerResult {
	return p.RunApproved(ctx, s, runID, p.Tools())
}

// RunApproved uses the attachment-wide tool proposal for lease identity.
func (p *GoVetPlan) RunApproved(ctx context.Context, s *Supervisor, runID string, approved []ToolInput) CheckerResult {
	out := CheckerResult{Diagnostics: []Diagnostic{}, Roots: slices.Clone(p.modules), Partial: p.partial, Availability: "available", Phase: "idle", Freshness: "uncertain", Notice: "Go vet checks saved, build-selected packages only; workspace mode and cgo are disabled. External changes are unwatched."}
	fail := func(code string) CheckerResult {
		out.Replace = false
		out.Partial = true
		out.Phase = "failed"
		out.Freshness = "stale"
		out.Failure = &Error{code, "Go vet did not complete; previous results must be retained stale"}
		if code == "canceled" {
			out.Phase = "canceled"
		}
		return out
	}
	if !bounded(runID, 200) || s == nil || ctx == nil {
		return fail("invalidArgument")
	}
	// Re-enumeration catches config creation/removal, not merely edits to known files.
	fresh, err := DiscoverGoVet(p.root, []string{filepath.Dir(p.executable)}, p.environment)
	if err != nil {
		return fail("approvalChanged")
	}
	a, err := Fingerprint(p.root, p.tools)
	if err != nil {
		return fail("approvalChanged")
	}
	b, err := Fingerprint(p.root, fresh.tools)
	if err != nil || a != b || !slices.Equal(p.modules, fresh.modules) || p.partial != fresh.partial {
		return fail("approvalChanged")
	}
	runs := []*Run{}
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
		if o.Kind != OutcomeAccepted || o.ExitCode != 0 || o.Partial || o.Truncated || len(o.Stderr) != 0 {
			out.Truncated = o.Truncated
			if o.Kind == OutcomeCanceled {
				return fail("canceled")
			}
			if o.Kind == OutcomeLimit {
				return fail("limitExceeded")
			}
			return fail("toolFailed")
		}
		if i == 0 {
			if string(o.Stdout) != "go version go1.26.8 darwin/arm64\n" {
				out.Availability = "unsupported"
				return fail("unsupported")
			}
			continue
		}
		parsed, err := parseGoVet(p.root, c.Directory, o.Stdout, 10000-len(out.Diagnostics))
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
	// Detect configuration set changes during the check, including new go.sum files.
	final, err := DiscoverGoVet(p.root, []string{filepath.Dir(p.executable)}, p.environment)
	if err != nil {
		return fail("approvalChanged")
	}
	finalFP, err := Fingerprint(p.root, final.tools)
	if err != nil || finalFP != b || !slices.Equal(p.modules, final.modules) || p.partial != final.partial {
		return fail("approvalChanged")
	}
	// CD-07 still enforces attachment-wide retention. Do not hand it a provider
	// result that alone exceeds the reserved encoded finding budget.
	encoded, err := json.Marshal(out.Diagnostics)
	if err != nil || len(encoded) > 7<<20 {
		out.Truncated = true
		return fail("limitExceeded")
	}
	for _, r := range runs {
		if err := r.ValidatePublication(); err != nil {
			return fail("approvalChanged")
		}
	}
	out.Replace = true
	return out
}

func rootedRead(root AuthorizedRoot, rel string, limit int64) ([]byte, error) {
	if _, err := root.ResolvePath(root.Project().HostID, rel); err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root.Project().Root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	f, err := r.Open(filepath.FromSlash(rel))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, failure("limitExceeded", "Saved source exceeds read limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, failure("limitExceeded", "Saved source exceeds read limit")
	}
	after, err := f.Stat()
	if err != nil || st.Size() != after.Size() || st.ModTime() != after.ModTime() {
		return nil, failure("resyncRequired", "Saved source changed during read")
	}
	return data, nil
}
