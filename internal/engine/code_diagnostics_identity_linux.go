//go:build linux

package engine

import (
	"errors"
	"os"
	"strings"
)

// diagnosticsMachineID reads the systemd/D-Bus machine ID, which WSL
// distributions also have. The account and the installation ID in the config
// directory complete the identity (localDiagnosticsIdentity).
func diagnosticsMachineID() (string, error) {
	return machineIDFrom([]string{"/etc/machine-id", "/var/lib/dbus/machine-id"}, os.Hostname)
}

// machineIDFrom falls back to the hostname on hosts without a machine ID
// (containers, minimal systems). The installation ID already separates
// installations; this only keeps a home directory shared between machines
// from sharing approvals. A renamed host asks for approval again.
func machineIDFrom(files []string, hostname func() (string, error)) (string, error) {
	for _, path := range files {
		data, err := os.ReadFile(path)
		if id := strings.TrimSpace(string(data)); err == nil && len(id) >= 16 && len(id) <= 64 {
			return id, nil
		}
	}
	if name, err := hostname(); err == nil && name != "" && len(name) <= 253 {
		return "hostname:" + name, nil
	}
	return "", errors.New("machine ID unavailable")
}
