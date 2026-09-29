package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/app"
	"github.com/ginkcode/agent-sessions/internal/bundle"
	"github.com/ginkcode/agent-sessions/internal/manage"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/provider"
	scancat "github.com/ginkcode/agent-sessions/internal/scan"
)

// exportCmd writes a bundle for one session. The catalog is built from a
// scan of the source provider, since the CLI has no long-lived one.
func exportCmd(ctx context.Context, args []string, providers provider.Set, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", string(bundle.ProfileComplete), "complete or share-safe")
	out := fs.String("o", "", "output .agent-session.zip path")
	budgetStr := fs.String("budget", "detailed", "handoff token budget")
	reasoning := fs.Bool("reasoning", false, "include reasoning in the handoff")
	redact := fs.Bool("redact", false, "redact secrets (always on for share-safe)")

	options, positional := exportArgs(args)
	if err := fs.Parse(options); err != nil {
		return 2
	}
	if len(positional) != 2 || *out == "" {
		usage(stderr)
		return 2
	}
	budget, err := parseBudget(*budgetStr)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	p, ref, code := resolveSession(positional[0], positional[1], providers, stderr)
	if code != 0 {
		return code
	}

	catalog := scancat.NewCatalog()
	if result, err := p.Scan(ctx, provider.ScanState{}); err == nil {
		catalog.Apply(result)
	}
	svc := app.NewService(catalog, provider.Set{p})
	mgr, err := captureManager()
	if err != nil && *profile != string(bundle.ProfileShareSafe) {
		_, _ = fmt.Fprintf(stderr, "export: %v\n", err)
		return 1
	}
	req := app.ExportRequest{
		Ref:              ref,
		Profile:          *profile,
		Budget:           budget,
		IncludeReasoning: *reasoning,
		RedactSecrets:    *redact,
	}
	path, err := svc.ExportBundle(ctx, req, *out, mgr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "export: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, path)
	return 0
}

// inspectCmd prints a validated bundle's summary.
func inspectCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "output the manifest as JSON")
	options, positional := exportArgs(args)
	if err := fs.Parse(options); err != nil {
		return 2
	}
	if len(positional) != 1 {
		usage(stderr)
		return 2
	}
	b, err := bundle.ReadFile(positional[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "inspect: %v\n", err)
		return 1
	}
	if *asJSON {
		body, err := json.MarshalIndent(b.Manifest, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "inspect: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(body))
		return 0
	}
	m := b.Manifest
	fmt.Fprintf(stdout, "format:   %s v%d\n", m.Format, m.Version)
	fmt.Fprintf(stdout, "profile:  %s\n", m.Profile)
	fmt.Fprintf(stdout, "source:   %s %s\n", m.Source.Agent, m.Source.ID)
	if m.Source.Title != "" {
		fmt.Fprintf(stdout, "title:    %s\n", m.Source.Title)
	}
	if m.Source.CWD != "" {
		fmt.Fprintf(stdout, "cwd:      %s\n", m.Source.CWD)
	}
	fmt.Fprintf(stdout, "sessions: %d\n", len(m.Sessions))
	native := 0
	for _, s := range m.Sessions {
		native += len(s.Native)
	}
	fmt.Fprintf(stdout, "native:   %d files\n", native)
	fmt.Fprintf(stdout, "redacted: %d\n", m.Redaction.Counts)
	return 0
}

// resolveSession maps the CLI's agent argument to a provider and a ref.
func resolveSession(agentArg, id string, providers provider.Set, stderr io.Writer) (provider.Provider, model.SessionRef, int) {
	agent := model.AgentID(agentArg)
	if agentArg == "claude" {
		agent = model.AgentClaude
	}
	p, ok := providers.Get(agent)
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown agent %q\n", agentArg)
		return nil, model.SessionRef{}, 2
	}
	return p, model.SessionRef{Agent: p.ID(), ID: id}, 0
}

// captureManager builds the read-only capture manager from the default roots.
// A root that is not configured is simply absent.
func captureManager() (*manage.Manager, error) {
	roots, err := paths.Default()
	if err != nil {
		return nil, err
	}
	if roots.Config == "" {
		return nil, fmt.Errorf("no config directory")
	}
	return manage.New(roots, roots.Config+"/config.toml")
}

// exportArgs splits flags from the positional agent and id, so -o can follow
// either of them.
func exportArgs(args []string) (options, positional []string) {
	valued := map[string]bool{"profile": true, "o": true, "budget": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			options = append(options, a)
			name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			if valued[name] && !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return options, positional
}
