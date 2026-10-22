package engine

import "context"

func (e *Engine) CodeDiagnosticsHostID(ctx context.Context) (string, error) {
	owner, err := e.diagnostics(ctx)
	if err != nil {
		return "", err
	}
	return owner.host, nil
}

// ResetCodeDiagnostics invalidates all local attachment and session-grant
// ownership when the desktop leaves/replaces its selected backend.
func (e *Engine) ResetCodeDiagnostics() {
	e.mu.Lock()
	owner := e.codeDiagnostics
	e.codeDiagnostics = nil
	e.mu.Unlock()
	if owner != nil {
		owner.close()
	}
}
