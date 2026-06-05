package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/time/rate"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/api"
	"lanweave/internal/server/auth"
	"lanweave/internal/server/config"
	"lanweave/internal/server/store"
	"lanweave/internal/server/wg"
	"lanweave/internal/testutil"
	"lanweave/pkg/protocol"
)

type nodeHarness struct {
	t      *testing.T
	router http.Handler
	store  *store.Store
	jwt    *auth.JWTManager
	wgName string
	wgCfg  config.WireGuardConfig
}

// newNodeHarness builds a real WireGuard interface + router. Registration adds a
// real peer, so this is a privileged integration harness (root / `unshare -rUn`).
func newNodeHarness(t *testing.T) *nodeHarness {
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

	jwtMgr := auth.NewJWTManager(harnessJWTSecret, time.Hour)
	router := api.NewRouter(api.Options{
		Version: "test", Limiter: rate.NewLimiter(rate.Limit(10000), 10000),
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Store:  st, JWT: jwtMgr, WG: srv, WGConfig: wgCfg,
	})
	return &nodeHarness{t: t, router: router, store: st, jwt: jwtMgr, wgName: wgName, wgCfg: wgCfg}
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
