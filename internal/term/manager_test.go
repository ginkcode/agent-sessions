//go:build !windows

package term

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// recorder collects what a manager emits.
type recorder struct {
	mu     sync.Mutex
	data   map[string]*bytes.Buffer
	events map[string]int
	exits  map[string]int
	order  []string
	notify chan struct{}
}

func newRecorder() *recorder {
	return &recorder{data: map[string]*bytes.Buffer{}, events: map[string]int{}, exits: map[string]int{}, notify: make(chan struct{}, 1)}
}

func (r *recorder) Emit(name string, payload any) {
	r.mu.Lock()
	switch p := payload.(type) {
	case DataPayload:
		b, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			panic(err)
		}
		if r.data[p.ID] == nil {
			r.data[p.ID] = &bytes.Buffer{}
		}
		r.data[p.ID].Write(b)
		r.events[p.ID]++
		r.order = append(r.order, name)
	case ExitPayload:
		r.exits[p.ID] = p.Code
		r.order = append(r.order, name)
	}
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *recorder) output(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b := r.data[id]; b != nil {
		return b.String()
	}
	return ""
}

func (r *recorder) exit(id string) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	code, ok := r.exits[id]
	return code, ok
}

// waitFor polls cond until it holds or the test times out.
func (r *recorder) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case <-r.notify:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (r *recorder) waitOutput(t *testing.T, id, want string) {
	t.Helper()
	r.waitFor(t, "output "+strconv.Quote(want), func() bool { return strings.Contains(r.output(id), want) })
}

func (r *recorder) waitExit(t *testing.T, id string) int {
	t.Helper()
	var code int
	r.waitFor(t, "exit of "+id, func() bool {
		c, ok := r.exit(id)
		code = c
		return ok
	})
	return code
}

func sh(script string) Spec {
	return Spec{Argv: []string{"/bin/sh", "-c", script}, Cols: 80, Rows: 24}
}

func TestStartEchoesInput(t *testing.T) {
	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	tab, err := m.Start(sh(`read -r line; echo "got:$line"`), Tab{Label: "sh", Kind: KindShell, Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if tab.ID == "" || tab.Label != "sh" || tab.Kind != KindShell || tab.Host != "h" {
		t.Fatalf("tab = %+v", tab)
	}
	if since := time.Since(time.UnixMilli(tab.StartedAt)); since < 0 || since > time.Minute {
		t.Fatalf("StartedAt = %d, %s ago", tab.StartedAt, since)
	}
	if !m.Running("h") || m.Running("") {
		t.Fatal("Running should report the host of the running terminal only")
	}
	if got, err := m.Tab(tab.ID); err != nil || got != tab {
		t.Fatalf("Tab = %+v, %v; want %+v", got, err, tab)
	}
	if err := m.Write(tab.ID, []byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	rec.waitOutput(t, tab.ID, "got:hello")
	if code := rec.waitExit(t, tab.ID); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if m.Running("h") {
		t.Fatal("an exited terminal still counts as running")
	}
	if err := m.Write(tab.ID, []byte("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Write after exit = %v, want ErrNotFound", err)
	}
	if _, err := m.Tab(tab.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Tab after exit = %v, want ErrNotFound", err)
	}
	if err := m.Close(tab.ID); err != nil {
		t.Fatalf("Close after exit = %v", err)
	}
}

func TestStartSetsEnvAndDir(t *testing.T) {
	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	dir := t.TempDir()
	spec := sh(`echo "env:$TERM:$COLORTERM:$FOO"; pwd`)
	spec.Dir = dir
	spec.Env = []string{"FOO=bar", "TERM=dumb", "PATH=" + os.Getenv("PATH")}
	tab, err := m.Start(spec, Tab{})
	if err != nil {
		t.Fatal(err)
	}
	rec.waitExit(t, tab.ID)
	out := rec.output(tab.ID)
	if !strings.Contains(out, "env:xterm-256color:truecolor:bar") {
		t.Fatalf("output %q lacks the terminal environment", out)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(out, dir) && !strings.Contains(out, real) {
		t.Fatalf("output %q lacks the directory %s", out, dir)
	}
}

func TestStartFailures(t *testing.T) {
	m := NewManager(newRecorder())
	if _, err := m.Start(Spec{}, Tab{}); err == nil {
		t.Fatal("empty argv started")
	}
	if _, err := m.Start(Spec{Argv: []string{"/no/such/binary"}}, Tab{}); err == nil {
		t.Fatal("missing binary started")
	}
}

func TestResize(t *testing.T) {
	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	spec := sh(`stty size; read -r _; stty size`)
	spec.Cols, spec.Rows = 100, 30
	tab, err := m.Start(spec, Tab{})
	if err != nil {
		t.Fatal(err)
	}
	rec.waitOutput(t, tab.ID, "30 100")
	if err := m.Resize(tab.ID, 132, 43); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(tab.ID, 0, 10); err == nil {
		t.Fatal("zero size accepted")
	}
	if err := m.Write(tab.ID, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	rec.waitOutput(t, tab.ID, "43 132")
}

func TestExitCodes(t *testing.T) {
	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	tab, err := m.Start(sh(`exit 7`), Tab{})
	if err != nil {
		t.Fatal(err)
	}
	if code := rec.waitExit(t, tab.ID); code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
	}
	tab, err = m.Start(sh(`kill -TERM $$`), Tab{})
	if err != nil {
		t.Fatal(err)
	}
	if code := rec.waitExit(t, tab.ID); code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code = %d, want %d", code, 128+int(syscall.SIGTERM))
	}
}

func TestOutputIsBatchedAndComplete(t *testing.T) {
	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	// 2000 short writes arrive as far fewer events, in order, before the
	// exit.
	tab, err := m.Start(sh(`i=0; while [ $i -lt 2000 ]; do echo "line $i"; i=$((i+1)); done`), Tab{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			if _, ok := rec.exit(tab.ID); ok {
				return
			}
			m.Ack(tab.ID, 1<<20)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	rec.waitExit(t, tab.ID)
	out := strings.ReplaceAll(rec.output(tab.ID), "\r\n", "\n")
	if !strings.HasPrefix(out, "line 0\n") || !strings.HasSuffix(out, "line 1999\n") || strings.Count(out, "\n") != 2000 {
		t.Fatalf("output incomplete: %d lines, ends %q", strings.Count(out, "\n"), out[max(0, len(out)-20):])
	}
	rec.mu.Lock()
	events := rec.events[tab.ID]
	last := rec.order[len(rec.order)-1]
	rec.mu.Unlock()
	if events >= 1000 {
		t.Fatalf("%d data events for 2000 writes: output is not batched", events)
	}
	if last != ExitEvent {
		t.Fatalf("last event = %s, want %s", last, ExitEvent)
	}
}

func TestFlowControlPausesReading(t *testing.T) {
	old := maxUnacked
	maxUnacked = 64 << 10
	defer func() { maxUnacked = old }()

	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	// yes writes without end; without acknowledgements the reader stops
	// soon after maxUnacked.
	tab, err := m.Start(Spec{Argv: []string{"yes"}}, Tab{})
	if err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, "first output", func() bool { return len(rec.output(tab.ID)) > int(maxUnacked) })
	time.Sleep(200 * time.Millisecond)
	paused := len(rec.output(tab.ID))
	time.Sleep(200 * time.Millisecond)
	if got := len(rec.output(tab.ID)); got != paused {
		t.Fatalf("output grew from %d to %d without acknowledgements", paused, got)
	}
	if limit := maxUnacked + flushSize + readChunk; paused > limit {
		t.Fatalf("%d bytes sent unacknowledged, limit %d", paused, limit)
	}
	m.Ack(tab.ID, paused)
	rec.waitFor(t, "output after the ack", func() bool { return len(rec.output(tab.ID)) > paused })
	if err := m.Close(tab.ID); err != nil {
		t.Fatal(err)
	}
	rec.waitExit(t, tab.ID)
}

func TestCloseKillsProcessGroup(t *testing.T) {
	// The grandchild ignores SIGHUP, so it takes the SIGKILL to end it.
	t.Run("holding the terminal", func(t *testing.T) {
		testCloseKillsGrandchild(t, `sh -c 'trap "" HUP; echo pid:$$; exec sleep 60' & wait`)
	})
	// Here it lets go of the terminal, so the tab ends with the shell, before
	// the kill, as every tab does on macOS. The kill still comes.
	t.Run("after the tab ends", func(t *testing.T) {
		testCloseKillsGrandchild(t, `sh -c 'trap "" HUP; echo pid:$$; exec sleep 60 </dev/null >/dev/null 2>&1' & wait`)
	})
}

func testCloseKillsGrandchild(t *testing.T, script string) {
	old := killGrace
	killGrace = 300 * time.Millisecond
	defer func() { killGrace = old }()

	rec := newRecorder()
	m := NewManager(rec)
	defer m.CloseAll()
	tab, err := m.Start(sh(script), Tab{})
	if err != nil {
		t.Fatal(err)
	}
	rec.waitOutput(t, tab.ID, "\n")
	var pid int
	rec.waitFor(t, "grandchild pid", func() bool {
		out := rec.output(tab.ID)
		i := strings.Index(out, "pid:")
		if i < 0 {
			return false
		}
		line, _, ok := strings.Cut(out[i+4:], "\n")
		if !ok {
			return false
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(line))
		return pid > 0
	})
	if err := m.Close(tab.ID); err != nil {
		t.Fatal(err)
	}
	rec.waitExit(t, tab.ID)
	rec.waitFor(t, "the grandchild to die", func() bool {
		return syscall.Kill(pid, 0) != nil
	})
}

func TestCloseAll(t *testing.T) {
	rec := newRecorder()
	m := NewManager(rec)
	var ids []string
	for i := 0; i < 3; i++ {
		tab, err := m.Start(sh(`sleep 60`), Tab{Host: "h"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tab.ID)
	}
	m.CloseAll()
	for _, id := range ids {
		if _, ok := rec.exit(id); !ok {
			t.Fatalf("%s did not exit before CloseAll returned", id)
		}
	}
	if m.Running("h") {
		t.Fatal("terminals still running after CloseAll")
	}
	if _, err := m.Start(sh(`true`), Tab{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after CloseAll = %v, want ErrClosed", err)
	}
}

func TestParseEnv(t *testing.T) {
	out := []byte("motd from .bashrc\n" + envMarker + "\nA=1\x00B=two\nlines\x00junk\x00=x\x00")
	got := parseEnv(out)
	want := []string{"A=1", "B=two\nlines"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("parseEnv = %q, want %q", got, want)
	}
	if parseEnv([]byte("A=1\x00")) != nil {
		t.Fatal("output without the marker should be ignored")
	}
}

func TestReadLoginEnv(t *testing.T) {
	if env := readLoginEnv("/bin/sh", 5*time.Second); len(env) == 0 {
		t.Fatal("no environment from /bin/sh")
	}
	if env := readLoginEnv("/no/such/shell", time.Second); env != nil {
		t.Fatalf("env from a missing shell: %q", env)
	}
}
