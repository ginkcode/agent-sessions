package rpc

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// RPC transports are remote. No authenticated managed-installation identity is
// provisioned by CD-09 yet. Do not reuse a local Engine host, picker label, or
// diagnostics fixture identity to authorize a remote installation.
func (s *Server) dispatchManagedTool(_ context.Context, req Request) (any, error) {
	if len(req.Params) > 2048 {
		return nil, cdError("invalidArgument", "Managed tool request exceeds limit")
	}
	var p engine.ManagedToolRequest
	decoder := json.NewDecoder(bytes.NewReader(req.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, cdError("invalidArgument", "Invalid managed tool parameters")
	}
	return nil, cdError("unsupported", "Managed remote installation requires reviewed authenticated host identity; use backend operator provisioning")
}

// ManagedTool deliberately fails closed with old peers too; no wire call or
// fallback to another host, legacy flags, PATH, or arbitrary installer exists.
func (c *Client) ManagedTool(context.Context, engine.ManagedToolRequest) (engine.ManagedToolState, error) {
	return engine.ManagedToolState{}, cdError("unsupported", "Managed remote installation is unavailable; backend operator provisioning is required")
}
