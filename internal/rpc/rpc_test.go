package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

func TestFrameReader_Limit(t *testing.T) {
	// A reader with a 100-byte limit
	limit := int64(100)
	smallData := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	bigData := `{"jsonrpc":"2.0","id":2,"method":"` + strings.Repeat("x", 120) + `"}`

	input := smallData + "\n" + bigData + "\n" + smallData + "\n"
	r := NewFrameReaderWithLimit(strings.NewReader(input), limit)

	// 1. First small frame succeeds
	frame1, err := r.ReadFrame()
	if err != nil {
		t.Fatalf("first frame error: %v", err)
	}
	if string(frame1) != smallData {
		t.Errorf("got %s, want %s", frame1, smallData)
	}

	// 2. Second frame exceeds limit
	_, err = r.ReadFrame()
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got: %v", err)
	}

	// 3. Third frame is small and should be readable after recovery
	frame3, err := r.ReadFrame()
	if err != nil {
		t.Fatalf("third frame error: %v", err)
	}
	if string(frame3) != smallData {
		t.Errorf("got %s, want %s", frame3, smallData)
	}
}

func TestFrameWriter_Concurrent(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func(id int) {
			defer wg.Done()
			req := Request{
				JSONRPC: JSONRPCVersion,
				ID:      id,
				Method:  "test",
			}
			if err := w.WriteFrame(req); err != nil {
				t.Errorf("write error: %v", err)
			}
		}(i)
	}

	wg.Wait()

	r := NewFrameReader(&buf)
	readCount := 0
	for {
		frame, err := r.ReadFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read error: %v", err)
		}
		if len(frame) > 0 {
			readCount++
		}
	}

	if readCount != n {
		t.Fatalf("read %d frames, want %d", readCount, n)
	}
}

func TestPreface_WaitForPreface(t *testing.T) {
	nonce := GenerateNonce()
	banner := "Welcome to Ubuntu 22.04 LTS (GNU/Linux 5.15.0-generic x86_64)\n" +
		" * Documentation:  https://help.ubuntu.com\n" +
		"Last login: Mon Sep 30 00:00:00 2026 from 192.168.1.1\n"

	prefaceLine := FormatPreface(nonce) + "\n"
	firstRpcLine := `{"jsonrpc":"2.0","method":"ready"}` + "\n"

	stream := strings.NewReader(banner + prefaceLine + firstRpcLine)

	r, err := WaitForPreface(context.Background(), stream, nonce)
	if err != nil {
		t.Fatalf("WaitForPreface failed: %v", err)
	}

	// Read remainder - it must immediately yield firstRpcLine
	fr := NewFrameReader(r)
	frame, err := fr.ReadFrame()
	if err != nil {
		t.Fatalf("failed to read RPC frame after preface: %v", err)
	}
	if string(frame) != `{"jsonrpc":"2.0","method":"ready"}` {
		t.Errorf("got %s, want ready notification", frame)
	}
}

func TestPreface_ScanCapExceeded(t *testing.T) {
	nonce := GenerateNonce()
	// Create garbage lines that exceed 64 KiB
	garbage := strings.Repeat("A very long banner line to fill up the 64 KiB preface scanner limit\n", 1500)
	stream := strings.NewReader(garbage + FormatPreface(nonce) + "\n")

	_, err := WaitForPreface(context.Background(), stream, nonce)
	if !errors.Is(err, ErrPrefaceNotFound) {
		t.Fatalf("expected ErrPrefaceNotFound, got %v", err)
	}
}

func TestErrors_RoundTripSentinels(t *testing.T) {
	testCases := []error{
		engine.ErrUnknownSession,
		engine.ErrUnknownProvider,
		engine.ErrUnknownGroup,
		engine.ErrManageDisabled,
		engine.ErrPreviewStale,
		engine.ErrSessionLive,
		engine.ErrPathOutsideRoot,
		engine.ErrUnsupportedAction,
		ErrRemoteBusy,
		ErrDisconnected,
		ErrCancelled,
		ErrProtocolMismatch,
		ErrPayloadTooLarge,
		ErrTimeout,
	}

	for _, tc := range testCases {
		rpcErr := ToRPCError(tc)
		if rpcErr == nil {
			t.Fatalf("ToRPCError returned nil for %v", tc)
		}

		reconstructed := FromRPCError(rpcErr)
		if !errors.Is(reconstructed, tc) {
			t.Errorf("FromRPCError: expected errors.Is(..., %v) to be true, got %v", tc, reconstructed)
		}
	}
}

func TestCancelRegistry(t *testing.T) {
	reg := NewCancelRegistry()
	ctx, cancel := context.WithCancel(context.Background())

	reg.Register("req-1", cancel)
	if ctx.Err() != nil {
		t.Fatal("context should not be cancelled yet")
	}

	cancelled := reg.Cancel("req-1")
	if !cancelled {
		t.Error("expected Cancel to return true")
	}
	if ctx.Err() != context.Canceled {
		t.Errorf("context was not cancelled, err = %v", ctx.Err())
	}

	// Cancelling again returns false
	if reg.Cancel("req-1") {
		t.Error("cancelling again should return false")
	}
}

func TestAcquireCacheLock(t *testing.T) {
	tmpDir := t.TempDir()

	lock1, err := AcquireCacheLock(tmpDir)
	if err != nil {
		t.Fatalf("first AcquireCacheLock failed: %v", err)
	}
	defer lock1.Unlock()

	// Second acquire should fail with ErrRemoteBusy
	_, err = AcquireCacheLock(tmpDir)
	if !errors.Is(err, ErrRemoteBusy) {
		t.Fatalf("expected ErrRemoteBusy, got %v", err)
	}

	// Unlock first
	if err := lock1.Unlock(); err != nil {
		t.Fatalf("unlock failed: %v", err)
	}

	// Now second acquire should succeed
	lock2, err := AcquireCacheLock(tmpDir)
	if err != nil {
		t.Fatalf("second AcquireCacheLock failed after unlock: %v", err)
	}
	_ = lock2.Unlock()
}
