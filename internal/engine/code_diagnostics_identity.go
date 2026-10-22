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
func localDiagnosticsIdentity(config string) (string, error) {
	machine, err := diagnosticsMachineID()
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
	return (cd.HostIdentity{Kind: "local", Authority: string(authority), Endpoint: string(data)}).ID()
}
