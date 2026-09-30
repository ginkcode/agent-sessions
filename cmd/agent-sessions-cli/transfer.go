package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/ginkcode/agent-sessions/internal/paths"
)

// MaxArtifactBytes bounds uploaded artifacts to 512 MiB.
const MaxArtifactBytes = 512 << 20

// validTokenRe ensures the token cannot traverse directories or contain control chars.
var validTokenRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func transferCmd(args []string, roots paths.Roots, stdout, stderr io.Writer, stdin io.Reader) int {
	if len(args) < 2 {
		_, _ = fmt.Fprintln(stderr, "Usage: agent-sessions-cli transfer <get|put> <token> [--remove] [--cache <dir>]")
		return 2
	}

	action := args[0]
	token := args[1]

	if !validTokenRe.MatchString(token) {
		_, _ = fmt.Fprintln(stderr, "agent-sessions-cli transfer: invalid token (must be 1-128 chars of [a-zA-Z0-9_-])")
		return 2
	}
	if action != "get" && action != "put" {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: unknown action %q (expected get or put)\n", action)
		return 2
	}

	fs := flag.NewFlagSet("transfer "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	removeAfter := fs.Bool("remove", false, "remove artifact after streaming (get only)")
	// The serve session reports its cache dir after the client's env
	// overrides; transfer runs in a fresh ssh command without them.
	cacheDir := fs.String("cache", "", "cache directory reported by serve (absolute)")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	if *cacheDir != "" {
		if !filepath.IsAbs(*cacheDir) {
			_, _ = fmt.Fprintln(stderr, "agent-sessions-cli transfer: --cache must be an absolute path")
			return 2
		}
		roots.Cache = filepath.Clean(*cacheDir)
	}

	stagingDir := filepath.Join(roots.Cache, "staging")
	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: create staging dir: %v\n", err)
		return 1
	}

	// Best-effort cleanup of old staging files (> 24 hours)
	pruneStaging(stagingDir, 24*time.Hour)

	targetFile := filepath.Join(stagingDir, token)

	if action == "put" {
		return transferPut(targetFile, stagingDir, token, stdin, stdout, stderr)
	}
	return transferGet(targetFile, *removeAfter, stdout, stderr)
}

func transferPut(targetFile, stagingDir, token string, stdin io.Reader, stdout, stderr io.Writer) int {
	tmpFile := filepath.Join(stagingDir, ".tmp-"+token)
	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: create temp file: %v\n", err)
		return 1
	}

	h := sha256.New()
	mw := io.MultiWriter(f, h)
	limitR := io.LimitReader(stdin, MaxArtifactBytes+1)

	n, err := io.Copy(mw, limitR)
	_ = f.Close()

	if err != nil {
		_ = os.Remove(tmpFile)
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: write error: %v\n", err)
		return 1
	}

	if n > MaxArtifactBytes {
		_ = os.Remove(tmpFile)
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: artifact exceeds maximum size (%d bytes)\n", MaxArtifactBytes)
		return 1
	}

	if err := os.Rename(tmpFile, targetFile); err != nil {
		_ = os.Remove(tmpFile)
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: commit file: %v\n", err)
		return 1
	}

	sumHex := hex.EncodeToString(h.Sum(nil))
	_, _ = fmt.Fprintf(stdout, "sha256:%s\n", sumHex)
	return 0
}

func transferGet(targetFile string, removeAfter bool, stdout, stderr io.Writer) int {
	f, err := os.Open(targetFile)
	if err != nil {
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: artifact not found\n")
		} else {
			_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: open artifact: %v\n", err)
		}
		return 1
	}
	defer f.Close()

	if _, err := io.Copy(stdout, f); err != nil {
		_, _ = fmt.Fprintf(stderr, "agent-sessions-cli transfer: stream error: %v\n", err)
		return 1
	}

	if removeAfter {
		_ = os.Remove(targetFile)
	}

	return 0
}

func pruneStaging(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
