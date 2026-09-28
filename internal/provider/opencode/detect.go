package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/sqliteread"
)

// Detect reports the generations of OpenCode session storage available under
// the configured root. Schema inspection reads only allowlisted session table
// names; credentials are never opened or queried.
func (p *Provider) Detect(ctx context.Context) (provider.Detection, error) {
	detection := provider.Detection{Roots: []string{p.root}}
	if err := ctx.Err(); err != nil {
		return detection, err
	}
	info, err := os.Stat(p.root)
	if os.IsNotExist(err) {
		return detection, nil
	}
	if err != nil {
		return detection, fmt.Errorf("opencode root %q: %w", p.root, err)
	}
	if !info.IsDir() {
		return detection, fmt.Errorf("opencode root %q: not a directory", p.root)
	}

	if err := p.detectDB(ctx, &detection); err != nil {
		return detection, err
	}
	legacy, err := p.hasLegacySessions(ctx)
	if err != nil {
		return detection, err
	}
	if legacy {
		detection.Generations = append(detection.Generations, GenLegacy)
	}
	detection.Present = len(detection.Generations) != 0
	return detection, nil
}

func (p *Provider) detectDB(ctx context.Context, d *provider.Detection) error {
	path := p.dbPath()
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opencode database %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("opencode database %q: not a regular file", path)
	}

	db, err := sqliteread.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("opencode database %q: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	present := make(map[string]bool, 5)
	for _, name := range []string{"session_v2", "session_message", "session", "message", "part"} {
		present[name], err = sqliteread.HasTable(ctx, db, name)
		if err != nil {
			return fmt.Errorf("opencode database %q: %w", path, err)
		}
	}

	if present["session_v2"] && present["session_message"] {
		d.Generations = append(d.Generations, GenV2)
	} else if present["session_v2"] || present["session_message"] {
		d.Notes = append(d.Notes, "incomplete OpenCode v2 schema: both session_v2 and session_message are required")
	}
	if present["session"] && present["message"] {
		d.Generations = append(d.Generations, GenV1)
		if !present["part"] {
			d.Notes = append(d.Notes, "OpenCode v1 part table is missing; transcript loading requires it")
		}
	} else if present["session"] || present["message"] || present["part"] {
		d.Notes = append(d.Notes, "incomplete OpenCode v1 schema: both session and message are required")
	}
	return nil
}

// hasLegacySessions looks only at JSON session files one level below
// storage/session. Other legacy storage trees are not proof of session data.
func (p *Provider) hasLegacySessions(ctx context.Context) (bool, error) {
	base := filepath.Join(p.root, storageSubdir, legacySession)
	projects, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opencode legacy sessions %q: %w", base, err)
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(base, project.Name())
		sessions, err := os.ReadDir(path)
		if err != nil {
			return false, fmt.Errorf("opencode legacy sessions %q: %w", path, err)
		}
		for _, session := range sessions {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if !session.IsDir() && strings.EqualFold(filepath.Ext(session.Name()), ".json") {
				return true, nil
			}
		}
	}
	return false, nil
}

// WatchPaths returns only already-existing store paths. M2 also watches
// parent directories to discover paths created after this call.
func (p *Provider) WatchPaths() []string {
	paths := []string{
		p.dbPath(),
		p.dbPath() + "-wal",
		filepath.Join(p.root, storageSubdir, legacySession),
		filepath.Join(p.root, storageSubdir, legacyMessage),
		filepath.Join(p.root, storageSubdir, legacyPart),
	}
	var existing []string
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, path)
		}
	}
	return existing
}
