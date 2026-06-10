package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"lanweave/internal/server/ipam"
	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

// announcePlatforms maps a node's self-reported platform to announcement
// capability. Only platforms whose client performs the synthetic↔real address
// translation (NETMAP + masquerade) may announce; this is the single place to
// extend when another platform learns the trick.
var announcePlatforms = map[string]bool{"openwrt": true}

// memberZone resolves the path zone and requires the caller to participate in
// it; both "no such zone" and "not yours to see" answer the same 404 (matching
// zoneMembers, FR-008).
func (h *handlers) memberZone(w http.ResponseWriter, r *http.Request, userID int64) (*store.Zone, bool) {
	zone, err := h.store.Zones().GetByName(r.Context(), r.PathValue("name"))
	if err != nil {
		h.serverError(w, err)
		return nil, false
	}
	if zone == nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return nil, false
	}
	participant, err := h.store.Zones().IsParticipant(r.Context(), zone.ID, userID)
	if err != nil {
		h.serverError(w, err)
		return nil, false
	}
	if !participant {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return nil, false
	}
	return zone, true
}

// createAnnouncement announces one of the caller's nodes' real subnets into the
// zone: validate → allocate/attach (one store transaction) → grow the dataplane
// (peer AllowedIPs + zone routes set), compensating on dataplane failure so DB
// and kernel state never drift (the 015 create-then-cleanup pattern).
func (h *handlers) createAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, ok := h.memberZone(w, r, id.UserID)
	if !ok {
		return
	}
	var req protocol.CreateAnnouncementRequest
	if err := decodeJSON(w, r, &req); err != nil {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Invalid request body.")
		return
	}
	if !h.announcePool.IsValid() {
		protocol.WriteJSONError(w, http.StatusServiceUnavailable, "announce_disabled", "Subnet announcements are disabled on this server (no synthetic pool configured).")
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
	if !announcePlatforms[node.Platform] {
		protocol.WriteJSONError(w, http.StatusConflict, "platform_unsupported", "This node's platform cannot announce subnets (requires a router client that performs address translation).")
		return
	}

	prefix, err := netip.ParsePrefix(req.Subnet)
	if err != nil || !prefix.Addr().Is4() {
		protocol.WriteJSONError(w, http.StatusBadRequest, "validation_error", "Subnet must be an IPv4 CIDR like 192.168.1.0/24.")
		return
	}
	prefix = prefix.Masked()
	if msg, ok := h.validAnnouncedSubnet(prefix); !ok {
		protocol.WriteJSONError(w, http.StatusBadRequest, "subnet_invalid", msg)
		return
	}

	limit := h.maxAnnouncedSubnetsPerUser
	if id.IsAdmin {
		limit = 0
	}
	ann, attached, err := h.store.Announcements().Create(r.Context(), id.UserID, node.ID,
		ipam.BlockFromPrefix(prefix), zone.ID, limit, h.announcePool)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotMember):
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Node is not a member of this zone.")
		case errors.Is(err, store.ErrSubnetOverlap):
			protocol.WriteJSONError(w, http.StatusConflict, "subnet_overlap", "The subnet overlaps another announcement of the same node.")
		case errors.Is(err, store.ErrAnnounceLimit):
			protocol.WriteJSONError(w, http.StatusConflict, "announce_limit_reached", "You have reached your announced subnet limit.")
		case errors.Is(err, store.ErrSyntheticPoolExhausted):
			protocol.WriteJSONError(w, http.StatusServiceUnavailable, "synthetic_pool_exhausted", "No synthetic address block of that size is available.")
		default:
			h.serverError(w, err)
		}
		return
	}

	// Dataplane growth only for a fresh attachment; an idempotent re-attach
	// already has its kernel state in place.
	if attached {
		routes, err := h.store.Announcements().RoutesForNode(r.Context(), node.ID)
		if err != nil {
			h.rollbackAnnouncement(r, zone.ID, ann.ID, node)
			h.serverError(w, err)
			return
		}
		if err := h.wg.SetPeerRoutes(node.PubKey, node.IP, routes); err != nil {
			h.rollbackAnnouncement(r, zone.ID, ann.ID, node)
			h.serverError(w, err)
			return
		}
		if err := h.netfw.AddZoneRoute(zone.ID, ann.Synthetic.Prefix()); err != nil {
			h.rollbackAnnouncement(r, zone.ID, ann.ID, node)
			h.serverError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusCreated, protocol.AnnouncementResponse{
		ID:        ann.ID,
		NodeID:    node.ID,
		NodeName:  node.Name,
		Owner:     id.Username,
		Subnet:    ann.Real.Prefix().String(),
		Synthetic: ann.Synthetic.Prefix().String(),
	})
}

// validAnnouncedSubnet enforces the FR-007 matrix: RFC1918 only, /16–/30, no
// overlap with the VPN pool or the synthetic pool. Cross-node overlap is
// deliberately allowed (the point of synthetic mapping).
func (h *handlers) validAnnouncedSubnet(prefix netip.Prefix) (string, bool) {
	if prefix.Bits() < 16 || prefix.Bits() > 30 {
		return "Subnet prefix length must be between /16 and /30.", false
	}
	if !ipam.IsRFC1918(prefix) {
		return "Subnet must be a private (RFC1918) range.", false
	}
	block := ipam.BlockFromPrefix(prefix)
	if vpn, err := netip.ParsePrefix(h.wgConfig.Network); err == nil {
		if ipam.Overlaps(block, ipam.BlockFromPrefix(vpn)) {
			return "Subnet overlaps the VPN address pool.", false
		}
	}
	if ipam.Overlaps(block, ipam.BlockFromPrefix(h.announcePool)) {
		return "Subnet overlaps the synthetic address pool.", false
	}
	return "", true
}

// rollbackAnnouncement undoes a freshly created attachment after a dataplane
// failure (best effort, logged): detach (reclaiming the body when this was the
// only attachment) and recompute the peer's routes from the database.
func (h *handlers) rollbackAnnouncement(r *http.Request, zoneID, annID int64, node *store.Node) {
	if _, _, err := h.store.Announcements().Detach(r.Context(), zoneID, annID); err != nil {
		h.log.Error("failed to roll back announcement after dataplane failure", "announcement_id", annID, "error", err.Error())
		return
	}
	routes, err := h.store.Announcements().RoutesForNode(r.Context(), node.ID)
	if err != nil {
		h.log.Error("failed to recompute routes on rollback", "node_id", node.ID, "error", err.Error())
		return
	}
	if err := h.wg.SetPeerRoutes(node.PubKey, node.IP, routes); err != nil {
		h.log.Error("failed to shrink peer routes on rollback", "node_id", node.ID, "error", err.Error())
	}
}

// deleteAnnouncement detaches an announcement from the zone. The announcing
// node's owner may detach their own; the zone owner may detach any attachment in
// their zone (the kick-member authority model, FR-008). Everyone else sees 404.
func (h *handlers) deleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, ok := h.memberZone(w, r, id.UserID)
	if !ok {
		return
	}
	annID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	ann, err := h.store.Announcements().Get(r.Context(), annID)
	if err != nil {
		if errors.Is(err, store.ErrAnnouncementNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		h.serverError(w, err)
		return
	}
	node, err := h.store.Nodes().GetByID(r.Context(), ann.NodeID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	if node.UserID != id.UserID && zone.OwnerID != id.UserID {
		protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}

	_, reclaimed, err := h.store.Announcements().Detach(r.Context(), zone.ID, annID)
	if err != nil {
		if errors.Is(err, store.ErrAnnouncementNotFound) {
			protocol.WriteJSONError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		h.serverError(w, err)
		return
	}

	// DB is authoritative; shrink the dataplane best-effort (008 pattern, the
	// startup rebuild reconciles any gap).
	if err := h.netfw.RemoveZoneRoute(zone.ID, ann.Synthetic.Prefix()); err != nil {
		h.log.Error("failed to remove zone route", "zone_id", zone.ID, "announcement_id", annID, "error", err.Error())
	}
	if reclaimed {
		routes, err := h.store.Announcements().RoutesForNode(r.Context(), node.ID)
		if err != nil {
			h.log.Error("failed to recompute routes after detach", "node_id", node.ID, "error", err.Error())
		} else if err := h.wg.SetPeerRoutes(node.PubKey, node.IP, routes); err != nil {
			h.log.Error("failed to shrink peer routes after detach", "node_id", node.ID, "error", err.Error())
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// listAnnouncements returns the zone's real→synthetic subnet mappings (any
// member; the data members need to know which synthetic address to dial).
func (h *handlers) listAnnouncements(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok {
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	zone, ok := h.memberZone(w, r, id.UserID)
	if !ok {
		return
	}
	anns, err := h.store.Announcements().ListByZone(r.Context(), zone.ID)
	if err != nil {
		h.serverError(w, err)
		return
	}
	items := make([]protocol.AnnouncementResponse, 0, len(anns))
	for _, a := range anns {
		items = append(items, protocol.AnnouncementResponse{
			ID:        a.ID,
			NodeID:    a.NodeID,
			NodeName:  a.NodeName,
			Owner:     a.Owner,
			Subnet:    a.Real.Prefix().String(),
			Synthetic: a.Synthetic.Prefix().String(),
		})
	}
	writeJSON(w, http.StatusOK, protocol.AnnouncementListResponse{Announcements: items})
}
