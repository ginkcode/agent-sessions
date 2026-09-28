// Command agent-sessions-cli inspects local coding-agent sessions without a GUI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/paths"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/all"
)

func main() {
	roots, err := paths.Default()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve roots: %v\n", err)
		os.Exit(1)
	}
	os.Exit(run(context.Background(), os.Args[1:], all.Providers(roots, pathutil.NewGitResolver()), os.Stdout, os.Stderr))
}

// run returns 0 on success, 1 on an operational error, and 2 on invalid usage.
func run(ctx context.Context, args []string, providers provider.Set, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}

	switch args[0] {
	case "scan":
		return scan(ctx, args[1:], providers, stdout, stderr)
	case "show":
		return show(ctx, args[1:], providers, stdout, stderr)
	case "detect":
		if len(args) != 1 {
			usage(stderr)
			return 2
		}
		return detect(ctx, providers, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  agent-sessions-cli scan [--agent claude-code] [--json] [--all]")
	_, _ = fmt.Fprintln(w, "  agent-sessions-cli show <agent> <id> [--json] [--meta] [--max-output 2000]")
	_, _ = fmt.Fprintln(w, "  agent-sessions-cli detect")
}

func scan(ctx context.Context, args []string, providers provider.Set, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	agent := fs.String("agent", "", "scan only this agent")
	asJSON := fs.Bool("json", false, "output JSON")
	includeAll := fs.Bool("all", false, "include subagents")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		usage(stderr)
		return 2
	}

	selected := providers
	if *agent != "" {
		p, ok := providers.Get(model.AgentID(*agent))
		if !ok {
			_, _ = fmt.Fprintf(stderr, "unknown agent %q\n", *agent)
			return 2
		}
		selected = provider.Set{p}
	}

	sessions := make([]model.SessionMeta, 0)
	for _, p := range selected {
		result, err := p.Scan(ctx, provider.ScanState{}) // empty state requests every session
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: scan: %v\n", p.ID(), err)
			return 1
		}
		for _, m := range result.Changed {
			if *includeAll || m.ParentID == "" {
				sessions = append(sessions, m)
			}
		}
		printDiagnostics(stderr, p.ID(), result.Diag)
	}

	slices.SortFunc(sessions, func(a, b model.SessionMeta) int {
		if a.UpdatedAt.After(b.UpdatedAt) {
			return -1
		}
		if a.UpdatedAt.Before(b.UpdatedAt) {
			return 1
		}
		return strings.Compare(a.Ref.Key(), b.Ref.Key())
	})
	if *asJSON {
		return writeJSON(stdout, stderr, sessions)
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "AGENT\tID\tUPDATED\tMSGS\tCWD\tTITLE")
	for _, m := range sessions {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", m.Ref.Agent, m.Ref.ID, formatUpdated(m.UpdatedAt), m.Counts.Total(), singleLine(m.CWD), singleLine(m.Title))
	}
	if err := w.Flush(); err != nil {
		_, _ = fmt.Fprintf(stderr, "scan: write output: %v\n", err)
		return 1
	}
	return 0
}

func printDiagnostics(w io.Writer, id model.AgentID, d provider.Diagnostics) {
	unknown := 0
	for _, count := range d.UnknownTypes {
		unknown += count
	}
	_, _ = fmt.Fprintf(w, "%s: %d parse errors, %d unknown types, %d warnings", id, d.ParseErrors, unknown, len(d.Warnings)+d.Dropped)
	if d.Dropped > 0 {
		_, _ = fmt.Fprintf(w, " (%d dropped)", d.Dropped)
	}
	_, _ = fmt.Fprintln(w)
}

func formatUpdated(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func show(ctx context.Context, args []string, providers provider.Set, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "output JSON")
	includeMeta := fs.Bool("meta", false, "include meta messages")
	maxOutput := fs.Int("max-output", 2000, "maximum tool output length in characters")
	options, positional := showArgs(args)
	if err := fs.Parse(options); err != nil {
		return 2
	}
	if len(positional) != 2 || *maxOutput < 0 {
		usage(stderr)
		return 2
	}
	p, ok := providers.Get(model.AgentID(positional[0]))
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown agent %q\n", positional[0])
		return 2
	}
	ref := model.SessionRef{Agent: p.ID(), ID: positional[1]}
	tr, err := p.Load(ctx, ref)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: load %s: %v\n", p.ID(), ref.ID, err)
		return 1
	}
	if tr == nil {
		_, _ = fmt.Fprintf(stderr, "%s: load %s: empty transcript\n", p.ID(), ref.ID)
		return 1
	}

	if !*includeMeta {
		visible := make([]model.Message, 0, len(tr.Messages))
		for _, m := range tr.Messages {
			if !m.IsMeta {
				visible = append(visible, m)
			}
		}
		copy := *tr
		copy.Messages = visible
		tr = &copy
	}
	if *asJSON {
		return writeJSON(stdout, stderr, tr)
	}
	if err := printTranscript(stdout, tr, *maxOutput); err != nil {
		_, _ = fmt.Fprintf(stderr, "show: write output: %v\n", err)
		return 1
	}
	return 0
}

// showArgs puts options before positionals because flag.Parse stops at the first
// positional argument, whereas the documented show form puts options last.
func showArgs(args []string) (options, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			options = append(options, a)
			if (a == "--max-output" || a == "-max-output") && i+1 < len(args) {
				i++
				options = append(options, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return options, positional
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_, _ = fmt.Fprintf(stderr, "encode JSON: %v\n", err)
		return 1
	}
	return 0
}

func printTranscript(w io.Writer, tr *model.Transcript, maxOutput int) error {
	for _, m := range tr.Messages {
		switch m.Role {
		case model.RoleUser:
			if _, err := fmt.Fprintf(w, "▶ user%s\n", messageTime(m.Time)); err != nil {
				return err
			}
		case model.RoleAssistant:
			modelName := ""
			if m.Model != "" {
				modelName = " (" + m.Model + ")"
			}
			if _, err := fmt.Fprintf(w, "◀ assistant%s%s\n", modelName, messageTime(m.Time)); err != nil {
				return err
			}
		case model.RoleSystem:
			// Compaction parts have their own banner; other system messages get a label.
			if !hasCompaction(m.Parts) {
				if _, err := fmt.Fprintln(w, "─ system ─"); err != nil {
					return err
				}
			}
		}
		for _, part := range m.Parts {
			if err := printPart(w, part, maxOutput); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func hasCompaction(parts []model.Part) bool {
	for _, part := range parts {
		if part.Kind == model.PartCompaction {
			return true
		}
	}
	return false
}

func messageTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return " " + t.Local().Format("15:04")
}

func printPart(w io.Writer, part model.Part, maxOutput int) error {
	switch part.Kind {
	case model.PartText:
		_, err := fmt.Fprintln(w, part.Text)
		return err
	case model.PartReasoning:
		_, err := fmt.Fprintf(w, "[reasoning %s]\n", part.Text)
		return err
	case model.PartTool:
		if part.Tool == nil {
			return nil
		}
		t := part.Tool
		status := "?"
		switch t.Status {
		case model.ToolCompleted:
			status = "✓"
		case model.ToolError:
			status = "✗"
		case model.ToolPending:
			status = "…"
		}
		input := singleLine(string(t.Input))
		output := truncateOutput(t.Output, maxOutput)
		if t.OutputTruncated && !strings.HasSuffix(output, "…") {
			output += "…"
		}
		_, err := fmt.Fprintf(w, "[tool %s %s] %s → %s\n", t.Name, status, input, output)
		return err
	case model.PartCompaction:
		if _, err := fmt.Fprintln(w, "── compacted ──"); err != nil {
			return err
		}
		if part.Text != "" {
			_, err := fmt.Fprintln(w, part.Text)
			return err
		}
	case model.PartFile:
		if part.File != nil {
			_, err := fmt.Fprintf(w, "[file %s %s]\n", part.File.Name, part.File.Mime)
			return err
		}
	case model.PartPatch:
		_, err := fmt.Fprintf(w, "[patch %s]\n", strings.Join(part.Files, ", "))
		return err
	case model.PartNotice:
		_, err := fmt.Fprintf(w, "[notice %s]\n", part.Text)
		return err
	}
	return nil
}

func truncateOutput(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "…"
}

func detect(ctx context.Context, providers provider.Set, stdout, stderr io.Writer) int {
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "AGENT\tPRESENT\tROOTS\tGENERATIONS\tNOTES")
	for _, p := range providers {
		found, err := p.Detect(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: detect: %v\n", p.ID(), err)
			return 1
		}
		_, _ = fmt.Fprintf(w, "%s\t%t\t%s\t%s\t%s\n", p.ID(), found.Present, strings.Join(found.Roots, ", "), strings.Join(found.Generations, ", "), strings.Join(found.Notes, "; "))
	}
	if err := w.Flush(); err != nil {
		_, _ = fmt.Fprintf(stderr, "detect: write output: %v\n", err)
		return 1
	}
	return 0
}
