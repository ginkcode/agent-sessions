// Package configfile updates the user's config.toml, which several tables
// share: [manage] for the engine, [terminal] and [translate] for the desktop
// app. Changes are made under one lock and edit only their own keys, so
// concurrent updates of different keys all survive and unknown keys, tables
// and comments are kept.
package configfile

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/filelock"
)

// LockWait bounds how long an update waits for another process holding the
// config lock, so a stalled holder cannot hang a settings call.
const LockWait = 10 * time.Second

// Lock takes the lock every writer of path takes: path+".lock".
func Lock(path string) (*filelock.Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	lock, err := filelock.Acquire(path+".lock", LockWait)
	if err != nil {
		return nil, fmt.Errorf("lock config: %w", err)
	}
	return lock, nil
}

// Update rewrites path under the lock: it reads the file as it is now (empty
// when missing), applies edit and writes the result atomically.
func Update(path string, edit func([]byte) []byte) error {
	if path == "" {
		return fmt.Errorf("empty config path")
	}
	lock, err := Lock(path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read config: %w", err)
	}
	return WriteAtomic(path, edit(data))
}

// WriteAtomic replaces path with data through a synced 0600 temp file in the
// same 0700 directory.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	// Enforce 0700 on an existing directory as well.
	_ = os.Chmod(dir, 0o700)

	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// Value returns the string value of key in [table]: quoted or bare, with a
// trailing comment removed.
func Value(data []byte, table, key string) (string, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	in := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if name, ok := tableName(line); ok {
			in = name == table
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		return parseValue(strings.TrimSpace(v)), true
	}
	return "", false
}

func parseValue(v string) string {
	if len(v) > 0 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : 1+end]
		}
		return v[1:]
	}
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// SetValue returns data with key in [table] set to value, a TOML literal
// such as `"konsole"`. The key is replaced where it is, added at the end of
// its table, or added with a new table at the end of the file. Every other
// line is kept as it was.
func SetValue(data []byte, table, key, value string) []byte {
	entry := key + " = " + value
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	var out []string
	in, seen, written := false, false, false
	// closeTable adds the key at the end of the table, before its trailing
	// blank lines.
	closeTable := func() {
		if !in || written {
			return
		}
		end := len(out)
		for end > 0 && strings.TrimSpace(out[end-1]) == "" {
			end--
		}
		out = append(out[:end], append([]string{entry}, out[end:]...)...)
		written = true
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if name, ok := tableName(trimmed); ok {
			closeTable()
			in = name == table
			seen = seen || in
			out = append(out, line)
			continue
		}
		if in {
			if k, _, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(k) == key {
				if !written {
					out = append(out, entry)
					written = true
				}
				continue
			}
		}
		out = append(out, line)
	}
	closeTable()
	if !seen {
		if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
			out = append(out, "")
		}
		out = append(out, "["+table+"]", entry)
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

func tableName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return "", false
	}
	return strings.TrimSpace(line[1 : len(line)-1]), true
}
