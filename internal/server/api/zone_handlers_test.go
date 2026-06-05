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

// newZoneHarness builds a router with a real nftables table (create/join mutate it),
// so this is a privileged integration harness (root / `unshare -rUn`).
func newZoneHarness(t *testing.T) *zoneHarness {
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

func (h *zoneHarness) seedNode(userID int64, name string) *store.Node {
	h.t.Helper()
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	k := nodePubKey(h.t)
	n, err := h.store.Nodes().Create(context.Background(), userID, name, k, first, last)
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
