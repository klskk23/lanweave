package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

const minZonePasswordLen = 8

// createZone creates a password-protected zone owned by the caller and installs its
// (empty) isolation set + accept rule. When req.NodeID names one of the caller's nodes,
// that node is auto-joined in the same operation. The whole thing is atomic: on any
// failure after the zone row is inserted, the zone is removed so DB and nftables stay
// consistent.
func (h *handlers) createZone(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	var req protocol.CreateZoneRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	name := strings.TrimSpace(req.Name)
	switch {
	case name == "" || len(name) > 64:
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Zone name must be 1-64 characters.")
		return
	case len(req.Password) < minZonePasswordLen:
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Zone password must be at least 8 characters.")
		return
	}

	// Resolve the auto-join node BEFORE creating anything: a foreign/unknown node_id must
	// not leave an orphaned zone. GetOwned conflates "absent" and "not yours" (no enum).
	var member *store.Node
	if req.NodeID != 0 {
		n, err := h.store.Nodes().GetOwned(r.Context(), id.UserID, req.NodeID)
		if err != nil {
			if errors.Is(err, store.ErrNodeNotFound) {
				protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Node not found.")
				return
			}
			h.serverError(w, err)
			return
		}
		member = n
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.serverError(w, err)
		return
	}
	// Admin is exempt from the cap; passing 0 reuses the store's unlimited path so no
	// separate role check leaks into persistence (research.md Decision 2).
	maxOwnedZones := h.maxOwnedZonesPerUser
	if id.IsAdmin {
		maxOwnedZones = 0
	}
	zone, err := h.store.Zones().Create(r.Context(), id.UserID, name, hash, maxOwnedZones)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrZoneNameTaken):
			protocol.WriteJSONError(w, http.StatusConflict, "zone_name_taken", "That zone name is already taken.")
		case errors.Is(err, store.ErrOwnedZoneLimitReached):
			protocol.WriteJSONError(w, http.StatusConflict, "zone_limit_reached", "You have reached your zone limit.")
		default:
			h.serverError(w, err)
		}
		return
	}
	if err := h.netfw.AddZone(zone.ID); err != nil {
		// Compensate: remove the zone so DB and nftables stay consistent. (Nothing to tear
		// down in nft since AddZone failed.)
		if derr := h.store.Zones().Delete(r.Context(), zone.ID); derr != nil {
			h.log.Error("failed to roll back zone after nft failure", "zone_id", zone.ID, "error", derr.Error())
		}
		h.serverError(w, err)
		return
	}

	if member != nil {
		if err := h.store.Zones().Join(r.Context(), zone.ID, member.ID); err != nil {
			h.rollbackZone(r.Context(), zone.ID)
			h.serverError(w, err)
			return
		}
		if err := h.netfw.AddMember(zone.ID, member.IP); err != nil {
			h.rollbackZone(r.Context(), zone.ID)
			h.serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, protocol.ZoneResponse{ID: zone.ID, Name: zone.Name, IsOwner: true})
}

// rollbackZone undoes a partially created zone after an auto-join step fails: delete the
// row (cascading any membership) and best-effort destroy its nft set + rule, so neither
// DB nor kernel keeps a zone the creator is not a consistent member of.
func (h *handlers) rollbackZone(ctx context.Context, zoneID int64) {
	if err := h.store.Zones().Delete(ctx, zoneID); err != nil {
		h.log.Error("failed to roll back zone after auto-join failure", "zone_id", zoneID, "error", err.Error())
	}
	if err := h.netfw.DeleteZone(zoneID); err != nil {
		h.log.Error("failed to delete zone nftables state on rollback", "zone_id", zoneID, "error", err.Error())
	}
}

// joinZone admits one of the caller's nodes to a zone (name + password). Unknown
// zone and wrong password return one generic error (no enumeration).
func (h *handlers) joinZone(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	var req protocol.JoinZoneRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	if req.NodeID == 0 || req.Password == "" {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "node_id and password are required.")
		return
	}

	zone, err := h.store.Zones().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		h.serverError(w, err)
		return
	}
	if zone == nil {
		auth.DummyVerify(req.Password) // equalize timing; do not disclose existence
		h.zoneOrPassword(w)
		return
	}
	if okPw, _ := auth.VerifyPassword(req.Password, zone.PasswordHash); !okPw {
		h.zoneOrPassword(w)
		return
	}

	node, err := h.store.Nodes().GetOwned(r.Context(), id.UserID, req.NodeID)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Node not found.")
			return
		}
		h.serverError(w, err)
		return
	}

	if err := h.store.Zones().Join(r.Context(), zone.ID, node.ID); err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.netfw.AddMember(zone.ID, node.IP); err != nil {
		// Compensate: undo the membership so DB and nftables stay consistent.
		if lerr := h.store.Zones().Leave(r.Context(), zone.ID, node.ID); lerr != nil {
			h.log.Error("failed to roll back membership after nft failure", "zone_id", zone.ID, "node_id", node.ID, "error", lerr.Error())
		}
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// leaveZone removes one of the caller's nodes from a zone.
func (h *handlers) leaveZone(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	var req protocol.LeaveZoneRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}

	zone, err := h.store.Zones().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		h.serverError(w, err)
		return
	}
	if zone == nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	node, err := h.store.Nodes().GetOwned(r.Context(), id.UserID, req.NodeID)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		h.serverError(w, err)
		return
	}
	if err := h.store.Zones().Leave(r.Context(), zone.ID, node.ID); err != nil {
		if errors.Is(err, store.ErrNotMember) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		h.serverError(w, err)
		return
	}
	// DB is authoritative; remove the set element best-effort (startup reconciles).
	if err := h.netfw.RemoveMember(zone.ID, node.IP); err != nil {
		h.log.Error("failed to remove set element on leave", "zone_id", zone.ID, "node_id", node.ID, "error", err.Error())
	}
	h.cascadeNodeZoneAnnouncements(r.Context(), node, zone.ID)
	w.WriteHeader(http.StatusNoContent)
}

// listZones returns the zones the caller participates in.
func (h *handlers) listZones(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zones, err := h.store.Zones().ListForUser(r.Context(), id.UserID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	items := make([]protocol.ZoneResponse, 0, len(zones))
	for _, z := range zones {
		items = append(items, protocol.ZoneResponse{ID: z.ID, Name: z.Name, IsOwner: z.IsOwner})
	}
	writeJSON(w, http.StatusOK, protocol.ZoneListResponse{Zones: items})
}

// zoneMembers lists a zone's members; visible only to participants.
func (h *handlers) zoneMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, err := h.store.Zones().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		h.serverError(w, err)
		return
	}
	if zone == nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	participant, err := h.store.Zones().IsParticipant(r.Context(), zone.ID, id.UserID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if !participant {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	members, err := h.store.Zones().MembersByZone(r.Context(), zone.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	items := make([]protocol.ZoneMemberResponse, 0, len(members))
	for _, m := range members {
		items = append(items, protocol.ZoneMemberResponse{NodeID: m.NodeID, NodeName: m.NodeName, IP: m.IP.String(), Owner: m.OwnerName})
	}
	writeJSON(w, http.StatusOK, protocol.ZoneMembersResponse{Members: items})
}

// zoneOrPassword writes the generic no-enumeration join failure (403). 403 (not 401)
// is deliberate: the caller IS authenticated, they just lack access to this zone.
func (h *handlers) zoneOrPassword(w http.ResponseWriter) {
	protocol.WriteJSONError(w, http.StatusForbidden, "invalid_zone_or_password", "Invalid zone or password.")
}

// ownedZone resolves the path zone and enforces owner-only access for the owner
// operations (feature 006): missing zone → 404, authenticated non-owner → 403. It
// returns (zone, true) only when the caller owns the zone. It writes the error and
// returns ok=false otherwise. The owner check runs BEFORE any node/membership check
// so a non-owner never learns whether a node or membership exists.
func (h *handlers) ownedZone(w http.ResponseWriter, r *http.Request, userID int64) (*store.Zone, bool) {
	zone, err := h.store.Zones().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		h.serverError(w, err)
		return nil, false
	}
	if zone == nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return nil, false
	}
	if zone.OwnerID != userID {
		protocol.WriteJSONError(w, http.StatusForbidden, "forbidden", "Only the zone owner may perform this operation.")
		return nil, false
	}
	return zone, true
}

// changeZonePassword lets the owner rotate the password without ejecting members.
func (h *handlers) changeZonePassword(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, ok := h.ownedZone(w, r, id.UserID)
	if !ok {
		return
	}
	var req protocol.ChangeZonePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	if len(req.Password) < minZonePasswordLen {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Zone password must be at least 8 characters.")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.store.Zones().UpdatePassword(r.Context(), zone.ID, hash); err != nil {
		h.serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// kickMember lets the owner remove any member node from the zone (incl. another
// user's). Precedence: owner gate (403/404) → node exists (404) → is a member (404).
func (h *handlers) kickMember(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, ok := h.ownedZone(w, r, id.UserID)
	if !ok {
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("node_id"), 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	node, err := h.store.Nodes().GetByID(r.Context(), nodeID)
	if err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		h.serverError(w, err)
		return
	}
	if err := h.store.Zones().Leave(r.Context(), zone.ID, node.ID); err != nil {
		if errors.Is(err, store.ErrNotMember) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		h.serverError(w, err)
		return
	}
	// DB is authoritative; remove the set element best-effort (startup reconciles).
	if err := h.netfw.RemoveMember(zone.ID, node.IP); err != nil {
		h.log.Error("failed to remove set element on kick", "zone_id", zone.ID, "node_id", node.ID, "error", err.Error())
	}
	h.cascadeNodeZoneAnnouncements(r.Context(), node, zone.ID)
	w.WriteHeader(http.StatusNoContent)
}

// deleteZone lets the owner delete the zone: memberships cascade, the set + rule are
// destroyed, and the name is released. Member nodes are not deleted.
func (h *handlers) deleteZone(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, ok := h.ownedZone(w, r, id.UserID)
	if !ok {
		return
	}
	// Detach the zone's announcements first (reclaiming bodies whose last
	// attachment this was — including other users' announcements made into this
	// zone alone, whose synthetic blocks would otherwise leak forever).
	dets, err := h.store.Announcements().DetachAllForZone(r.Context(), zone.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if err := h.store.Zones().Delete(r.Context(), zone.ID); err != nil {
		h.serverError(w, err)
		return
	}
	// DB is authoritative; destroy the set + rule best-effort (startup reconciles).
	// The routes set dies with the zone; only reclaimed announcements need their
	// peers' AllowedIPs recomputed.
	if err := h.netfw.DeleteZone(zone.ID); err != nil {
		h.log.Error("failed to delete zone nftables state", "zone_id", zone.ID, "error", err.Error())
	}
	h.recomputeReclaimedPeers(r.Context(), dets)
	w.WriteHeader(http.StatusNoContent)
}

// cascadeNodeZoneAnnouncements removes the node's announcement attachments to a
// zone it just left (leave/kick), shrinking the zone routes set and — when a
// last attachment was reclaimed — the peer's AllowedIPs. Best-effort after the
// authoritative DB change (startup rebuild reconciles any gap).
func (h *handlers) cascadeNodeZoneAnnouncements(ctx context.Context, node *store.Node, zoneID int64) {
	dets, err := h.store.Announcements().DetachAllForNodeZone(ctx, node.ID, zoneID)
	if err != nil {
		h.log.Error("failed to detach announcements on zone exit", "zone_id", zoneID, "node_id", node.ID, "error", err.Error())
		return
	}
	reclaimed := false
	for _, det := range dets {
		if err := h.netfw.RemoveZoneRoute(zoneID, det.Synthetic); err != nil {
			h.log.Error("failed to remove zone route on zone exit", "zone_id", zoneID, "error", err.Error())
		}
		reclaimed = reclaimed || det.Reclaimed
	}
	if reclaimed {
		routes, err := h.store.Announcements().RoutesForNode(ctx, node.ID)
		if err != nil {
			h.log.Error("failed to recompute routes on zone exit", "node_id", node.ID, "error", err.Error())
			return
		}
		if err := h.wg.SetPeerRoutes(node.PubKey, node.IP, routes); err != nil {
			h.log.Error("failed to shrink peer routes on zone exit", "node_id", node.ID, "error", err.Error())
		}
	}
}

// recomputeReclaimedPeers recomputes AllowedIPs for every node that lost an
// announcement body in a zone-wide detach (zone deletion). Best-effort.
func (h *handlers) recomputeReclaimedPeers(ctx context.Context, dets []store.ZoneDetachment) {
	seen := map[int64]bool{}
	for _, det := range dets {
		if !det.Reclaimed || seen[det.NodeID] {
			continue
		}
		seen[det.NodeID] = true
		node, err := h.store.Nodes().GetByID(ctx, det.NodeID)
		if err != nil {
			h.log.Error("failed to load node for route recompute", "node_id", det.NodeID, "error", err.Error())
			continue
		}
		routes, err := h.store.Announcements().RoutesForNode(ctx, det.NodeID)
		if err != nil {
			h.log.Error("failed to recompute routes", "node_id", det.NodeID, "error", err.Error())
			continue
		}
		if err := h.wg.SetPeerRoutes(node.PubKey, node.IP, routes); err != nil {
			h.log.Error("failed to update peer routes", "node_id", det.NodeID, "error", err.Error())
		}
	}
}
