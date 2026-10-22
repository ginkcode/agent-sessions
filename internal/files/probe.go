package files

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Probe limits bound both work and wire output. No directories are enumerated.
const (
	MaxProbePaths     = 32
	MaxProbePathBytes = 4096
	MaxProbeBytes     = 8 << 20
)

var ErrInvalidProbe = errors.New("invalid file probe batch")

// DiskObservation is a point-in-time observation, not a mutation event or file
// object identity. Revision hashes raw bytes, like Content.Edit.Revision.
// Absent never proves rename or authorizes discarding a draft.
type DiskObservation struct {
	Path     string `json:"path"`
	State    string `json:"state"` // present, absent, unknown
	Revision string `json:"revision,omitempty"`
	Reason   string `json:"reason,omitempty"` // sanitized; unknown only
}

type ProbeResult struct {
	Root         string            `json:"root"`
	Observations []DiskObservation `json:"observations"`
}

// Probe observes only explicitly named canonical relative paths. Missing parents,
// links, permissions and races are unknown. Only a missing leaf below verified
// existing directories can be absent. Callers must authorize dir before calling.
func Probe(ctx context.Context, dir string, paths []string) (ProbeResult, error) {
	if len(paths) == 0 || len(paths) > MaxProbePaths {
		return ProbeResult{}, ErrInvalidProbe
	}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if len(p) == 0 || len(p) > MaxProbePathBytes || p == "." || path.Clean(p) != p || !filepath.IsLocal(p) || strings.ContainsAny(p, "\\\x00") || seen[p] {
			return ProbeResult{}, ErrInvalidProbe
		}
		seen[p] = true
	}
	out := ProbeResult{Observations: make([]DiskObservation, len(paths))}
	for i, p := range paths {
		out.Observations[i] = DiskObservation{Path: p, State: "unknown", Reason: "unavailable"}
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return out, nil
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return out, nil
	}
	out.Root = root
	r, err := os.OpenRoot(root)
	if err != nil {
		return out, nil
	}
	defer func() { _ = r.Close() }()
	if sameDir(r, root) != nil {
		return out, nil
	}
	budget := int64(MaxProbeBytes)
	for i, p := range paths {
		if ctx.Err() != nil {
			out.Observations[i].Reason = "cancelled"
			continue
		}
		out.Observations[i] = probePath(ctx, r, p, &budget)
	}
	// A renamed/replaced root or retargeted catalog alias invalidates the batch.
	current, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(current) != root || sameDir(r, root) != nil {
		for i, p := range paths {
			out.Observations[i] = DiskObservation{Path: p, State: "unknown", Reason: "changed"}
		}
	}
	return out, nil
}

func probePath(ctx context.Context, r *os.Root, p string, budget *int64) DiskObservation {
	unknown := DiskObservation{Path: p, State: "unknown", Reason: "unavailable"}
	// Retain metadata only for this call, to detect directory replacement. Never
	// expose, persist or log filesystem identities.
	parents := map[string]os.FileInfo{}
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		parent := strings.Join(parts[:i], "/")
		info, err := r.Lstat(parent)
		if err != nil || !info.IsDir() {
			return unknown
		}
		parents[parent] = info
	}
	stable := func() bool {
		for parent, before := range parents {
			after, err := r.Lstat(parent)
			if err != nil || !after.IsDir() || !os.SameFile(before, after) {
				return false
			}
		}
		return ctx.Err() == nil
	}
	before, err := r.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		if stable() {
			return DiskObservation{Path: p, State: "absent"}
		}
		return unknown
	}
	if err != nil || !before.Mode().IsRegular() {
		return unknown
	}
	if before.Size() > MaxReadBytes || before.Size() > *budget {
		unknown.Reason = "limit"
		return unknown
	}
	f, err := r.Open(p)
	if err != nil {
		return unknown
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !sameProbeFile(before, opened) {
		return unknown
	}
	data, err := io.ReadAll(io.LimitReader(f, min(int64(MaxReadBytes), *budget)+1))
	*budget -= int64(len(data))
	if err != nil || *budget < 0 || int64(len(data)) != before.Size() {
		return unknown
	}
	after, err := r.Lstat(p)
	final, statErr := f.Stat()
	if err != nil || statErr != nil || !sameProbeFile(before, after) || !sameProbeFile(before, final) || !stable() {
		return unknown
	}
	return DiskObservation{Path: p, State: "present", Revision: revisionOf(data)}
}

func sameProbeFile(a, b os.FileInfo) bool {
	return b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
