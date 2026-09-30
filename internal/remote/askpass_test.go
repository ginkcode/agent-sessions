package remote

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAskpass_RoundTrip(t *testing.T) {
	var mu sync.Mutex
	var receivedID, receivedPrompt string
	promptArrived := make(chan struct{})

	broker, err := NewAskpassBroker(func(id, prompt string) {
		mu.Lock()
		receivedID = id
		receivedPrompt = prompt
		mu.Unlock()
		close(promptArrived)
	})
	if err != nil {
		t.Fatalf("NewAskpassBroker failed: %v", err)
	}
	defer broker.Close()

	// Verify permissions
	info, err := os.Stat(broker.dir)
	if err != nil {
		t.Fatalf("stat socket dir failed: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("socket dir perm = %o, want 0700", info.Mode().Perm())
	}

	testPrompt := "Enter passphrase for key '/id_ed25519':"
	testAnswer := "super-secret-passphrase-1234"

	var stdout, stderr bytes.Buffer
	helperDone := make(chan error, 1)

	go func() {
		err := RunAskpassHelper(
			context.Background(),
			broker.SocketPath(),
			broker.Token(),
			testPrompt,
			&stdout,
			&stderr,
		)
		helperDone <- err
	}()

	// Wait for prompt to arrive at broker handler
	select {
	case <-promptArrived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt to arrive")
	}

	mu.Lock()
	if receivedPrompt != testPrompt {
		t.Errorf("got prompt %q, want %q", receivedPrompt, testPrompt)
	}
	idToReply := receivedID
	mu.Unlock()

	// Reply with answer
	if !broker.Reply(idToReply, testAnswer) {
		t.Fatalf("failed to reply to prompt %q", idToReply)
	}

	// Helper should complete
	select {
	case err := <-helperDone:
		if err != nil {
			t.Fatalf("RunAskpassHelper failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RunAskpassHelper to complete")
	}

	output := strings.TrimSpace(stdout.String())
	if output != testAnswer {
		t.Errorf("got stdout %q, want %q", output, testAnswer)
	}
}

func TestAskpass_InvalidToken(t *testing.T) {
	broker, err := NewAskpassBroker(nil)
	if err != nil {
		t.Fatalf("NewAskpassBroker failed: %v", err)
	}
	defer broker.Close()

	var stdout, stderr bytes.Buffer
	err = RunAskpassHelper(
		context.Background(),
		broker.SocketPath(),
		"wrong-token",
		"Enter password:",
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("expected error with invalid token, got nil")
	}
	if !strings.Contains(err.Error(), "invalid askpass token") {
		t.Errorf("expected 'invalid askpass token', got: %v", err)
	}
}

func TestAskpass_Cancelled(t *testing.T) {
	promptArrived := make(chan string, 1)
	broker, err := NewAskpassBroker(func(id, prompt string) {
		promptArrived <- id
	})
	if err != nil {
		t.Fatalf("NewAskpassBroker failed: %v", err)
	}
	defer broker.Close()

	var stdout, stderr bytes.Buffer
	helperDone := make(chan error, 1)

	go func() {
		err := RunAskpassHelper(
			context.Background(),
			broker.SocketPath(),
			broker.Token(),
			"Host key confirmation:",
			&stdout,
			&stderr,
		)
		helperDone <- err
	}()

	var id string
	select {
	case id = <-promptArrived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt")
	}

	// Cancel the prompt
	broker.Cancel(id)

	select {
	case err := <-helperDone:
		if err == nil {
			t.Fatal("expected error on cancelled prompt, got nil")
		}
		if !strings.Contains(err.Error(), "prompt cancelled") {
			t.Errorf("expected 'prompt cancelled', got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for helper after cancel")
	}
}
