package files

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BlameLine describes the last commit responsible for one working-tree line.
// Uncommitted lines have an empty commit/author, zero authorTime and the
// summary "Not Committed Yet". Line is always the requested 1-based line.
type BlameLine struct {
	Line        int    `json:"line"`
	Commit      string `json:"commit"`
	Author      string `json:"author"`
	AuthorTime  int64  `json:"authorTime"`
	Summary     string `json:"summary"`
	Uncommitted bool   `json:"uncommitted"`
}

// BlameFile returns blame for a single line, using the current on-disk contents.
func BlameFile(ctx context.Context, dir, path string, line int) (BlameLine, error) {
	if line <= 0 {
		return BlameLine{}, fmt.Errorf("blame: line must be positive")
	}
	full, err := Resolve(dir, path)
	if err != nil {
		return BlameLine{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return BlameLine{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return BlameLine{}, err
	}
	if !info.Mode().IsRegular() {
		return BlameLine{}, fmt.Errorf("blame: not a regular file: %s", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil {
		return BlameLine{}, err
	}
	if len(data) > MaxReadBytes || IsBinary(data) {
		return BlameLine{}, fmt.Errorf("blame: requires a text file of at most %d bytes", MaxReadBytes)
	}
	lines := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	if line > lines {
		return BlameLine{}, fmt.Errorf("blame: line %d is out of range (file has %d lines)", line, lines)
	}
	// Discover Git from the file's directory, not the workspace root: a
	// workspace may contain several independent repositories or submodules.
	// Resolve above still enforces the workspace's containment boundary.
	realDir := filepath.Dir(full)
	rel := filepath.Base(full)
	inside, err := blameGit(ctx, realDir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return BlameLine{}, err
	}
	if strings.TrimSpace(string(inside)) != "true" {
		return BlameLine{}, fmt.Errorf("blame: not a git working tree")
	}
	n := strconv.Itoa(line)
	out, blameErr := blameGit(ctx, realDir, "blame", "--line-porcelain", "-L", n+","+n, "--", rel)
	if blameErr != nil {
		// Git cannot blame untracked files or staged additions absent from
		// HEAD (including an unborn branch). Only these explicit statuses
		// qualify; never disguise repository or command failures as new files.
		status, err := blameGit(ctx, realDir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--", rel)
		if err == nil && (bytes.HasPrefix(status, []byte("?? ")) || (len(status) >= 3 && status[0] == 'A')) {
			return uncommittedBlame(line), nil
		}
		return BlameLine{}, blameErr
	}
	return parseBlameLine(string(out), line)
}

func uncommittedBlame(line int) BlameLine {
	return BlameLine{Line: line, Summary: "Not Committed Yet", Uncommitted: true}
}

func parseBlameLine(out string, line int) (BlameLine, error) {
	header, rest, ok := strings.Cut(out, "\n")
	fields := strings.Fields(header)
	if !ok || len(fields) != 4 || (len(fields[0]) != 40 && len(fields[0]) != 64) {
		return BlameLine{}, fmt.Errorf("blame: invalid porcelain header")
	}
	if strings.Trim(fields[0], "0") == "" {
		return uncommittedBlame(line), nil
	}
	result := BlameLine{Line: line, Commit: fields[0]}
	var hasAuthor, hasTime, hasSummary bool
	for s := range strings.SplitSeq(rest, "\n") {
		if strings.HasPrefix(s, "\t") {
			break
		}
		key, value, _ := strings.Cut(s, " ")
		switch key {
		case "author":
			result.Author, hasAuthor = value, true
		case "author-time":
			var err error
			result.AuthorTime, err = strconv.ParseInt(value, 10, 64)
			if err != nil {
				return BlameLine{}, fmt.Errorf("blame: invalid author-time: %w", err)
			}
			hasTime = true
		case "summary":
			result.Summary, hasSummary = value, true
		}
	}
	if !hasAuthor || !hasTime || !hasSummary {
		return BlameLine{}, fmt.Errorf("blame: incomplete porcelain metadata")
	}
	return result, nil
}

// Unlike listing's best-effort runGit, blame preserves actionable git errors.
func blameGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	argv := append([]string{"-C", dir, "--literal-pathspecs", "-c", "core.quotepath=off"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...) //nolint:gosec
	hideConsole(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("blame: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("blame: git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
