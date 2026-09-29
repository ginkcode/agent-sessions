package bundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Bundle is a validated archive. Native holds verbatim file bytes keyed by the
// zip-internal name (native/<agent>/...). Nothing here is a filesystem path.
type Bundle struct {
	Manifest   Manifest
	Transcript []byte
	Handoff    string
	Native     map[string][]byte
}

// ReadFile opens and validates a bundle from disk.
func ReadFile(path string) (*Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bundle: open: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("bundle: stat: %w", err)
	}
	return Read(f, info.Size())
}

// Read reads and validates a bundle. size is the zip's total byte length,
// which archive/zip needs in order to find the central directory.
func Read(r io.ReaderAt, size int64) (*Bundle, error) {
	if size < 0 || size > MaxTotalBytes {
		return nil, fmt.Errorf("bundle: archive size %d out of range", size)
	}
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("bundle: read zip: %w", err)
	}
	return readZip(zr)
}

// ReadBytes validates a bundle held entirely in memory.
func ReadBytes(data []byte) (*Bundle, error) {
	return Read(bytes.NewReader(data), int64(len(data)))
}

func readZip(zr *zip.Reader) (*Bundle, error) {
	if len(zr.File) > MaxEntries {
		return nil, fmt.Errorf("bundle: more than %d entries", MaxEntries)
	}
	b := &Bundle{Native: map[string][]byte{}}
	seen := map[string]bool{}
	var total uint64

	for _, f := range zr.File {
		name := f.Name
		if err := safeZipName(name); err != nil {
			return nil, fmt.Errorf("bundle: %w", err)
		}
		if seen[name] {
			return nil, fmt.Errorf("bundle: duplicate entry %s", name)
		}
		seen[name] = true
		if f.FileInfo().IsDir() {
			return nil, fmt.Errorf("bundle: directory entry %s", name)
		}
		if f.UncompressedSize64 > MaxEntryBytes {
			return nil, fmt.Errorf("bundle: entry %s exceeds %d bytes", name, MaxEntryBytes)
		}
		if f.UncompressedSize64 > 0 && f.CompressedSize64 > 0 {
			ratio := f.UncompressedSize64 / f.CompressedSize64
			if ratio > MaxCompression && f.UncompressedSize64 > 1<<20 {
				return nil, fmt.Errorf("bundle: entry %s compression ratio %d exceeds %d", name, ratio, MaxCompression)
			}
		}
		total += f.UncompressedSize64
		if total > MaxTotalBytes {
			return nil, fmt.Errorf("bundle: bundle exceeds %d bytes", MaxTotalBytes)
		}

		data, err := readEntry(f)
		if err != nil {
			return nil, fmt.Errorf("bundle: read %s: %w", name, err)
		}
		switch name {
		case "manifest.json":
			if err := json.Unmarshal(data, &b.Manifest); err != nil {
				return nil, fmt.Errorf("bundle: manifest: %w", err)
			}
		case "transcript.json":
			b.Transcript = data
		case "handoff.md":
			b.Handoff = string(data)
		default:
			b.Native[name] = data
		}
	}

	if err := validateBundle(b); err != nil {
		return nil, err
	}
	return b, nil
}

func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// Read one extra byte so a lying header is caught.
	limit := int64(f.UncompressedSize64) + 1
	data, err := io.ReadAll(io.LimitReader(rc, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != int64(f.UncompressedSize64) {
		return nil, fmt.Errorf("size mismatch: header %d, got %d", f.UncompressedSize64, len(data))
	}
	return data, nil
}

func validateBundle(b *Bundle) error {
	m := b.Manifest
	if m.Format != CurrentFormat {
		return fmt.Errorf("bundle: unknown format %q", m.Format)
	}
	if m.Version != CurrentVersion {
		return fmt.Errorf("bundle: unsupported version %d", m.Version)
	}
	switch m.Profile {
	case ProfileComplete, ProfileShareSafe:
	default:
		return fmt.Errorf("bundle: unknown profile %q", m.Profile)
	}
	if m.Handoff != "handoff.md" {
		return fmt.Errorf("bundle: handoff path %q is not handoff.md", m.Handoff)
	}
	if b.Transcript == nil {
		return fmt.Errorf("bundle: missing transcript.json")
	}
	if !json.Valid(b.Transcript) {
		return fmt.Errorf("bundle: transcript.json is not valid JSON")
	}
	if !safeAgent(string(m.Source.Agent)) {
		return fmt.Errorf("bundle: unsafe source agent %q", m.Source.Agent)
	}
	if !safeID(m.Source.ID) {
		return fmt.Errorf("bundle: unsafe source id %q", m.Source.ID)
	}

	declared := map[string]NativeFile{}
	for _, s := range m.Sessions {
		if !safeAgent(string(s.Ref.Agent)) {
			return fmt.Errorf("bundle: unsafe session agent %q", s.Ref.Agent)
		}
		if !safeID(s.Ref.ID) {
			return fmt.Errorf("bundle: unsafe session id %q", s.Ref.ID)
		}
		if s.ParentID != "" && !safeID(s.ParentID) {
			return fmt.Errorf("bundle: unsafe parent id %q", s.ParentID)
		}
		for _, n := range s.Native {
			if _, ok := declared[n.Name]; ok {
				return fmt.Errorf("bundle: duplicate native declaration %s", n.Name)
			}
			if err := safeZipName(n.Name); err != nil {
				return fmt.Errorf("bundle: declared %w", err)
			}
			if !strings.HasPrefix(n.Name, "native/") {
				return fmt.Errorf("bundle: declared entry %q is not under native/", n.Name)
			}
			if _, err := safeRel(n.RootRel); err != nil {
				return fmt.Errorf("bundle: declared root path %q: %w", n.RootRel, err)
			}
			declared[n.Name] = n
		}
	}

	if m.Profile == ProfileShareSafe && (len(declared) > 0 || len(b.Native) > 0) {
		return fmt.Errorf("bundle: share-safe profile cannot contain native records")
	}

	for name, data := range b.Native {
		n, ok := declared[name]
		if !ok {
			return fmt.Errorf("bundle: entry %s is not declared in the manifest", name)
		}
		if int64(len(data)) != n.Size {
			return fmt.Errorf("bundle: entry %s size %d does not match declared %d", name, len(data), n.Size)
		}
		sum := sha256.Sum256(data)
		got := hex.EncodeToString(sum[:])
		if got != strings.ToLower(n.SHA256) {
			return fmt.Errorf("bundle: entry %s checksum mismatch", name)
		}
	}
	for name := range declared {
		if _, ok := b.Native[name]; !ok {
			return fmt.Errorf("bundle: declared entry %s is missing", name)
		}
	}
	return nil
}
