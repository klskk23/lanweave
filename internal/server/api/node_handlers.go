package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/ipam"
	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

// statusProvider reports per-node online state derived from the live tunnel.
// Satisfied by *status.Tracker; an interface keeps the API package independent of
// the data plane and lets handler tests use a fake over a real store.
type statusProvider interface {
	Online(pubKey string) bool
	LastHandshake(pubKey string) (time.Time, bool)
}

// registerNode allocates an address, persists the node, and adds its tunnel peer.
func (h *handlers) registerNode(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	var req protocol.RegisterNodeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Node name must be 1-64 characters.")
		return
	}
	if _, err := wgtypes.ParseKey(req.WGPubKey); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid WireGuard public key.")
		return
	}

	first, last, err := ipam.PoolRange(h.wgConfig.Network)
	if err != nil {
		h.serverError(w, err)
		return
	}

	// Admin is exempt from the cap; passing 0 reuses the store's unlimited path so no
	// separate role check leaks into persistence (research.md Decision 2).
	maxDevices := h.maxDevicesPerUser
	if id.IsAdmin {
		maxDevices = 0
	}
	node, err := h.store.Nodes().Create(r.Context(), id.UserID, name, req.WGPubKey, first, last, maxDevices)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNodeNameTaken):
			protocol.WriteJSONError(w, http.StatusConflict, "node_name_taken", "You already have a node with that name.")
		case errors.Is(err, store.ErrPubKeyTaken):
			protocol.WriteJSONError(w, http.StatusConflict, "pubkey_taken", "That public key is already registered.")
		case errors.Is(err, store.ErrDeviceLimitReached):
			protocol.WriteJSONError(w, http.StatusConflict, "device_limit_reached", "You have reached your device limit.")
		case errors.Is(err, store.ErrPoolExhausted):
			protocol.WriteJSONError(w, http.StatusServiceUnavailable, "pool_exhausted", "No addresses are available in the pool.")
		default:
			h.serverError(w, err)
		}
		return
	}

	// Add the tunnel peer; on failure compensate by deleting the node so the
	// database and tunnel never drift (FR-004).
	if err := h.wg.AddPeer(node.PubKey, node.IP); err != nil {
		if _, derr := h.store.Nodes().DeleteOwned(r.Context(), id.UserID, node.ID); derr != nil {
			h.log.Error("failed to roll back node after peer-add failure", "node_id", node.ID, "error", derr.Error())
		}
		h.serverError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, protocol.NodeResponse{ID: node.ID, Name: node.Name, IP: node.IP.String()})
}

// listNodes returns the caller's own nodes.
func (h *handlers) listNodes(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	nodes, err := h.store.Nodes().ListByUser(r.Context(), id.UserID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	items := make([]protocol.NodeResponse, 0, len(nodes))
	for _, n := range nodes {
		item := protocol.NodeResponse{
			ID:        n.ID,
			Name:      n.Name,
			IP:        n.IP.String(),
			CreatedAt: n.CreatedAt.Format(time.RFC3339),
			Online:    h.status.Online(n.PubKey),
		}
		if ts, ok := h.status.LastHandshake(n.PubKey); ok {
			item.LastHandshake = ts.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, protocol.NodeListResponse{Nodes: items})
}

// deleteNode removes the caller's node and its tunnel peer.
func (h *handlers) deleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Node not found.")
		return
	}

	// Look up the owned node (for its address + zones) before deleting it.
	node, err := h.store.Nodes().GetOwned(r.Context(), id.UserID, nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Node not found.")
			return
		}
		h.serverError(w, err)
		return
	}
	zoneIDs, err := h.store.Zones().ZonesForNode(r.Context(), nodeID)
	if err != nil {
		h.serverError(w, err)
		return
	}

	if _, err := h.store.Nodes().DeleteOwned(r.Context(), id.UserID, nodeID); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Node not found.")
			return
		}
		h.serverError(w, err)
		return
	}

	// DB is authoritative; remove the peer and the node's address from every zone
	// set best-effort. Clearing the set elements is essential: feature-004 IP
	// recycling means a stale element would let a new node inherit this node's zone
	// reachability (FR-018). The startup rebuild reconciles any best-effort gap.
	if err := h.wg.RemovePeer(node.PubKey); err != nil {
		h.log.Error("failed to remove peer for deleted node", "node_id", nodeID, "error", err.Error())
	}
	for _, zid := range zoneIDs {
		if err := h.netfw.RemoveMember(zid, node.IP); err != nil {
			h.log.Error("failed to remove set element for deleted node", "node_id", nodeID, "zone_id", zid, "error", err.Error())
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
