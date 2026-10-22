//go:build !darwin && !linux

package codediagnostics

// HostSessionLaunch is unavailable where language-server process-tree
// ownership has not been implemented; the live route fails closed.
func HostSessionLaunch(Invocation) (SessionProcess, SessionStreams, error) {
	return nil, SessionStreams{}, failure("unsupported", "Language server process ownership is unavailable on this host")
}
