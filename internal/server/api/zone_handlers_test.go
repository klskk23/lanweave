package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/nftables"
	"golang.org/x/time/rate"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/ipam"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/testutil"
	"lanweave/pkg/protocol"
)

type zoneHarness struct {
	t      *testing.T
	router http.Handler
	store  *store.Store
	jwt    *auth.JWTManager
	table  string
}

// newZoneHarness builds a router with a real nftables table (create/join mutate it)
// and no per-user caps, so this is a privileged integration harness (root /
// `unshare -rUn`).
func newZoneHarness(t *testing.T) *zoneHarness { return newZoneHarnessLimited(t, 0, 0) }

// newZoneHarnessLimited is newZoneHarness with explicit per-user caps wired into the
// router (0 = unlimited), so owned-zone-cap acceptance tests can drive the limits.
func newZoneHarnessLimited(t *testing.T, maxDevices, maxOwnedZones int) *zoneHarness {
	t.Helper()
	testutil.RequireNetAdmin(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	table := "lwz" + hex.EncodeToString(b)
	mgr := netfw.NewManager(table)
	if err := mgr.Rebuild(nil, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatalf("nft rebuild: %v", err)
	}
	t.Cleanup(func() {
		if conn, e := nftables.New(); e == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: table})
			_ = conn.Flush()
		}
	})

	jwtMgr := auth.NewJWTManager(harnessJWTSecret, time.Hour)
	router := api.NewRouter(api.Options{
		Version: "test", Limiter: rate.NewLimiter(rate.Limit(10000), 10000),
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Store:  st, JWT: jwtMgr, NetFW: mgr,
		Status:               &fakeStatus{handshakes: map[string]time.Time{}},
		MaxDevicesPerUser:    maxDevices,
		MaxOwnedZonesPerUser: maxOwnedZones,
	})
	return &zoneHarness{t: t, router: router, store: st, jwt: jwtMgr, table: table}
}

func (h *zoneHarness) user(name string) (int64, string) {
	h.t.Helper()
	u, err := h.store.Users().CreateAdmin(context.Background(), name, "hash")
	if err != nil {
		h.t.Fatalf("seed user: %v", err)
	}
	tok, _ := h.jwt.Issue(auth.Claims{UserID: u.ID, Username: name})
	return u.ID, tok
}

// adminUser seeds an account and returns an admin JWT for it (user() issues non-admin
// tokens). Used to assert the admin cap exemption.
func (h *zoneHarness) adminUser(name string) (int64, string) {
	h.t.Helper()
	u, err := h.store.Users().CreateAdmin(context.Background(), name, "hash")
	if err != nil {
		h.t.Fatalf("seed admin user: %v", err)
	}
	tok, _ := h.jwt.Issue(auth.Claims{UserID: u.ID, Username: name, IsAdmin: true})
	return u.ID, tok
}

func (h *zoneHarness) seedNode(userID int64, name string) *store.Node {
	h.t.Helper()
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	k := nodePubKey(h.t)
	n, err := h.store.Nodes().Create(context.Background(), userID, name, k, "unknown", first, last, 0)
	if err != nil {
		h.t.Fatalf("seed node: %v", err)
	}
	return n
}

func (h *zoneHarness) req(method, path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var r *http.Request
	if body != nil {
		bs, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(bs))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, r)
	return rec
}

func (h *zoneHarness) setHas(zoneID int64, ip netip.Addr) bool {
	h.t.Helper()
	conn, _ := nftables.New()
	s, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: h.table}, fmt.Sprintf("zone_%d", zoneID))
	if err != nil {
		h.t.Fatalf("get set: %v", err)
	}
	elems, _ := conn.GetSetElements(s)
	want := ip.As4()
	for _, e := range elems {
		if len(e.Key) == 4 && [4]byte(e.Key) == want {
			return true
		}
	}
	return false
}

// setExists reports whether the zone's nftables set still exists.
func (h *zoneHarness) setExists(zoneID int64) bool {
	h.t.Helper()
	conn, _ := nftables.New()
	_, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: h.table}, fmt.Sprintf("zone_%d", zoneID))
	return err == nil
}

// US2 (023) — owned-zone cap end to end: refused at the cap with no zone created,
// allowed one below, a delete frees a slot, and joining another user's zone while at
// the cap still succeeds (FR-004/006, SC-002/004).
func TestCreateZoneOwnedLimit(t *testing.T) {
	h := newZoneHarnessLimited(t, 0, 2)
	aliceID, tok := h.user("alice")
	const pw = "zone-strong-pw"

	for i := range 2 {
		if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: fmt.Sprintf("z%d", i), Password: pw}); r.Code != http.StatusCreated {
			t.Fatalf("create %d (under cap): status %d %s", i, r.Code, r.Body.String())
		}
	}
	// At the cap → 409 zone_limit_reached, nothing created.
	rec := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "over", Password: pw})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Error != "zone_limit_reached" {
		t.Fatalf("at cap: status %d body %s", rec.Code, rec.Body.String())
	}
	var list protocol.ZoneListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones", tok, nil).Body.Bytes(), &list)
	if len(list.Zones) != 2 {
		t.Fatalf("after refusal: %d zones, want 2 (nothing created)", len(list.Zones))
	}

	// Delete an owned zone → a slot frees; the next create succeeds again.
	if r := h.req(http.MethodDelete, "/api/v1/zones/z0", tok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete owned zone: status %d", r.Code)
	}
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "fresh", Password: pw}); r.Code != http.StatusCreated {
		t.Fatalf("create after delete: status %d %s", r.Code, r.Body.String())
	}

	// Join is uncapped: while at her owned-zone cap, alice joins a zone bob owns.
	_, bobTok := h.user("bob")
	if r := h.req(http.MethodPost, "/api/v1/zones", bobTok, protocol.CreateZoneRequest{Name: "bobzone", Password: pw}); r.Code != http.StatusCreated {
		t.Fatalf("bob create: status %d %s", r.Code, r.Body.String())
	}
	an := h.seedNode(aliceID, "alice-laptop")
	if r := h.req(http.MethodPost, "/api/v1/zones/bobzone/join", tok, protocol.JoinZoneRequest{NodeID: an.ID, Password: pw}); r.Code != http.StatusOK {
		t.Fatalf("join while at owned cap should succeed: status %d %s", r.Code, r.Body.String())
	}
}

// US3 (023) — the admin account is exempt from the owned-zone cap (SC-008).
func TestCreateZoneAdminExempt(t *testing.T) {
	h := newZoneHarnessLimited(t, 0, 1) // a tiny positive cap
	_, adminTok := h.adminUser("admin")
	for i := range 3 { // well past the cap of 1
		if r := h.req(http.MethodPost, "/api/v1/zones", adminTok, protocol.CreateZoneRequest{Name: fmt.Sprintf("z%d", i), Password: "zone-strong-pw"}); r.Code != http.StatusCreated {
			t.Fatalf("admin create %d should bypass the cap: status %d %s", i, r.Code, r.Body.String())
		}
	}
}

func TestCreateZone(t *testing.T) {
	h := newZoneHarness(t)
	_, tok := h.user("alice")

	rec := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "devteam", Password: "zone-strong-pw"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body.String())
	}
	var z protocol.ZoneResponse
	decodeJSONBody(t, rec.Body.Bytes(), &z)
	if z.Name != "devteam" || !z.IsOwner {
		t.Fatalf("unexpected zone: %+v", z)
	}
	// Duplicate name → 409.
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "devteam", Password: "another-pw"}); r.Code != http.StatusConflict {
		t.Errorf("dup name: %d, want 409", r.Code)
	}
	// Short password / empty name → 400.
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "x", Password: "short"}); r.Code != http.StatusBadRequest {
		t.Errorf("short pw: %d, want 400", r.Code)
	}
	// Unauth → 401.
	if r := h.req(http.MethodPost, "/api/v1/zones", "", protocol.CreateZoneRequest{Name: "y", Password: "zone-strong-pw"}); r.Code != http.StatusUnauthorized {
		t.Errorf("unauth: %d, want 401", r.Code)
	}
	// Malformed body (a JSON string, not an object) → 400.
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, "not-an-object"); r.Code != http.StatusBadRequest {
		t.Errorf("malformed body: %d, want 400", r.Code)
	}
}

func TestCreateZoneAutoJoin(t *testing.T) {
	h := newZoneHarness(t)
	uid, tok := h.user("alice")
	node := h.seedNode(uid, "laptop")

	// Happy path: create with an owned node_id → zone created AND the node is a member,
	// with its IP in the real nft set (traffic-eligible, not just a DB row).
	var z protocol.ZoneResponse
	rec := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "auto", Password: "zone-strong-pw", NodeID: node.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create auto-join: %d %s", rec.Code, rec.Body.String())
	}
	decodeJSONBody(t, rec.Body.Bytes(), &z)
	if !z.IsOwner {
		t.Errorf("want is_owner true, got %+v", z)
	}
	if !h.setHas(z.ID, node.IP) {
		t.Error("creator node IP not in zone set after auto-join")
	}
	var members protocol.ZoneMembersResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones/auto/members", tok, nil).Body.Bytes(), &members)
	if len(members.Members) != 1 || members.Members[0].NodeID != node.ID {
		t.Errorf("creator not the sole member: %+v", members.Members)
	}

	// Security: a node_id the caller does NOT own → 404, and nothing is created.
	_, bobTok := h.user("bob")
	if r := h.req(http.MethodPost, "/api/v1/zones", bobTok, protocol.CreateZoneRequest{Name: "foreign", Password: "zone-strong-pw", NodeID: node.ID}); r.Code != http.StatusNotFound {
		t.Errorf("foreign node_id: %d, want 404", r.Code)
	}
	if r := h.req(http.MethodPost, "/api/v1/zones", bobTok, protocol.CreateZoneRequest{Name: "bogus", Password: "zone-strong-pw", NodeID: 999999}); r.Code != http.StatusNotFound {
		t.Errorf("bogus node_id: %d, want 404", r.Code)
	}
	// No zone leaked: bob participates in nothing, and the rejected name is still free.
	var bobZones protocol.ZoneListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones", bobTok, nil).Body.Bytes(), &bobZones)
	if len(bobZones.Zones) != 0 {
		t.Errorf("foreign-node create leaked a zone: %+v", bobZones.Zones)
	}
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "foreign", Password: "zone-strong-pw"}); r.Code != http.StatusCreated {
		t.Errorf("name 'foreign' should be free after the failed create: %d", r.Code)
	}

	// Backward-compat: omitted node_id (0) → create-only, no members.
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "shell", Password: "zone-strong-pw"}); r.Code != http.StatusCreated {
		t.Fatalf("create-only: %d %s", r.Code, r.Body.String())
	}
	var shellMembers protocol.ZoneMembersResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones/shell/members", tok, nil).Body.Bytes(), &shellMembers)
	if len(shellMembers.Members) != 0 {
		t.Errorf("create-only zone unexpectedly has members: %+v", shellMembers.Members)
	}
}

func TestJoinZone(t *testing.T) {
	h := newZoneHarness(t)
	uid, tok := h.user("alice")
	node := h.seedNode(uid, "laptop")

	var z protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "zone-strong-pw"}).Body.Bytes(), &z)

	join := func(pw string, nodeID int64, name string) *httptest.ResponseRecorder {
		return h.req(http.MethodPost, "/api/v1/zones/"+name+"/join", tok, protocol.JoinZoneRequest{NodeID: nodeID, Password: pw})
	}

	if r := join("zone-strong-pw", node.ID, "z1"); r.Code != http.StatusOK {
		t.Fatalf("join: %d %s", r.Code, r.Body.String())
	}
	if !h.setHas(z.ID, node.IP) {
		t.Error("node IP not in zone set after join")
	}
	// Idempotent re-join.
	if r := join("zone-strong-pw", node.ID, "z1"); r.Code != http.StatusOK {
		t.Errorf("re-join: %d", r.Code)
	}
	// Wrong password AND unknown zone → identical 403 body.
	wrong := join("nope-nope", node.ID, "z1")
	unknown := join("whatever-x", node.ID, "ghostzone")
	if wrong.Code != http.StatusForbidden || unknown.Code != http.StatusForbidden {
		t.Fatalf("expected 403/403, got %d/%d", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("no-enum leak: %q vs %q", wrong.Body, unknown.Body)
	}
	if decodeError(t, wrong).Error != "invalid_zone_or_password" {
		t.Error("expected invalid_zone_or_password")
	}
	// Foreign node → 404.
	_, bobTok := h.user("bob")
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/join", bobTok, protocol.JoinZoneRequest{NodeID: node.ID, Password: "zone-strong-pw"}); r.Code != http.StatusNotFound {
		t.Errorf("foreign node join: %d, want 404", r.Code)
	}
	// Missing fields → 400.
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{}); r.Code != http.StatusBadRequest {
		t.Errorf("missing fields join: %d, want 400", r.Code)
	}
}

func TestLeaveZoneErrors(t *testing.T) {
	h := newZoneHarness(t)
	uid, tok := h.user("alice")
	node := h.seedNode(uid, "laptop")
	h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "zone-strong-pw"})

	// Unknown zone → 404.
	if r := h.req(http.MethodPost, "/api/v1/zones/ghost/leave", tok, protocol.LeaveZoneRequest{NodeID: node.ID}); r.Code != http.StatusNotFound {
		t.Errorf("unknown-zone leave: %d, want 404", r.Code)
	}
	// Foreign node → 404.
	_, bobTok := h.user("bob")
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/leave", bobTok, protocol.LeaveZoneRequest{NodeID: node.ID}); r.Code != http.StatusNotFound {
		t.Errorf("foreign-node leave: %d, want 404", r.Code)
	}
	// Unauthenticated list → 401.
	if r := h.req(http.MethodGet, "/api/v1/zones", "", nil); r.Code != http.StatusUnauthorized {
		t.Errorf("unauth list: %d, want 401", r.Code)
	}
}

func TestLeaveZoneMultiZone(t *testing.T) {
	h := newZoneHarness(t)
	uid, tok := h.user("alice")
	node := h.seedNode(uid, "laptop")

	var z1, z2 protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "zone-strong-pw"}).Body.Bytes(), &z1)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z2", Password: "zone-strong-pw"}).Body.Bytes(), &z2)
	h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{NodeID: node.ID, Password: "zone-strong-pw"})
	h.req(http.MethodPost, "/api/v1/zones/z2/join", tok, protocol.JoinZoneRequest{NodeID: node.ID, Password: "zone-strong-pw"})

	// Multi-zone: the address is in BOTH sets.
	if !h.setHas(z1.ID, node.IP) || !h.setHas(z2.ID, node.IP) {
		t.Fatal("node should be in both zone sets")
	}
	// Leave z1 → gone from z1, still in z2.
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/leave", tok, protocol.LeaveZoneRequest{NodeID: node.ID}); r.Code != http.StatusNoContent {
		t.Fatalf("leave: %d", r.Code)
	}
	if h.setHas(z1.ID, node.IP) {
		t.Error("node still in z1 set after leave")
	}
	if !h.setHas(z2.ID, node.IP) {
		t.Error("leaving z1 must not affect z2")
	}
	// Leave again → 404 (not a member).
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/leave", tok, protocol.LeaveZoneRequest{NodeID: node.ID}); r.Code != http.StatusNotFound {
		t.Errorf("leave non-member: %d, want 404", r.Code)
	}
}

func TestListAndMembers(t *testing.T) {
	h := newZoneHarness(t)
	aliceID, aliceTok := h.user("alice")
	bobID, bobTok := h.user("bob")
	an := h.seedNode(aliceID, "anode")
	bn := h.seedNode(bobID, "bnode")

	h.req(http.MethodPost, "/api/v1/zones", aliceTok, protocol.CreateZoneRequest{Name: "shared", Password: "zone-strong-pw"})
	h.req(http.MethodPost, "/api/v1/zones/shared/join", aliceTok, protocol.JoinZoneRequest{NodeID: an.ID, Password: "zone-strong-pw"})
	h.req(http.MethodPost, "/api/v1/zones/shared/join", bobTok, protocol.JoinZoneRequest{NodeID: bn.ID, Password: "zone-strong-pw"})

	// Both see the zone in their list.
	var aliceZones protocol.ZoneListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones", aliceTok, nil).Body.Bytes(), &aliceZones)
	if len(aliceZones.Zones) != 1 {
		t.Fatalf("alice zones = %d, want 1", len(aliceZones.Zones))
	}

	// Members: full transparency across users.
	var members protocol.ZoneMembersResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones/shared/members", aliceTok, nil).Body.Bytes(), &members)
	if len(members.Members) != 2 {
		t.Fatalf("members = %d, want 2", len(members.Members))
	}
	owners := map[string]string{}
	for _, m := range members.Members {
		owners[m.NodeName] = m.Owner
	}
	if owners["anode"] != "alice" || owners["bnode"] != "bob" {
		t.Errorf("transparency wrong: %+v", owners)
	}

	// Non-participant → 404 (no disclosure).
	_, carolTok := h.user("carol")
	if r := h.req(http.MethodGet, "/api/v1/zones/shared/members", carolTok, nil); r.Code != http.StatusNotFound {
		t.Errorf("non-participant members: %d, want 404", r.Code)
	}
	// Unknown zone members → 404.
	if r := h.req(http.MethodGet, "/api/v1/zones/ghostzone/members", aliceTok, nil); r.Code != http.StatusNotFound {
		t.Errorf("unknown-zone members: %d, want 404", r.Code)
	}
}

// ---- Feature 006: owner controls ----

func TestChangeZonePassword(t *testing.T) {
	h := newZoneHarness(t)
	uid, tok := h.user("alice")
	na := h.seedNode(uid, "na")
	var z protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "orig-strong-pw"}).Body.Bytes(), &z)
	h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{NodeID: na.ID, Password: "orig-strong-pw"})

	// Owner changes the password.
	if r := h.req(http.MethodPatch, "/api/v1/zones/z1", tok, protocol.ChangeZonePasswordRequest{Password: "new-strong-pw"}); r.Code != http.StatusOK {
		t.Fatalf("change password: %d %s", r.Code, r.Body.String())
	}
	// Existing member is kept (not ejected).
	var members protocol.ZoneMembersResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/zones/z1/members", tok, nil).Body.Bytes(), &members)
	if len(members.Members) != 1 {
		t.Fatalf("password change ejected members: %d remain", len(members.Members))
	}
	// A fresh join: old password fails, new password works.
	nb := h.seedNode(uid, "nb")
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{NodeID: nb.ID, Password: "orig-strong-pw"}); r.Code != http.StatusForbidden {
		t.Errorf("join with old password: %d, want 403", r.Code)
	}
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{NodeID: nb.ID, Password: "new-strong-pw"}); r.Code != http.StatusOK {
		t.Errorf("join with new password: %d, want 200", r.Code)
	}
	// Weak new password → 400; non-owner → 403; missing zone → 404.
	if r := h.req(http.MethodPatch, "/api/v1/zones/z1", tok, protocol.ChangeZonePasswordRequest{Password: "short"}); r.Code != http.StatusBadRequest {
		t.Errorf("weak password: %d, want 400", r.Code)
	}
	_, bobTok := h.user("bob")
	if r := h.req(http.MethodPatch, "/api/v1/zones/z1", bobTok, protocol.ChangeZonePasswordRequest{Password: "hijack-attempt"}); r.Code != http.StatusForbidden {
		t.Errorf("non-owner change: %d, want 403", r.Code)
	}
	if r := h.req(http.MethodPatch, "/api/v1/zones/ghost", tok, protocol.ChangeZonePasswordRequest{Password: "whatever-pw"}); r.Code != http.StatusNotFound {
		t.Errorf("missing zone: %d, want 404", r.Code)
	}
}

func TestKickMember(t *testing.T) {
	h := newZoneHarness(t)
	aliceID, aliceTok := h.user("alice")
	bobID, bobTok := h.user("bob")
	var z1, z2 protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", aliceTok, protocol.CreateZoneRequest{Name: "z1", Password: "kick-strong-pw"}).Body.Bytes(), &z1)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", aliceTok, protocol.CreateZoneRequest{Name: "z2", Password: "kick-strong-pw"}).Body.Bytes(), &z2)

	// Bob's node joins z1 and z2 (cross-user membership).
	nb := h.seedNode(bobID, "nb")
	h.req(http.MethodPost, "/api/v1/zones/z1/join", bobTok, protocol.JoinZoneRequest{NodeID: nb.ID, Password: "kick-strong-pw"})
	h.req(http.MethodPost, "/api/v1/zones/z2/join", bobTok, protocol.JoinZoneRequest{NodeID: nb.ID, Password: "kick-strong-pw"})
	if !h.setHas(z1.ID, nb.IP) || !h.setHas(z2.ID, nb.IP) {
		t.Fatal("setup: bob's node not in both zone sets")
	}

	path := "/api/v1/zones/z1/members/" + fmt.Sprintf("%d", nb.ID)
	// Owner (alice) kicks bob's node from z1.
	if r := h.req(http.MethodDelete, path, aliceTok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("kick: %d", r.Code)
	}
	if h.setHas(z1.ID, nb.IP) {
		t.Error("kicked node still in z1 set")
	}
	if !h.setHas(z2.ID, nb.IP) {
		t.Error("kick from z1 affected z2 membership")
	}
	// The node itself still exists (bob can list it).
	var bobNodes protocol.NodeListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", bobTok, nil).Body.Bytes(), &bobNodes)
	if len(bobNodes.Nodes) != 1 {
		t.Errorf("kick deleted the node: %d remain", len(bobNodes.Nodes))
	}
	// Re-kick → 404.
	if r := h.req(http.MethodDelete, path, aliceTok, nil); r.Code != http.StatusNotFound {
		t.Errorf("re-kick: %d, want 404", r.Code)
	}
	// Non-owner kick → 403 (bob is not z1's owner).
	na := h.seedNode(aliceID, "na")
	h.req(http.MethodPost, "/api/v1/zones/z1/join", aliceTok, protocol.JoinZoneRequest{NodeID: na.ID, Password: "kick-strong-pw"})
	if r := h.req(http.MethodDelete, "/api/v1/zones/z1/members/"+fmt.Sprintf("%d", na.ID), bobTok, nil); r.Code != http.StatusForbidden {
		t.Errorf("non-owner kick: %d, want 403", r.Code)
	}
}

func TestDeleteZoneOwner(t *testing.T) {
	h := newZoneHarness(t)
	uid, tok := h.user("alice")
	na := h.seedNode(uid, "na")
	var z1, z2 protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "del-strong-pw"}).Body.Bytes(), &z1)
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z2", Password: "del-strong-pw"}).Body.Bytes(), &z2)
	h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{NodeID: na.ID, Password: "del-strong-pw"})
	h.req(http.MethodPost, "/api/v1/zones/z2/join", tok, protocol.JoinZoneRequest{NodeID: na.ID, Password: "del-strong-pw"})

	// Non-owner delete → 403.
	_, bobTok := h.user("bob")
	if r := h.req(http.MethodDelete, "/api/v1/zones/z1", bobTok, nil); r.Code != http.StatusForbidden {
		t.Errorf("non-owner delete: %d, want 403", r.Code)
	}
	// Owner deletes z1 → 204; set + rule destroyed.
	if r := h.req(http.MethodDelete, "/api/v1/zones/z1", tok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Code)
	}
	if h.setExists(z1.ID) {
		t.Error("z1 set still present after delete")
	}
	// z2 unaffected; member node still exists.
	if !h.setHas(z2.ID, na.IP) {
		t.Error("deleting z1 affected z2")
	}
	var nodes protocol.NodeListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", tok, nil).Body.Bytes(), &nodes)
	if len(nodes.Nodes) != 1 {
		t.Errorf("delete zone removed the member node: %d remain", len(nodes.Nodes))
	}
	// Name released → re-creatable.
	if r := h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "fresh-strong-pw"}); r.Code != http.StatusCreated {
		t.Errorf("recreate name: %d, want 201", r.Code)
	}
	// Missing zone → 404.
	if r := h.req(http.MethodDelete, "/api/v1/zones/ghost", tok, nil); r.Code != http.StatusNotFound {
		t.Errorf("delete missing: %d, want 404", r.Code)
	}
}

// US4 — a member who is not the owner cannot perform owner ops, but can still view.
func TestOwnerOpsRequireOwnership(t *testing.T) {
	h := newZoneHarness(t)
	_, aliceTok := h.user("alice")
	bobID, bobTok := h.user("bob")
	h.req(http.MethodPost, "/api/v1/zones", aliceTok, protocol.CreateZoneRequest{Name: "z1", Password: "authz-strong-pw"})
	nb := h.seedNode(bobID, "nb")
	h.req(http.MethodPost, "/api/v1/zones/z1/join", bobTok, protocol.JoinZoneRequest{NodeID: nb.ID, Password: "authz-strong-pw"})

	// Bob is a MEMBER but not the owner → 403 on all three owner ops.
	if r := h.req(http.MethodPatch, "/api/v1/zones/z1", bobTok, protocol.ChangeZonePasswordRequest{Password: "member-hijack"}); r.Code != http.StatusForbidden {
		t.Errorf("member change: %d, want 403", r.Code)
	}
	if r := h.req(http.MethodDelete, "/api/v1/zones/z1/members/"+fmt.Sprintf("%d", nb.ID), bobTok, nil); r.Code != http.StatusForbidden {
		t.Errorf("member kick: %d, want 403", r.Code)
	}
	if r := h.req(http.MethodDelete, "/api/v1/zones/z1", bobTok, nil); r.Code != http.StatusForbidden {
		t.Errorf("member delete: %d, want 403", r.Code)
	}
	// But bob (participant) can view members.
	if r := h.req(http.MethodGet, "/api/v1/zones/z1/members", bobTok, nil); r.Code != http.StatusOK {
		t.Errorf("member view: %d, want 200", r.Code)
	}
}
