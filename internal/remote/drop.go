package remote

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// MaxDropBytes bounds what one drop copies to an SSH host.
const MaxDropBytes = 100 << 20

// ErrDropFolder means a folder was dropped where only files can be copied.
var ErrDropFolder = errors.New("only files can be dropped into an SSH terminal, not folders")

// ErrDropTooLarge means the dropped files are more than MaxDropBytes.
var ErrDropTooLarge = fmt.Errorf("the dropped files are larger than %d MB", MaxDropBytes>>20)

// dropMarker starts the lines that carry paths back, so what a login
// shell prints first is skipped.
const dropMarker = "AGENT_SESSIONS_DROP:"

// DropFiles makes files dropped on a terminal tab readable on host and
// returns their paths there, in order. A WSL distribution reads this
// computer's files through its mounts; an SSH host gets copies, in a new
// directory under its temporary directory that only the user can read.
func DropFiles(ctx context.Context, host string, opts SSHOptions, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if _, ok := ParseWSLTarget(host); ok {
		return wslDropPaths(ctx, host, paths)
	}
	return uploadDrop(ctx, host, opts, paths)
}

// wslDropPaths translates Windows paths, including \\wsl.localhost ones,
// with the distribution's wslpath.
func wslDropPaths(ctx context.Context, host string, paths []string) ([]string, error) {
	script := fmt.Sprintf(`for p in %s; do out=$(wslpath -u "$p") || exit 1; printf '%s%%s\n' "$out"; done`,
		QuoteArgs(paths), dropMarker)
	var stdout bytes.Buffer
	if err := runRemote(ctx, host, SSHOptions{}, &stdout, nil, script); err != nil {
		return nil, fmt.Errorf("find the dropped files in WSL: %w", err)
	}
	got := dropLines(stdout.String())
	if len(got) != len(paths) {
		return nil, fmt.Errorf("find the dropped files in WSL: got %d paths for %d files", len(got), len(paths))
	}
	return got, nil
}

// uploadDrop copies files to an SSH host as one tar stream.
func uploadDrop(ctx context.Context, host string, opts SSHOptions, paths []string) ([]string, error) {
	names, total := make([]string, len(paths)), int64(0)
	seen := map[string]bool{}
	for i, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, ErrDropFolder
		}
		if total += info.Size(); total > MaxDropBytes {
			return nil, ErrDropTooLarge
		}
		names[i] = dropName(filepath.Base(p), seen)
	}
	pr, pw := io.Pipe()
	go func() { _ = pw.CloseWithError(writeDropTar(pw, paths, names)) }()
	defer func() { _ = pr.Close() }()
	script := `umask 077 && t=${TMPDIR:-/tmp} && d=$(mktemp -d "${t%/}/agent-sessions-drop.XXXXXX") && ` +
		`tar -xf - -C "$d" && printf '` + dropMarker + `%s\n' "$d"`
	var stdout bytes.Buffer
	if err := runRemote(ctx, host, opts, &stdout, pr, script); err != nil {
		return nil, fmt.Errorf("copy the dropped files to %s: %w", host, err)
	}
	got := dropLines(stdout.String())
	if len(got) != 1 || !strings.HasPrefix(got[0], "/") {
		return nil, fmt.Errorf("copy the dropped files to %s: no directory was made", host)
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = path.Join(got[0], name)
	}
	return out, nil
}

// dropName is a file's name in the upload directory: its own, numbered when
// an earlier file took it.
func dropName(base string, seen map[string]bool) string {
	name := base
	ext := path.Ext(base)
	for n := 2; seen[name]; n++ {
		name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, ext), n, ext)
	}
	seen[name] = true
	return name
}

func writeDropTar(w io.Writer, paths, names []string) error {
	tw := tar.NewWriter(w)
	for i, p := range paths {
		if err := addDropFile(tw, p, names[i]); err != nil {
			return err
		}
	}
	return tw.Close()
}

func addDropFile(tw *tar.Writer, p, name string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr := &tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, f, hdr.Size); err != nil {
		return fmt.Errorf("read %s: %w", p, err)
	}
	return nil
}

// dropLines returns what follows dropMarker on each line that has it.
func dropLines(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), dropMarker); ok {
			got = append(got, rest)
		}
	}
	return got
}
