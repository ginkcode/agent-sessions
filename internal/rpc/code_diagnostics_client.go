package rpc

import (
	"context"
	"encoding/json"
	"errors"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/engine"
)

func (c *Client) CodeDiagnosticsHostID(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", cdError("disconnected", "Connection closed")
	}
	if !c.caps.CodeDiagnostics || c.cdHost == "" {
		return "", cdError("unsupported", "Code diagnostics unavailable")
	}
	return c.cdHost, nil
}

func (c *Client) diagnosticsCall(ctx context.Context, method string, p codeDiagnosticsParams, out any) error {
	if _, err := c.CodeDiagnosticsHostID(ctx); err != nil {
		return err
	}
	if method != "attachCodeDiagnostics" {
		c.mu.Lock()
		owned := c.cdOwned[p.Context.AttachmentID] == p.Context
		c.mu.Unlock()
		if !owned {
			return cdError("staleContext", "Context is not owned by this connection")
		}
	}
	return normalizeDiagnosticsError(c.call(ctx, method, p, out))
}

// normalizeDiagnosticsError restores the stable contract code. FromRPCError maps
// a few shared codes (unsupported, disconnected, canceled) onto sentinels, which
// would otherwise lose the string code the App/frontend normalizes on.
func normalizeDiagnosticsError(err error) error {
	if err == nil {
		return nil
	}
	var diagnostic *cd.Error
	if errors.As(err, &diagnostic) {
		return err
	}
	var rpcErr *ResponseError
	if errors.As(err, &rpcErr) && rpcErr.Data != nil && rpcErr.Data.Code != "" {
		return &cd.Error{Code: rpcErr.Data.Code, Message: rpcErr.Message}
	}
	for _, m := range []struct {
		target error
		code   string
	}{{engine.ErrUnsupportedAction, "unsupported"}, {ErrDisconnected, "disconnected"}, {ErrCancelled, "canceled"}} {
		if errors.Is(err, m.target) {
			message := err.Error()
			if len(message) > 2048 {
				message = message[:2048]
			}
			return &cd.Error{Code: m.code, Message: message}
		}
	}
	return err
}

func (c *Client) AttachCodeDiagnostics(ctx context.Context, dir string, version int) (engine.CodeDiagnosticsAttachment, error) {
	c.mu.Lock()
	c.cdAttaching++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.cdAttaching--
		if c.cdAttaching == 0 {
			c.cdEarly = nil
			c.cdEarlyBytes = 0
		}
		c.mu.Unlock()
	}()
	var out engine.CodeDiagnosticsAttachment
	err := c.diagnosticsCall(ctx, "attachCodeDiagnostics", codeDiagnosticsParams{Dir: dir, ContractVersion: version}, &out)
	if err == nil {
		c.mu.Lock()
		if c.closed || out.Project.HostID != c.cdHost || out.Context != out.Snapshot.Context {
			err = cdError("staleContext", "Invalid attachment identity")
		} else {
			c.cdOwned[out.Context.AttachmentID] = out.Context
			c.cdSequences[out.Context.AttachmentID] = out.Snapshot.Sequence
		}
		early := c.cdEarly
		c.cdEarly = nil
		c.cdEarlyBytes = 0
		c.mu.Unlock()
		for _, raw := range early {
			c.handleNotification(engine.CodeDiagnosticsUpdatedEvent, raw)
		}
	}
	return out, err
}

func (c *Client) DetachCodeDiagnostics(ctx context.Context, token cd.Context) (engine.CodeDiagnosticsAck, error) {
	var out engine.CodeDiagnosticsAck
	err := c.diagnosticsCall(ctx, "detachCodeDiagnostics", codeDiagnosticsParams{Context: token}, &out)
	if err == nil {
		c.mu.Lock()
		delete(c.cdOwned, token.AttachmentID)
		c.mu.Unlock()
	}
	return out, err
}

func (c *Client) GetCodeDiagnosticsSnapshot(ctx context.Context, token cd.Context) (cd.Snapshot, error) {
	var out cd.Snapshot
	err := c.diagnosticsCall(ctx, "getCodeDiagnosticsSnapshot", codeDiagnosticsParams{Context: token}, &out)
	return out, err
}

func (c *Client) ApproveCodeDiagnostics(ctx context.Context, token cd.Context, mode, fingerprint string) (cd.Snapshot, error) {
	var out cd.Snapshot
	err := c.diagnosticsCall(ctx, "approveCodeDiagnostics", codeDiagnosticsParams{Context: token, Mode: mode, Fingerprint: fingerprint}, &out)
	return out, err
}

func (c *Client) RevokeCodeDiagnostics(ctx context.Context, token cd.Context) (cd.Snapshot, error) {
	var out cd.Snapshot
	err := c.diagnosticsCall(ctx, "revokeCodeDiagnostics", codeDiagnosticsParams{Context: token}, &out)
	if out.Context.ConnectionID == token.ConnectionID && out.Context.AttachmentID == token.AttachmentID && out.Approval.State == "revoked" {
		c.mu.Lock()
		if !c.closed {
			if out.Sequence >= c.cdSequences[token.AttachmentID] {
				c.cdOwned[token.AttachmentID] = out.Context
				c.cdSequences[token.AttachmentID] = out.Sequence
			}
		}
		c.mu.Unlock()
	}
	return out, err
}

func (c *Client) RunCodeDiagnostics(ctx context.Context, token cd.Context, ids []string) (engine.CodeDiagnosticsRuns, error) {
	var out engine.CodeDiagnosticsRuns
	err := c.diagnosticsCall(ctx, "runCodeDiagnostics", codeDiagnosticsParams{Context: token, ProviderIDs: ids}, &out)
	return out, err
}

func (c *Client) CancelCodeDiagnostics(ctx context.Context, token cd.Context, runs []cd.RunIdentity) (engine.CodeDiagnosticsAck, error) {
	var out engine.CodeDiagnosticsAck
	err := c.diagnosticsCall(ctx, "cancelCodeDiagnostics", codeDiagnosticsParams{Context: token, Runs: runs}, &out)
	return out, err
}

func (c *Client) diagnosticsNotification(params json.RawMessage) (cd.Event, bool) {
	var ev cd.Event
	if len(params) > 1<<20 || json.Unmarshal(params, &ev) != nil {
		return ev, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	owned, ok := c.cdOwned[ev.Context.AttachmentID]
	if c.closed {
		return ev, false
	}
	if !ok {
		// Early attach events are bounded by both parts and bytes. Overflow closes
		// the connection: callers must attach anew, never accept an incomplete update.
		if c.cdAttaching > 0 {
			if len(c.cdEarly) >= 64 || c.cdEarlyBytes+len(params) > 16<<20 {
				go func() { _ = c.Close() }()
			} else {
				c.cdEarly = append(c.cdEarly, append(json.RawMessage(nil), params...))
				c.cdEarlyBytes += len(params)
			}
		}
		return ev, false
	}
	if ev.Sequence < c.cdSequences[ev.Context.AttachmentID] {
		return ev, false
	}
	if owned != ev.Context {
		if ev.Sequence <= c.cdSequences[ev.Context.AttachmentID] || owned.ConnectionID != ev.Context.ConnectionID || ev.Snapshot.Approval.State != "revoked" || ev.Snapshot.Context != ev.Context {
			return ev, false
		}
		c.cdOwned[ev.Context.AttachmentID] = ev.Context
	}
	c.cdSequences[ev.Context.AttachmentID] = ev.Sequence
	return ev, true
}

// liveCall checks the separate liveCodeDiagnostics capability and every size
// bound before anything is written, so an old server never sees a live request
// and an oversized draft never reaches the wire.
func (c *Client) liveCall(ctx context.Context, method string, p codeDiagnosticsParams, out *engine.CodeDiagnosticsDocument) error {
	if _, err := c.CodeDiagnosticsHostID(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	live := c.caps.LiveCodeDiagnostics
	c.mu.Unlock()
	if !live {
		return cdError("unsupported", "Live code diagnostics unavailable")
	}
	if err := checkLiveParams(p); err != nil {
		return err
	}
	if raw, err := json.Marshal(p); err != nil {
		return cdError("invalidArgument", "Invalid diagnostics parameters")
	} else if len(raw) > maxLiveParamsBytes {
		return cdError("limitExceeded", "Document request exceeds the transport budget")
	}
	err := c.diagnosticsCall(ctx, method, p, out)
	if err == nil && (out.Context != p.Context || (p.DocumentID != "" && out.DocumentID != p.DocumentID)) {
		*out = engine.CodeDiagnosticsDocument{}
		return cdError("staleContext", "Document acknowledgement does not match the request")
	}
	return err
}

func (c *Client) OpenCodeDiagnosticDocument(ctx context.Context, token cd.Context, path, documentID string, version int64, text, revision string) (engine.CodeDiagnosticsDocument, error) {
	var out engine.CodeDiagnosticsDocument
	err := c.liveCall(ctx, "openCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, Path: path, DocumentID: documentID, Version: version, Text: text, Revision: revision}, &out)
	return out, err
}

func (c *Client) ChangeCodeDiagnosticDocument(ctx context.Context, token cd.Context, documentID string, version int64, text string) (engine.CodeDiagnosticsDocument, error) {
	var out engine.CodeDiagnosticsDocument
	err := c.liveCall(ctx, "changeCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, DocumentID: documentID, Version: version, Text: text}, &out)
	return out, err
}

func (c *Client) SaveCodeDiagnosticDocument(ctx context.Context, token cd.Context, documentID string, savedVersion int64, revision string) (engine.CodeDiagnosticsDocument, error) {
	var out engine.CodeDiagnosticsDocument
	err := c.liveCall(ctx, "saveCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, DocumentID: documentID, SavedVersion: savedVersion, Revision: revision}, &out)
	return out, err
}

func (c *Client) CloseCodeDiagnosticDocument(ctx context.Context, token cd.Context, documentID string) (engine.CodeDiagnosticsDocument, error) {
	var out engine.CodeDiagnosticsDocument
	err := c.liveCall(ctx, "closeCodeDiagnosticDocument", codeDiagnosticsParams{Context: token, DocumentID: documentID}, &out)
	return out, err
}

func (c *Client) InvalidateCodeDiagnosticsPath(ctx context.Context, token cd.Context, path string) (cd.Snapshot, error) {
	var out cd.Snapshot
	err := c.diagnosticsCall(ctx, "invalidateCodeDiagnosticsPath", codeDiagnosticsParams{Context: token, Path: path}, &out)
	return out, err
}
