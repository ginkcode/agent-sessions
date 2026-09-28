// Command fixture-sanitize turns Claude Code JSONL sessions into deterministic,
// privacy-preserving test fixtures. It is a development tool, not part of the
// application runtime.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	defaultMaxLines = 400
	// A transparent 1×1 PNG. Replacing image payloads preserves the image
	// content block's shape while avoiding any original image data.
	pixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/lXcAAAAASUVORK5CYII="
	filler   = "loremipsumdolorsitametconsecteturadipiscingelit"
)

var homePath = regexp.MustCompile(`/home/[^/]+/`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fixture-sanitize:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("fixture-sanitize", flag.ContinueOnError)
	in := fs.String("in", "", "source JSONL file")
	out := fs.String("out", "", "sanitized JSONL file")
	maxLines := fs.Int("max-lines", defaultMaxLines, "maximum records to write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *in == "" || *out == "" {
		return errors.New("usage: fixture-sanitize -in source.jsonl -out fixture.jsonl [-max-lines 400]")
	}
	if *maxLines < 1 {
		return errors.New("-max-lines must be positive")
	}
	if err := checkInputPath(*in); err != nil {
		return err
	}

	input, err := os.Open(*in)
	if err != nil {
		return fmt.Errorf("open input: %w", err)
	}
	defer func() { _ = input.Close() }()

	// Do not truncate the input if both paths refer to the same file, including
	// through a hard link or symlink.
	if info, err := os.Stat(*out); err == nil {
		inputInfo, statErr := input.Stat()
		if statErr != nil {
			return fmt.Errorf("stat input: %w", statErr)
		}
		if os.SameFile(inputInfo, info) {
			return errors.New("input and output must be different files")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat output: %w", err)
	}

	// Write atomically so a malformed input never leaves a misleadingly valid
	// partial fixture (or clobbers an earlier good one).
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(*out), ".fixture-sanitize-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	if err := sanitize(input, f, *maxLines); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Rename(f.Name(), *out); err != nil {
		return fmt.Errorf("replace output: %w", err)
	}
	return nil
}

func checkInputPath(path string) error {
	// Check both the supplied spelling and the resolved path; symlinks must
	// not be able to smuggle protected files through an innocuous alias.
	if protectedPath(path) {
		return errors.New("refusing to read a credentials or auth.json path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil && protectedPath(resolved) {
		return errors.New("refusing to read a credentials or auth.json path")
	}
	return nil
}

func protectedPath(path string) bool {
	path = filepath.ToSlash(path)
	return strings.Contains(path, ".credentials") || strings.Contains(path, "auth.json")
}

func sanitize(src io.Reader, dst io.Writer, maxLines int) error {
	// Reader rather than Scanner handles records over Scanner's 64 KiB default.
	r := bufio.NewReader(src)
	w := bufio.NewWriter(dst)
	for lineNo := 1; lineNo <= maxLines; lineNo++ {
		line, err := r.ReadBytes('\n')
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read line %d: %w", lineNo, err)
		}

		var record map[string]any
		if e := json.Unmarshal(line, &record); e != nil {
			return fmt.Errorf("parse line %d: %w", lineNo, e)
		}
		if record == nil {
			return fmt.Errorf("parse line %d: expected JSON object", lineNo)
		}
		sanitizeValue(record, false, false)
		encoded, e := json.Marshal(record)
		if e != nil {
			return fmt.Errorf("marshal line %d: %w", lineNo, e)
		}
		if _, e := w.Write(encoded); e != nil {
			return fmt.Errorf("write line %d: %w", lineNo, e)
		}
		if e := w.WriteByte('\n'); e != nil {
			return fmt.Errorf("write newline %d: %w", lineNo, e)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush output: %w", err)
	}
	return nil
}

// sanitizeValue walks decoded JSON in place. redactAll applies to the leaf
// strings inside tool input/results; image marks image sources so only image
// data gets replaced, not unrelated fields also named "data".
func sanitizeValue(value any, redactAll, image bool) any {
	switch v := value.(type) {
	case map[string]any:
		if v["type"] == "image" || isImageMediaType(v["media_type"]) {
			image = true
		}
		for key, child := range v {
			if key == "data" && image {
				if _, ok := child.(string); ok {
					v[key] = pixelPNG
					continue
				}
			}
			redact := redactAll || redactedKey(key, child)
			v[key] = sanitizeValue(child, redact, image)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = sanitizeValue(child, redactAll, image)
		}
		return v
	case string:
		if redactAll {
			return redacted(v)
		}
		return homePath.ReplaceAllString(v, "/home/dev/")
	default:
		return v
	}
}

func redactedKey(key string, child any) bool {
	switch key {
	case "text", "thinking", "lastPrompt", "aiTitle", "summary", "rendered", "description":
		_, ok := child.(string)
		return ok
	case "content":
		_, ok := child.(string) // preserve structured content blocks
		return ok
	case "input", "output", "toolUseResult":
		return true
	default:
		return false
	}
}

func isImageMediaType(value any) bool {
	mediaType, ok := value.(string)
	return ok && strings.HasPrefix(mediaType, "image/")
}

func redacted(original string) string {
	n := min(utf8.RuneCountInString(original), 200)
	if n == 0 {
		return ""
	}
	hash := sha256.Sum256([]byte(original))
	var out strings.Builder
	out.Grow(n)
	start := int(hash[0]) % len(filler)
	for i := range n {
		out.WriteByte(filler[(start+i)%len(filler)])
	}
	return out.String()
}
