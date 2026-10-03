//go:build windows

package manage

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	recycleOle32          = windows.NewLazySystemDLL("ole32.dll")
	recycleCreateInstance = recycleOle32.NewProc("CoCreateInstance")
	recycleShell32        = windows.NewLazySystemDLL("shell32.dll")
	recycleCreateItem     = recycleShell32.NewProc("SHCreateItemFromParsingName")

	recycleCLSIDOperation = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	recycleIIDOperation   = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	recycleIIDItem        = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	recycleIIDSink        = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}
	recycleIIDUnknown     = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

type windowsRecycleNative struct{}

func (windowsRecycleNative) available() bool {
	// RECYCLEONDELETE/ADDUNDORECORD appeared in Windows 8. Limit support to
	// Windows 10/11 rather than relying on an older Shell ignoring flags.
	return windows.RtlGetVersion().MajorVersion >= 10 &&
		recycleCreateInstance.Find() == nil && recycleCreateItem.Find() == nil
}

func (windowsRecycleNative) lockThread()   { runtime.LockOSThread() }
func (windowsRecycleNative) unlockThread() { runtime.UnlockOSThread() }
func (windowsRecycleNative) initialize() recycleHRESULT {
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err == nil {
		return recycleOK
	}
	var code syscall.Errno
	if errors.As(err, &code) {
		return recycleHRESULT(code)
	}
	return recycleAbort
}
func (windowsRecycleNative) uninitialize() { windows.CoUninitialize() }

func (windowsRecycleNative) checkLocation(path string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return errRecyclePath
	}
	var volume [260]uint16
	if err := windows.GetVolumePathName(name, &volume[0], uint32(len(volume))); err != nil {
		return errRecyclePath
	}
	root := windows.UTF16ToString(volume[:])
	// Reject mount points as well as UNC, network, removable, unknown drives
	// and SUBST aliases. Do not translate them to an underlying different path.
	if !strings.EqualFold(root, path[:3]) || windows.GetDriveType(&volume[0]) != windows.DRIVE_FIXED {
		return errRecyclePath
	}
	drive, _ := windows.UTF16PtrFromString(path[:2])
	var device [1024]uint16
	if _, err := windows.QueryDosDevice(drive, &device[0], uint32(len(device))); err != nil ||
		!strings.HasPrefix(windows.UTF16ToString(device[:]), `\Device\HarddiskVolume`) {
		return errRecyclePath
	}
	var filesystem [32]uint16
	if err := windows.GetVolumeInformation(&volume[0], nil, 0, nil, nil, nil, &filesystem[0], uint32(len(filesystem))); err != nil ||
		!strings.EqualFold(windows.UTF16ToString(filesystem[:]), "NTFS") {
		return errRecyclePath
	}
	// No traversal of junctions, symlinks or any other reparse point (including
	// cloud placeholders). Also require every path component to exist now.
	for end := 3; end <= len(path); end++ {
		if end != 3 && end != len(path) && path[end] != '\\' {
			continue
		}
		part, _ := windows.UTF16PtrFromString(path[:end])
		attributes, err := windows.GetFileAttributes(part)
		if err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errRecyclePath
		}
	}
	return nil
}

// Named complete SDK vtables make the ABI reviewable. IFileOperation's
// DeleteItem, PerformOperations and GetAnyOperationsAborted are slots 18,21,22
// (not DeleteItems at 19, or an out-of-bounds slot 23).
type windowsFileOperationVTable struct {
	queryInterface, addRef, release                               uintptr
	advise, unadvise, setOperationFlags                           uintptr
	setProgressMessage, setProgressDialog, setProperties          uintptr
	setOwnerWindow, applyPropertiesToItem, applyPropertiesToItems uintptr
	renameItem, renameItems, moveItem, moveItems                  uintptr
	copyItem, copyItems, deleteItem, deleteItems                  uintptr
	newItem, performOperations, getAnyOperationsAborted           uintptr
}

type windowsFileOperation struct{ vtable *windowsFileOperationVTable }

type windowsShellItemVTable struct {
	queryInterface, addRef, release                                  uintptr
	bindToHandler, getParent, getDisplayName, getAttributes, compare uintptr
}

type windowsShellItem struct{ vtable *windowsShellItemVTable }

func (windowsRecycleNative) newOperation() (recycleOperation, recycleHRESULT) {
	var ptr *windowsFileOperation
	hr, _, _ := recycleCreateInstance.Call(
		uintptr(unsafe.Pointer(&recycleCLSIDOperation)), 0, windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&recycleIIDOperation)), uintptr(unsafe.Pointer(&ptr)))
	if ptr == nil {
		return nil, recycleHRESULT(hr)
	}
	return &windowsRecycleOperation{ptr: ptr}, recycleHRESULT(hr)
}

func (windowsRecycleNative) newItem(path string) (recycleItem, recycleHRESULT) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, recycleAbort
	}
	var ptr *windowsShellItem
	hr, _, _ := recycleCreateItem.Call(uintptr(unsafe.Pointer(name)), 0,
		uintptr(unsafe.Pointer(&recycleIIDItem)), uintptr(unsafe.Pointer(&ptr)))
	runtime.KeepAlive(name)
	if ptr == nil {
		return nil, recycleHRESULT(hr)
	}
	return ptr, recycleHRESULT(hr)
}

func (item *windowsShellItem) addRef() {
	syscall.SyscallN(item.vtable.addRef, uintptr(unsafe.Pointer(item)))
}

func (item *windowsShellItem) release() {
	syscall.SyscallN(item.vtable.release, uintptr(unsafe.Pointer(item)))
}

func (item *windowsShellItem) same(other *windowsShellItem) bool {
	if other == nil {
		return false
	}
	if item == other {
		return true
	}
	var order int32 = 1
	const canonical = 0x10000000 // SICHINT_CANONICAL
	hr, _, _ := syscall.SyscallN(item.vtable.compare, uintptr(unsafe.Pointer(item)),
		uintptr(unsafe.Pointer(other)), canonical, uintptr(unsafe.Pointer(&order)))
	return recycleHRESULT(hr) == recycleOK && order == 0
}

type windowsRecycleOperation struct {
	ptr  *windowsFileOperation
	sink *windowsRecycleSink
}

func (op *windowsRecycleOperation) release() {
	syscall.SyscallN(op.ptr.vtable.release, uintptr(unsafe.Pointer(op.ptr)))
	// The caller's reference and pinned callback storage outlive the COM
	// operation, including an unsuccessful Unadvise.
	if op.sink != nil {
		op.sink.release()
		op.sink = nil
	}
}

func (op *windowsRecycleOperation) setFlags(flags uint32) recycleHRESULT {
	hr, _, _ := syscall.SyscallN(op.ptr.vtable.setOperationFlags, uintptr(unsafe.Pointer(op.ptr)), uintptr(flags))
	return recycleHRESULT(hr)
}

func (op *windowsRecycleOperation) advise(progress *recycleProgress, item recycleItem) (uint32, recycleHRESULT) {
	if op.sink != nil {
		return 0, recycleAbort
	}
	var target *windowsShellItem
	if item != nil {
		var ok bool
		target, ok = item.(*windowsShellItem)
		if !ok {
			return 0, recycleAbort
		}
	}
	sink := newWindowsRecycleSink(progress, target)
	var cookie uint32
	hr, _, _ := syscall.SyscallN(op.ptr.vtable.advise, uintptr(unsafe.Pointer(op.ptr)),
		uintptr(unsafe.Pointer(sink.header)), uintptr(unsafe.Pointer(&cookie)))
	// Retain even on failed Advise: COM could have taken a reference. The
	// operation is released before the caller's pinned sink reference.
	op.sink = sink
	return cookie, recycleHRESULT(hr)
}

func (op *windowsRecycleOperation) unadvise(cookie uint32) recycleHRESULT {
	hr, _, _ := syscall.SyscallN(op.ptr.vtable.unadvise, uintptr(unsafe.Pointer(op.ptr)), uintptr(cookie))
	return recycleHRESULT(hr)
}

func (op *windowsRecycleOperation) deleteItem(item recycleItem) recycleHRESULT {
	native, ok := item.(*windowsShellItem)
	if !ok || op.sink == nil {
		return recycleAbort
	}
	// Global Advise covers every item. Do not pass the same sink twice.
	hr, _, _ := syscall.SyscallN(op.ptr.vtable.deleteItem, uintptr(unsafe.Pointer(op.ptr)), uintptr(unsafe.Pointer(native)), 0)
	return recycleHRESULT(hr)
}

func (op *windowsRecycleOperation) perform() recycleHRESULT {
	hr, _, _ := syscall.SyscallN(op.ptr.vtable.performOperations, uintptr(unsafe.Pointer(op.ptr)))
	return recycleHRESULT(hr)
}

func (op *windowsRecycleOperation) aborted() (bool, recycleHRESULT) {
	var aborted int32 = 1 // BOOL, not a one-byte Go bool; fail closed by default.
	hr, _, _ := syscall.SyscallN(op.ptr.vtable.getAnyOperationsAborted, uintptr(unsafe.Pointer(op.ptr)), uintptr(unsafe.Pointer(&aborted)))
	return aborted != 0, recycleHRESULT(hr)
}

// A COM callback object must remain at a stable address while native code
// retains it. Pin a pointer-free header and keep Go state in a registry keyed by
// an integer token. Never hand native COM an unpinned Go object containing Go
// pointers. Only nineteen process-lifetime stdcall thunks are allocated.
type windowsRecycleSinkHeader struct {
	vtable uintptr
	token  uintptr
}

type windowsRecycleSink struct {
	header   *windowsRecycleSinkHeader
	pin      runtime.Pinner
	refs     atomic.Int32
	progress *recycleProgress
	target   *windowsShellItem
}

var (
	windowsRecycleSinks      sync.Map
	windowsRecycleToken      atomic.Uintptr
	windowsRecycleSinkVTable = [...]uintptr{
		windows.NewCallback(windowsRecycleQueryInterface),
		windows.NewCallback(windowsRecycleAddRef),
		windows.NewCallback(windowsRecycleRelease),
		windows.NewCallback(windowsRecycleStart),
		windows.NewCallback(windowsRecycleFinish),
		windows.NewCallback(windowsRecyclePreRename),
		windows.NewCallback(windowsRecyclePostRename),
		windows.NewCallback(windowsRecyclePreMoveCopy),
		windows.NewCallback(windowsRecyclePostMoveCopy),
		windows.NewCallback(windowsRecyclePreMoveCopy),
		windows.NewCallback(windowsRecyclePostMoveCopy),
		windows.NewCallback(windowsRecyclePreDelete),
		windows.NewCallback(windowsRecyclePostDelete),
		windows.NewCallback(windowsRecyclePreNew),
		windows.NewCallback(windowsRecyclePostNew),
		windows.NewCallback(windowsRecycleUpdate),
		windows.NewCallback(windowsRecycleTimer),
		windows.NewCallback(windowsRecycleTimer),
		windows.NewCallback(windowsRecycleTimer),
	}
)

func newWindowsRecycleSink(progress *recycleProgress, target *windowsShellItem) *windowsRecycleSink {
	sink := &windowsRecycleSink{
		header: &windowsRecycleSinkHeader{
			vtable: uintptr(unsafe.Pointer(&windowsRecycleSinkVTable[0])),
			token:  windowsRecycleToken.Add(1),
		},
		progress: progress,
		target:   target,
	}
	sink.refs.Store(1)
	sink.pin.Pin(sink.header)
	if target != nil {
		target.addRef()
	}
	windowsRecycleSinks.Store(sink.header.token, sink)
	return sink
}

func windowsRecycleLookup(header *windowsRecycleSinkHeader) *windowsRecycleSink {
	if header == nil {
		return nil
	}
	value, ok := windowsRecycleSinks.Load(header.token)
	if !ok {
		return nil
	}
	return value.(*windowsRecycleSink)
}

func (sink *windowsRecycleSink) release() uint32 {
	refs := sink.refs.Add(-1)
	if refs == 0 {
		windowsRecycleSinks.Delete(sink.header.token)
		if sink.target != nil {
			sink.target.release()
		}
		sink.pin.Unpin()
	}
	return uint32(refs)
}

func windowsRecycleQueryInterface(header *windowsRecycleSinkHeader, iid *windows.GUID, out **windowsRecycleSinkHeader) uintptr {
	if out == nil {
		return uintptr(recyclePointer)
	}
	*out = nil
	if iid == nil {
		return uintptr(recyclePointer)
	}
	sink := windowsRecycleLookup(header)
	if sink == nil || (*iid != recycleIIDSink && *iid != recycleIIDUnknown) {
		return uintptr(recycleNoInterface)
	}
	sink.refs.Add(1)
	*out = header
	return uintptr(recycleOK)
}

func windowsRecycleAddRef(header *windowsRecycleSinkHeader) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.refs.Add(1))
	}
	return 0
}

func windowsRecycleRelease(header *windowsRecycleSinkHeader) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.release())
	}
	return 0
}

func windowsRecycleStart(header *windowsRecycleSinkHeader) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.progress.start())
	}
	return uintptr(recycleAbort)
}

func windowsRecycleFinish(header *windowsRecycleSinkHeader, hr uint32) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.progress.finish(recycleHRESULT(hr)))
	}
	return uintptr(recycleAbort)
}

func windowsRecyclePreDelete(header *windowsRecycleSinkHeader, flags uint32, item *windowsShellItem) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.progress.preDelete(flags, sink.target != nil && sink.target.same(item)))
	}
	return uintptr(recycleAbort)
}

func windowsRecyclePostDelete(header *windowsRecycleSinkHeader, flags uint32, item *windowsShellItem, hr uint32, recycled *windowsShellItem) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.progress.postDelete(flags, sink.target != nil && sink.target.same(item), recycleHRESULT(hr), recycled != nil))
	}
	return uintptr(recycleAbort)
}

// Unexpected rename/move/copy/new-item operations are always vetoed, not
// acknowledged as success. Keep the exact SDK arity (stdcall on Windows/386).
func windowsRecycleVeto(header *windowsRecycleSinkHeader) uintptr {
	if sink := windowsRecycleLookup(header); sink != nil {
		return uintptr(sink.progress.veto())
	}
	return uintptr(recycleAbort)
}

func windowsRecyclePreRename(h *windowsRecycleSinkHeader, _ uint32, _ *windowsShellItem, _ *uint16) uintptr {
	return windowsRecycleVeto(h)
}

func windowsRecyclePostRename(h *windowsRecycleSinkHeader, _ uint32, _ *windowsShellItem, _ *uint16, _ uint32, _ *windowsShellItem) uintptr {
	return windowsRecycleVeto(h)
}

func windowsRecyclePreMoveCopy(h *windowsRecycleSinkHeader, _ uint32, _, _ *windowsShellItem, _ *uint16) uintptr {
	return windowsRecycleVeto(h)
}

func windowsRecyclePostMoveCopy(h *windowsRecycleSinkHeader, _ uint32, _, _ *windowsShellItem, _ *uint16, _ uint32, _ *windowsShellItem) uintptr {
	return windowsRecycleVeto(h)
}

func windowsRecyclePreNew(h *windowsRecycleSinkHeader, _ uint32, _ *windowsShellItem, _ *uint16) uintptr {
	return windowsRecycleVeto(h)
}

func windowsRecyclePostNew(h *windowsRecycleSinkHeader, _ uint32, _ *windowsShellItem, _, _ *uint16, _, _ uint32, _ *windowsShellItem) uintptr {
	return windowsRecycleVeto(h)
}

func windowsRecycleUpdate(h *windowsRecycleSinkHeader, _, _ uint32) uintptr {
	return windowsRecycleTimer(h)
}

func windowsRecycleTimer(h *windowsRecycleSinkHeader) uintptr {
	if windowsRecycleLookup(h) == nil {
		return uintptr(recycleAbort)
	}
	return uintptr(recycleOK)
}
