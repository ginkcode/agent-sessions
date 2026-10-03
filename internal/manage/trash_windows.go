//go:build windows

package manage

// windowsRecycleSafetyValidated is a release gate, not a user's setting. Leave
// it false until TestWindowsRecycleNativeSmoke has verified both recycling and
// the pre-delete permanent-deletion veto on the supported Windows Shell. A
// successful non-destructive COM availability probe cannot replace that QA.
const windowsRecycleSafetyValidated = false

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
