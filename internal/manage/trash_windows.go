//go:build windows

package manage

// windowsRecycleSafetyValidated is a release gate, not a user's setting.
// TestWindowsRecycleNativeSmoke verified recycling and the pre-delete
// permanent-deletion veto on Windows 11 (2026-10-04). Cases where the Shell
// could delete permanently (Bin turned off, item larger than the Bin) are
// refused beforehand by the read-only checkBin pre-checks.
const windowsRecycleSafetyValidated = true

// WindowsRecycleTrash uses the Windows Shell's IFileOperation. There is no
// command, PowerShell, os.Remove or permanent-delete fallback.
type WindowsRecycleTrash struct{ recycleTransport }

// NewWindowsRecycleTrash probes the required API without deleting anything.
// Only ordinary fixed-local-volume paths are supported by CheckTrashPath.
func NewWindowsRecycleTrash() (WindowsRecycleTrash, error) {
	if !windowsRecycleSafetyValidated {
		return WindowsRecycleTrash{}, errRecycleUnavailable
	}
	t, err := newRecycleTransport(windowsRecycleNative{})
	return WindowsRecycleTrash{recycleTransport: t}, err
}

func platformTrash() (Trash, error) {
	return NewWindowsRecycleTrash()
}
