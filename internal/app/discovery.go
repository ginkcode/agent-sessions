package app

import (
	"errors"

	"github.com/ginkcode/agent-sessions/internal/engine"
)

// DiscoveryStatusReply is the startup discovery status of the selected host.
// Available is false when that backend has no discovery status (an older
// host, or a backend without engine.DiscoveryBackend): the status is then
// unknown, never completed. Host and Generation identify the route that
// answered, as on DiscoveryStatusEvent.
type DiscoveryStatusReply struct {
	Available  bool                    `json:"available"`
	Status     *engine.DiscoveryStatus `json:"status,omitempty"`
	Host       string                  `json:"host"`
	Generation uint64                  `json:"generation"`
}

// DiscoveryStatusEvent is the Wails payload of engine.DiscoveryStatusEvent:
// the engine snapshot plus the route it came from. Host is empty for Local.
// Generation is the remote connection generation (0 for Local). Connect and
// Disconnect change it; an automatic reconnect to the same host keeps it but
// starts a new server whose lifetimes restart at 1, so consumers reset held
// status when the connection leaves and re-enters the connected phase. The
// emitters already drop local events while a host is selected and events of a
// superseded generation.
type DiscoveryStatusEvent struct {
	engine.DiscoveryStatus
	Host       string `json:"host"`
	Generation uint64 `json:"generation"`
}

// DiscoveryStatus reports the active backend's startup discovery status. An
// offline host keeps its disconnected error.
func (a *App) DiscoveryStatus() (DiscoveryStatusReply, error) {
	r := a.route()
	reply := DiscoveryStatusReply{Host: r.host}
	if r.host != "" {
		reply.Generation = r.generation
	}
	d, ok := r.backend.(engine.DiscoveryBackend)
	if !ok {
		return reply, nil
	}
	status, err := d.DiscoveryStatus(a.appCtx())
	if errors.Is(err, engine.ErrUnsupportedAction) {
		return reply, nil
	}
	if err != nil {
		return DiscoveryStatusReply{}, err
	}
	reply.Available, reply.Status = true, &status
	return reply, nil
}
