package rpc

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// PrefacePrefix is the prefix used for the startup preface line.
const PrefacePrefix = "AGENT_SESSIONS_PREFACE_"

// GenerateNonce creates a secure 16-byte random hex string.
func GenerateNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// FormatPreface builds the exact preface string expected on stdout.
func FormatPreface(nonce string) string {
	return PrefacePrefix + nonce
}

// WritePreface writes the formatted preface line followed by a newline to w.
func WritePreface(w io.Writer, nonce string) error {
	line := FormatPreface(nonce) + "\n"
	_, err := w.Write([]byte(line))
	return err
}

// WaitForPreface scans r until it encounters the preface line for the given nonce.
// It skips login-shell banners and MOTDs up to MaxScanCapBytes (64 KiB).
// Returns an io.Reader positioned immediately after the preface line for RPC reading.
func WaitForPreface(ctx context.Context, r io.Reader, nonce string) (io.Reader, error) {
	expected := FormatPreface(nonce)
	br := bufio.NewReader(r)

	var totalRead int64
	for {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		line, err := readLineCapped(br, MaxScanCapBytes-totalRead)
		totalRead += int64(len(line))
		if err == errLineTooLong {
			return nil, ErrPrefaceNotFound
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == expected {
			// Found the preface! Return the buffered reader so subsequent bytes are not lost.
			return br, nil
		}

		if err != nil {
			if err == io.EOF {
				return nil, fmt.Errorf("%w: EOF reached before preface", ErrPrefaceNotFound)
			}
			return nil, err
		}
	}
}

var errLineTooLong = errors.New("line exceeds scan budget")

// readLineCapped reads through the next newline like ReadString, but gives
// up with errLineTooLong once the line passes budget bytes, so output with no
// newline cannot grow memory without bound.
func readLineCapped(br *bufio.Reader, budget int64) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := br.ReadSlice('\n')
		if int64(sb.Len()+len(chunk)) > budget {
			return sb.String(), errLineTooLong
		}
		sb.Write(chunk)
		if err == bufio.ErrBufferFull {
			continue
		}
		return sb.String(), err
	}
}

// WaitForPrefaceWithTimeout is a helper that wraps WaitForPreface with a timeout.
func WaitForPrefaceWithTimeout(r io.Reader, nonce string, timeout time.Duration) (io.Reader, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	type result struct {
		reader io.Reader
		err    error
	}

	ch := make(chan result, 1)
	go func() {
		reader, err := WaitForPreface(ctx, r, nonce)
		ch <- result{reader: reader, err: err}
	}()

	select {
	case res := <-ch:
		return res.reader, res.err
	case <-ctx.Done():
		return nil, ErrTimeout
	}
}
