package api_test

import (
	"net/http"
	"testing"

	"lanweave/pkg/protocol"
)

// US2 — admin issues and lists invites; non-admins and anonymous are refused.
func TestAdminCreateAndListInvites(t *testing.T) {
	h := newHarness(t)
	adminToken := h.loginToken("admin", h.adminPW)

	code := h.createInviteCode(adminToken)

	rec := h.do(http.MethodGet, "/api/v1/admin/invites", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d", rec.Code)
	}
	var list protocol.InviteListResponse
	decodeJSONBody(t, rec.Body.Bytes(), &list)
	if len(list.Invites) != 1 {
		t.Fatalf("expected 1 invite, got %d", len(list.Invites))
	}
	if list.Invites[0].Code != code || list.Invites[0].Status != "unused" {
		t.Errorf("unexpected invite item: %+v", list.Invites[0])
	}
}

func TestInviteEndpointsAuthz(t *testing.T) {
	h := newHarness(t)
	adminToken := h.loginToken("admin", h.adminPW)

	// Register a non-admin and log in as them.
	code := h.createInviteCode(adminToken)
	h.do(http.MethodPost, "/api/v1/register", "", protocol.RegisterRequest{
		InviteCode: code, Username: "bob", Password: "bobs-strong-pw",
	})
	bobToken := h.loginToken("bob", "bobs-strong-pw")

	// Non-admin → 403 on both invite endpoints.
	if rec := h.do(http.MethodPost, "/api/v1/admin/invites", bobToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin create: status %d, want 403", rec.Code)
	}
	if rec := h.do(http.MethodGet, "/api/v1/admin/invites", bobToken, nil); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin list: status %d, want 403", rec.Code)
	}

	// Anonymous → 401.
	if rec := h.do(http.MethodPost, "/api/v1/admin/invites", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon create: status %d, want 401", rec.Code)
	}
}
