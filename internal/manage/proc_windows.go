//go:build windows

package manage

import (
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsImageProcFS lists executable names for every process. Arguments are
// read only for Codex/OpenCode processes, to recognize their server modes,
// and are never returned to callers outside the guard.
type windowsImageProcFS struct{}

func defaultProcFS() ProcFS { return windowsImageProcFS{} }

func (windowsImageProcFS) Cmdlines() (map[int][]string, error) {
	return nil, ErrProcessUnknown
}

func (windowsImageProcFS) Images() (map[int]string, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, ErrProcessUnknown
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, ErrProcessUnknown
	}
	images := make(map[int]string)
	for {
		images[int(entry.ProcessID)] = windows.UTF16ToString(entry.ExeFile[:])
		err := windows.Process32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return images, nil
		}
		if err != nil {
			return nil, ErrProcessUnknown
		}
	}
}

// Args reads pid's command line (Windows 8.1+). It fails if the PID has been
// reused by a different executable since the snapshot.
func (windowsImageProcFS) Args(pid int, image string) ([]string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil, ErrProcessUnknown
	}
	defer windows.CloseHandle(h)

	path := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(path))
	if err := windows.QueryFullProcessImageName(h, 0, &path[0], &n); err != nil {
		return nil, ErrProcessUnknown
	}
	exe := windows.UTF16ToString(path[:n])
	if i := strings.LastIndexByte(exe, '\\'); i >= 0 {
		exe = exe[i+1:]
	}
	if !strings.EqualFold(exe, image) {
		return nil, ErrProcessUnknown
	}

	var size uint32
	_ = windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, nil, 0, &size)
	header := uint32(unsafe.Sizeof(windows.NTUnicodeString{}))
	if size <= header || size > 1<<20 {
		return nil, ErrProcessUnknown
	}
	buf := make([]uint64, (size+7)/8) // 8-byte aligned for the header.
	if err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), size, &size); err != nil {
		return nil, ErrProcessUnknown
	}
	us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
	// The string must lie inside buf, after the header.
	offset := uintptr(unsafe.Pointer(us.Buffer)) - uintptr(unsafe.Pointer(&buf[0]))
	if us.Buffer == nil || us.Length == 0 || us.Length%2 != 0 ||
		offset < uintptr(header) || offset+uintptr(us.Length) > uintptr(size) {
		return nil, ErrProcessUnknown
	}
	cmd := append([]uint16(nil), unsafe.Slice(us.Buffer, us.Length/2)...)
	for _, c := range cmd {
		if c == 0 {
			return nil, ErrProcessUnknown
		}
	}
	return decodeWindowsCommandLine(append(cmd, 0))
}

func decodeWindowsCommandLine(cmd []uint16) ([]string, error) {
	var argc int32
	argv, err := windows.CommandLineToArgv(&cmd[0], &argc)
	if err != nil {
		return nil, ErrProcessUnknown
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(argv)))
	if argc < 1 || argc > int32(len(argv)) {
		return nil, ErrProcessUnknown
	}
	args := make([]string, argc)
	for i := range args {
		args[i] = windows.UTF16PtrToString(&argv[i][0])
	}
	return args, nil
}
