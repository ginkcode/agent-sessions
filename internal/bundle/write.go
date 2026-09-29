package bundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Limits applied while writing. A bundle that would exceed them is refused
// rather than truncated.
const (
	MaxEntries     = 4096
	MaxEntryBytes  = 256 << 20 // 256 MiB per entry
	MaxTotalBytes  = 1 << 30   // 1 GiB uncompressed across all entries
	MaxCompression = 200       // uncompressed/compressed ratio cap
)

// NativeInput is one verbatim native record to embed under native/<agent>/.
type NativeInput struct {
	// Agent is the owning tool; the entry is stored at
	// native/<agent>/<Rel>.
	Agent string
	// Rel is the path relative to the tool's storage root.
	Rel string
	// Data is the verbatim file content.
	Data []byte
}

// WriteRequest is everything the writer needs. The writer fills in manifest
// checksums, sizes and the format header; callers do not set those.
type WriteRequest struct {
	CreatedAt  time.Time
	AppVersion string
	Profile    Profile
	Source     SourceMeta
	Sessions   []SessionInput
	Redaction  RedactionManifest
	// Handoff is the pre-rendered handoff.md body.
	Handoff string
	// Transcript is the raw transcript.json body ([]model.Transcript).
	Transcript []byte
}

// SessionInput is one session (root or descendant) plus its native files.
type SessionInput struct {
	Agent    string
	ID       string
	ParentID string
	Native   []NativeInput
}

// WriteToFile writes the bundle to path atomically: a 0600 temp file in the
// same directory, fsync, then rename over the destination.
func WriteToFile(path string, req WriteRequest) error {
	if path == "" {
		return fmt.Errorf("bundle: empty output path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("bundle: create output dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".bundle-*.tmp")
	if err != nil {
		return fmt.Errorf("bundle: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("bundle: chmod temp file: %w", err)
	}

	if err := writeZip(tmp, req); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("bundle: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("bundle: close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("bundle: rename bundle: %w", err)
	}
	return nil
}

// Write writes the bundle to w. w is not closed.
func Write(w io.Writer, req WriteRequest) error {
	return writeZip(w, req)
}

func writeZip(w io.Writer, req WriteRequest) error {
	if err := validateRequest(req); err != nil {
		return err
	}

	zw := zip.NewWriter(w)
	var total int64
	var entries int

	add := func(name string, data []byte) error {
		if entries >= MaxEntries {
			return fmt.Errorf("bundle: more than %d entries", MaxEntries)
		}
		if int64(len(data)) > MaxEntryBytes {
			return fmt.Errorf("bundle: entry %s exceeds %d bytes", name, MaxEntryBytes)
		}
		if total+int64(len(data)) > MaxTotalBytes {
			return fmt.Errorf("bundle: bundle exceeds %d bytes", MaxTotalBytes)
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.Modified = req.CreatedAt.UTC()
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return fmt.Errorf("bundle: create %s: %w", name, err)
		}
		if _, err := fw.Write(data); err != nil {
			return fmt.Errorf("bundle: write %s: %w", name, err)
		}
		total += int64(len(data))
		entries++
		return nil
	}

	manifest := buildManifest(req)
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("bundle: marshal manifest: %w", err)
	}
	if err := add("manifest.json", append(body, '\n')); err != nil {
		return err
	}
	if err := add("transcript.json", req.Transcript); err != nil {
		return err
	}
	if err := add("handoff.md", []byte(req.Handoff)); err != nil {
		return err
	}
	for _, s := range req.Sessions {
		for _, n := range s.Native {
			name := nativeName(n.Agent, n.Rel)
			if err := add(name, n.Data); err != nil {
				return err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("bundle: close zip: %w", err)
	}
	return nil
}

func buildManifest(req WriteRequest) Manifest {
	sessions := make([]SessionManifest, 0, len(req.Sessions))
	for _, s := range req.Sessions {
		sm := SessionManifest{
			Ref:      sessionRef(s.Agent, s.ID),
			ParentID: s.ParentID,
		}
		if req.Profile == ProfileComplete {
			for _, n := range s.Native {
				sum := sha256.Sum256(n.Data)
				sm.Native = append(sm.Native, NativeFile{
					Name:    nativeName(n.Agent, n.Rel),
					RootRel: cleanRel(n.Rel),
					SHA256:  hex.EncodeToString(sum[:]),
					Size:    int64(len(n.Data)),
				})
			}
		}
		sessions = append(sessions, sm)
	}
	created := req.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	return Manifest{
		Format:     CurrentFormat,
		Version:    CurrentVersion,
		CreatedAt:  created.UTC(),
		AppVersion: req.AppVersion,
		Profile:    req.Profile,
		Source:     req.Source,
		Sessions:   sessions,
		Redaction:  req.Redaction,
		Handoff:    "handoff.md",
	}
}

func validateRequest(req WriteRequest) error {
	switch req.Profile {
	case ProfileComplete, ProfileShareSafe:
	default:
		return fmt.Errorf("bundle: unknown profile %q", req.Profile)
	}
	if !safeAgent(string(req.Source.Agent)) {
		return fmt.Errorf("bundle: unsafe source agent %q", req.Source.Agent)
	}
	if !safeID(req.Source.ID) {
		return fmt.Errorf("bundle: unsafe source id %q", req.Source.ID)
	}
	if req.Profile == ProfileShareSafe {
		for _, s := range req.Sessions {
			if len(s.Native) > 0 {
				return fmt.Errorf("bundle: share-safe profile cannot contain native records")
			}
		}
	}
	seen := map[string]bool{}
	for _, s := range req.Sessions {
		if !safeAgent(s.Agent) {
			return fmt.Errorf("bundle: unsafe session agent %q", s.Agent)
		}
		if !safeID(s.ID) {
			return fmt.Errorf("bundle: unsafe session id %q", s.ID)
		}
		if s.ParentID != "" && !safeID(s.ParentID) {
			return fmt.Errorf("bundle: unsafe parent id %q", s.ParentID)
		}
		for _, n := range s.Native {
			if !safeAgent(n.Agent) {
				return fmt.Errorf("bundle: unsafe native agent %q", n.Agent)
			}
			rel, err := safeRel(n.Rel)
			if err != nil {
				return fmt.Errorf("bundle: native path %q: %w", n.Rel, err)
			}
			name := nativeName(n.Agent, rel)
			if seen[name] {
				return fmt.Errorf("bundle: duplicate entry %s", name)
			}
			seen[name] = true
		}
	}
	if len(req.Transcript) == 0 {
		return fmt.Errorf("bundle: empty transcript")
	}
	return nil
}

func nativeName(agent, rel string) string {
	return "native/" + agent + "/" + cleanRel(rel)
}

func cleanRel(rel string) string {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
	return pathClean(rel)
}

// pathClean is filepath.Clean on slash-separated input without touching the OS
// separator, so the zip name is stable across platforms.
func pathClean(p string) string {
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return strings.Join(out, "/")
}
