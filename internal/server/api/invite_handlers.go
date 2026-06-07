package api

import (
	"net/http"
	"time"

	"lanweave/internal/server/store"
	"lanweave/pkg/protocol"
)

// createInvite mints a new one-time invite code attributed to the calling admin.
func (h *handlers) createInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r.Context())
	if !ok { // defensive: AdminRequired guarantees identity
		protocol.WriteJSONError(w, http.StatusUnauthorized, "unauthorized", "Authentication required.")
		return
	}
	code, expiresAt, err := h.store.Invites().Create(r.Context(), id.UserID, h.inviteTTL)
	if err != nil {
		h.serverError(w, err)
		return
	}
	resp := protocol.CreateInviteResponse{Code: code}
	if expiresAt != nil {
		s := expiresAt.Format(time.RFC3339)
		resp.ExpiresAt = &s
	}
	writeJSON(w, http.StatusCreated, resp)
}

// listInvites returns all invites, newest first.
func (h *handlers) listInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := h.store.Invites().List(r.Context())
	if err != nil {
		h.serverError(w, err)
		return
	}
	items := make([]protocol.InviteListItem, 0, len(invites))
	for _, inv := range invites {
		items = append(items, toInviteListItem(inv))
	}
	writeJSON(w, http.StatusOK, protocol.InviteListResponse{Invites: items})
}

func toInviteListItem(inv store.Invite) protocol.InviteListItem {
	// Status precedence: a redeemed code is "used" regardless of expiry; an unused
	// code past its expiry moment is "expired"; otherwise "unused". A NULL
	// expires_at never counts as expired.
	status := "unused"
	var usedAt *string
	if inv.UsedAt != nil {
		status = "used"
		s := inv.UsedAt.Format(time.RFC3339)
		usedAt = &s
	} else if inv.ExpiresAt != nil && inv.ExpiresAt.Before(time.Now()) {
		status = "expired"
	}
	var expiresAt *string
	if inv.ExpiresAt != nil {
		s := inv.ExpiresAt.Format(time.RFC3339)
		expiresAt = &s
	}
	return protocol.InviteListItem{
		Code:      inv.Code,
		Status:    status,
		CreatedBy: inv.CreatedByName,
		CreatedAt: inv.CreatedAt.Format(time.RFC3339),
		UsedBy:    inv.UsedByName,
		UsedAt:    usedAt,
		ExpiresAt: expiresAt,
	}
}
