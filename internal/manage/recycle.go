package manage

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// TrashPathChecker optionally rejects paths that a transport cannot safely
// recycle. Planning may use it; Trash must repeat the check at execution time.
type TrashPathChecker interface {
	CheckTrashPath(string) error
}

// ErrTrashOutcomeUnknown means execution started but recycling could not be
// verified. The current path must not be described as untouched or retried
// automatically. Earlier successfully recycled paths remain successful.
var ErrTrashOutcomeUnknown = errors.New("Recycle Bin outcome is unknown; inspect the original location and Recycle Bin before retrying")

var (
	errRecycleUnavailable = errors.New("manage: safe Recycle Bin support is unavailable")
	errRecyclePath        = errors.New("path is not supported by the Recycle Bin transport")
)

type recycleHRESULT uint32

const (
	recycleOK          recycleHRESULT = 0
	recycleFalse       recycleHRESULT = 1
	recycleAbort       recycleHRESULT = 0x80004004
	recycleNoInterface recycleHRESULT = 0x80004002
	recyclePointer     recycleHRESULT = 0x80004003

	// COPYENGINE_S_DONT_PROCESS_CHILDREN (sherrors.h): the item was handled as a
	// whole. Native QA observed it for a successfully recycled file.
	recycleDontProcessChildren recycleHRESULT = 0x00270008
)

// These values and the COM vtables come from the Windows SDK ShObjIdl_core.h.
const (
	recycleSilent          uint32 = 0x00000004 // FOF_SILENT
	recycleAllowUndo       uint32 = 0x00000040 // FOF_ALLOWUNDO
	recycleNoErrorUI       uint32 = 0x00000400 // FOF_NOERRORUI
	recycleNoConnected     uint32 = 0x00002000 // FOF_NO_CONNECTED_ELEMENTS
	recycleWantNukeWarning uint32 = 0x00004000 // FOF_WANTNUKEWARNING
	recycleOnDelete        uint32 = 0x00080000 // FOFX_RECYCLEONDELETE (Windows 8+)
	recycleEarlyFailure    uint32 = 0x00100000 // FOFX_EARLYFAILURE
	recycleAddUndoRecord   uint32 = 0x20000000 // FOFX_ADDUNDORECORD (Windows 8+)
	recycleIfPossible      uint32 = 0x00000080 // TSF_DELETE_RECYCLE_IF_POSSIBLE

	// Never include FOF_NOCONFIRMATION: "Yes to All" can approve a permanent
	// delete. WANTNUKEWARNING, NOERRORUI and EARLYFAILURE request failure rather
	// than approving a destructive warning. The pre-delete veto is mandatory.
	recycleOperationFlags = recycleSilent | recycleAllowUndo | recycleNoErrorUI |
		recycleNoConnected | recycleWantNukeWarning | recycleOnDelete |
		recycleEarlyFailure | recycleAddUndoRecord
)

func (hr recycleHRESULT) failed() bool { return hr&0x80000000 != 0 }

func recycleNativeError(stage string, hr recycleHRESULT) error {
	// Never FormatMessage or wrap arbitrary native errors: they can include
	// private paths. Numeric HRESULTs contain no target information.
	return fmt.Errorf("Recycle Bin %s failed (HRESULT 0x%08X)", stage, uint32(hr))
}

// recycleNative is the injectable boundary. Its implementation owns no COM
// apartment outside the caller's locked-thread scope. Probing never queues or
// performs a deletion.
type recycleNative interface {
	available() bool
	checkLocation(string) error
	lockThread()
	unlockThread()
	initialize() recycleHRESULT
	uninitialize()
	newOperation() (recycleOperation, recycleHRESULT)
	newItem(string) (recycleItem, recycleHRESULT)
}

type recycleItem interface{ release() }

type recycleOperation interface {
	release()
	setFlags(uint32) recycleHRESULT
	advise(*recycleProgress, recycleItem) (uint32, recycleHRESULT)
	unadvise(uint32) recycleHRESULT
	deleteItem(recycleItem) recycleHRESULT
	perform() recycleHRESULT
	aborted() (bool, recycleHRESULT)
}

type recycleTransport struct{ native recycleNative }

func (recycleTransport) Name() string        { return "windows-recycle-bin" }
func (recycleTransport) DisplayName() string { return "Recycle Bin" }

func (t recycleTransport) CheckTrashPath(path string) error {
	if err := checkNativeRecyclePath(path); err != nil {
		return err
	}
	if t.native == nil || !t.native.available() {
		return errRecycleUnavailable
	}
	if err := t.native.checkLocation(path); err != nil {
		return errRecyclePath
	}
	return nil
}

func newRecycleTransport(native recycleNative) (recycleTransport, error) {
	t := recycleTransport{native: native}
	if native == nil || !native.available() {
		return recycleTransport{}, errRecycleUnavailable
	}
	// Non-destructive availability probe: initialize COM, create the real
	// interface, set all required flags and register/unregister the veto sink.
	// Neither DeleteItem nor PerformOperations is called here.
	if err := t.withOperation(func(op recycleOperation) error {
		if hr := op.setFlags(recycleOperationFlags); hr != recycleOK {
			return recycleNativeError("configuration", hr)
		}
		cookie, hr := op.advise(&recycleProgress{}, nil)
		if hr != recycleOK {
			return recycleNativeError("progress registration", hr)
		}
		if hr := op.unadvise(cookie); hr != recycleOK {
			return recycleNativeError("progress release", hr)
		}
		return nil
	}); err != nil {
		return recycleTransport{}, errRecycleUnavailable
	}
	return t, nil
}

func (t recycleTransport) withOperation(run func(recycleOperation) error) error {
	t.native.lockThread()
	defer t.native.unlockThread()
	hr := t.native.initialize()
	// CoInitializeEx's S_FALSE still acquires a reference requiring exactly
	// one CoUninitialize. RPC_E_CHANGED_MODE acquires none and fails closed.
	if hr != recycleOK && hr != recycleFalse {
		return recycleNativeError("COM initialization", hr)
	}
	defer t.native.uninitialize()
	op, hr := t.native.newOperation()
	if op != nil {
		defer op.release()
	}
	if hr != recycleOK || op == nil {
		return recycleNativeError("interface creation", hr)
	}
	return run(op)
}

func (t recycleTransport) Trash(path string) error {
	if err := t.CheckTrashPath(path); err != nil {
		return err
	}
	return t.withOperation(func(op recycleOperation) error {
		if hr := op.setFlags(recycleOperationFlags); hr != recycleOK {
			return recycleNativeError("configuration", hr)
		}
		item, hr := t.native.newItem(path) // Exact validated path; no Clean/Abs.
		if item != nil {
			defer item.release()
		}
		if hr != recycleOK || item == nil {
			return recycleNativeError("item creation", hr)
		}
		progress := &recycleProgress{}
		cookie, hr := op.advise(progress, item)
		if hr != recycleOK {
			return recycleNativeError("progress registration", hr)
		}
		// Release order: our item reference (deferred above) is released before
		// the operation (deferred in withOperation). That is safe because the
		// native sink AddRefs its own target and the operation holds its own
		// item reference. The sink's pinned callback storage is released only by
		// operation release, so a failed Unadvise cannot leave a dangling sink.
		// Releasing a COM interface never performs queued work.
		unadvise := true
		defer func() {
			if unadvise {
				_ = op.unadvise(cookie)
			}
		}()
		if hr := op.deleteItem(item); hr != recycleOK {
			return recycleNativeError("queueing", hr)
		}
		// Repeat the non-destructive volume/reparse check immediately before
		// execution, not just at preview time or before COM initialization.
		if err := t.native.checkLocation(path); err != nil {
			return errRecyclePath
		}
		performHR := op.perform()
		// Microsoft requires this query even when PerformOperations failed.
		aborted, abortHR := op.aborted()
		releaseHR := op.unadvise(cookie)
		unadvise = false
		if performHR != recycleOK {
			return fmt.Errorf("%w: %v", ErrTrashOutcomeUnknown, recycleNativeError("operation", performHR))
		}
		if abortHR != recycleOK || aborted {
			return fmt.Errorf("%w: operation was aborted or its completion could not be checked", ErrTrashOutcomeUnknown)
		}
		if releaseHR != recycleOK || !progress.verified() {
			return fmt.Errorf("%w: recycled item could not be verified", ErrTrashOutcomeUnknown)
		}
		return nil
	})
}

// checkNativeRecyclePath deliberately supports only ordinary drive-letter
// absolute paths under MAX_PATH (UTF-16 including NUL). Rejected spellings are
// not normalized into different targets. UNC, extended/device namespaces,
// roots, relative paths, ADS, wildcards and Win32 name aliases are unsupported.
func checkNativeRecyclePath(path string) error {
	if !utf8.ValidString(path) || len(path) < 4 ||
		!((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) ||
		path[1] != ':' || path[2] != '\\' || len(utf16.Encode([]rune(path))) >= 260 {
		return errRecyclePath
	}
	for _, r := range path[3:] {
		if r < 32 || strings.ContainsRune(`/<>:"|?*`, r) {
			return errRecyclePath
		}
	}
	for _, component := range strings.Split(path[3:], `\`) {
		if component == "" || component == "." || component == ".." ||
			strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
			return errRecyclePath
		}
		name := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		switch name {
		case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "$RECYCLE", "$RECYCLE.BIN", "RECYCLER", "SYSTEM VOLUME INFORMATION":
			return errRecyclePath
		}
		if strings.EqualFold(component, "$Recycle.Bin") {
			return errRecyclePath
		}
		if len([]rune(name)) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) {
			digit := []rune(name)[3]
			if (digit >= '1' && digit <= '9') || strings.ContainsRune("¹²³", digit) {
				return errRecyclePath
			}
		}
	}
	return nil
}

// recycleProgress is a fail-closed per-operation state machine. The Shell's
// TSF_DELETE_RECYCLE_IF_POSSIBLE is NOT a recycle-only guarantee: it is checked
// only in conjunction with the mandatory modern RECYCLEONDELETE operation
// flag. Any deletion without that flag is vetoed before it starts. A successful
// post-delete callback must also provide the item now in the Recycle Bin.
//
// API safety evidence:
// https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperation-setoperationflags
// https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperationprogresssink-predeleteitem
// https://learn.microsoft.com/windows/win32/api/shobjidl_core/nf-shobjidl_core-ifileoperationprogresssink-postdeleteitem
// The first documents the modern recycle flag; the second says a failing
// PreDeleteItem cancels this deletion and all subsequent operations; the third
// says a NULL newly-created item means fully deleted, not recycled. Native QA
// for nonrecyclable cases remains a release prerequisite, not something an
// injected test or successful availability probe can establish.
type recycleProgress struct {
	// trace is set only by the opt-in native smoke test; it never receives paths.
	trace    func(event string, flags uint32, target bool, hr recycleHRESULT, recycled bool)
	mu       sync.Mutex
	started  bool
	finished bool
	pre      bool
	post     bool
	failed   bool
}

func (p *recycleProgress) traceEvent(event string, flags uint32, target bool, hr recycleHRESULT, recycled bool) {
	if p.trace != nil {
		p.trace(event, flags, target, hr, recycled)
	}
}

func (p *recycleProgress) start() recycleHRESULT {
	p.traceEvent("start", 0, false, recycleOK, false)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || p.finished || p.failed {
		p.failed = true
		return recycleAbort
	}
	p.started = true
	return recycleOK
}

func (p *recycleProgress) finish(hr recycleHRESULT) recycleHRESULT {
	p.traceEvent("finish", 0, false, hr, false)
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.started || p.finished || hr != recycleOK || !p.post {
		p.failed = true
	}
	p.finished = true
	if p.failed {
		return recycleAbort
	}
	return recycleOK
}

func (p *recycleProgress) veto() recycleHRESULT {
	p.traceEvent("unexpected-operation", 0, false, recycleAbort, false)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = true
	return recycleAbort
}

func (p *recycleProgress) preDelete(flags uint32, target bool) recycleHRESULT {
	p.traceEvent("pre-delete", flags, target, recycleOK, false)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed || !p.started || p.finished || p.pre || !target || flags&recycleIfPossible == 0 {
		p.failed = true
		return recycleAbort
	}
	p.pre = true
	return recycleOK
}

func (p *recycleProgress) postDelete(flags uint32, target bool, hr recycleHRESULT, recycled bool) recycleHRESULT {
	p.traceEvent("post-delete", flags, target, hr, recycled)
	p.mu.Lock()
	defer p.mu.Unlock()
	// Only these two success codes are accepted; skips such as S_FALSE or
	// COPYENGINE_S_USER_IGNORED are not evidence of recycling.
	success := hr == recycleOK || hr == recycleDontProcessChildren
	if p.failed || !p.pre || p.finished || p.post || !target || flags&recycleIfPossible == 0 || !success || !recycled {
		p.failed = true
		return recycleAbort
	}
	p.post = true
	return recycleOK
}

func (p *recycleProgress) verified() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started && p.finished && p.pre && p.post && !p.failed
}
