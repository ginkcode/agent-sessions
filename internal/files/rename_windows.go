package files

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Native rooted opens avoid resolving a pathname outside the checked parent.
// ReplaceIfExists is zero: the kernel refuses an existing destination atomically.
func renameNoReplace(parent *os.File, source, name string) error {
	objectName, err := windows.NewNTUnicodeString(source)
	if err != nil {
		return err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: objectName, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.DELETE|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, &oa, &iosb, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN,
		windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if err != nil {
		return renameWindowsError(err)
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrEditLink
	}
	utf16, err := windows.UTF16FromString(name)
	if err != nil {
		return err
	}
	utf16 = utf16[:len(utf16)-1]
	type renameInformation struct {
		ReplaceIfExists uint32
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [1]uint16
	}
	var layout renameInformation
	size := int(unsafe.Offsetof(layout.FileName)) + len(utf16)*2
	buffer := make([]byte, size)
	rename := (*renameInformation)(unsafe.Pointer(&buffer[0]))
	rename.RootDirectory = windows.Handle(parent.Fd())
	rename.FileNameLength = uint32(len(utf16) * 2)
	copy(unsafe.Slice(&rename.FileName[0], len(utf16)), utf16)
	return renameWindowsError(windows.NtSetInformationFile(handle, &iosb, &buffer[0], uint32(size), windows.FileRenameInformation))
}

func renameWindowsError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
