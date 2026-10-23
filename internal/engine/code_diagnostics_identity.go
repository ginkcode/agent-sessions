package engine

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/configfile"
)

// Identity discovery is host-owned metadata, never a friendly picker label.
// Missing machine/account identity disables diagnostics rather than guessing.
// machineID is the host machine-identity source; nil means the platform
// default (diagnosticsMachineID), which fails closed where unverified.
func localDiagnosticsIdentity(config string, machineID func() (string, error)) (string, error) {
	return diagnosticsIdentity("local", config, machineID)
}

// diagnosticsIdentity is computed by the host that runs the checks: a headless
// server on an SSH or WSL host uses its own machine, account and installation
// ID, with the kind its launcher named. kind never comes from a client request.
func diagnosticsIdentity(kind, config string, machineID func() (string, error)) (string, error) {
	if machineID == nil {
		machineID = diagnosticsMachineID
	}
	machine, err := machineID()
	if err != nil || strings.TrimSpace(machine) == "" {
		return "", diagnosticError("unsupported", "Stable machine identity is unavailable")
	}
	account, err := user.Current()
	if err != nil || account.Uid == "" {
		return "", diagnosticError("unsupported", "Effective account identity is unavailable")
	}
	if !filepath.IsAbs(config) {
		return "", diagnosticError("unsupported", "Host config directory is unavailable")
	}
	path := filepath.Join(config, "code-diagnostics", "installation-id")
	lock, err := configfile.Lock(path)
	if err != nil {
		return "", diagnosticError("busy", "Host identity is unavailable")
	}
	defer func() { _ = lock.Unlock() }()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		var id [16]byte
		_, _ = rand.Read(id[:])
		data = []byte(hex.EncodeToString(id[:]))
		err = configfile.WriteAtomic(path, data)
	}
	if err != nil || len(data) != 32 {
		return "", diagnosticError("unsupported", "Host installation identity is unavailable")
	}
	if _, err := hex.DecodeString(string(data)); err != nil {
		return "", diagnosticError("unsupported", "Host installation identity is invalid")
	}
	authority, _ := json.Marshal([]string{strings.TrimSpace(machine), account.Uid})
	return (cd.HostIdentity{Kind: kind, Authority: string(authority), Endpoint: string(data)}).ID()
}
