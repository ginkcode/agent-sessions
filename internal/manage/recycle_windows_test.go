//go:build windows

package manage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsRecycleReleaseSafetyGate(t *testing.T) {
	if windowsRecycleSafetyValidated {
		t.Skip("release safety validation has been recorded")
	}
	if tr, err := NewWindowsRecycleTrash(); err == nil || tr.native != nil {
		t.Fatalf("unvalidated native transport advertised: %#v, %v", tr, err)
	}
	if PlatformTrashSupported() {
		t.Fatal("unvalidated native transport reported available")
	}
}

func TestWindowsRecycleCOMVTableABI(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	v := windowsFileOperationVTable{}
	if unsafe.Sizeof(v) != 23*word ||
		unsafe.Offsetof(v.advise) != 3*word || unsafe.Offsetof(v.unadvise) != 4*word ||
		unsafe.Offsetof(v.setOperationFlags) != 5*word || unsafe.Offsetof(v.deleteItem) != 18*word ||
		unsafe.Offsetof(v.performOperations) != 21*word || unsafe.Offsetof(v.getAnyOperationsAborted) != 22*word {
		t.Fatal("IFileOperation vtable differs from SDK ABI")
	}
	i := windowsShellItemVTable{}
	if unsafe.Sizeof(i) != 8*word || unsafe.Offsetof(i.compare) != 7*word {
		t.Fatal("IShellItem vtable differs from SDK ABI")
	}
	if len(windowsRecycleSinkVTable) != 19 || unsafe.Sizeof(windowsRecycleSinkHeader{}) != 2*word {
		t.Fatal("IFileOperationProgressSink vtable/header differs from SDK ABI")
	}
	for slot, callback := range windowsRecycleSinkVTable {
		if callback == 0 {
			t.Fatalf("missing callback at slot %d", slot)
		}
	}
}

func newTestWindowsShellItem(t *testing.T) (*windowsShellItem, *int) {
	t.Helper()
	refs := new(int)
	*refs = 1
	item := &windowsShellItem{vtable: &windowsShellItemVTable{
		addRef:  windows.NewCallback(func(*windowsShellItem) uintptr { *refs++; return uintptr(*refs) }),
		release: windows.NewCallback(func(*windowsShellItem) uintptr { *refs--; return uintptr(*refs) }),
		compare: windows.NewCallback(func(_ *windowsShellItem, _ *windowsShellItem, _ uint32, order *int32) uintptr {
			*order = 1
			return uintptr(recycleFalse)
		}),
	}}
	return item, refs
}

func callWindowsRecycleSink(sink *windowsRecycleSink, slot int, args ...uintptr) recycleHRESULT {
	callArgs := append([]uintptr{uintptr(unsafe.Pointer(sink.header))}, args...)
	hr, _, _ := syscall.SyscallN(windowsRecycleSinkVTable[slot], callArgs...)
	runtime.KeepAlive(sink)
	return recycleHRESULT(hr)
}

func TestWindowsRecycleSinkIUnknownAndPinnedLifecycle(t *testing.T) {
	item, refs := newTestWindowsShellItem(t)
	sink := newWindowsRecycleSink(&recycleProgress{}, item)
	if *refs != 2 {
		t.Fatal("sink did not retain target")
	}
	token := sink.header.token
	var result *windowsRecycleSinkHeader
	hr := callWindowsRecycleSink(sink, 0, uintptr(unsafe.Pointer(&recycleIIDSink)), uintptr(unsafe.Pointer(&result)))
	if hr != recycleOK || result != sink.header || sink.refs.Load() != 2 {
		t.Fatal("QueryInterface failed to retain supported interface")
	}
	if callWindowsRecycleSink(sink, 2) != 1 {
		t.Fatal("Release did not decrement QueryInterface reference")
	}
	result = sink.header
	hr = callWindowsRecycleSink(sink, 0, uintptr(unsafe.Pointer(&recycleIIDItem)), uintptr(unsafe.Pointer(&result)))
	if hr != recycleNoInterface || result != nil || sink.refs.Load() != 1 {
		t.Fatal("QueryInterface supported an unrelated interface")
	}
	if callWindowsRecycleSink(sink, 0, uintptr(unsafe.Pointer(&recycleIIDSink)), 0) != recyclePointer {
		t.Fatal("null QueryInterface output not refused")
	}
	runtime.GC()
	if callWindowsRecycleSink(sink, 3) != recycleOK {
		t.Fatal("pinned callback lost state after GC")
	}
	if sink.release() != 0 || *refs != 1 {
		t.Fatal("sink/target reference leaked")
	}
	if _, ok := windowsRecycleSinks.Load(token); ok {
		t.Fatal("callback state leaked")
	}
}

func TestWindowsRecycleSinkNativeCallbackSafetyGates(t *testing.T) {
	for _, scenario := range []string{"recycled", "recycled-whole-item", "permanent", "no-bin-item", "item-error", "other-target", "unknown-target", "no-pre"} {
		t.Run(scenario, func(t *testing.T) {
			item, _ := newTestWindowsShellItem(t)
			p := &recycleProgress{}
			sink := newWindowsRecycleSink(p, item)
			defer sink.release()
			if callWindowsRecycleSink(sink, 3) != recycleOK {
				t.Fatal("StartOperations failed")
			}
			flags := recycleIfPossible
			if scenario == "permanent" {
				flags = 0
			}
			target := item
			if scenario == "other-target" {
				target, _ = newTestWindowsShellItem(t)
			}
			if scenario == "unknown-target" {
				target = nil
			}
			if scenario != "no-pre" {
				hr := callWindowsRecycleSink(sink, 11, uintptr(flags), uintptr(unsafe.Pointer(target)))
				if (scenario == "permanent" || scenario == "other-target" || scenario == "unknown-target") && hr != recycleAbort {
					t.Fatal("unsafe native deletion not vetoed")
				}
			}
			recycled := item // Pointer presence is the documented bin evidence.
			if scenario == "no-bin-item" {
				recycled = nil
			}
			hr := recycleOK
			if scenario == "item-error" {
				hr = 0x80070005
			}
			if scenario == "recycled-whole-item" {
				hr = recycleDontProcessChildren
			}
			callWindowsRecycleSink(sink, 12, uintptr(flags), uintptr(unsafe.Pointer(target)), uintptr(hr), uintptr(unsafe.Pointer(recycled)))
			callWindowsRecycleSink(sink, 4, uintptr(recycleOK))
			if p.verified() != (scenario == "recycled" || scenario == "recycled-whole-item") {
				t.Fatalf("incorrect native callback verification: %v", p.verified())
			}
		})
	}
}

func TestWindowsRecycleSinkVetoOtherOperationsABI(t *testing.T) {
	// Invoke the actual stdcall thunks with every exact SDK argument count.
	// These calls also catch stack-cleanup errors on Windows/386.
	for slot, arity := range map[int]int{5: 3, 6: 5, 7: 4, 8: 6, 9: 4, 10: 6, 13: 3, 14: 7} {
		t.Run(string(rune('A'+slot)), func(t *testing.T) {
			p := &recycleProgress{}
			sink := newWindowsRecycleSink(p, nil)
			defer sink.release()
			if hr := callWindowsRecycleSink(sink, slot, make([]uintptr, arity)...); hr != recycleAbort {
				t.Fatalf("unexpected operation at slot %d acknowledged: %#x", slot, hr)
			}
			if p.start() != recycleAbort {
				t.Fatal("unexpected operation did not poison completion")
			}
		})
	}
}

func TestWindowsRecycleOperationCallsExactSDKSlots(t *testing.T) {
	var seen []string
	item, refs := newTestWindowsShellItem(t)
	vtable := &windowsFileOperationVTable{
		release: windows.NewCallback(func(*windowsFileOperation) uintptr { seen = append(seen, "release"); return 0 }),
		setOperationFlags: windows.NewCallback(func(_ *windowsFileOperation, flags uint32) uintptr {
			if flags != recycleOperationFlags {
				t.Errorf("changed flags: %#x", flags)
			}
			seen = append(seen, "flags")
			return uintptr(recycleOK)
		}),
		advise: windows.NewCallback(func(_ *windowsFileOperation, h *windowsRecycleSinkHeader, cookie *uint32) uintptr {
			if windowsRecycleLookup(h) == nil {
				t.Error("invalid native callback object")
			}
			windowsRecycleAddRef(h)
			*cookie = 123
			seen = append(seen, "advise")
			return uintptr(recycleOK)
		}),
		unadvise: windows.NewCallback(func(_ *windowsFileOperation, cookie uint32) uintptr {
			if cookie != 123 {
				t.Error("wrong native cookie")
			}
			seen = append(seen, "unadvise")
			return uintptr(recycleOK)
		}),
		deleteItem: windows.NewCallback(func(_ *windowsFileOperation, target *windowsShellItem, perItem *windowsRecycleSinkHeader) uintptr {
			if target != item || perItem != nil {
				t.Error("wrong target or duplicate progress sink")
			}
			seen = append(seen, "delete-item")
			return uintptr(recycleOK)
		}),
		deleteItems:       windows.NewCallback(func(_ *windowsFileOperation, _ uintptr) uintptr { t.Error("called DeleteItems"); return 0 }),
		performOperations: windows.NewCallback(func(*windowsFileOperation) uintptr { seen = append(seen, "perform"); return uintptr(recycleOK) }),
		getAnyOperationsAborted: windows.NewCallback(func(_ *windowsFileOperation, aborted *int32) uintptr {
			*aborted = 0x100 // Not a single-byte Go bool.
			seen = append(seen, "aborted")
			return uintptr(recycleOK)
		}),
	}
	op := &windowsRecycleOperation{ptr: &windowsFileOperation{vtable: vtable}}
	op.setFlags(recycleOperationFlags)
	cookie, hr := op.advise(&recycleProgress{}, item)
	if hr != recycleOK {
		t.Fatal(hr)
	}
	op.deleteItem(item)
	op.perform()
	if aborted, hr := op.aborted(); !aborted || hr != recycleOK {
		t.Fatal("BOOL/HRESULT interpreted incorrectly")
	}
	op.unadvise(cookie)
	// Fake COM release hands its callback reference back; native operation
	// release then releases the caller's reference and pinned storage.
	windowsRecycleRelease(op.sink.header)
	op.release()
	if *refs != 1 || len(seen) != 7 || seen[2] != "delete-item" || seen[3] != "perform" || seen[4] != "aborted" {
		t.Fatalf("incorrect vtable calls/reference cleanup: %#v, refs=%d", seen, *refs)
	}
}

// Test-only native wrapper removes the recycle request so the actual Shell
// must enter its permanent-deletion preflight. The production transport never
// exposes or uses configurable flags.
type smokePermanentNative struct {
	windowsRecycleNative
	op *smokePermanentOperation
}

type smokePermanentOperation struct {
	recycleOperation
	progress *recycleProgress
}

func (n *smokePermanentNative) newOperation() (recycleOperation, recycleHRESULT) {
	op, hr := n.windowsRecycleNative.newOperation()
	if op == nil {
		return nil, hr
	}
	n.op = &smokePermanentOperation{recycleOperation: op}
	return n.op, hr
}

func (o *smokePermanentOperation) setFlags(uint32) recycleHRESULT {
	return o.recycleOperation.setFlags(recycleSilent | recycleNoErrorUI | recycleEarlyFailure | recycleNoConnected)
}

func (o *smokePermanentOperation) advise(p *recycleProgress, item recycleItem) (uint32, recycleHRESULT) {
	o.progress = p
	return o.recycleOperation.advise(p, item)
}

type smokeTraceNative struct {
	windowsRecycleNative
	t *testing.T
}

type smokeTraceOperation struct {
	recycleOperation
	t *testing.T
}

func (n smokeTraceNative) newOperation() (recycleOperation, recycleHRESULT) {
	op, hr := n.windowsRecycleNative.newOperation()
	if op == nil {
		return nil, hr
	}
	return smokeTraceOperation{recycleOperation: op, t: n.t}, hr
}

func (o smokeTraceOperation) advise(p *recycleProgress, item recycleItem) (uint32, recycleHRESULT) {
	p.trace = func(event string, flags uint32, target bool, hr recycleHRESULT, recycled bool) {
		o.t.Logf("Shell callback %s: flags=0x%08X target=%t HRESULT=0x%08X recycled=%t", event, flags, target, uint32(hr), recycled)
	}
	return o.recycleOperation.advise(p, item)
}

// Opt-in only. It creates synthetic files and a directory in a private temporary
// directory, never reads stores/transcripts or enumerates the user's Recycle
// Bin. Recycled fixtures remain recoverable in the Bin for manual restore QA.
// AGENT_SESSIONS_RECYCLE_SMOKE=1 is deliberately required even with -run.
func TestWindowsRecycleNativeSmoke(t *testing.T) {
	if os.Getenv("AGENT_SESSIONS_RECYCLE_SMOKE") != "1" {
		t.Skip("native Recycle Bin smoke requires explicit opt-in and parent coordination")
	}
	native := smokeTraceNative{t: t}
	transport, err := newRecycleTransport(native)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-sessions-recycle-smoke-你好.jsonl")
	if err := os.WriteFile(path, []byte("synthetic Recycle Bin smoke fixture; not a session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transport.CheckTrashPath(path); err != nil {
		t.Fatalf("purpose-created temp location unsupported; gate remains closed: %v", err)
	}
	if err := transport.Trash(path); err != nil {
		_, statErr := os.Stat(path)
		t.Logf("synthetic source still exists=%t (no retry)", statErr == nil)
		t.Fatalf("recycling did not provide verified native bin evidence; gate remains closed: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recycled source did not leave its original location: %v", err)
	}
	t.Logf("purpose-created fixture recycled; manual restoration can verify %q", path)

	// Claude deletion also recycles a session directory with children. The
	// state machine accepts exactly one matching pre/post pair, so if the Shell
	// reports per-child callbacks this fails closed and the gate stays closed.
	sessionDir := filepath.Join(dir, "agent-sessions-recycle-smoke-dir")
	child := filepath.Join(sessionDir, "subagents", "agent-smoke.jsonl")
	if err := os.MkdirAll(filepath.Dir(child), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("synthetic child fixture; not a session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transport.Trash(sessionDir); err != nil {
		t.Fatalf("directory recycling did not provide verified bin evidence; gate remains closed: %v", err)
	}
	if _, err := os.Stat(sessionDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recycled directory did not leave its original location: %v", err)
	}
	t.Logf("purpose-created directory recycled; manual restoration can verify %q contains %q", sessionDir, child)

	permanentPath := filepath.Join(dir, "agent-sessions-permanent-veto-smoke.jsonl")
	if err := os.WriteFile(permanentPath, []byte("synthetic permanent-deletion veto fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	permanentNative := &smokePermanentNative{}
	err = (recycleTransport{native: permanentNative}).Trash(permanentPath)
	if !errors.Is(err, ErrTrashOutcomeUnknown) || permanentNative.op == nil {
		t.Fatalf("permanent-deletion path did not fail closed: %v", err)
	}
	p := permanentNative.op.progress
	p.mu.Lock()
	vetoed := p.started && p.failed && !p.pre && !p.post
	p.mu.Unlock()
	if !vetoed {
		t.Fatal("actual Shell permanent preflight not vetoed; gate remains closed")
	}
	if contents, err := os.ReadFile(permanentPath); err != nil || string(contents) != "synthetic permanent-deletion veto fixture\n" {
		t.Fatalf("permanent-veto fixture did not survive unchanged; gate remains closed: %v", err)
	}
}
