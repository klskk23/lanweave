package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/nftables"

	"lanweave/internal/server/auth"
	"lanweave/pkg/protocol"
)

// adminToken issues an admin JWT for the given user id (the privileged harness's
// token() issues non-admin tokens).
func (h *nodeHarness) adminToken(userID int64) string {
	tok, _ := h.jwt.Issue(auth.Claims{UserID: userID, Username: "admin", IsAdmin: true})
	return tok
}

// nftSetExists reports whether a zone's isolation set is present (without fataling, so
// it can assert a set's absence).
func nftSetExists(t *testing.T, table string, zoneID int64) bool {
	t.Helper()
	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("nft conn: %v", err)
	}
	_, err = conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: table}, fmt.Sprintf("zone_%d", zoneID))
	return err == nil
}

// TestDeleteUserGuards (non-privileged) covers the rejection paths, which never touch
// the data plane: unauthenticated → 401, non-admin → 403, self-delete → 403
// cannot_delete_self, unknown id → 404.
func TestDeleteUserGuards(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	adminTok := h.loginToken("admin", h.adminPW)
	admin, err := h.store.Users().GetByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("look up admin: %v", err)
	}
	selfPath := "/api/v1/admin/users/" + strconv.FormatInt(admin.ID, 10)

	if rec := h.do(http.MethodDelete, selfPath, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated: %d, want 401", rec.Code)
	}
	nonAdmin, _ := h.jwt.Issue(auth.Claims{UserID: 4242, Username: "nobody", IsAdmin: false})
	if rec := h.do(http.MethodDelete, "/api/v1/admin/users/1", nonAdmin, nil); rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: %d, want 403", rec.Code)
	}
	rec := h.do(http.MethodDelete, selfPath, adminTok, nil)
	if rec.Code != http.StatusForbidden || decodeError(t, rec).Error != "cannot_delete_self" {
		t.Errorf("self-delete: status %d body %s", rec.Code, rec.Body.String())
	}
	if rec := h.do(http.MethodDelete, "/api/v1/admin/users/99999", adminTok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: %d, want 404", rec.Code)
	}
	// A different admin caller deleting the sole real admin → 409 last_admin (the guard
	// passes the self-check because the caller's id differs, then the store refuses).
	otherAdmin, _ := h.jwt.Issue(auth.Claims{UserID: 9000, Username: "ghost-admin", IsAdmin: true})
	if rec := h.do(http.MethodDelete, selfPath, otherAdmin, nil); rec.Code != http.StatusConflict || decodeError(t, rec).Error != "last_admin" {
		t.Errorf("last admin: status %d body %s", rec.Code, rec.Body.String())
	}
}

// TestDeleteUserCascadeAcceptance (privileged) is US1 end to end on a real kernel: an
// admin deletes a user whose node A is in the user's owned zone and node B is in the
// admin's (surviving) zone. After 204: no peer for A or B, the owned zone's set is
// destroyed, the surviving zone no longer contains B, the records are clean, and a new
// registration reuses a freed address.
func TestDeleteUserCascadeAcceptance(t *testing.T) {
	h := newNodeHarness(t)
	ctx := context.Background()
	adminID := h.seedUser("admin")
	adminTok := h.adminToken(adminID)
	victimID := h.seedUser("victim")
	victimTok := h.token(victimID)

	pub1, pub2 := nodePubKey(t), nodePubKey(t)
	var n1, n2 protocol.NodeResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/nodes", victimTok, protocol.RegisterNodeRequest{Name: "n1", WGPubKey: pub1}).Body.Bytes(), &n1)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/nodes", victimTok, protocol.RegisterNodeRequest{Name: "n2", WGPubKey: pub2}).Body.Bytes(), &n2)

	var vz, sz protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", victimTok, protocol.CreateZoneRequest{Name: "vz", Password: "zone-strong-pw"}).Body.Bytes(), &vz)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", adminTok, protocol.CreateZoneRequest{Name: "sz", Password: "zone-strong-pw"}).Body.Bytes(), &sz)
	if r := h.req(http.MethodPost, "/api/v1/zones/vz/join", victimTok, protocol.JoinZoneRequest{NodeID: n1.ID, Password: "zone-strong-pw"}); r.Code != http.StatusOK {
		t.Fatalf("join vz: %d", r.Code)
	}
	if r := h.req(http.MethodPost, "/api/v1/zones/sz/join", victimTok, protocol.JoinZoneRequest{NodeID: n2.ID, Password: "zone-strong-pw"}); r.Code != http.StatusOK {
		t.Fatalf("join sz: %d", r.Code)
	}
	if !h.peerExists(pub1) || !h.peerExists(pub2) || !h.zoneSetHas(vz.ID, n1.IP) || !h.zoneSetHas(sz.ID, n2.IP) {
		t.Fatal("setup precondition failed")
	}

	if r := h.req(http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(victimID, 10), adminTok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete user: %d %s", r.Code, r.Body.String())
	}

	if h.peerExists(pub1) || h.peerExists(pub2) {
		t.Error("deleted user's peers still present")
	}
	if nftSetExists(t, h.nftName, vz.ID) {
		t.Error("owned zone's set still present after delete")
	}
	if !nftSetExists(t, h.nftName, sz.ID) {
		t.Error("surviving zone wrongly destroyed")
	}
	if h.zoneSetHas(sz.ID, n2.IP) {
		t.Error("surviving zone still contains the deleted user's node")
	}
	if u, _ := h.store.Users().GetByUsername(ctx, "victim"); u != nil {
		t.Error("victim user not removed")
	}
	if nodes, _ := h.store.Nodes().ListByUser(ctx, victimID); len(nodes) != 0 {
		t.Errorf("victim's nodes remain: %d", len(nodes))
	}
	var fresh protocol.NodeResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/nodes", adminTok, protocol.RegisterNodeRequest{Name: "fresh", WGPubKey: nodePubKey(t)}).Body.Bytes(), &fresh)
	if fresh.IP != n1.IP {
		t.Errorf("freed address not reused: got %s, want %s", fresh.IP, n1.IP)
	}
}

// TestDeleteUserCrossUserIntegrity (privileged) is US3: deleting user A leaves user B's
// account, node, peer, and owned zone intact; A's owned zone is destroyed; A's node
// leaves B's zone; B's node leaves A's (now-gone) zone but B's node survives.
func TestDeleteUserCrossUserIntegrity(t *testing.T) {
	h := newNodeHarness(t)
	ctx := context.Background()
	adminTok := h.adminToken(h.seedUser("admin"))
	aID := h.seedUser("usera")
	aTok := h.token(aID)
	bID := h.seedUser("userb")
	bTok := h.token(bID)

	aPub, bPub := nodePubKey(t), nodePubKey(t)
	var na, nb protocol.NodeResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/nodes", aTok, protocol.RegisterNodeRequest{Name: "na", WGPubKey: aPub}).Body.Bytes(), &na)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/nodes", bTok, protocol.RegisterNodeRequest{Name: "nb", WGPubKey: bPub}).Body.Bytes(), &nb)

	var za, zb protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", aTok, protocol.CreateZoneRequest{Name: "za", Password: "zone-strong-pw"}).Body.Bytes(), &za)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", bTok, protocol.CreateZoneRequest{Name: "zb", Password: "zone-strong-pw"}).Body.Bytes(), &zb)
	// B's node joins A's zone; A's node joins B's zone (sharing in both directions).
	if r := h.req(http.MethodPost, "/api/v1/zones/za/join", bTok, protocol.JoinZoneRequest{NodeID: nb.ID, Password: "zone-strong-pw"}); r.Code != http.StatusOK {
		t.Fatalf("join za: %d", r.Code)
	}
	if r := h.req(http.MethodPost, "/api/v1/zones/zb/join", aTok, protocol.JoinZoneRequest{NodeID: na.ID, Password: "zone-strong-pw"}); r.Code != http.StatusOK {
		t.Fatalf("join zb: %d", r.Code)
	}

	if r := h.req(http.MethodDelete, "/api/v1/admin/users/"+strconv.FormatInt(aID, 10), adminTok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete A: %d %s", r.Code, r.Body.String())
	}

	// B intact.
	if u, _ := h.store.Users().GetByUsername(ctx, "userb"); u == nil {
		t.Error("B wrongly removed")
	}
	if nodes, _ := h.store.Nodes().ListByUser(ctx, bID); len(nodes) != 1 {
		t.Errorf("B's node count = %d, want 1", len(nodes))
	}
	if !h.peerExists(bPub) {
		t.Error("B's peer wrongly removed")
	}
	// B's zone survives; A's deleted node is gone from it.
	if !nftSetExists(t, h.nftName, zb.ID) {
		t.Error("B's zone wrongly destroyed")
	}
	if h.zoneSetHas(zb.ID, na.IP) {
		t.Error("deleted A's node still in B's zone set")
	}
	// A's zone destroyed; A's peer gone.
	if nftSetExists(t, h.nftName, za.ID) {
		t.Error("A's zone set still present")
	}
	if h.peerExists(aPub) {
		t.Error("A's peer still present")
	}
}
