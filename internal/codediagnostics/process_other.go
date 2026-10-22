//go:build !darwin && !linux && !windows

package codediagnostics

func startProcess(Invocation) (Process, error) {
	return nil, failure("unsupported", "Checker process-tree ownership is unavailable on this host")
}
