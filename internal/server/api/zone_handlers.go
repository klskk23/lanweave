package api

import (
	"errors"
	"net/http"
	"strings"

	"lanweave/internal/server/auth"
	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

const minZonePasswordLen = 8

// createZone creates a password-protected zone owned by the caller and installs its
// (empty) isolation set + accept rule. Insert + nft are atomic: on nft failure the
// zone row is removed.
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

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		h.serverError(w, err)
		return
	}
	zone, err := h.store.Zones().Create(r.Context(), id.UserID, name, hash)
	if err != nil {
		if errors.Is(err, store.ErrZoneNameTaken) {
			protocol.WriteJSONError(w, http.StatusConflict, "zone_name_taken", "That zone name is already taken.")
			return
		}
		h.serverError(w, err)
		return
	}
	if err := h.netfw.AddZone(zone.ID); err != nil {
		// Compensate: remove the zone so DB and nftables stay consistent.
		if derr := h.store.Zones().Delete(r.Context(), zone.ID); derr != nil {
			h.log.Error("failed to roll back zone after nft failure", "zone_id", zone.ID, "error", derr.Error())
		}
		h.serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, protocol.ZoneResponse{ID: zone.ID, Name: zone.Name, IsOwner: true})
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
		items = append(items, protocol.ZoneMemberResponse{NodeName: m.NodeName, IP: m.IP.String(), Owner: m.OwnerName})
	}
	writeJSON(w, http.StatusOK, protocol.ZoneMembersResponse{Members: items})
}

// zoneOrPassword writes the generic no-enumeration join failure (403). 403 (not 401)
// is deliberate: the caller IS authenticated, they just lack access to this zone.
func (h *handlers) zoneOrPassword(w http.ResponseWriter) {
	protocol.WriteJSONError(w, http.StatusForbidden, "invalid_zone_or_password", "Invalid zone or password.")
}
