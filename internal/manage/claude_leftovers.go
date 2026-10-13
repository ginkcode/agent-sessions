package manage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

var (
	claudeSlugKey     = []byte(`"slug":"`)
	claudeSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	claudeAgentPlan   = regexp.MustCompile(`^-agent-[0-9a-zA-Z_]+\.md$`)
)

const maxClaudeSlugLen = 128

// leftoverFiles lists what Claude keeps for a main session outside its
// transcript: file edit backups, per-session environment scripts, todo
// lists, the debug log and plan files. They go to the Trash with the
// transcript so they can be restored together. Anything unexpected (a
// symlink, a protected or special entry) is left in place rather than
// blocking the deletion: these files are optional.
func (cm *claudeManager) leftoverFiles(ctx context.Context, m model.SessionMeta, slugs *claudeSlugIndex) ([]itemFile, error) {
	id := m.Ref.ID
	if m.ParentID != "" || !claudeMainIDPattern.MatchString(id) {
		return nil, nil
	}
	ctxDone := func() error { return ctx.Err() }
	var files []itemFile
	add := func(path, allowTop string, wantDir bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		mode, err := cm.ps.classifyIn(path, allowTop)
		if err != nil || mode.IsDir() != wantDir {
			return nil
		}
		if wantDir {
			if err := cm.ps.checkTreeIn(ctxDone, path, allowTop); err != nil {
				return ctx.Err()
			}
		}
		files = append(files, itemFile{Path: path, Mode: mode})
		return nil
	}
	exists := func(path string) bool {
		_, err := os.Lstat(path)
		return err == nil
	}

	for _, dir := range []string{"file-history", "session-env"} {
		if path := filepath.Join(cm.root, dir, id); exists(path) {
			if err := add(path, dir, true); err != nil {
				return nil, err
			}
		}
	}
	todos, _ := filepath.Glob(filepath.Join(cm.root, "todos", id+"-agent-*.json"))
	sort.Strings(todos)
	for _, path := range todos {
		if err := add(path, "", false); err != nil {
			return nil, err
		}
	}
	if path := filepath.Join(cm.root, "debug", id+".txt"); exists(path) {
		if err := add(path, "", false); err != nil {
			return nil, err
		}
	}

	if slugs == nil {
		return files, nil
	}
	own, err := claudeTranscriptSlugs(m.SourcePath)
	if err != nil {
		// Without the slugs no plan is matched; the rest is still safe.
		return files, ctx.Err()
	}
	sessionDir := filepath.Join(filepath.Dir(filepath.Clean(m.SourcePath)), id)
	isOwn := func(path string) bool {
		return path == filepath.Clean(m.SourcePath) || strings.HasPrefix(path, sessionDir+string(filepath.Separator))
	}
	plansDir := filepath.Join(cm.root, "plans")
	for _, slug := range own {
		plans := claudePlanFiles(plansDir, slug)
		if len(plans) == 0 {
			continue
		}
		index, err := slugs.load(ctx, filepath.Join(cm.root, "projects"))
		if err != nil {
			return nil, err
		}
		if index.mentionedElsewhere(slug, isOwn) {
			continue
		}
		for _, path := range plans {
			// <slug>-agent-<x>.md could also be the main plan of a session
			// whose slug happens to be "<slug>-agent-<x>".
			name := strings.TrimSuffix(filepath.Base(path), ".md")
			if name != slug && index.mentionedElsewhere(name, func(string) bool { return false }) {
				continue
			}
			if err := add(path, "", false); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// claudePlanFiles returns the existing plan files named after slug: the
// session's plan and the plans of its subagents.
func claudePlanFiles(dir, slug string) []string {
	var out []string
	main := filepath.Join(dir, slug+".md")
	if _, err := os.Lstat(main); err == nil {
		out = append(out, main)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), slug)
		if ok && claudeAgentPlan.MatchString(rest) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// claudeTranscriptSlugs returns the plan slugs a transcript records in the
// top-level "slug" field of its lines, in first-seen order.
func claudeTranscriptSlugs(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	seen := map[string]bool{}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, claudeSlugKey) {
			var rec struct {
				Slug string `json:"slug"`
			}
			if json.Unmarshal(line, &rec) == nil && validClaudeSlug(rec.Slug) && !seen[rec.Slug] {
				seen[rec.Slug] = true
				out = append(out, rec.Slug)
			}
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func validClaudeSlug(s string) bool {
	return len(s) <= maxClaudeSlugLen && claudeSlugPattern.MatchString(s)
}

// claudeSlugIndex records which transcripts mention each slug. Resumed and
// forked sessions share their parent's slug and plan files, so a plan is
// only removed when no other transcript mentions it. It is built at most
// once per preview or delete, and only when a plan file exists.
type claudeSlugIndex struct {
	built bool
	err   error
	// complete is false when some transcript could not be read; every slug
	// then counts as mentioned elsewhere.
	complete bool
	files    map[string][]string
}

func (s *claudeSlugIndex) load(ctx context.Context, projectsDir string) (*claudeSlugIndex, error) {
	if s.built {
		return s, s.err
	}
	s.files = map[string][]string{}
	s.complete = true
	err := filepath.WalkDir(projectsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if !errors.Is(walkErr, fs.ErrNotExist) || path != projectsDir {
				s.complete = false
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			s.complete = false
			return nil
		}
		seen := map[string]bool{}
		err = scanClaudeSlugs(f, func(slug string) {
			if !seen[slug] {
				seen[slug] = true
				s.files[slug] = append(s.files[slug], path)
			}
		})
		f.Close()
		if err != nil {
			s.complete = false
		}
		return nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		// Cancelled: leave the index unbuilt so a later call can retry.
		return nil, ctxErr
	}
	if err != nil {
		s.complete = false
	}
	s.built = true
	return s, nil
}

func (s *claudeSlugIndex) mentionedElsewhere(slug string, isOwn func(string) bool) bool {
	if !s.complete {
		return true
	}
	for _, path := range s.files[slug] {
		if !isOwn(path) {
			return true
		}
	}
	return false
}

// scanClaudeSlugs calls fn with every valid `"slug":"<value>"` in r,
// wherever it appears, reading in chunks so large transcripts are never
// held whole. Matching anywhere, not only the top-level field, errs on the
// side of keeping a plan.
func scanClaudeSlugs(r io.Reader, fn func(string)) error {
	chunk := make([]byte, 1<<20)
	var buf []byte
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)
		eof := err == io.EOF
		if err != nil && !eof {
			return err
		}
		next, wait := 0, -1
		for {
			i := bytes.Index(buf[next:], claudeSlugKey)
			if i < 0 {
				break
			}
			start := next + i + len(claudeSlugKey)
			end := bytes.IndexByte(buf[start:], '"')
			if end < 0 && !eof && len(buf)-start <= maxClaudeSlugLen {
				// The value continues in the next chunk.
				wait = next + i
				break
			}
			if end < 0 {
				next = start
				continue
			}
			if value := string(buf[start : start+end]); validClaudeSlug(value) {
				fn(value)
			}
			next = start + end + 1
		}
		if eof {
			return nil
		}
		// Keep what a match could still start in: a pending value, or a key
		// split across the chunk boundary.
		keep := wait
		if keep < 0 {
			keep = max(next, len(buf)-(len(claudeSlugKey)-1))
		}
		buf = append(buf[:0], buf[keep:]...)
	}
}
