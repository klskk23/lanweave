package api_test

import (
	"net/http"
	"testing"
	"time"

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

// TestCreateInviteExpiry — with invite_ttl>0 the create response carries an
// expires_at ≈ now+TTL; with expiry disabled the field is omitted (FR-009 / SC-006).
func TestCreateInviteExpiry(t *testing.T) {
	h := newHarnessOpts(t, 24*time.Hour)
	adminToken := h.loginToken("admin", h.adminPW)

	before := time.Now().UTC()
	rec := h.do(http.MethodPost, "/api/v1/admin/invites", adminToken, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body.String())
	}
	var resp protocol.CreateInviteResponse
	decodeJSONBody(t, rec.Body.Bytes(), &resp)
	if resp.ExpiresAt == nil {
		t.Fatal("expected expires_at when invite_ttl>0")
	}
	exp, err := time.Parse(time.RFC3339, *resp.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expires_at %q: %v", *resp.ExpiresAt, err)
	}
	if exp.Before(before.Add(23*time.Hour)) || exp.After(time.Now().UTC().Add(25*time.Hour)) {
		t.Errorf("expires_at %v not ≈ now+24h", exp)
	}

	// Expiry disabled → the response omits expires_at.
	h2 := newHarnessOpts(t, 0)
	adminToken2 := h2.loginToken("admin", h2.adminPW)
	rec2 := h2.do(http.MethodPost, "/api/v1/admin/invites", adminToken2, nil)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("create (disabled) status %d: %s", rec2.Code, rec2.Body.String())
	}
	var resp2 protocol.CreateInviteResponse
	decodeJSONBody(t, rec2.Body.Bytes(), &resp2)
	if resp2.ExpiresAt != nil {
		t.Errorf("expected no expires_at when disabled, got %v", *resp2.ExpiresAt)
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
