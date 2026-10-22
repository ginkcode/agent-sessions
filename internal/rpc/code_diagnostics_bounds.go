package rpc

import (
	"encoding/json"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

func boundDiagnosticsResult(result any) error {
	var snapshot cd.Snapshot
	switch v := result.(type) {
	case cd.Snapshot:
		snapshot = v
	case engine.CodeDiagnosticsAttachment:
		snapshot = v.Snapshot
	default:
		return nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return cdError("limitExceeded", "Diagnostics snapshot exceeds encoded budget")
	}
	return nil
}
