package api

import (
	"errors"
	"net/http"
	"strconv"

	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

// deleteUser removes a user and cascades through everything attributable to them. The
// database cascade is atomic (store.DeleteCascade); afterwards the live tunnel and
// isolation rules are synced best-effort (the database is the source of truth, so a
// transient data-plane failure is logged but does not fail the request — the startup
// rebuild reconciles any gap). Admin only (AdminRequired wraps the route).
func (h *handlers) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok { // defensive: AdminRequired guarantees identity
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	targetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "User not found.")
		return
	}
	// An admin may not delete their own account through this operation (FR-012).
	if targetID == id.UserID {
		protocol.WriteJSONError(w, http.StatusForbidden, "cannot_delete_self", "You cannot delete your own account.")
		return
	}

	result, err := h.store.Users().DeleteCascade(r.Context(), targetID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrUserNotFound):
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "User not found.")
		case errors.Is(err, store.ErrLastAdmin):
			protocol.WriteJSONError(w, http.StatusConflict, "last_admin", "Cannot delete the last administrator.")
		default:
			h.serverError(w, err)
		}
		return
	}

	// Best-effort data-plane reconciliation against the now-deleted records.
	for _, pub := range result.NodePubKeys {
		if err := h.wg.RemovePeer(pub); err != nil {
			h.log.Error("failed to remove peer for deleted user's node", "user_id", targetID, "error", err.Error())
		}
	}
	for _, m := range result.SurvivingMemberships {
		if err := h.netfw.RemoveMember(m.ZoneID, m.IP); err != nil {
			h.log.Error("failed to remove set element for deleted user's node", "user_id", targetID, "zone_id", m.ZoneID, "error", err.Error())
		}
	}
	for _, zid := range result.OwnedZoneIDs {
		if err := h.netfw.DeleteZone(zid); err != nil {
			h.log.Error("failed to delete owned zone's rules", "user_id", targetID, "zone_id", zid, "error", err.Error())
		}
	}
	// Announced blocks of the deleted user attached to surviving zones: remove
	// the route elements (owned zones' sets were destroyed wholesale above).
	for _, route := range result.SurvivingZoneRoutes {
		if err := h.netfw.RemoveZoneRoute(route.ZoneID, route.Synthetic); err != nil {
			h.log.Error("failed to remove zone route for deleted user", "user_id", targetID, "zone_id", route.ZoneID, "error", err.Error())
		}
	}
	// Surviving nodes (other users') whose announcements were orphaned by the
	// owned-zone cascade: their peers' AllowedIPs shrink to the recomputed set.
	for _, n := range result.RouteRecomputeNodes {
		routes, err := h.store.Announcements().RoutesForNode(r.Context(), n.NodeID)
		if err != nil {
			h.log.Error("failed to recompute routes for surviving node", "node_id", n.NodeID, "error", err.Error())
			continue
		}
		if err := h.wg.SetPeerRoutes(n.PubKey, n.IP, routes); err != nil {
			h.log.Error("failed to update surviving node's peer routes", "node_id", n.NodeID, "error", err.Error())
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
