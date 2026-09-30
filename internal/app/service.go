package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

// Service aliases engine.Service.
type Service = engine.Service

// Sentinel errors aliased from internal/engine.
var (
	ErrUnknownSession  = engine.ErrUnknownSession
	ErrUnknownProvider = engine.ErrUnknownProvider
	ErrUnknownGroup    = engine.ErrUnknownGroup
)

// NewService wires a Service onto an existing catalog and provider set.
func NewService(catalog *scan.Catalog, providers provider.Set) *Service {
	return engine.NewService(catalog, providers)
}
