//go:build windows

package manage

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsImageProcFS avoids inspecting command lines. Any runtime that might
// host an agent makes the deletion guard fail closed; no daemon exemptions.
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
