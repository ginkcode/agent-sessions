//go:build !windows

package launch

const terminalSupported = false

func startTerminal(string, string) error { return ErrUnsupported }
