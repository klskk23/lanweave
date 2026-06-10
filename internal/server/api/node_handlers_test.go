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
	"strconv"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.org/x/time/rate"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/ipam"
	"lanweave/internal/server/netfw"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
	"lanweave/pkg/protocol"
)

type nodeHarness struct {
	t       *testing.T
	router  http.Handler
	store   *store.Store
	jwt     *auth.JWTManager
	wgName  string
	wgCfg   config.WireGuardConfig
	nftName string
}

// newNodeHarness builds a real WireGuard interface + router with no per-user caps.
// Registration adds a real peer, so this is a privileged integration harness (root /
// `unshare -rUn`).
func newNodeHarness(t *testing.T) *nodeHarness { return newNodeHarnessLimited(t, 0, 0) }

// newNodeHarnessLimited is newNodeHarness with explicit per-user caps wired into the
// router (0 = unlimited), so cap-enforcement acceptance tests can drive the limits.
func newNodeHarnessLimited(t *testing.T, maxDevices, maxOwnedZones int) *nodeHarness {
	return newNodeHarnessWith(t, func(o *api.Options) {
		o.MaxDevicesPerUser = maxDevices
		o.MaxOwnedZonesPerUser = maxOwnedZones
	})
}

// newNodeHarnessWith builds the full privileged harness (real store + wg + nft)
// and lets the caller adjust router Options before construction (announce pool,
// caps, ...).
func newNodeHarnessWith(t *testing.T, mutate func(*api.Options)) *nodeHarness {
	t.Helper()
	testutil.RequireNetAdmin(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	b := make([]byte, 4)
	_, _ = rand.Read(b)
	wgName := "wgt" + hex.EncodeToString(b)
	wgCfg := config.WireGuardConfig{
		Network: "100.127.0.0/16", ListenPort: 0, Interface: wgName, MTU: 1420,
		Endpoint: "vpn.example.com:51820",
	}
	serverKey, _ := wgtypes.GeneratePrivateKey()
	srv, err := wg.EnsureInterface(wgCfg, serverKey, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("ensure interface: %v", err)
	}
	t.Cleanup(func() {
		_ = srv.Close()
		if l, e := netlink.LinkByName(wgName); e == nil {
			_ = netlink.LinkDel(l)
		}
	})

	nftName := "lwn" + hex.EncodeToString(b)
	mgr := netfw.NewManager(nftName)
	if err := mgr.Rebuild(nil, slog.New(slog.NewJSONHandler(io.Discard, nil))); err != nil {
		t.Fatalf("nft rebuild: %v", err)
	}
	t.Cleanup(func() {
		if conn, e := nftables.New(); e == nil {
			conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: nftName})
			_ = conn.Flush()
		}
	})

	jwtMgr := auth.NewJWTManager(harnessJWTSecret, time.Hour)
	opts := api.Options{
		Version: "test", Limiter: rate.NewLimiter(rate.Limit(10000), 10000),
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Store:  st, JWT: jwtMgr, WG: srv, NetFW: mgr, WGConfig: wgCfg,
		Status: &fakeStatus{handshakes: map[string]time.Time{}},
	}
	if mutate != nil {
		mutate(&opts)
	}
	router := api.NewRouter(opts)
	return &nodeHarness{t: t, router: router, store: st, jwt: jwtMgr, wgName: wgName, wgCfg: wgCfg, nftName: nftName}
}

func (h *nodeHarness) seedUser(name string) int64 {
	h.t.Helper()
	u, err := h.store.Users().CreateAdmin(context.Background(), name, "hash")
	if err != nil {
		h.t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func (h *nodeHarness) token(userID int64) string {
	tok, _ := h.jwt.Issue(auth.Claims{UserID: userID, Username: "u", IsAdmin: false})
	return tok
}

func (h *nodeHarness) req(method, path, token string, body any) *httptest.ResponseRecorder {
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

func (h *nodeHarness) peerExists(pub string) bool {
	h.t.Helper()
	wgc, err := wgctrl.New()
	if err != nil {
		h.t.Fatalf("wgctrl: %v", err)
	}
	defer wgc.Close()
	dev, err := wgc.Device(h.wgName)
	if err != nil {
		h.t.Fatalf("device: %v", err)
	}
	want, _ := wgtypes.ParseKey(pub)
	for _, p := range dev.Peers {
		if p.PublicKey == want {
			return true
		}
	}
	return false
}

func (h *nodeHarness) zoneSetHas(zoneID int64, ip string) bool {
	h.t.Helper()
	conn, _ := nftables.New()
	s, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: h.nftName}, fmt.Sprintf("zone_%d", zoneID))
	if err != nil {
		h.t.Fatalf("get set: %v", err)
	}
	elems, _ := conn.GetSetElements(s)
	want := netip.MustParseAddr(ip).As4()
	for _, e := range elems {
		if len(e.Key) == 4 && [4]byte(e.Key) == want {
			return true
		}
	}
	return false
}

// US5 — deleting a node clears it from its zones' sets (FR-018), so a recycled
// address cannot inherit the deleted node's reachability.
func TestDeleteNodeClearsZoneMembership(t *testing.T) {
	h := newNodeHarness(t)
	uid := h.seedUser("alice")
	tok := h.token(uid)
	pub := nodePubKey(t)

	var node protocol.NodeResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "laptop", WGPubKey: pub}).Body.Bytes(), &node)
	var zone protocol.ZoneResponse
	decodeJSONBody(t, h.req(http.MethodPost, "/api/v1/zones", tok, protocol.CreateZoneRequest{Name: "z1", Password: "zone-strong-pw"}).Body.Bytes(), &zone)
	if r := h.req(http.MethodPost, "/api/v1/zones/z1/join", tok, protocol.JoinZoneRequest{NodeID: node.ID, Password: "zone-strong-pw"}); r.Code != http.StatusOK {
		t.Fatalf("join: %d", r.Code)
	}
	if !h.zoneSetHas(zone.ID, node.IP) {
		t.Fatal("node not in zone set after join")
	}

	if r := h.req(http.MethodDelete, "/api/v1/nodes/"+strconv.FormatInt(node.ID, 10), tok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", r.Code)
	}
	if h.zoneSetHas(zone.ID, node.IP) {
		t.Error("deleted node still in zone set (recycled address would inherit reachability)")
	}
}

func nodePubKey(t *testing.T) string {
	t.Helper()
	k, _ := wgtypes.GeneratePrivateKey()
	return k.PublicKey().String()
}

// US1 — register + server-info + peer present.
func TestRegisterNodeAndServerInfo(t *testing.T) {
	h := newNodeHarness(t)
	uid := h.seedUser("alice")
	tok := h.token(uid)
	pub := nodePubKey(t)

	rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "laptop", WGPubKey: pub})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status %d: %s", rec.Code, rec.Body.String())
	}
	var node protocol.NodeResponse
	decodeJSONBody(t, rec.Body.Bytes(), &node)
	if node.Name != "laptop" || node.IP != "100.127.0.2" {
		t.Fatalf("unexpected node: %+v", node)
	}
	if !h.peerExists(pub) {
		t.Error("peer not present on the interface after register")
	}

	srvRec := h.req(http.MethodGet, "/api/v1/server", tok, nil)
	if srvRec.Code != http.StatusOK {
		t.Fatalf("server-info status %d", srvRec.Code)
	}
	var info protocol.ServerInfoResponse
	decodeJSONBody(t, srvRec.Body.Bytes(), &info)
	if info.Endpoint != "vpn.example.com:51820" || info.Network != "100.127.0.0/16" || info.MTU != 1420 || info.PublicKey == "" {
		t.Fatalf("unexpected server info: %+v", info)
	}
}

func TestRegisterNodeValidationAndConflicts(t *testing.T) {
	h := newNodeHarness(t)
	uid := h.seedUser("alice")
	tok := h.token(uid)

	// Invalid public key → 400.
	if rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "x", WGPubKey: "not-a-key"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad pubkey: status %d, want 400", rec.Code)
	}
	// Empty name → 400.
	if rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "", WGPubKey: nodePubKey(t)}); rec.Code != http.StatusBadRequest {
		t.Errorf("empty name: status %d, want 400", rec.Code)
	}

	pub := nodePubKey(t)
	if rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "laptop", WGPubKey: pub}); rec.Code != http.StatusCreated {
		t.Fatalf("setup register: %d", rec.Code)
	}
	// Duplicate name → 409 node_name_taken.
	rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "laptop", WGPubKey: nodePubKey(t)})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Error != "node_name_taken" {
		t.Errorf("dup name: status %d body %s", rec.Code, rec.Body.String())
	}
	// Duplicate pubkey → 409 pubkey_taken.
	rec = h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "other", WGPubKey: pub})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Error != "pubkey_taken" {
		t.Errorf("dup pubkey: status %d body %s", rec.Code, rec.Body.String())
	}
}

// US1 (023) — device cap end to end: a regular user is refused at the cap with no node
// created, allowed one below, and a delete frees exactly one slot (FR-003/005, SC-001/003).
func TestRegisterNodeDeviceLimit(t *testing.T) {
	h := newNodeHarnessLimited(t, 2, 0)
	tok := h.token(h.seedUser("alice"))

	for i := range 2 {
		if r := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: fmt.Sprintf("d%d", i), WGPubKey: nodePubKey(t)}); r.Code != http.StatusCreated {
			t.Fatalf("register %d (under cap): status %d %s", i, r.Code, r.Body.String())
		}
	}
	// At the cap → 409 device_limit_reached, nothing created.
	rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "over", WGPubKey: nodePubKey(t)})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Error != "device_limit_reached" {
		t.Fatalf("at cap: status %d body %s", rec.Code, rec.Body.String())
	}
	var list protocol.NodeListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", tok, nil).Body.Bytes(), &list)
	if len(list.Nodes) != 2 {
		t.Fatalf("after refusal: %d nodes, want 2 (nothing created)", len(list.Nodes))
	}
	// Delete one → a slot frees; the next register succeeds again.
	idPath := "/api/v1/nodes/" + strconv.FormatInt(list.Nodes[0].ID, 10)
	if r := h.req(http.MethodDelete, idPath, tok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", r.Code)
	}
	if r := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "fresh", WGPubKey: nodePubKey(t)}); r.Code != http.StatusCreated {
		t.Fatalf("register after delete: status %d %s", r.Code, r.Body.String())
	}
}

// US3 (023) — the admin account is exempt from the device cap (SC-008).
func TestRegisterNodeAdminExempt(t *testing.T) {
	h := newNodeHarnessLimited(t, 1, 0) // a tiny positive cap
	adminTok := h.adminToken(h.seedUser("admin"))
	for i := range 3 { // well past the cap of 1
		if r := h.req(http.MethodPost, "/api/v1/nodes", adminTok, protocol.RegisterNodeRequest{Name: fmt.Sprintf("d%d", i), WGPubKey: nodePubKey(t)}); r.Code != http.StatusCreated {
			t.Fatalf("admin register %d should bypass the cap: status %d %s", i, r.Code, r.Body.String())
		}
	}
}

// US3 (023) — grandfathering: a cap lowered below a user's current count keeps every
// existing device listed and usable; only new registration is refused (FR-010, SC-009).
func TestRegisterNodeGrandfathering(t *testing.T) {
	h := newNodeHarnessLimited(t, 2, 0) // cap is now 2...
	uid := h.seedUser("alice")
	tok := h.token(uid)
	// ...but seed 4 devices directly, as if registered under an earlier, higher cap.
	first, last, _ := ipam.PoolRange("100.127.0.0/16")
	for i := range 4 {
		if _, err := h.store.Nodes().Create(context.Background(), uid, fmt.Sprintf("old%d", i), nodePubKey(t), "unknown", first, last, 0); err != nil {
			t.Fatalf("seed pre-existing device %d: %v", i, err)
		}
	}
	// All pre-existing devices remain listed (nothing evicted).
	var list protocol.NodeListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", tok, nil).Body.Bytes(), &list)
	if len(list.Nodes) != 4 {
		t.Fatalf("grandfathered list = %d, want 4 (nothing evicted)", len(list.Nodes))
	}
	// New registration is refused until the count drops below the cap.
	rec := h.req(http.MethodPost, "/api/v1/nodes", tok, protocol.RegisterNodeRequest{Name: "new", WGPubKey: nodePubKey(t)})
	if rec.Code != http.StatusConflict || decodeError(t, rec).Error != "device_limit_reached" {
		t.Fatalf("over-cap register: status %d body %s", rec.Code, rec.Body.String())
	}
	// The refusal removed nothing.
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", tok, nil).Body.Bytes(), &list)
	if len(list.Nodes) != 4 {
		t.Errorf("after refusal: %d devices, want 4", len(list.Nodes))
	}
}

func TestNodeAuthRequired(t *testing.T) {
	h := newNodeHarness(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/nodes"},
		{http.MethodGet, "/api/v1/nodes"},
		{http.MethodDelete, "/api/v1/nodes/1"},
		{http.MethodGet, "/api/v1/server"},
	} {
		if rec := h.req(tc.method, tc.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

// US2 — list scoped to the caller.
func TestListNodesScoped(t *testing.T) {
	h := newNodeHarness(t)
	alice := h.token(h.seedUser("alice"))
	bob := h.token(h.seedUser("bob"))

	h.req(http.MethodPost, "/api/v1/nodes", alice, protocol.RegisterNodeRequest{Name: "a1", WGPubKey: nodePubKey(t)})

	var aliceList protocol.NodeListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", alice, nil).Body.Bytes(), &aliceList)
	if len(aliceList.Nodes) != 1 {
		t.Fatalf("alice list = %d, want 1", len(aliceList.Nodes))
	}
	var bobList protocol.NodeListResponse
	decodeJSONBody(t, h.req(http.MethodGet, "/api/v1/nodes", bob, nil).Body.Bytes(), &bobList)
	if len(bobList.Nodes) != 0 {
		t.Fatalf("bob list = %d, want 0 (empty, not error)", len(bobList.Nodes))
	}
}

// US1 (007) — GET /nodes reports per-node online status and last_handshake. This
// is non-privileged: nodes are seeded directly into the real SQLite store and the
// online state is driven through the fake statusProvider (our own seam), so no
// WireGuard device is needed.
func TestListNodesOnlineStatus(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	u, err := h.store.Users().CreateAdmin(ctx, "alice", "hash")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	first, last, err := ipam.PoolRange("100.127.0.0/16")
	if err != nil {
		t.Fatalf("pool range: %v", err)
	}
	onlinePub := nodePubKey(t)
	neverPub := nodePubKey(t)
	if _, err := h.store.Nodes().Create(ctx, u.ID, "connected", onlinePub, "unknown", first, last, 0); err != nil {
		t.Fatalf("seed connected node: %v", err)
	}
	if _, err := h.store.Nodes().Create(ctx, u.ID, "never", neverPub, "unknown", first, last, 0); err != nil {
		t.Fatalf("seed never node: %v", err)
	}
	// connected handshaked just now → online; "never" is absent from the snapshot.
	h.status.handshakes[onlinePub] = time.Now()

	// Unauthenticated → 401.
	if rec := h.do(http.MethodGet, "/api/v1/nodes", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth list: status %d, want 401", rec.Code)
	}

	tok, err := h.jwt.Issue(auth.Claims{UserID: u.ID, Username: "alice"})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	rec := h.do(http.MethodGet, "/api/v1/nodes", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", rec.Code, rec.Body.String())
	}
	var list protocol.NodeListResponse
	decodeJSONBody(t, rec.Body.Bytes(), &list)
	byName := map[string]protocol.NodeResponse{}
	for _, n := range list.Nodes {
		byName[n.Name] = n
	}
	if c := byName["connected"]; !c.Online || c.LastHandshake == "" {
		t.Errorf("connected node: online=%v last_handshake=%q, want online=true with a timestamp", c.Online, c.LastHandshake)
	}
	if n := byName["never"]; n.Online || n.LastHandshake != "" {
		t.Errorf("never-connected node: online=%v last_handshake=%q, want online=false with no last_handshake", n.Online, n.LastHandshake)
	}
}

// US3 — delete frees the address and removes the peer; ownership enforced.
func TestDeleteNode(t *testing.T) {
	h := newNodeHarness(t)
	alice := h.seedUser("alice")
	aliceTok := h.token(alice)
	bobTok := h.token(h.seedUser("bob"))
	pub := nodePubKey(t)

	rec := h.req(http.MethodPost, "/api/v1/nodes", aliceTok, protocol.RegisterNodeRequest{Name: "laptop", WGPubKey: pub})
	var node protocol.NodeResponse
	decodeJSONBody(t, rec.Body.Bytes(), &node)
	idPath := "/api/v1/nodes/" + strconv.FormatInt(node.ID, 10)

	// Bob cannot delete Alice's node → 404 (no enumeration).
	if r := h.req(http.MethodDelete, idPath, bobTok, nil); r.Code != http.StatusNotFound {
		t.Errorf("cross-user delete: status %d, want 404", r.Code)
	}
	// Owner deletes → 204; peer removed.
	if r := h.req(http.MethodDelete, idPath, aliceTok, nil); r.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", r.Code)
	}
	if h.peerExists(pub) {
		t.Error("peer still present after delete")
	}
	// Double delete → 404.
	if r := h.req(http.MethodDelete, idPath, aliceTok, nil); r.Code != http.StatusNotFound {
		t.Errorf("double delete: status %d, want 404", r.Code)
	}
}

// TestRegisterNodePlatform covers FR-001/FR-012 (slice 030): self-reported
// platform round-trips, legacy registrations default to "unknown", and a
// malformed value is rejected at the boundary.
func TestRegisterNodePlatform(t *testing.T) {
	h := newNodeHarness(t)
	uid := h.seedUser("alice")
	tok := h.token(uid)

	rec := h.req(http.MethodPost, "/api/v1/nodes", tok,
		protocol.RegisterNodeRequest{Name: "router", WGPubKey: nodePubKey(t), Platform: "openwrt"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d: %s", rec.Code, rec.Body.String())
	}
	var created protocol.NodeResponse
	decodeJSONBody(t, rec.Body.Bytes(), &created)
	if created.Platform != "openwrt" {
		t.Errorf("created platform = %q, want openwrt", created.Platform)
	}

	// Pre-030 shape: no platform field at all → unknown (zero behavior change).
	rec = h.req(http.MethodPost, "/api/v1/nodes", tok,
		protocol.RegisterNodeRequest{Name: "legacy", WGPubKey: nodePubKey(t)})
	if rec.Code != http.StatusCreated {
		t.Fatalf("legacy register status = %d: %s", rec.Code, rec.Body.String())
	}
	var legacy protocol.NodeResponse
	decodeJSONBody(t, rec.Body.Bytes(), &legacy)
	if legacy.Platform != "unknown" {
		t.Errorf("legacy platform = %q, want unknown", legacy.Platform)
	}

	// Malformed platform is a boundary validation error.
	rec = h.req(http.MethodPost, "/api/v1/nodes", tok,
		protocol.RegisterNodeRequest{Name: "bad", WGPubKey: nodePubKey(t), Platform: "Open WRT!"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad platform status = %d, want 400", rec.Code)
	}
	if e := decodeError(t, rec); e.Error != "validation_error" {
		t.Errorf("error = %q, want validation_error", e.Error)
	}

	// The list endpoint surfaces platform for every node.
	rec = h.req(http.MethodGet, "/api/v1/nodes", tok, nil)
	var list protocol.NodeListResponse
	decodeJSONBody(t, rec.Body.Bytes(), &list)
	seen := map[string]string{}
	for _, n := range list.Nodes {
		seen[n.Name] = n.Platform
	}
	if seen["router"] != "openwrt" || seen["legacy"] != "unknown" {
		t.Errorf("list platforms = %v, want router=openwrt legacy=unknown", seen)
	}
}
