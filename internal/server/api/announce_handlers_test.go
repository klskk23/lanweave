package api_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"testing"

	"github.com/google/nftables"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"lanweave/internal/server/api"
	"lanweave/pkg/protocol"
)

const testAnnouncePool = "100.100.0.0/16"

// newAnnounceHarness is the node harness with the synthetic pool enabled.
func newAnnounceHarness(t *testing.T, limit int) *nodeHarness {
	return newNodeHarnessWith(t, func(o *api.Options) {
		o.AnnouncePool = netip.MustParsePrefix(testAnnouncePool)
		o.MaxAnnouncedSubnetsPerUser = limit
	})
}

// registerNode registers a node with the given platform and returns it.
func (h *nodeHarness) registerNode(token, name, platform string) protocol.NodeResponse {
	h.t.Helper()
	rec := h.req(http.MethodPost, "/api/v1/nodes", token,
		protocol.RegisterNodeRequest{Name: name, WGPubKey: nodePubKey(h.t), Platform: platform})
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("register %s status %d: %s", name, rec.Code, rec.Body.String())
	}
	var resp protocol.NodeResponse
	decodeJSONBody(h.t, rec.Body.Bytes(), &resp)
	return resp
}

// createZoneJoin creates a zone via the API and joins the node into it.
func (h *nodeHarness) createZoneJoin(token, zone string, nodeID int64) {
	h.t.Helper()
	rec := h.req(http.MethodPost, "/api/v1/zones", token,
		protocol.CreateZoneRequest{Name: zone, Password: "zone-pass-1", NodeID: nodeID})
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create zone %s: %d %s", zone, rec.Code, rec.Body.String())
	}
}

// peerAllowed returns the peer's AllowedIPs as a string set.
func (h *nodeHarness) peerAllowed(pub string) map[string]bool {
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
	out := map[string]bool{}
	for _, p := range dev.Peers {
		if p.PublicKey == want {
			for _, a := range p.AllowedIPs {
				out[a.String()] = true
			}
		}
	}
	return out
}

// routesSetElems counts elements in a zone's routes interval set.
func (h *nodeHarness) routesSetElems(zoneID int64) int {
	h.t.Helper()
	conn, _ := nftables.New()
	s, err := conn.GetSetByName(&nftables.Table{Family: nftables.TableFamilyINet, Name: h.nftName}, fmt.Sprintf("zone_%d_routes", zoneID))
	if err != nil {
		h.t.Fatalf("get routes set: %v", err)
	}
	elems, _ := conn.GetSetElements(s)
	return len(elems)
}

func (h *nodeHarness) announce(token, zone string, nodeID int64, subnet string) (*protocol.AnnouncementResponse, int, protocol.ErrorResponse) {
	h.t.Helper()
	rec := h.req(http.MethodPost, "/api/v1/zones/"+zone+"/announcements", token,
		protocol.CreateAnnouncementRequest{NodeID: nodeID, Subnet: subnet})
	if rec.Code == http.StatusCreated {
		var resp protocol.AnnouncementResponse
		decodeJSONBody(h.t, rec.Body.Bytes(), &resp)
		return &resp, rec.Code, protocol.ErrorResponse{}
	}
	return nil, rec.Code, decodeError(h.t, rec)
}

// TestAnnounceHappyPath covers US1 acceptance 1/2/4: announce → synthetic block
// in pool, peer AllowedIPs grown, routes set populated, listing visible to
// another member, 404 for outsiders.
func TestAnnounceHappyPath(t *testing.T) {
	h := newAnnounceHarness(t, 0)
	uid := h.seedUser("alice")
	tok := h.token(uid)
	node := h.registerNode(tok, "router", "openwrt")
	h.createZoneJoin(tok, "home", node.ID)

	ann, code, _ := h.announce(tok, "home", node.ID, "192.168.1.0/24")
	if code != http.StatusCreated {
		t.Fatalf("announce status %d", code)
	}
	synth := netip.MustParsePrefix(ann.Synthetic)
	if synth.Bits() != 24 || !netip.MustParsePrefix(testAnnouncePool).Contains(synth.Addr()) {
		t.Errorf("synthetic %s not an in-pool /24", ann.Synthetic)
	}

	// Dataplane: peer AllowedIPs and the zone routes set carry the block (the
	// pubkey is not echoed in NodeResponse, resolve it from the store).
	stNode, err := h.store.Nodes().GetByID(t.Context(), node.ID)
	if err != nil {
		t.Fatalf("load node: %v", err)
	}
	allowed := h.peerAllowed(stNode.PubKey)
	if !allowed[ann.Synthetic] {
		t.Errorf("peer allowed ips %v missing %s", allowed, ann.Synthetic)
	}
	zone, _ := h.store.Zones().GetByName(t.Context(), "home")
	if n := h.routesSetElems(zone.ID); n != 2 {
		t.Errorf("routes set elems = %d, want 2 (one CIDR)", n)
	}

	// Listing: another member of the zone sees the mapping triple.
	bob := h.seedUser("bob")
	bobTok := h.token(bob)
	bobNode := h.registerNode(bobTok, "laptop", "windows")
	rec := h.req(http.MethodPost, "/api/v1/zones/home/join", bobTok,
		protocol.JoinZoneRequest{NodeID: bobNode.ID, Password: "zone-pass-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("bob join: %d %s", rec.Code, rec.Body.String())
	}
	rec = h.req(http.MethodGet, "/api/v1/zones/home/announcements", bobTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d", rec.Code)
	}
	var list protocol.AnnouncementListResponse
	decodeJSONBody(t, rec.Body.Bytes(), &list)
	if len(list.Announcements) != 1 || list.Announcements[0].Subnet != "192.168.1.0/24" ||
		list.Announcements[0].Synthetic != ann.Synthetic || list.Announcements[0].NodeName != "router" {
		t.Errorf("list = %+v, want the mapping triple", list.Announcements)
	}

	// Outsider: not a participant → 404, indistinguishable from no such zone.
	carol := h.seedUser("carol")
	rec = h.req(http.MethodGet, "/api/v1/zones/home/announcements", h.token(carol), nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("outsider list status = %d, want 404", rec.Code)
	}
}

// TestAnnounceErrorMatrix walks the full SC-006 rejection table at the API
// boundary; each row must answer its own machine code with no partial state.
func TestAnnounceErrorMatrix(t *testing.T) {
	h := newAnnounceHarness(t, 1)
	uid := h.seedUser("alice")
	tok := h.token(uid)
	node := h.registerNode(tok, "router", "openwrt")
	winNode := h.registerNode(tok, "desktop", "windows")
	h.createZoneJoin(tok, "home", node.ID)
	rec := h.req(http.MethodPost, "/api/v1/zones/home/join", tok,
		protocol.JoinZoneRequest{NodeID: winNode.ID, Password: "zone-pass-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("join win node: %d", rec.Code)
	}

	for _, tc := range []struct {
		name     string
		nodeID   int64
		subnet   string
		wantCode int
		wantErr  string
	}{
		{"windows platform rejected", winNode.ID, "192.168.9.0/24", http.StatusConflict, "platform_unsupported"},
		{"public range", node.ID, "8.8.8.0/24", http.StatusBadRequest, "subnet_invalid"},
		{"prefix too wide", node.ID, "10.0.0.0/15", http.StatusBadRequest, "subnet_invalid"},
		{"prefix /15 inside rfc1918 still too wide", node.ID, "172.16.0.0/15", http.StatusBadRequest, "subnet_invalid"},
		{"prefix too narrow", node.ID, "192.168.1.0/31", http.StatusBadRequest, "subnet_invalid"},
		{"overlaps vpn pool", node.ID, "100.127.1.0/24", http.StatusBadRequest, "subnet_invalid"},
		{"overlaps synthetic pool", node.ID, "100.100.1.0/24", http.StatusBadRequest, "subnet_invalid"},
		{"not a cidr", node.ID, "not-a-subnet", http.StatusBadRequest, "validation_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, code, e := h.announce(tok, "home", tc.nodeID, tc.subnet)
			if code != tc.wantCode || e.Error != tc.wantErr {
				t.Errorf("= %d/%s, want %d/%s", code, e.Error, tc.wantCode, tc.wantErr)
			}
		})
	}

	// Foreign node → 404 (no existence leak).
	mallory := h.seedUser("mallory")
	if _, code, e := h.announce(h.token(mallory), "home", node.ID, "192.168.9.0/24"); code != http.StatusNotFound || e.Error != "not_found" {
		t.Errorf("foreign node = %d/%s, want 404/not_found", code, e.Error)
	}

	// Quota (limit 1): first announce fills it, second hits the cap, admin is
	// exempt in the same scenario.
	if _, code, _ := h.announce(tok, "home", node.ID, "192.168.1.0/24"); code != http.StatusCreated {
		t.Fatalf("first announce: %d", code)
	}
	if _, code, e := h.announce(tok, "home", node.ID, "192.168.2.0/24"); code != http.StatusConflict || e.Error != "announce_limit_reached" {
		t.Errorf("quota = %d/%s, want 409/announce_limit_reached", code, e.Error)
	}
	if _, code, _ := h.announce(h.adminToken(uid), "home", node.ID, "192.168.2.0/24"); code != http.StatusCreated {
		t.Errorf("admin exemption: status %d, want 201", code)
	}

	// Self overlap.
	if _, code, e := h.announce(h.adminToken(uid), "home", node.ID, "192.168.2.128/25"); code != http.StatusConflict || e.Error != "subnet_overlap" {
		t.Errorf("self overlap = %d/%s, want 409/subnet_overlap", code, e.Error)
	}
}

// TestAnnounceDisabledAndExhausted covers the pool-off and pool-full service
// states (FR-003/FR-006).
func TestAnnounceDisabledAndExhausted(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		h := newNodeHarnessWith(t, nil) // zero AnnouncePool = feature off
		uid := h.seedUser("alice")
		tok := h.token(uid)
		node := h.registerNode(tok, "router", "openwrt")
		h.createZoneJoin(tok, "home", node.ID)

		_, code, e := h.announce(tok, "home", node.ID, "192.168.1.0/24")
		if code != http.StatusServiceUnavailable || e.Error != "announce_disabled" {
			t.Errorf("= %d/%s, want 503/announce_disabled", code, e.Error)
		}
		// Listing still works and is empty.
		rec := h.req(http.MethodGet, "/api/v1/zones/home/announcements", tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list status %d", rec.Code)
		}
		var list protocol.AnnouncementListResponse
		decodeJSONBody(t, rec.Body.Bytes(), &list)
		if len(list.Announcements) != 0 {
			t.Errorf("list = %+v, want empty", list.Announcements)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		h := newNodeHarnessWith(t, func(o *api.Options) {
			o.AnnouncePool = netip.MustParsePrefix("100.100.0.0/24") // one /24
		})
		uid := h.seedUser("alice")
		tok := h.token(uid)
		node := h.registerNode(tok, "router", "openwrt")
		h.createZoneJoin(tok, "home", node.ID)

		if _, code, _ := h.announce(tok, "home", node.ID, "192.168.1.0/24"); code != http.StatusCreated {
			t.Fatalf("fill pool: %d", code)
		}
		_, code, e := h.announce(tok, "home", node.ID, "192.168.2.0/24")
		if code != http.StatusServiceUnavailable || e.Error != "synthetic_pool_exhausted" {
			t.Errorf("= %d/%s, want 503/synthetic_pool_exhausted", code, e.Error)
		}
	})
}

// TestAnnounceDetachPermissions covers FR-008 detach authority: the announcing
// node's owner and the zone owner may detach; a plain member may not.
func TestAnnounceDetachPermissions(t *testing.T) {
	h := newAnnounceHarness(t, 0)
	owner := h.seedUser("owner")
	ownerTok := h.token(owner)
	ownerNode := h.registerNode(ownerTok, "gw", "openwrt")
	h.createZoneJoin(ownerTok, "home", ownerNode.ID)

	alice := h.seedUser("alice")
	aliceTok := h.token(alice)
	aliceNode := h.registerNode(aliceTok, "router", "openwrt")
	rec := h.req(http.MethodPost, "/api/v1/zones/home/join", aliceTok,
		protocol.JoinZoneRequest{NodeID: aliceNode.ID, Password: "zone-pass-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("alice join: %d", rec.Code)
	}
	bob := h.seedUser("bob")
	bobTok := h.token(bob)
	bobNode := h.registerNode(bobTok, "laptop", "windows")
	rec = h.req(http.MethodPost, "/api/v1/zones/home/join", bobTok,
		protocol.JoinZoneRequest{NodeID: bobNode.ID, Password: "zone-pass-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("bob join: %d", rec.Code)
	}

	ann, code, _ := h.announce(aliceTok, "home", aliceNode.ID, "192.168.1.0/24")
	if code != http.StatusCreated {
		t.Fatalf("announce: %d", code)
	}
	path := fmt.Sprintf("/api/v1/zones/home/announcements/%d", ann.ID)

	// A plain member (neither announcer nor zone owner) cannot detach.
	if rec := h.req(http.MethodDelete, path, bobTok, nil); rec.Code != http.StatusNotFound {
		t.Errorf("plain member detach = %d, want 404", rec.Code)
	}
	// The zone owner can (kick-authority model).
	if rec := h.req(http.MethodDelete, path, ownerTok, nil); rec.Code != http.StatusNoContent {
		t.Errorf("zone owner detach = %d, want 204", rec.Code)
	}
	// Re-announce, then the announcer detaches their own.
	ann, code, _ = h.announce(aliceTok, "home", aliceNode.ID, "192.168.1.0/24")
	if code != http.StatusCreated {
		t.Fatalf("re-announce: %d", code)
	}
	path = fmt.Sprintf("/api/v1/zones/home/announcements/%d", ann.ID)
	if rec := h.req(http.MethodDelete, path, aliceTok, nil); rec.Code != http.StatusNoContent {
		t.Errorf("announcer detach = %d, want 204", rec.Code)
	}
	// Dataplane shrank: routes set empty, peer back to its /32 only.
	zone, _ := h.store.Zones().GetByName(t.Context(), "home")
	if n := h.routesSetElems(zone.ID); n != 0 {
		t.Errorf("routes set elems after detach = %d, want 0", n)
	}
	stNode, _ := h.store.Nodes().GetByID(t.Context(), aliceNode.ID)
	if allowed := h.peerAllowed(stNode.PubKey); len(allowed) != 1 {
		t.Errorf("peer allowed ips after detach = %v, want only /32", allowed)
	}
}

// annTableCounts returns (announcements, attachments) row counts — the DB side
// of the zero-residue assertions.
func (h *nodeHarness) annTableCounts() (int, int) {
	h.t.Helper()
	var anns, atts int
	if err := h.store.DB().QueryRow(`SELECT COUNT(*) FROM announcements`).Scan(&anns); err != nil {
		h.t.Fatalf("count announcements: %v", err)
	}
	if err := h.store.DB().QueryRow(`SELECT COUNT(*) FROM announcement_zones`).Scan(&atts); err != nil {
		h.t.Fatalf("count attachments: %v", err)
	}
	return anns, atts
}

// TestAnnounceSameSubnetCoexistence is the US2 / SC-002 acceptance at the API
// boundary: identical real subnets coexist across nodes (same and different
// zones) without preemption, and multi-zone reuse returns the same synthetic.
func TestAnnounceSameSubnetCoexistence(t *testing.T) {
	h := newAnnounceHarness(t, 0)
	uid := h.seedUser("alice")
	tok := h.token(uid)
	nodeA := h.registerNode(tok, "router-a", "openwrt")
	nodeB := h.registerNode(tok, "router-b", "openwrt")
	h.createZoneJoin(tok, "zx", nodeA.ID)
	h.createZoneJoin(tok, "zy", nodeB.ID)
	// nodeA also joins zy (for the multi-zone reuse leg).
	rec := h.req(http.MethodPost, "/api/v1/zones/zy/join", tok,
		protocol.JoinZoneRequest{NodeID: nodeA.ID, Password: "zone-pass-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("join zy: %d", rec.Code)
	}

	a1, code, _ := h.announce(tok, "zx", nodeA.ID, "192.168.1.0/24")
	if code != http.StatusCreated {
		t.Fatalf("a1: %d", code)
	}
	// Snapshot node A's dataplane before the colliding announcement.
	stA, _ := h.store.Nodes().GetByID(t.Context(), nodeA.ID)
	beforeAllowed := h.peerAllowed(stA.PubKey)
	zx, _ := h.store.Zones().GetByName(t.Context(), "zx")
	beforeRoutes := h.routesSetElems(zx.ID)

	// Different node, *same real subnet*, different zone → distinct synthetic.
	b1, code, _ := h.announce(tok, "zy", nodeB.ID, "192.168.1.0/24")
	if code != http.StatusCreated {
		t.Fatalf("b1 same subnet: %d", code)
	}
	if b1.Synthetic == a1.Synthetic {
		t.Errorf("synthetic collision: %s", b1.Synthetic)
	}
	// No preemption: A's dataplane is byte-identical.
	afterAllowed := h.peerAllowed(stA.PubKey)
	if len(afterAllowed) != len(beforeAllowed) || !afterAllowed[a1.Synthetic] {
		t.Errorf("node A allowed ips changed: %v -> %v", beforeAllowed, afterAllowed)
	}
	if got := h.routesSetElems(zx.ID); got != beforeRoutes {
		t.Errorf("zx routes set changed: %d -> %d", beforeRoutes, got)
	}

	// Same zone, two nodes, same subnet → both visible in the zone listing.
	rec = h.req(http.MethodPost, "/api/v1/zones/zy/join", tok,
		protocol.JoinZoneRequest{NodeID: nodeB.ID, Password: "zone-pass-1"})
	_ = rec // nodeB already a member of zy; idempotent
	a2, code, _ := h.announce(tok, "zy", nodeA.ID, "192.168.1.0/24")
	if code != http.StatusCreated {
		t.Fatalf("a into zy: %d", code)
	}
	// Multi-zone reuse: node A announced the same subnet into zx and zy → same
	// synthetic block, no extra announcement row.
	if a2.Synthetic != a1.Synthetic || a2.ID != a1.ID {
		t.Errorf("multi-zone reuse broken: %+v vs %+v", a2, a1)
	}
	anns, _ := h.annTableCounts()
	if anns != 2 {
		t.Errorf("announcement rows = %d, want 2 (A's reused + B's)", anns)
	}
	rec = h.req(http.MethodGet, "/api/v1/zones/zy/announcements", tok, nil)
	var list protocol.AnnouncementListResponse
	decodeJSONBody(t, rec.Body.Bytes(), &list)
	if len(list.Announcements) != 2 {
		t.Errorf("zy listing = %d entries, want 2 (same subnet, two nodes)", len(list.Announcements))
	}
}

// TestAnnounceCascades walks the US3 / SC-003 deletion paths at the API
// boundary, asserting all three places (DB rows / peer AllowedIPs / nft routes
// set) end clean and the synthetic block is immediately reusable.
func TestAnnounceCascades(t *testing.T) {
	type fixture struct {
		h       *nodeHarness
		tok     string
		uid     int64
		node    protocol.NodeResponse
		ann     *protocol.AnnouncementResponse
		zoneID  int64
		nodePub string
		nodeIP  string
	}
	setup := func(t *testing.T) *fixture {
		h := newAnnounceHarness(t, 0)
		uid := h.seedUser("alice")
		tok := h.token(uid)
		node := h.registerNode(tok, "router", "openwrt")
		h.createZoneJoin(tok, "home", node.ID)
		ann, code, _ := h.announce(tok, "home", node.ID, "192.168.1.0/24")
		if code != http.StatusCreated {
			t.Fatalf("announce: %d", code)
		}
		zone, _ := h.store.Zones().GetByName(t.Context(), "home")
		stNode, _ := h.store.Nodes().GetByID(t.Context(), node.ID)
		return &fixture{h: h, tok: tok, uid: uid, node: node, ann: ann,
			zoneID: zone.ID, nodePub: stNode.PubKey, nodeIP: stNode.IP.String()}
	}
	assertClean := func(t *testing.T, f *fixture, peerGone bool) {
		t.Helper()
		anns, atts := f.h.annTableCounts()
		if anns != 0 || atts != 0 {
			t.Errorf("DB residue: %d announcements, %d attachments", anns, atts)
		}
		allowed := f.h.peerAllowed(f.nodePub)
		if peerGone {
			if len(allowed) != 0 {
				t.Errorf("peer still present: %v", allowed)
			}
		} else if len(allowed) != 1 || !allowed[f.nodeIP+"/32"] {
			t.Errorf("peer allowed ips = %v, want only /32", allowed)
		}
	}

	t.Run("leave zone", func(t *testing.T) {
		f := setup(t)
		rec := f.h.req(http.MethodPost, "/api/v1/zones/home/leave", f.tok,
			protocol.LeaveZoneRequest{NodeID: f.node.ID})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("leave: %d %s", rec.Code, rec.Body.String())
		}
		if n := f.h.routesSetElems(f.zoneID); n != 0 {
			t.Errorf("routes set elems = %d, want 0", n)
		}
		assertClean(t, f, false)
	})

	t.Run("kicked by owner", func(t *testing.T) {
		f := setup(t)
		// A second user announces into alice's zone, then alice (owner) kicks them.
		bob := f.h.seedUser("bob")
		bobTok := f.h.token(bob)
		bobNode := f.h.registerNode(bobTok, "bgw", "openwrt")
		rec := f.h.req(http.MethodPost, "/api/v1/zones/home/join", bobTok,
			protocol.JoinZoneRequest{NodeID: bobNode.ID, Password: "zone-pass-1"})
		if rec.Code != http.StatusOK {
			t.Fatalf("bob join: %d", rec.Code)
		}
		bobAnn, code, _ := f.h.announce(bobTok, "home", bobNode.ID, "192.168.50.0/24")
		if code != http.StatusCreated {
			t.Fatalf("bob announce: %d", code)
		}
		rec = f.h.req(http.MethodDelete, fmt.Sprintf("/api/v1/zones/home/members/%d", bobNode.ID), f.tok, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("kick: %d %s", rec.Code, rec.Body.String())
		}
		// Bob's announcement is gone; alice's survives (2 elements = her 1 CIDR).
		stBob, _ := f.h.store.Nodes().GetByID(t.Context(), bobNode.ID)
		if allowed := f.h.peerAllowed(stBob.PubKey); len(allowed) != 1 {
			t.Errorf("kicked node allowed ips = %v, want only /32", allowed)
		}
		if n := f.h.routesSetElems(f.zoneID); n != 2 {
			t.Errorf("routes set elems = %d, want 2 (alice's only)", n)
		}
		anns, _ := f.h.annTableCounts()
		if anns != 1 {
			t.Errorf("announcements = %d, want 1 (alice's)", anns)
		}
		_ = bobAnn
	})

	t.Run("node deleted", func(t *testing.T) {
		f := setup(t)
		rec := f.h.req(http.MethodDelete, fmt.Sprintf("/api/v1/nodes/%d", f.node.ID), f.tok, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete node: %d %s", rec.Code, rec.Body.String())
		}
		if n := f.h.routesSetElems(f.zoneID); n != 0 {
			t.Errorf("routes set elems = %d, want 0", n)
		}
		assertClean(t, f, true)
	})

	t.Run("zone deleted reclaims foreign announcement", func(t *testing.T) {
		f := setup(t)
		// Bob announces into alice's zone only; alice deletes the zone → bob's
		// announcement body must be reclaimed and his peer shrunk.
		bob := f.h.seedUser("bob")
		bobTok := f.h.token(bob)
		bobNode := f.h.registerNode(bobTok, "bgw", "openwrt")
		rec := f.h.req(http.MethodPost, "/api/v1/zones/home/join", bobTok,
			protocol.JoinZoneRequest{NodeID: bobNode.ID, Password: "zone-pass-1"})
		if rec.Code != http.StatusOK {
			t.Fatalf("bob join: %d", rec.Code)
		}
		if _, code, _ := f.h.announce(bobTok, "home", bobNode.ID, "192.168.50.0/24"); code != http.StatusCreated {
			t.Fatalf("bob announce: %d", code)
		}
		rec = f.h.req(http.MethodDelete, "/api/v1/zones/home", f.tok, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete zone: %d %s", rec.Code, rec.Body.String())
		}
		stBob, _ := f.h.store.Nodes().GetByID(t.Context(), bobNode.ID)
		if allowed := f.h.peerAllowed(stBob.PubKey); len(allowed) != 1 {
			t.Errorf("bob allowed ips = %v, want only /32", allowed)
		}
		assertClean(t, f, false)
	})

	t.Run("admin deletes announcer with surviving zone", func(t *testing.T) {
		f := setup(t)
		// Bob joins alice's zone and announces; admin deletes bob → alice's zone
		// survives, its routes set loses bob's element.
		bob := f.h.seedUser("bob")
		bobTok := f.h.token(bob)
		bobNode := f.h.registerNode(bobTok, "bgw", "openwrt")
		rec := f.h.req(http.MethodPost, "/api/v1/zones/home/join", bobTok,
			protocol.JoinZoneRequest{NodeID: bobNode.ID, Password: "zone-pass-1"})
		if rec.Code != http.StatusOK {
			t.Fatalf("bob join: %d", rec.Code)
		}
		if _, code, _ := f.h.announce(bobTok, "home", bobNode.ID, "192.168.50.0/24"); code != http.StatusCreated {
			t.Fatalf("bob announce: %d", code)
		}
		before := f.h.routesSetElems(f.zoneID) // alice's + bob's = 4
		rec = f.h.req(http.MethodDelete, fmt.Sprintf("/api/v1/admin/users/%d", bob), f.h.adminToken(f.uid), nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete user: %d %s", rec.Code, rec.Body.String())
		}
		if n := f.h.routesSetElems(f.zoneID); n != before-2 {
			t.Errorf("routes set elems = %d, want %d (bob's element gone)", n, before-2)
		}
		anns, _ := f.h.annTableCounts()
		if anns != 1 {
			t.Errorf("announcements = %d, want 1 (alice's)", anns)
		}
	})

	t.Run("synthetic reusable after cascade", func(t *testing.T) {
		f := setup(t)
		firstSynth := f.ann.Synthetic
		rec := f.h.req(http.MethodDelete, fmt.Sprintf("/api/v1/nodes/%d", f.node.ID), f.tok, nil)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("delete node: %d", rec.Code)
		}
		// A new node announcing a same-size subnet gets the freed block back.
		node2 := f.h.registerNode(f.tok, "router2", "openwrt")
		rec = f.h.req(http.MethodPost, "/api/v1/zones/home/join", f.tok,
			protocol.JoinZoneRequest{NodeID: node2.ID, Password: "zone-pass-1"})
		if rec.Code != http.StatusOK {
			t.Fatalf("join: %d", rec.Code)
		}
		ann2, code, _ := f.h.announce(f.tok, "home", node2.ID, "10.7.0.0/24")
		if code != http.StatusCreated {
			t.Fatalf("re-announce: %d", code)
		}
		if ann2.Synthetic != firstSynth {
			t.Errorf("synthetic = %s, want reclaimed %s", ann2.Synthetic, firstSynth)
		}
	})
}
