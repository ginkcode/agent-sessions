// Package version provides the single application version string.
package version

// Version is the application release version (e.g. "0.2.4").
// It is stamped at link time via:
//
//	-ldflags "-X github.com/ginkcode/agent-sessions/internal/version.Version=..."
//
// When built without flags (e.g. in tests or plain go run), it defaults to "dev".
var Version = "dev"

// Current returns the application version string.
func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
