package api

import (
	"net/http"

	"lanweave/pkg/protocol"
)

// serverInfo returns the connection details a client needs to build its tunnel.
func (h *handlers) serverInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, protocol.ServerInfoResponse{
		PublicKey: h.wg.PublicKey().String(),
		Endpoint:  h.wgConfig.Endpoint,
		Network:   h.wgConfig.Network,
		MTU:       h.wgConfig.MTU,
	})
}
